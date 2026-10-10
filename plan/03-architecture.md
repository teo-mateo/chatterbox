# 3. Architecture

## 3.1 Process model

Two roles, one binary.

```
agent harness ──spawn──▶ chatterbox listen        (long-lived, owns THE connection)
                              │  stdout: wake lines (FR2)      stderr: diagnostics (FR2.6)
                              │  unix socket <run>/<nick>.sock 0600 (FR3, FR6.3)
        ┌─────────────────────┼──────────────────────┐
        ▼                     ▼                      ▼
  chatterbox send      chatterbox whos-there   chatterbox channels / create-channel
  (short-lived, writes one JSON request, reads one JSON reply, exits)
```

Invariant I1: exactly one TCP/TLS connection per nick, owned by the listener. Send commands open
zero TCP sockets (FR3.1/FR3.2, asserted by test T-4.1).

Invariant I2: stdout is a machine interface. Only wake records, never prose (FR2.6).

Invariant I3: the listener never blocks on a stdout write for longer than the write buffer; if the
consumer stops reading, the listener buffers up to 512 wake records, then drops oldest with one
stderr warning per 100 dropped (`stdout stalled; N wakes dropped`). An agent that stopped reading
must not be able to stall the IRC connection or trigger a ping timeout. This is a deliberate
priority call: IRC liveness over wake completeness, and it is documented in the skill.

Invariant I4: a send command's exit code is a statement about *server* acceptance (FR3.5), never
about socket writability.

## 3.2 Source layout

```
Cargo.toml                     # edition 2024, MSRV 1.85, [profile.release] lto=true, strip=true
src/main.rs                    # clap dispatch, exit-code mapping (no logic)
src/cli/mod.rs                 # subcommand arg structs
src/cli/{listen,publish,send,whos_there,channels,create_channel,status,doctor,trust,skill}.rs
src/irc/mod.rs                 # connection state machine (owns one rustls connection)
src/irc/line.rs                # parse/serialize: tags, prefix, command, params, CRLF framing
src/irc/tags.rs                # message-tag codec incl. \\ , \: \r \n escapes
src/irc/cap.rs                 # CAP LS paging, REQ set, granted-set diff, required assertions
src/irc/isupport.rs            # 005 token store, mid-session re-read, typed accessors
src/irc/batch.rs               # batch tree: chathistory envelope, nested draft/multiline, coalescing
src/irc/frame.rs               # outbound framing: budgets, UTF-8-safe split, multiline batching
src/irc/nicks.rs               # case-mapping, PREFIX stripping, addressed-pattern matcher
src/listen/mod.rs              # the event loop, timers, signal handling
src/listen/pipeline.rs         # inbound line → Message → suppress → wake filter → format
src/listen/format.rs           # plain + JSON formatters, --state records
src/listen/gapfill.rs          # cursor, paging, dedup LRU, catchup accounting
src/listen/presence.rs         # NAMES snapshot + bot cache + WHOIS queue
src/ipc/proto.rs               # request/response types (serde), versioned
src/ipc/socket.rs              # bind/chmod/accept/SO_PEERCRED, per-platform
src/ipc/discover.rs            # nick resolution, instance registry, staleness
src/config.rs                  # config file, env, precedence, validation
src/pin.rs                     # pin store read/write, TOFU, mismatch report
src/err.rs                     # Error enum ↔ ExitCode mapping, user-facing text
src/version.rs                 # version, build target, feature banner for `doctor`
skill/SKILL.md                 # FR8.1 deliverable
docs/protocol-notes.md         # regenerated from the T-0 spike harness
tests/fake_ircd.rs             # in-process TLS IRC server (common to IT-*)
tests/live_ergo.rs             # gated live tests against tiny2
tests/e2e/acceptance.sh        # E2E-1..12
tests/e2e/proxy.py             # killable TLS relay to force a drop (E2E-6)
```

~4.5 kLoC of Rust including tests, 30–40 source files. No code generation, no build scripts.

## 3.3 Event loop

Single thread, `poll`-driven (`polling` crate), no async runtime. Readable sources: TLS socket,
control socket, self-pipe (wakeup from the signal handler and from timer fires). One timer wheel
with four timers: ping (50 s), liveness (120 s), reconnect backoff, send-ack timeout sweep.

`rustls` is driven through its `Stream` impl on a non-blocking socket: a read returns
`WouldBlock`/`WantRead` → poll again; a write returning `WouldBlock` leaves the connection in
"wants write" state and the writable poll resumes it — **no** blocking `write_all` on a TLS stream,
which is a known hang. Outbound bytes go through one 256 KiB buffer; past 220 KiB the listener
refuses new sends with `send_buffer_full` (exit 17, retry after `status.retry_in_s`) rather than
growing. Two distinct back-pressures, two distinct codes: exit 4 means "the request queue is full"
(a caller-side pacing problem) and exit 17 means "the socket cannot take bytes yet" (retry later).
`TCP_NODELAY` is on; the resolver returns all A/AAAA records and connects with a 250 ms head start
for the first address, then races the rest (Happy Eyeballs, IPv6 first), because the LAN target has
both an AAAA and an A record and a stalled v6 route must not wedge a listener.

Rationale for the simpler alternative that was rejected: one OS thread per concern with blocking
1 s read timeouts is fewer concepts, but with `rustls` a partial TLS record plus a blocking read
cannot be resumed cleanly, so the poll loop is the smaller overall risk. Recorded in `07#R10`.

## 3.4 Control socket and IPC protocol

Path (FR5.3, FR6.3), all under the runtime dir, which is resolved in this order and **validated**
before use:

```
$CHATTERBOX_RUNTIME_DIR                              (explicit, trusted operator override)
$XDG_RUNTIME_DIR/chatterbox                          (normal case: /run/user/$UID/chatterbox)
$XDG_STATE_HOME/chatterbox/run                       (default: ~/.local/share/chatterbox/run)
$HOME/.local/share/chatterbox/run
/tmp/chatterbox-$UID                                 (last resort; containers with no writable HOME)

  <runtime>/                       0700
  <runtime>/<nick>.sock            0600   nick is ASCII-case-folded for the filename
  <runtime>/instances/<nick>.json  0600   holds the gap-fill cursor (03#3.6) — not messages
  <state>/pins.toml                0600   lives in the state dir, not the runtime dir
```

The fallbacks matter more than they look: `XDG_RUNTIME_DIR` is unset in most containers, CI jobs and
agent harnesses, so the `/tmp` form is the common path, not the exotic one. Validation before
`bind`: `mkdir -p -m 0700`, then `stat` and require `st_uid == euid`, `mode & 0777 == 0700`, and
`!S_ISLNK`; anything else is `usage` (exit 2) naming the path and the reason — never a silent
fallback, because a foreign-owned directory means someone else can point our clients at their socket.
Socket mode is set with `umask(0177)` before creation plus `fchmod` after `bind` (`bind` uses the
umask for the inode). On accept: `getsockopt(SO_PEERCRED)` → uid must equal the listener's euid,
else reply `peer_uid` and close.

**Lifecycle, which is a correctness issue and not housekeeping:** the socket and instance file are
created **after** registration reaches READY, and removed at shutdown only if the on-disk inode and
the recorded pid both match ours. A `listen --as pi` that fails on `433` (exit 10) must therefore
never touch the live listener's socket — otherwise a second attempt makes a healthy listener
undiscoverable (`3 no_listener`) while its IRC connection is still up (R18, IT-233).

Transport: one JSON request per connection, one JSON reply, then close. 1 MiB request ceiling.
No streaming (nothing here needs it; the wake channel is stdout).

```jsonc
// request
{"v":1,"cmd":"send","as":"pi","to":"claude","text":"look at FR3.5",
 "important":false,"mode":"channel","id":"c1"}
// reply
{"v":1,"ok":true,"id":"c1","detail":{"accepted":"echo","label":"cb1759-4","msgid":"9pk4…"}}
// error reply
{"v":1,"ok":false,"id":"c2","error":{"code":"server_reject","message":"No such nick",
  "numeric":"401","retryable":false}}
```

`cmd` ∈ `publish send whos-there channels create-channel status ping cancel`.
**Cancellation (R15):** a send request carries a client id and the CLI's connection is held open
until the reply. If that connection closes before the reply — the CLI's `--timeout` expired, the
agent was interrupted — the listener drops the request if it is still queued, and if the bytes are
already on the wire it replies `delivery=uncertain` to whoever asked next and marks the label in
`status`. The exit-6 hint must never encourage a blind retry of a message that may have posted:
`04#4.6` prints the retry advice only when the listener confirmed the request never left. Error codes
(`src/err.rs`, exit codes in `04#4.6`): `no_listener stale_socket nick_mismatch peer_uid
queue_full server_reject ack_timeout conn_lost bad_request unsupported cap_missing pin_mismatch`.

The listener serialises outbound sends: one in-flight labelled message at a time, FIFO over ≤ 64
queued requests, per-request ack timeout 10 s. `publish` of a multi-chunk message (FR3.4) counts as
one queued unit with N chunks; all chunks must ack for the reply to be `ok`.

`status` returns `{connected, joined, caps_granted[], caps_missing[], uptime_s, last_error,
last_wake_at, wakes_emitted, wakes_dropped, reconnects, gapfill:{pending,backfilled,duplicates},
server:{host,port,ergo_version,isupport_bot}}` — the payload `doctor` and `whos-there` build on.

## 3.5 Nick resolution and the instance registry (FR5.2)

Resolution order, identical in every subcommand:

1. `--as <nick>`
2. `$CHATTERBOX_NICK`
3. exactly one *live* entry in `…/chatterbox/instances/` → that nick
4. ≥ 2 live entries → exit 7, stderr lists them; 0 → exit 3 with the `listen` command line

An entry is *live* iff its socket file exists **and** `kill(pid, 0)` succeeds for the recorded pid
with the same uid, **and** the instance file is owned by us. Stale entries are unlinked by the
listener that owns them at exit; a listener killed with SIGKILL leaves a stale entry, hence the
liveness probe (and `doctor --repair` cleans orphans). Instance file contents:
`{nick, pid, socket, channel, server, started_unix, version}`.

For `listen`, resolution stops at step 2 plus config `defaults.nick`, and a missing nick is exit 2
(OQ3). Auto-derivation is deliberately absent.

## 3.6 Connection, reconnect and gap fill (FR1.6)

```
DISCONNECTED ──connect+tls+pin──▶ NEGOTIATING ──001+CAP LIST ok──▶ READY
     ▲                              │ pin_mismatch → exit 9 (fatal)
     │                              │ required cap missing → exit 13 (fatal)
     │                              │ 433 → exit 10 (fatal)
     └────backoff(1→60s ×2 ±20%)────┴── read EOF / PONG timeout / ERR:KILLED / conn lost
READY ──QUIT/SIGTERM──▶ SHUTTING_DOWN (flush QUIT, unlink socket+instance, exit 0)
```

Gap-fill state, in memory only (issue #1: no local store):

- `seen: LRUSet<msgid>` capacity 4096 — exact duplicate suppression
- `cursor: {msgid, time, at, channel, server}` — **persisted** in `instances/<nick>.json` (0600) and
  advanced **only after a wake record has been written to stdout**, never when a record is dropped by
  the stall policy (I3) or suppressed as self/service. A cursor that moves past a message the agent
  never saw turns a temporary stall into permanent loss, and that is exactly what revision 1 of this
  section did. A cursor is a pointer into the server's copy, not a message store, so persisting it
  stays inside issue #1's "no local message store" (R11)
- `hold: Vec<Message>` — a **single** queue for the whole catch-up window: replayed messages *and*
  live messages arriving during it are buffered here, ordered by the server `time` of each message's
  **first** part, ties broken by arrival position, and flushed together when the envelope closes. The
  earlier design emitted live traffic immediately while buffering replay, which prints a newer
  message before an older one — precisely the ordering FR1.6 forbids, and the case that happens on
  every busy channel reconnect. If `hold` exceeds `limits.catchup_buffer` (default 512) the oldest
  are flushed with a stderr WARN naming the count: order is then best-effort for that window, and
  `wakes_dropped`/`gapfill` say so rather than pretending

Algorithm on entry to READY — **including the first READY of a process**, which is new in revision 2
because restarting the listener is the prescribed recovery for dropped wakes (`08`), so "first join ⇒
skip gap fill" made that recovery guarantee loss:

```
cursor = load_cursor(instances/<nick>.json)   # null if absent, or if server/channel changed
if cursor is null:
    skip gap fill                             # genuinely nothing to fetch; stderr says so
else:
  loop:
    q = CHATHISTORY LATEST <chan> msgid=<cursor.msgid> 1000   # timestamp= fallback below
    collect lines until matching BATCH -<id> or the +draft/chathistory-end tag
    coalesce nested draft/multiline parts into messages
    for m in collected_sorted_by_first_part_time_stable:
        if m.msgid in seen: continue                        # dedup, FR1.6
        if m.sender == self or is_service(m):                # FR2.4: silent, but the cursor moves
            seen.insert(m.msgid); advance_cursor(m); continue
        seen.insert(m.msgid); hold.push(m)                   # live traffic joins the same queue
    if collected.is_empty() or end_marker: flush(hold); break
    if iterations >= iterations_max:
        warn("gap exceeds server retention (2048): some messages are unrecoverable")
        flush(hold); break
  stderr: "catchup complete: N messages delivered, D duplicates suppressed"

advance_cursor(m): only after that message's wake line is written to stdout;
                   then persist (write-temp + rename, 0600) into the instance file
```

Correctness notes. msgid is the only exact dedup key (measured on every replayed line) and is
**never** an ordering key — the observed msgids (`_p8txu58…`, `3gutycsm…`, `bnkwjgmp…`) are opaque
and not monotonic, so `time` orders and msgid dedups (`02#2.14`, UT-113). Live messages arriving
during replay enter `seen` before the replay reaches them, so dedup must work in both directions.
`iterations_max = ceil(retention / page) + 1` (3 at 2048/1000), not a magic 8; reaching it is a WARN
because it means data is unrecoverable, not merely late.

Timestamp-cursor fallback, pre-written rather than improvised if T-0.3(a) fails: query
`timestamp=<cursor.time − 1000 ms>` and let the msgid LRU swallow the overlap. The 1000 ms window is
required, not advisory: `02#2.14` measured two messages inside the same millisecond, so a cursor at
exactly `time` loses boundary messages. LRU 4096 > 2048 server retention, so the overlap always fits
the dedup window.

Backoff resets to 1 s after 120 s of healthy connection. Reconnect is attempted forever: FR1.5 says
the listener stays connected until terminated, so "give up after N tries" would violate it. The one
exception is a fatal exit (pin mismatch, required cap missing, nick taken), which are configuration
errors that retrying cannot fix. **The same backoff applies before the first READY** (revision 2):
DNS failure, TCP refusal and a TLS handshake failure that is not a pin mismatch are transient — a
listener started before the server is up retries with `conn`/`dns`/`tcp`/`tls` sub-labels (exit 16
only when `listen --no-retry` is set, for CI), so FR1.5's "stays connected until terminated" holds
from process start rather than from the first success. A mid-session `CAP DEL` for anything in the
required set marks the connection unhealthy and reconnects, which re-asserts it; revision 1 said CAP
DEL "needs no handling", which left a listener sending batches its server no longer accepts.

## 3.7 Configuration (FR6.1)

`$XDG_CONFIG_HOME/chatterbox/config.toml` (default `~/.config/…`), created `0600` by
`chatterbox init` (never auto-created silently):

```toml
[server]
host = "192.168.0.204"        # required
port = 6697                    # required
sni  = "irc.heapzilla.eu"      # optional; TLS ServerName when connecting by IP
auto_join = "#hof"             # optional default channel

[defaults]
nick  = "pi"                   # optional, see OQ3
wake  = "all"                  # all | mentions
state = false                  # emit {"type":"state"} records in --json mode
quit_reason = "chatterbox: session ended"

[limits]
line_bytes = 450               # ≤ 512 including tags
batch_bytes = 4096             # ≤ server max-bytes
batch_lines = 100
ack_timeout_secs = 10
queue_capacity = 64
stdout_buffer = 512

[history]
gapfill = true                 # FR1.6
initial = "none"               # none | <count>  (catch-up on first join, off by default)

[services]
suppress_nicks = ["HistServ", "NickServ", "ChanServ", "MOTD"]   # FR2.4
```

Precedence **CLI flag > env > config > default**. Env: `CHATTERBOX_NICK`, `CHATTERBOX_SERVER_HOST`,
`CHATTERBOX_SERVER_PORT`, `CHATTERBOX_CHANNEL`, `CHATTERBOX_WAKE`, `CHATTERBOX_JSON`,
`CHATTERBOX_RUNTIME_DIR`, `CHATTERBOX_STATE_DIR`. Limits are clamped, not trusted: `line_bytes >
500` is refused with the reason, and the effective `batch_bytes` is
`min(config, server max-bytes from the CAP parameter)`. A config key that is present but unknown is
an error (exit 8), not a shrug — a typo'd `hsot` must not silently mean "no server".

## 3.8 TLS and pinning (FR6.2)

`rustls` with a custom `ServerCertVerifier`:

1. Connect, receive the chain, take the leaf DER.
2. Compute SHA-256 over the leaf DER.
3. Look up `host:port` in the pin store.
   - present and equal → accept
   - present and different → `PinMismatch{pinned, presented, host, port}` → exit 9, print both
     fingerprints and the exact `chatterbox trust --server … --port … --fingerprint sha256:<new>`
     command; **never** silently accept
   - absent, `--pin sha256:…` given on the command line → verify equal, then store
   - absent, `--tofu` **and** stderr is a TTY → print the fingerprint, ask `[y/N]`, store on yes
   - absent otherwise → exit 14 with the `trust` command to run. Non-interactive agents must be
     given a pin; the error text contains the observed fingerprint so the human's next step is one
     copy-paste.
4. `ServerCertVerifier::verify_server_cert` returns `Ok` only after step 3; signature schemes and
   verifier builder use rustls' `danger()` API. Certificate *expiry* is reported as a warning, not a
   hard failure, because a self-signed leaf rotating annually would otherwise brick listeners; the
   mismatch check is the security property, and the expiry warning appears in `doctor`.

`chatterbox trust --list` and `--forget <host:port>` manage the store. `pin store` is
`$XDG_DATA_HOME/chatterbox/pins.toml`, `0600`, written atomically (`tempfile` + rename).
`pin` and `fingerprint` mean the same thing here; the flag accepts `SHA256:`/`sha256:`/bare hex.

## 3.9 Outbound send state machine (FR3.5)

```
QUEUED → FRAMED(line_budget_check) → SENT(label=cbX) → {ACKED(echo label) | REJECTED(numeric|FAIL)
        | TIMED_OUT(10s) | CONN_LOST}
```

`FRAMED` raises `FrameTooSmall` if a single indivisible token exceeds the line budget after
word-splitting and hard-cut fallback. `REJECTED` carries numeric/FAIL code + server text +
`retryable` (`401`/`404` not retryable; `fakelag` delay is retryable after 2 s; conn-lost is
retryable once with a new label, because the server may have accepted-then-dropped — the reply says
so explicitly rather than claiming a duplicate is safe).

## 3.10 Observability

- `chatterbox status --json` — machine view (`03#3.4`).
- `chatterbox doctor` — ordered preconditions, each `PASS`/`FAIL`/`WARN` with a fix line: config
  parse → server resolves → TCP → TLS+pin → caps granted (list vs required) → `BOT=` present →
  nick free (PRIVMSG-less probe: registration attempt on a scratch connection with a derived nick
  `<nick>-doctor`, immediately QUIT) → channel joinable → listener alive → socket perms → peer-uid
  mechanism → wake pipeline (echo one test line through the formatter). Exit 0 only if all PASS.
  The scratch-connection check is the one thing chatterbox does outside the listener; it is a
  *diagnostic*, not a send, and prints a stderr note saying so.
- `--verbose` on any command: protocol-level trace to stderr with tag values truncated.
- No telemetry, no network egress beyond the configured server.

## 3.11 Dependencies (11 non-transitive, for `cargo deny` review)

`clap` (derive), `serde`, `serde_json`, `toml`, `rustls`, `rustls-pki-types`, `polling`, `libc`,
`thiserror`, `time` (or `chrono`, whichever has the smaller tree at T-0.4), plus whatever `rustls`
platform-verifier pieces the custom verifier turns out to need. Dev-only: `rcgen` (fake-server
certs), `anyhow`, `predicates`, `tempfile`, `assert_cmd`. Nothing that shells out, nothing that reads
env secrets, no TLS stack swap-in.

Dropped in revision 2, with reasons, because both were load-bearing on FR6.1:
- **`dirs`** — its macOS mapping is `~/Library/Application Support`, so FR6.1's promised
  `~/.config/chatterbox/config.toml` would not be the file read on a platform we ship binaries for.
  Paths resolve from `XDG_CONFIG_HOME`/`XDG_DATA_HOME`/`XDG_STATE_HOME`/`XDG_RUNTIME_DIR` with
  `$HOME` fallbacks, by hand, on every platform, so the documented paths are the real ones (R19).
  `$HOME` itself comes from `getpwuid_r` when the environment omits it.
- **`percent-encoding`** — it existed only to make a nick safe as a filename, and nicks are validated
  against `%a{NICKCHARS}` (no `/`, no whitespace) before that; the filename is the case-folded nick.

## 3.12 Packaging and install

`cargo build --release --target x86_64-unknown-linux-musl` (static, ~3 MB) → GitHub release tarballs
for linux x86_64/aarch64 (musl) + macOS aarch64/x86_64 — **macOS is build-verified only**: CI
compiles it and does nothing else, and the release notes must say "build verified, not smoke tested"
(R19). `cargo install --path .` for source installs; `chatterbox --version` prints version + rustls version + build target, which `doctor`
echoes. Install location for the operator: `~/.cargo/bin/chatterbox`, matching websearch-cli /
webfetch-cli. No systemd unit ships in v1: the agent harness owns the background process, per issue
#1's "agents use their built-in ability to watch a background process".

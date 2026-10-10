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
which is a known hang. Outbound bytes go through one 256 KiB buffer; if it exceeds 220 KiB the
listener stops accepting new sends (queue-full error, exit 4) rather than growing.

Rationale for the simpler alternative that was rejected: one OS thread per concern with blocking
1 s read timeouts is fewer concepts, but with `rustls` a partial TLS record plus a blocking read
cannot be resumed cleanly, so the poll loop is the smaller overall risk. Recorded in `07#R10`.

## 3.4 Control socket and IPC protocol

Path (FR5.3, FR6.3):

```
$XDG_RUNTIME_DIR/chatterbox/                    0700  (fallback: /tmp/chatterbox-$UID, 0700)
                       /<percent-encoded nick>.sock   0600
                       /instances/<nick>.json         0600
                       /pins.toml (state dir)         0600
```

Socket dir mode is set with `umask(0177)` before creation, and `fchmod` immediately after `bind`
(the two-step is required because `bind` uses the umask for the inode). On accept:
`getsockopt(SO_PEERCRED)` → `uid` must equal the listener's euid, else reply `peer_uid` and close.

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

`cmd` ∈ `publish send whos-there channels create-channel status ping`. Error codes
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
- `cursor: {msgid?, time}` — updated **after** a message has been handed to the pipeline, including
  suppressed ones, so gap fill never re-requests the same window
- `backfill_pending: Vec<Message>` — messages replayed during catch-up are buffered and emitted in
  `time` order after the batch closes, so FR1.6's "in order" holds even though the replay arrives
  after live traffic has resumed

Algorithm on entry to READY after a reconnect:

```
if first_join: cursor = none; skip gap fill            # FR1.6 covers reconnects only
else:
  loop:
    q = CHATHISTORY LATEST <chan> msgid=<cursor.msgid> 1000   # or timestamp= if no msgid
    collect lines until matching BATCH -<id> or +draft/chathistory-end
    for m in collected sorted by time:                   # nested multiline coalesced first
        if m.msgid in seen: continue                     # dedup, FR1.6
        if m.sender == self or is_service(m): advance(cursor); continue   # FR2.4
        seen.insert(m.msgid); advance(cursor); emit(m)
    if collected.is_empty() or end_marker: break         # caught up
    if iterations == 8: warn("history truncated by server retention (2048)"); break
  stderr: "catchup complete: N messages delivered, D duplicates suppressed"
```

Correctness notes: msgid is the only exact dedup key (measured on every replayed line); `time`
alone collides at millisecond resolution; live messages arriving *during* replay are inserted into
`seen` before the replay reaches them, which is what makes the dedup necessary in both directions.
The 8-iteration cap is `2048/1000 ≈ 2` pages of retention with headroom, and its only purpose is to
bound the loop.

Backoff resets to 1 s after 120 s of healthy connection. Reconnect is attempted forever: FR1.5 says
the listener stays connected until terminated, so "give up after N tries" would violate it. The one
exception is a fatal exit (pin mismatch, required cap missing, nick taken), which are configuration
errors that retrying cannot fix.

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

## 3.11 Dependencies (12 non-transitive, for `cargo deny` review)

`clap` (derive), `serde`, `serde_json`, `toml`, `rustls`, `rustls-pki-types`, `webpki` (only if the
verifier needs anchor types — drop if not), `polling`, `libc`, `dirs`, `thiserror`, `percent-encoding`,
`time` (or `chrono`, whichever has the smaller tree at T-0.4). Dev-only: `rcgen` (fake-server
certs), `anyhow`, `predicates`, `tempfile`, `assert_cmd`. Nothing that shells out, nothing that
reads env secrets, no TLS stack swap-in.

## 3.12 Packaging and install

`cargo build --release --target x86_64-unknown-linux-musl` (static, ~3 MB) → GitHub release tarballs
for linux x86_64/aarch64 (musl) + macOS aarch64/x86_64; `cargo install --path .` for source
installs; `chatterbox --version` prints version + rustls version + build target, which `doctor`
echoes. Install location for the operator: `~/.cargo/bin/chatterbox`, matching websearch-cli /
webfetch-cli. No systemd unit ships in v1: the agent harness owns the background process, per issue
#1's "agents use their built-in ability to watch a background process".

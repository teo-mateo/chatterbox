# 1. Scope, requirement resolution, decisions

Every FR from issue #1 appears exactly once in §1.2 with: the concrete behaviour chatterbox will
implement, the tasks that build it, and the tests that prove it. §1.3 answers the five open
questions from the issue. §1.4 records what v1 deliberately does not do.

## 1.1 Naming and vocabulary used in this plan

- **listener** — the long-running `chatterbox listen` process, the only thing that holds an IRC
  connection (issue #1, "Concepts").
- **send command** — `publish`, `send`, and the read-only commands `whos-there`, `channels`,
  `create-channel`. All are short-lived clients of the listener over a local socket.
- **wake** — one printed line on the listener's stdout, i.e. the unit that may interrupt an agent.
- **suppress** — drop a line before the wake filter, because FR2.4 forbids printing it. Suppressed
  lines still advance the gap-fill cursor; "not printed" and "not counted" are different things.
- **FR-id / T-id / UT-id / IT-id / E2E-id** — requirement, task, and test ids. A claim with no id
  is a wish, not a plan item.

## 1.2 Requirement resolution

### FR1 — Listen mode

| FR | Issue text, abbreviated | Implemented as | Tasks | Tests |
|---|---|---|---|---|
| FR1.1 | `listen --as <nick> --channel <#name>`, TLS, take nick, join | TLS always, no plaintext code path. Order: TCP → TLS → `CAP LS 302` → `CAP REQ :…` → `NICK`/`USER` → `CAP END` → `MODE <nick> +B` → `JOIN`. `+B` before `JOIN` so the JOIN echo already carries the `bot` tag (measured). Exit 10 on `433`.  T-2.1 T-2.2 T-2.5 | UT-101..104, IT-201, IT-202, E2E-1 |
| FR1.2 | Listener marks itself as a bot (`+B`) | `MODE <self> +B` immediately post-registration, gated on the `BOT=B` token in `RPL_ISUPPORT` (measured: `BOT=B`; `MODE cbver2 +B` accepted; WHOIS `379 … +BZi`; `335 … is a Bot on HofNet`). If `BOT=` is absent, print one stderr warning that participants cannot be distinguished and continue — chatterbox does not invent a second signal.  T-1.4 T-2.4 | UT-105, IT-203, E2E-2 |
| FR1.3 | Exactly one channel per listener | Enforced locally: the server allows many JOINs (measured), so this is chatterbox's rule. The listener never originates a second JOIN; `create-channel` moves it instead (OQ2). Inbound events for another channel are ignored with a stderr note. If the listener is `KICK`ed out of its channel it re-JOINs once; if that fails it exits 12 rather than idling in a channel-less state that still looks alive.  T-2.5 T-5.5 | UT-106, IT-205, E2E-8 |
| FR1.4 | Nick in use → clear error, never another name | On `433`: exit 10, stderr `nickname "pi" is already in use on irc.heapzilla.eu:6697; start with a different --as`. No retry, no mangling, no suffix. Reachability confirmed: `433 * probebot :Nickname is already in use`.  T-2.3 | UT-107, IT-204 |
| FR1.5 | Stays connected until terminated; on exit the nick leaves | Runs until SIGINT/SIGTERM or a fatal protocol error. The signal handler sends `QUIT :<reason>` and waits up to 250 ms for it to leave the socket buffer before closing, because a bare `close()` loses the buffered QUIT and leaves a ghost until the server's ping timeout (2 m 30 s, measured). `--quit-reason` overrides the text.  T-2.6 T-2.9 | UT-108, UT-109, IT-230, E2E-7 |
| FR1.6 | Reconnect with backoff, fetch missed history, in order, no duplicates | Full algorithm in `03#3.6`. Backoff 1 s → 60 s, ×2, ±20 % jitter, reset after 120 s healthy, unbounded retries (FR1.5 outranks giving up). Gap fill: `CHATHISTORY LATEST #chan msgid=<cursor> 1000` (fallback `timestamp=<cursor>`), looped until the reply batch closes, deduped by a 4096-entry msgid LRU plus a monotonic `time` watermark. The `+draft/chathistory-end` tag (measured) marks end of replay; only then does stderr say `catchup complete`.  T-2.6 T-3.7 | UT-110..114, IT-206, IT-209, E2E-6 |

### FR2 — Message delivery to the agent

| FR | Issue text, abbreviated | Implemented as | Tasks | Tests |
|---|---|---|---|---|
| FR2.1 | One line per message, including multi-line wire messages | The batch tree (T-3.3) coalesces a `draft/multiline` batch into one `Message` before formatting, and the plain formatter refuses any text containing a raw newline (escaped to the two characters `\n`), so a wake line can never be split. Measured wire shape: `02#2.6`.  T-1.5 T-3.1 T-3.5 | UT-120..123, IT-207, E2E-3 |
| FR2.2 | Line identifies sender, bot/human, channel, time, addressed, important | Column layout and JSON schema in `04#4.3`/`04#4.4`. bot/human comes from the `bot` message tag when present (measured on PRIVMSG, JOIN and echo), else from the WHOIS-`335` cache, else `?`. `?` is a real value, not a silent `human`.  T-3.3 T-3.5 | UT-124..127, IT-217, E2E-2, E2E-5 |
| FR2.3 | `--json`, one object per line | NDJSON, `v:1` schema, real newlines preserved by JSON escaping. Field set and nullability in `04#4.4`. Consumers must ignore unknown fields (stated in the skill).  T-3.6 | UT-128, IT-208, E2E-3 |
| FR2.4 | Own messages, joins, quits, server noise are not printed | Layered suppression, order matters: (a) service replies such as `HistServ!HistServ@localhost` — measured inside history batches, (b) `JOIN`/`PART`/`QUIT`/`NICK`/`MODE`/`TOPIC`/`AWAY`/`NOTICE`/`TAGMSG`/`PING`, (c) numerics not consumed by the state machine, (d) `sender == self.nick`. (d) is required, not optional: `echo-message` is negotiated for FR3.5, and `CHATHISTORY` replay returns the listener's own messages (measured: our `echo-check-unique-token` came back inside a history batch).  T-3.2 | UT-129..133, IT-209, IT-213, E2E-3 |
| FR2.5 | Wake filter: all, or addressed-to-me + important | `--wake all` (default) \| `--wake mentions`. *Addressed* = body matches one of, case-insensitively under `CASEMAPPING=ascii` (measured): `^<nick>[:, ]`, `@<nick>` at a word boundary, or a STATUSMSG prefix (`STATUSMSG=~&@%+` measured). Importance is decided by the `[important]` body marker (`02#2.9`). Under `--wake mentions`, important messages are always printed (FR3.3).  T-1.7 T-3.4 | UT-134..138, IT-208, E2E-4 |
| FR2.6 | Diagnostics to stderr, never stdout | stdout carries wake lines only, plus `{"type":"state",…}` records when `--json --state` is given. Everything else is stderr, line-buffered, prefixed `chatterbox[<nick>]: `. A stdout write error (agent stopped reading) is logged to stderr and is never fatal.  T-2.7 | UT-139, IT-210, IT-229, E2E-6 |

### FR3 — Sending

| FR | Issue text, abbreviated | Implemented as | Tasks | Tests |
|---|---|---|---|---|
| FR3.1 | `publish <message>` → listener's channel | Control-socket command `publish`; the listener writes the framed message on its existing connection. The send process never opens a TCP socket — asserted in T-4.1 by a socket-creation guard in the test build.  T-4.3 T-4.8 | UT-144, IT-211, IT-231, E2E-3 |
| FR3.2 | `send <nick> <message>` → one participant | Default = channel PRIVMSG with an addressed body (`pi: …`) so the human sees the exchange (OQ1). `--dm` sends a real `PRIVMSG pi`. Nonstderr warning when the target is not in the NAMES cache. Measured failure path: `PRIVMSG nosuchnick9` → `401` carrying our `label`.  T-4.3 | UT-140..142, IT-212, E2E-4 |
| FR3.3 | `--important` always printed; marking visible in a normal IRC client | Body prefix `[important] ` — visible in every client, survives history replay and survives a tag-stripping server (measured, `02#2.6`). The client tag `chatterbox/important=1` is attached as a forward-looking bonus only, with a stderr note that it is not relied on. Receiving listeners treat the body marker as authoritative.  T-3.4 T-4.4 | UT-143, IT-213, E2E-4 |
| FR3.4 | Long messages via `draft/multiline`; over the server limit → split into several messages, in order | Pre-flight framing, never optimistic: each wire line ≤ `limits.max_line_bytes` (default 450, headroom under 512 — measured `417 Input line too long` at ~630 bytes); each batch ≤ `max-bytes=4096` and ≤ `max-lines=100` parsed from the CAP parameter (measured, and `FAIL BATCH MULTILINE_MAX_BYTES 4096` when exceeded). Over 4096 bytes → N sequential batches, each within limits, sent and acked in order; a failed batch stops the run and names the failing chunk.  T-1.6 T-4.5 | UT-144..149, IT-214, LIVE |
| FR3.5 | Exit 0 only after the listener accepted the message; non-zero with explanation | "Accepted" means **server acknowledgement**, not a socket write: `echo-message` + `labeled-response` (measured working). Success = echo of our PRIVMSG/BATCH carrying `label=<ours>`. Failure = error numeric (`401`, `404`, `481`, `417`) or `FAIL …` carrying the same label. Timeout after `limits.ack_timeout_secs` (10 s) → exit 6. Taxonomy: `04#4.6`.  T-4.3 T-4.6 | UT-150..153, IT-211, IT-215, E2E-3 |
| FR3.6 | No listener → failure that says how to start one | Discovery failure → exit 3 with the literal command to run. Distinguishes: no socket, stale socket (recorded pid gone), socket not ours (exit 7), ambiguous (exit 7).  T-4.7 T-5.2 | UT-154..156, IT-216, E2E-13 |

### FR4 — Presence and channels

| FR | Issue text, abbreviated | Implemented as | Tasks | Tests |
|---|---|---|---|---|
| FR4.1 | `whos-there` lists participants with bot/human | `NAMES` snapshot for the listener's channel (`userhost-in-names` measured ⇒ entries are `nick!user@host`) joined with the listener's bot cache, plus `WHOIS <nick>` (numeric `335`, measured working cross-party on nick `pi`) for cache misses. `NAMES`, `WHO %cu…` and `USERHOST` were measured to carry **no** usermode/bot field, so there is no cheaper shortcut. Status ∈ `bot \| human \| unknown`.  T-3.8 T-5.3 | UT-160..162, IT-217, E2E-2 |
| FR4.2 | `channels` lists channels with names and last activity | `LIST` (measured `322 <chan> <members> :` with no activity field; `ELIST=U` sorts by user count, not time) for the set plus member counts; `CHATHISTORY LATEST <chan> * 1` for the one channel the listener occupies. Other channels print `-` with `activity_source=unavailable` (OQ4).  T-3.9 T-5.4 | UT-163, IT-218, E2E-9 |
| FR4.3 | `create-channel <#name>` creates a channel | Ask the listener to `JOIN` the new channel — measured that this creates it and grants `@` — then `PART` the previous one, because a channel exists only while occupied (measured: `#cbtest-doesnotexist`, `#cbtest2`, `#cbtest-planb` all vanished after `PART`; `LIST` afterwards showed only the occupied channels). `--move` is therefore implied and `--keep` is rejected with an explanation. Stderr prints the human step for durability: `/msg ChanServ REGISTER <#chan>`.  T-5.5 | UT-164, IT-219, E2E-10 |

### FR5 — Identity

| FR | Issue text, abbreviated | Implemented as | Tasks | Tests |
|---|---|---|---|---|
| FR5.1 | Every agent has a nickname; `--as` gives it | `listen` requires a nick from `--as` \| `$CHATTERBOX_NICK` \| `defaults.nick`, else exit 2 listing the three ways to supply one. Validated against `NICKLEN=32` (measured) and the IRC charset `[A-Za-z\[\]\\_\^{}|-][A-Za-z0-9\[\]\\_\^{}|-]*`.  T-5.1 | UT-170, UT-171, IT-220, E2E-1 |
| FR5.2 | `--as` → `CHATTERBOX_NICK` → auto-detect when exactly one listener for the current user | Same resolution order everywhere, then the instance registry (`03#3.5`) for auto-detection. Exactly one live instance → use it. Two or more → exit 7, listing candidate nicks. Zero → exit 3 (FR3.6).  T-5.1 T-5.2 | UT-172..174, IT-216, E2E-1 |
| FR5.3 | Several agents per machine, no accidental cross-posting | Socket path is per nick, so a command cannot reach the wrong listener without naming it. Defence in depth: the listener rejects a requested `as` that differs from its own nick (error `nick_mismatch`, exit 8) and `SO_PEERCRED` requires `uid == euid`. **Honest limit:** processes of the same uid are not mutually isolated — the filesystem cannot enforce that, and there is no authentication in v1 (SASL out of scope). FR5.3 says "by accident"; this implements accident-proofing, not adversarial same-user isolation. See `07#R6`.  T-4.1 T-5.6 | UT-175..178, IT-221, IT-224, LIVE |

### FR6 — Configuration and security

| FR | Issue text, abbreviated | Implemented as | Tasks | Tests |
|---|---|---|---|---|
| FR6.1 | Config file `~/.config/chatterbox/config.toml`, flags override | Precedence: CLI flag > environment > config file > built-in. Schema in `03#3.7`. `XDG_CONFIG_HOME` respected. Parse errors name file, key, line; exit 8.  T-6.1 T-6.5 | UT-180..183, IT-222, IT-226, E2E-13 |
| FR6.2 | TLS required; self-signed supported via fingerprint pinning; later mismatch refused with an explanation | The measured environment is exactly this case: self-signed `O=Ergo`, valid 2026-10-10 → 2027-10-10, SHA-256 `44:0D:EE:9D:…:CE:0xBA` (DRIFT). No plaintext path in the binary. Pin store `~/.local/share/chatterbox/pins.toml`, `0600`, one entry per `host:port`. On mismatch: exit 9, stderr prints pinned vs presented fingerprints and the exact `chatterbox trust` command. Trusting is explicit (`chatterbox trust`, or `--pin sha256:…`); TOFU only with `--tofu` **and** a TTY.  T-6.2 T-6.3 T-6.4 | UT-184..189, IT-223, IT-223b, E2E-11 |
| FR6.3 | Control socket accessible only to the user who started the listener | Runtime dir `0700`, socket `0600` (mode set before `bind` where the platform cannot `fchmod` a socket); `SO_PEERCRED` uid check on every accept, replying `{"ok":false,"error":{"code":"peer_uid"}}` then closing; instance files `0600`. macOS has no `SO_PEERCRED`: use `LOCAL_PEERCRED`, else fall back to directory permissions plus an `lstat` owner check on the socket inode, and `doctor` reports which mechanism is active.  T-4.1 | UT-190..192, IT-224, LIVE |

### FR7 — Human participation

| FR | Issue text, abbreviated | Implemented as | Tasks | Tests |
|---|---|---|---|---|
| FR7.1 | Human with any standard IRC client reads everything agents publish and can post | Nothing chatterbox sends is private to chatterbox: `publish` and default `send` are ordinary channel PRIVMSGs, confirmed end to end between two raw clients and visible in `LIST`.  T-4.3 | IT-211, E2E-3, E2E-5 |
| FR7.2 | Human messages delivered like agent messages, flagged as human | A human message simply lacks the `bot` tag that Ergo stamps on `+B` senders, so the listener resolves `human` and prints `hum`. Measured: `teodor` (human) produced no `335`; `pi` (bot) produced `335 … is a Bot on HofNet`.  T-3.3 | UT-126, IT-217, E2E-5 |
| FR7.3 | Nothing required of the human's client beyond standard IRC | No chatterbox-specific syntax on the wire except the `[important] ` and `nick: ` body conventions. `chatterbox doctor` prints the plain-IRC lines a human should type, and the skill documents the human's view.  T-7.2 | E2E-5 |

### FR8 — Agent onboarding

| FR | Issue text, abbreviated | Implemented as | Tasks | Tests |
|---|---|---|---|---|
| FR8.1 | Short instruction block (skill or snippet) | `plan/08-agent-onboarding-skill.md` is the draft; it ships as `skill/SKILL.md` (pi/Claude-compatible) and is printed by `chatterbox skill`, so an agent needs neither the repo nor a network. Covers: start the listener in the background, watch stdout, reply-only-when-a-reply-is-needed etiquette, command reference, exit codes, troubleshooting. Budget ≤ 120 lines, ≤ 4 KB.  T-7.1 T-7.2 | UT-195, IT-227, E2E-12 |

## 1.3 Open questions from issue #1 — resolved

### OQ1 — Visibility of `send`

**Decision: default is a channel message addressed to the nick; `--dm` opts into a real private
message.**

Three measured facts decide it.

1. Client-sent message tags are stripped by the server (`02#2.6`), so an addressing convention
   carried in a tag would be invisible to the human *and* to the receiving listener.
2. A PM reaches only the recipient and never enters channel history, so a PM sent during a
   reconnect is unrecoverable by gap fill (FR1.6) and invisible to the human (FR7.1).
3. Issue #1's stated primary use case is "coordinating in one IRC channel, with a human able to
   watch and steer".

So `chatterbox send pi "…"` sends `PRIVMSG #hof :pi: …`, and the receiving listener marks it
addressed. `--dm` sends `PRIVMSG pi …` for the genuinely private case; the skill states that DMs
are invisible to the human and are not replayed by gap fill, i.e. not for decisions.

### OQ2 — Meaning of `create-channel`

**Decision: create-by-join, which moves the listener; no registration in v1.**

Measured: `JOIN #cbtest-doesnotexist` created the channel and granted `@`; the channel vanished
after `PART`. Registering a permanent channel requires an account (tiny2 config:
`channels.registration.enabled: true`, `operator-only: false`, `max-channels-per-account: 15`) and
accounts/SASL are explicitly out of scope. Since FR1.3 allows one channel per listener, "create"
without "occupy" would yield a channel that ceases to exist within seconds. `create-channel`
therefore PARTs the current channel and JOINs the new one; `--keep` is rejected with that
explanation; stderr prints `/msg ChanServ REGISTER #chan` as the human step for durability.

### OQ3 — Default nickname

**Decision: mandatory for `listen`; never auto-derived.**

A derived nick (`bot-<project>`) guesses wrong in exactly the two situations that matter — two
agents on one project, and a harness whose name chatterbox cannot know (issue #1 excludes
harness-specific code) — and it converts FR1.4's "clear error" into a silent collision race.
`--as`, `CHATTERBOX_NICK` and `defaults.nick` cover every real invocation, and the harness must
know the nick anyway in order to send to it. FR5.2's auto-*detection* is a different question
(finding an already-running listener) and is implemented.

### OQ4 — Last activity in `channels`

**Decision: report activity only where the server exposes it, and print `-` otherwise.**

Measured: `LIST` carries no timestamp (`322 <chan> <count> :`), `ELIST=U` sorts by user count, and
history for a channel we are not in returns `FAIL CHATHISTORY INVALID_TARGET`. Ergo additionally
sets `+n` on every new channel (measured `+Cnt`). So `channels` prints `name`, `members`,
`last_activity` (`-` when unknown) and an explicit `activity_source` (`chathistory` |
`unavailable`) instead of inventing a plausible-looking date. The skill tells agents that `-` means
"join it and ask".

### OQ5 — Implementation language

**Decision: Rust.** Rationale and the honest counter-argument in §1.6.

## 1.4 Non-goals (from issue #1, restated so reviewers can check the cut)

Running or managing the IRC server; registered accounts, SASL, nickname protection; a listener on
more than one channel; a local message store (the server's history is the record — chatterbox keeps
only an in-memory dedup window and a cursor); harness-specific hooks or extensions. Two additions
of this plan: no plaintext IRC at all, and no plugin system.

## 1.5 Definition of done for v1

1. `cargo test`, `cargo clippy --all-targets -- -D warnings`, `cargo fmt --check` green; CI green
   with no live-server requirement.
2. `E2E-1 … E2E-7` (issue #1's acceptance scenario, steps 1–7) pass against Ergo 2.19.1 on tiny2,
   driven by `tests/e2e/acceptance.sh`, unattended, leaving no channel or process behind.
3. `chatterbox doctor` exits 0 on a working setup and names the first failing precondition
   otherwise.
4. Every FR has ≥ 1 green test with the id from §1.2. A FR with no test is not done.
5. `chatterbox skill` output ≤ 4 KB, and `E2E-12` passes: a fresh agent given only that output
   completes one round trip.
6. The residual-unknown list in `07-risks-and-open-questions.md` is closed or downgraded to a
   documented limitation before the v1 tag.

## 1.6 D1 — Language: Rust (resolves OQ5)

**Decision: Rust, edition 2024, one binary, no async runtime.** Toolchain on this machine:
`rustc 1.98.1`, `cargo 1.98.1`. MSRV 1.85.

*Why Rust over Go (the only serious alternative here):* the deliverable is a single file dropped on
agent machines that have no runtime; `cargo build --release --target x86_64-unknown-linux-musl`
delivers that; the operator's existing CLIs in this environment (websearch-cli, webfetch-cli) are
already Rust, so the build/release muscle and CI template exist; `clippy` plus `thiserror` give the
explicit error taxonomy FR3.5 and FR6 need; the crate set is small and auditable.

*Where Go is genuinely better, recorded honestly:* TLS pinning. With `crypto/tls` a pinned
self-signed fingerprint is ~20 lines (`InsecureSkipVerify` + a `VerifyPeerCertificate` closure). In
Rust we must implement `rustls::client::danger::ServerCertVerifier` by hand (≈ 60–80 lines, plus
`danger()` configuration), because rustls' default verifier rejects a self-signed leaf before we
can look at it. **Mitigation:** T-0.2 is a spike whose sole purpose is a working pinning verifier
against tiny2. **Reversal trigger:** if that spike is not green within half a day, fall back to Go
with the same module layout (the architecture in `03` is language-neutral except `03#3.3`). Risk
write-up: `07#R2`.

*Deliberate constraints:* no `tokio`/`async-std` — the listener multiplexes three fds and a timer,
which a `polling` event loop does in fewer moving parts than a runtime, and blocking reads fight
`rustls`'s `WouldBlock` contract; non-transitive dependencies capped at 12, listed in T-0.4, with
`cargo deny` in CI; `rustls` (ring) rather than OpenSSL, so the musl build needs no system CA
store — which is also why custom pinning is the only CA story we have.

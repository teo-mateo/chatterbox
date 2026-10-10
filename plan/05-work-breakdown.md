# 05. Work breakdown

Each task lists what it implements and how it is known to be done. Test identifiers refer to [06-testing-and-acceptance.md](06-testing-and-acceptance.md).

## Status on 2026-10-10

| Milestone | State | Open |
|---|---|---|
| M0 | done, except as listed | T0.4 done: the workflow (`.github/workflows/ci.yml`) runs on every push to `main` and then replaces the `latest` release with the Linux binaries, which `scripts/install.sh` installs. T0.6: S7 was not measured on tiny2, S14 not examined for Pi. T0.7: owner sign-off, see 01 §5 |
| M1 | done | |
| M2 | done | T2.12 was built as watching the chain of parent processes (03 §2) |
| M3 | done | |
| M4 | done | built on D9, D11 and D15 as proposed; they are not signed off (01 §5) |
| M5 | done | |
| M6 | done | |
| M7 | done | AT-ONBOARD (a fresh agent given only the skill) has not been observed |
| M8 | partly | T8.1 done: AT-1 to AT-7 pass in both configurations. T8.4 done: stdout is written in one place, fed by one function. T8.2 open: the built binary was exercised on tiny2 on a temporary channel (two listeners, every command), but the checklist with two real agents and a human on `#hof` has not been run. T8.3 (12-hour soak) and T8.5 (update issue #1) open |

All unit tests pass with the race detector; the integration and acceptance suite passes against Ergo 2.19.1; `go vet` and `staticcheck` are clean; the four release binaries build.

## M0. Sign-off, bootstrap, spikes

| Task | Description | Done when |
|---|---|---|
| T0.1 | Owner sign-off on the answers to the open questions, D1–D5 | **done 2026-10-10** (01 §5) |
| T0.2 | Go module, directory layout of 02 §2, `Makefile` targets `build`, `test`, `test-integration`, `lint` | `make build` produces `bin/chatterbox`; `chatterbox version` prints a version |
| T0.3 | Local Ergo harness in `test/ergo/`: fetch a pinned Ergo 2.19.x release (2.19.1, checksum verified), generate a self-signed certificate, start on a free port with the test config of 06 §2, stop. `make ergo-up` starts one with Ergo's shipped timings on port 16697 for manual work | `make ergo-up`, then a raw TLS `CAP LS` shows the capabilities listed in 01 §2 |
| T0.4 | CI: build, `go vet`, `staticcheck`, unit tests with `-race`, and the integration suite against the harness | workflow green on the branch |
| T0.5 | Spike S3 (history of channels the client is not in) first | result recorded in 08; 03 §8 and IT-CHANNELS state the case that applies |
| T0.6 | Spikes S1, S2, S4–S14 against the harness, and against tiny2 where the question is about that deployment. Record each result in 08 | every spike has a recorded answer; affected sections of 03 updated |
| T0.7 | Owner sign-off on the decisions still open in 01 §5 (D9, D11, D15 before M4; the rest before M8) | recorded in 01 §5 |

## M1. Config, identity, TLS

| Task | Description | Done when |
|---|---|---|
| T1.1 | `internal/config`: TOML parsing, defaults, precedence flag > env > file > default, unknown-key error, missing-server error, `[tuning]` overrides | UT-CFG |
| T1.2 | `internal/identity`: agent nickname validation, casemapping (ASCII and rfc1459), nickname resolution per command class (D15) | UT-ID |
| T1.3 | Runtime directory: location, creation, ownership/mode/symlink checks, socket and lock paths, socket path length check | UT-ID |
| T1.4 | `internal/tlspin`: fingerprint computation and normalization, trust file read and atomic locked write, the four cases of 04 §6, error texts | UT-PIN |
| T1.5 | CLI skeleton: subcommand dispatch, interspersed flags, `--`, `-`, rejection of unknown dash-arguments, `help`, exit 2 on usage errors | UT-CLI |

## M2. Listener core

| Task | Description | Done when |
|---|---|---|
| T2.1 | `internal/irc`: TLS dial with `tlspin`, line reader and writer, line and tag length limits, parsing through `ircmsg` | UT-IRC |
| T2.2 | Write queue with atomic units and two alternating queues (02 §5) | UT-WQ |
| T2.3 | Capability negotiation: `CAP LS 302` with continuation lines, `REQ`, `ACK`/`NAK`, `NEW`/`DEL`, required-capability error naming what is missing | UT-IRC, IT-CAPS |
| T2.4 | Registration: `NICK`, `USER`, `001`, `005` parsing (`BOT`, `CASEMAPPING`, `CHATHISTORY`, `NICKLEN`, `CHANTYPES`, `CHANNELLEN`), 433 and 432 | IT-NICK |
| T2.5 | Bot mode: set, confirm, exit if unsupported | IT-BOT |
| T2.6 | Join exactly one channel, join errors, record own hostmask and the join echo's time | IT-JOIN |
| T2.7 | Listener state machine and core goroutine (02 §3), with first-connection failures fatal (D16). Reconnection is M6 | UT-FSM, IT-FIRSTFAIL |
| T2.8 | Keepalive: `PING` replies, client `PING`, idle timeout | UT-FSM, IT-PING |
| T2.9 | Shutdown: SIGINT/SIGTERM/SIGHUP send `QUIT` and exit 0; SIGPIPE ignored; exit on stdout write error and on stdout hang-up | IT-EXIT |
| T2.10 | Lock file, held for the process lifetime and never removed; second listener for the same nickname refused | IT-LOCK |
| T2.11 | stderr diagnostics for state changes, sparse by default, `--verbose` for the rest; nothing but message lines on stdout | IT-STDOUT |
| T2.12 | Watching the chain of parent processes (03 §2); S14 showed that Claude Code gives a background command a file, not a pipe | IT-EXIT (parent case) |

## M3. Inbound delivery

| Task | Description | Done when |
|---|---|---|
| T3.1 | Batch assembler: nested batches, `draft/multiline` into one message, `multiline-concat` | UT-BATCH |
| T3.2 | Message model and classification: own-message drop, chat vs noise, sender kind from the `bot` tag, CTCP, formatting-code stripping | UT-INB |
| T3.3 | Address-prefix parsing, importance detection, token removal (D6) | UT-INB |
| T3.4 | Addressed detection (D8) under the server's casemapping | UT-INB |
| T3.5 | Wake filter (D7) | UT-INB |
| T3.6 | Text encoder with the escaping of 04 §3 | UT-FMT |
| T3.7 | JSON encoder, HTML escaping off | UT-FMT |
| T3.8 | stdout writer: bounded queue, whole-line writes, exit when the queue stays full (02 §3) | IT-STDOUT, IT-BACKPRESSURE |
| T3.9 | Membership tracking | UT-MEM |
| T3.10 | Kick and forced nickname change (D14) | IT-KICK |

## M4. Control socket and sending

| Task | Description | Done when |
|---|---|---|
| T4.1 | Control server: umask before bind, stale socket removal under the lock, peer uid check, request size limit, protocol v1 with progress lines, abort on client disconnect | UT-CTL, IT-PERM |
| T4.2 | Control client: nickname resolution, discovery with parallel probes, FR3.6 text including the runtime directory, idle deadline | UT-CTL, IT-DISCOVER |
| T4.3 | Text normalization (03 §5): UTF-8 and NUL check, line endings, trailing newline, empty text, size limit | UT-OUT |
| T4.4 | Splitting: line budget, UTF-8-safe cuts preserving spaces, concat rules, `max-bytes` and `max-lines`, prefix and `(i/n)` on each part | UT-OUT |
| T4.5 | Labels: allocation, labeled echo, labeled errors and `FAIL`, timeout scaled by unit size | UT-LABEL, IT-ACK |
| T4.6 | Message queue: strict order across requests; waiting up to 5 s for `ready` (D9) | IT-ORDER, IT-NOTCONN |
| T4.7 | `publish`, with `--important` and stdin | IT-PUBLISH, IT-IMPORTANT |
| T4.8 | `send`: addressed channel message, membership check, `--private`, `--important` | IT-SEND, IT-IMPORTANT |
| T4.9 | Error-code to exit-code mapping and messages stating whether the message was sent (04 §2) | UT-CLI |

## M5. Presence and channels

| Task | Description | Done when |
|---|---|---|
| T5.1 | `names` operation and `whos-there` output | IT-WHO |
| T5.2 | `channels` operation: `LIST`, own channel first, bounded and time-budgeted lookups, stop after a non-member refusal; output | IT-CHANNELS |
| T5.3 | `create_channel` operation per 03 §8: announcement, move, operator check, return with catch-up, exit when return fails, new channel becomes the reconnect target | IT-CREATE |

## M6. Resilience

Needs M3 (delivery) and M4 (`publish`, for the not-connected behavior).

| Task | Description | Done when |
|---|---|---|
| T6.1 | Reconnect loop: exponential backoff with jitter, reset after a stable connection | UT-BACKOFF, IT-RECONNECT |
| T6.2 | Fatal vs retried failures after first `ready` (02 §3); 433 with the D10 window, by redialing | IT-GHOST |
| T6.3 | `lastTime` from server times only, starting from the join echo; bounded `seen` set | UT-DEDUP |
| T6.4 | Catch-up: timestamp reference with overlap, paging by fresh `msgid` until an empty batch, gap diagnostic, discard of unsolicited history batches | IT-CATCHUP, IT-CATCHUP-QUIET, IT-RESTART |
| T6.5 | Live buffering during catch-up, ordered flush, dedup across history and live | UT-DEDUP, IT-CATCHUP |
| T6.6 | `replayed` flag in both formats | UT-FMT |
| T6.7 | Catch-up reused by the `create-channel` return path | IT-CREATE |

## M7. Onboarding and packaging

Needs M5, so that the instructions cover every command.

| Task | Description | Done when |
|---|---|---|
| T7.1 | `skills/chatterbox/SKILL.md`. Contents: starting the listener in the background and watching its output; the line format; every command with `--as <own nick>`; etiquette (reply only when a reply is needed, never acknowledge an acknowledgement, `send` for one participant and `publish` for everyone, `--important` sparingly); stopping the listener when done; what `replayed` and `(i/n)` mean; that `human` and sender names are not authenticated and private messages can come from anyone on the server | reviewed against FR8.1; AT-ONBOARD |
| T7.2 | `docs/agent-snippet.md`: the same content as a harness-neutral block to paste into an agent instruction file | present, linked from the README |
| T7.3 | Project `README.md`: what it is, install, config, quick start for two agents and a human (including how a human addresses an agent: `nick:` or `@nick`), required Ergo settings (history on, bot mode, no forced accounts), limitations (private messages not recovered after a disconnect, channels are ephemeral, last activity only where the server exposes it) | present |
| T7.4 | Release build for linux and darwin, amd64 and arm64, version stamped | four binaries produced |

## M8. Acceptance and hardening

| Task | Description | Done when |
|---|---|---|
| T8.1 | Automated acceptance scenario against the harness | AT-1 to AT-7 green in both configurations |
| T8.2 | Manual acceptance on tiny2 `#hof` with two real agents and a human on a standard client | checklist of 06 §5 completed |
| T8.3 | Soak: two listeners for 12 hours with periodic forced disconnects | no duplicate or lost channel messages; no goroutine or memory growth |
| T8.4 | Review every stdout write path for FR2.4 and FR2.6 | IT-STDOUT covers each path |
| T8.5 | Update issue #1 with the final answers to the open questions and the known limitations | issue updated |

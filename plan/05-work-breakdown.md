# 05. Work breakdown

Each task lists what it implements and how it is known to be done. Test identifiers refer to [06-testing-and-acceptance.md](06-testing-and-acceptance.md).

## M0. Bootstrap and spikes

| Task | Description | Done when |
|---|---|---|
| T0.1 | Create the Go module, directory layout of 02 §2, `Makefile` targets `build`, `test`, `test-integration`, `lint` | `make build` produces `bin/chatterbox`; `chatterbox version` prints a version |
| T0.2 | CI workflow: build, `go vet`, `staticcheck`, unit tests with `-race` | workflow green on the branch |
| T0.3 | Local Ergo harness in `test/ergo/`: script that fetches a pinned Ergo 2.19.x release, generates a self-signed certificate, starts it on a random port with a test config (history on, multiline on, no accounts required), and stops it | `make ergo-up` then a raw TLS `CAP LS` shows the same capabilities as tiny2 |
| T0.4 | Run spikes S1–S12 against the harness and, where the question is about deployment configuration, against tiny2. Record results in `08-risks-and-spikes.md` | every spike has a recorded answer; decisions D1, D2, D4 confirmed or revised |
| T0.5 | Get sign-off on D1–D5 from the issue owner | answers recorded in issue #1 |

## M1. Config, identity, TLS

| Task | Description | Done when |
|---|---|---|
| T1.1 | `internal/config`: parse TOML, defaults, precedence flag > env > file > default, unknown-key error, missing-server error | UT-CFG |
| T1.2 | `internal/identity`: nickname validation, ASCII and rfc1459 casemapping, nick resolution for `listen` | UT-ID |
| T1.3 | Runtime directory creation and permission checks, socket and lock path derivation | UT-ID |
| T1.4 | `internal/tlspin`: fingerprint computation, pin file read and atomic locked write, the four-step verification of 04 §6, mismatch error text | UT-PIN |
| T1.5 | CLI skeleton: subcommand dispatch, `help`, usage errors with exit 2, `--` and `-` handling | UT-CLI |

## M2. Listener core

| Task | Description | Done when |
|---|---|---|
| T2.1 | `internal/irc`: TLS dial using `tlspin`, line reader and writer, 512-byte and tag-length limits, parsing through `ircmsg` | UT-IRC |
| T2.2 | Capability negotiation: `CAP LS 302` with continuation lines, `CAP REQ`, `ACK`/`NAK`, required-capability check with a clear error naming what is missing | UT-IRC, IT-CAPS |
| T2.3 | Registration: `NICK`, `USER`, `001`, `005` parsing (`BOT`, `CASEMAPPING`, `CHATHISTORY`, `NICKLEN`, `CHANTYPES`, `CHANNELLEN`, `MSGREFTYPES`), 433 and 432 handling | IT-NICK |
| T2.4 | Bot mode: set `+B`, confirm, exit if unsupported | IT-BOT |
| T2.5 | Join exactly one channel, join error handling, record own hostmask, reject a second channel at the CLI | IT-JOIN, UT-CLI |
| T2.6 | Listener state machine and core goroutine of 02 §3 (without backoff and catch-up, which are M6) | UT-FSM |
| T2.7 | Keepalive: server `PING` replies, client `PING`, idle timeout | UT-FSM, IT-PING |
| T2.8 | Clean shutdown on SIGINT, SIGTERM, SIGHUP: `QUIT`, exit 0. Exit on closed stdout, including hang-up polling | IT-EXIT |
| T2.9 | Lock file: refuse a second listener for the same nickname; replace stale sockets | IT-LOCK |
| T2.10 | stderr diagnostics for every state change; nothing but message lines on stdout | IT-STDOUT |

## M3. Inbound delivery

| Task | Description | Done when |
|---|---|---|
| T3.1 | Batch assembler: nested batches, `draft/multiline` to one message with `multiline-concat` | UT-BATCH |
| T3.2 | Message model and classification: own-message drop, chat-vs-noise, sender kind from `bot` tag, CTCP handling, formatting-code stripping | UT-INB |
| T3.3 | Importance detection and token removal (D6) | UT-INB |
| T3.4 | Addressed detection (D8) under the server's casemapping | UT-INB |
| T3.5 | Wake filter `all` and `addressed` (D7) | UT-INB |
| T3.6 | Text encoder with the escaping of 04 §3 | UT-FMT (golden files) |
| T3.7 | JSON encoder | UT-FMT (golden files) |
| T3.8 | Single stdout writer; whole-line writes; exit on write error | IT-STDOUT |
| T3.9 | Membership tracking from names, `JOIN`, `PART`, `QUIT`, `KICK`, `NICK` | UT-MEM |
| T3.10 | Kick and forced nick change handling (D14) | IT-KICK |

## M4. Control socket and sending

| Task | Description | Done when |
|---|---|---|
| T4.1 | `internal/control` server: socket creation with `0600` in a `0700` directory, peer uid check, request size limit, one request per connection, JSON protocol v1 | UT-CTL, IT-PERM |
| T4.2 | `internal/control` client: listener resolution (`--as`, env, auto-detect), FR3.6 error text, ambiguity error, deadline | UT-CTL, IT-DISCOVER |
| T4.3 | `internal/outbound`: line budget from hostmask, UTF-8-safe cutting, multiline batch construction, splitting over `max-bytes` and `max-lines`, prefix repeated per part, size cap | UT-OUT (table and property tests) |
| T4.4 | Label allocation and correlation: labeled echo, labeled error numerics, `FAIL`, timeout | UT-LABEL, IT-ACK |
| T4.5 | Outgoing queue: strictly sequential sends across control clients | IT-ORDER |
| T4.6 | `publish` command, including `--important` and stdin | IT-PUBLISH |
| T4.7 | `send` command: addressed channel message, membership check, `--private`, `--important` | IT-SEND |
| T4.8 | Mapping of control error codes to exit codes and messages (04 §2) | UT-CLI |

## M5. Presence and channels

| Task | Description | Done when |
|---|---|---|
| T5.1 | `names` operation: labeled `WHO`, bot flag from the `BOT` letter; `whos-there` text and JSON output | IT-WHO |
| T5.2 | `channels` operation: labeled `LIST`, bounded `CHATHISTORY LATEST` lookups, unknown handling; `channels` output | IT-CHANNELS |
| T5.3 | `create_channel` operation per D2, including the revert path and catch-up state reset | IT-CREATE |

## M6. Resilience

| Task | Description | Done when |
|---|---|---|
| T6.1 | Reconnect loop with exponential backoff and jitter; reset after stable connection; first connect uses the same loop | UT-BACKOFF, IT-RECONNECT |
| T6.2 | Classification of fatal and retried failures (02 §3), including 433 on reconnect with the 5-minute bound (D10) | IT-GHOST |
| T6.3 | Tracking of `lastMsgid`, `lastTime` and the bounded recent-id set | UT-DEDUP |
| T6.4 | Catch-up: `CHATHISTORY AFTER` with pagination, `msgid` then `timestamp` fallback, replay cap, discard of unsolicited history batches | IT-CATCHUP |
| T6.5 | Live buffering during catch-up, ordered flush, dedup across history and live | UT-DEDUP, IT-CATCHUP |
| T6.6 | Control requests fail with `not_connected` outside `ready` | IT-NOTCONN |
| T6.7 | `replayed` flag in both output formats | UT-FMT |

## M7. Onboarding and packaging

| Task | Description | Done when |
|---|---|---|
| T7.1 | `skills/chatterbox/SKILL.md`: starting the listener in the background, watching its output, line format, the commands, setting `CHATTERBOX_NICK`, etiquette (reply only when a reply is needed, do not acknowledge acknowledgements, use `send` for one participant and `publish` for everyone, reserve `--important`) | reviewed against FR8.1; AT-ONBOARD |
| T7.2 | `docs/agent-snippet.md`: the same content as a block to paste into an agent instruction file, harness-neutral | present and linked from README |
| T7.3 | Project `README.md`: what it is, install, config, quick start for two agents and a human, limitations (no private-message catch-up, ephemeral channels) | present |
| T7.4 | Release build: `make release` for linux and darwin, amd64 and arm64, version stamped | four binaries produced |

## M8. Acceptance and hardening

| Task | Description | Done when |
|---|---|---|
| T8.1 | Automated acceptance scenario, steps 1–7, against the Ergo harness | AT-1 to AT-7 green |
| T8.2 | Manual acceptance on tiny2 `#hof` with two real agents and a human on a standard client | checklist in 06 §5 completed |
| T8.3 | Soak: two listeners for 12 hours with periodic forced disconnects | no duplicate or lost channel messages, no goroutine or memory growth |
| T8.4 | Review of every stdout write path for FR2.4 and FR2.6 | `IT-STDOUT` covers each path |
| T8.5 | Update issue #1 with final answers to the open questions and known limitations | issue updated |

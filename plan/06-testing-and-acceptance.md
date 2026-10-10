# 06. Testing and acceptance

## 1. Layers

| Layer | Runs against | Purpose |
|---|---|---|
| Unit (`UT-*`) | nothing external | parsing, splitting, classification, formatting, config, pinning, state machine |
| Integration (`IT-*`) | a real Ergo 2.19 started by `test/ergo/` | every protocol interaction, one behavior per test |
| Fault injection | Ergo behind a test TCP proxy that can cut or stall the connection | reconnection, catch-up, timeouts |
| Acceptance (`AT-*`) | Ergo harness, the built binary as a black box | the issue's acceptance scenario |
| Manual | tiny2 `#hof`, a human on a standard IRC client | final confirmation of FR7 and FR8 |

Integration and acceptance tests run the compiled `chatterbox` binary as subprocesses and read its stdout and stderr, so they test what an agent sees.

## 2. Test helpers

- **Ergo launcher**: starts a private Ergo per test package on a free port with a fresh self-signed certificate and a temporary data directory.
- **Plain client**: a minimal IRC client in the test code that negotiates no capabilities at all. It stands in for "a human using any standard IRC client" (FR7.3): if it can read and post, nothing beyond standard IRC is required.
- **Cutting proxy**: a TCP proxy between a listener and Ergo that can drop the connection, black-hole it (to trigger ping timeout), and restore it.
- **Listener handle**: starts `chatterbox listen` with isolated `XDG_CONFIG_HOME` and `XDG_RUNTIME_DIR`, exposes stdout lines as a channel, stderr as a log, and methods to wait for `joined`.

## 3. Unit tests

| ID | Covers |
|---|---|
| UT-CFG | precedence, defaults, unknown keys, missing server, `--config` |
| UT-ID | nickname validation and rejection of path characters, casemapping, resolution order, runtime dir permission refusal |
| UT-PIN | fingerprint value matches `openssl`, first-use pin, match, mismatch message, configured fingerprint, CA-valid path pins nothing, concurrent writers |
| UT-CLI | flag parsing per command, repeated `--channel` rejected, `-` and `--`, exit-code mapping |
| UT-IRC | line parsing, tags and escaping, `CAP LS` continuation, `005` parsing, over-long lines |
| UT-FSM | state transitions, fatal vs retried classification, keepalive timeout |
| UT-BATCH | multiline assembly, concat, nested in chathistory, interleaved batches, unterminated batch on disconnect |
| UT-INB | own-message drop, noise drop, `bot` tag, CTCP, formatting strip, `[IMPORTANT]` with and without address prefix, addressed rules 1–3 including nick-as-substring non-matches and casemapped matches, both filters |
| UT-FMT | golden lines for text and JSON, escaping of every special character, invalid UTF-8, a property test that no output line contains a line terminator |
| UT-MEM | membership through join, part, quit, kick, nick change |
| UT-CTL | request and response encoding, size limit, version check, discovery with zero, one, many and stale sockets |
| UT-OUT | fits in one line; newline forces multiline; long line concat at UTF-8 boundary; `max-bytes` and `max-lines` splits; prefix on each part; size cap; property test: reassembling the parts yields the original text |
| UT-LABEL | echo, error, `FAIL`, timeout, unrelated labeled traffic |
| UT-BACKOFF | growth, cap, jitter bounds, reset |
| UT-DEDUP | id set bound, history and live overlap, ordering of flush |

## 4. Integration tests

| ID | Scenario | Requirement |
|---|---|---|
| IT-CAPS | listener against a server config with multiline disabled exits with an error naming the capability | FR1.1 |
| IT-NICK | second listener with a taken nickname exits 4 with the nickname in the message and does not appear under another name | FR1.4 |
| IT-BOT | plain client sees the listener flagged as bot in `WHO` | FR1.2 |
| IT-JOIN | listener appears in the channel's names; `--channel` twice is rejected | FR1.1, FR1.3 |
| IT-PING | black-holed connection is detected within the idle timeout | FR1.6 |
| IT-EXIT | SIGTERM: plain client sees `QUIT`; SIGKILL: plain client sees the quit; closing the stdout pipe ends the listener | FR1.5 |
| IT-LOCK | second listener for the same nickname on the same machine fails; a stale socket is replaced | FR5.3 |
| IT-STDOUT | during connect, join, others joining and leaving, nick changes, topic changes, own sends, reconnect: stdout stays empty; stderr has the diagnostics | FR2.4, FR2.6 |
| IT-RECV | plain client posts one line: exactly one stdout line with sender, `human`, channel, time | FR2.1, FR2.2, FR7.2 |
| IT-RECV-ML | a multiline message of 5 lines from another listener is one stdout line in text and in JSON | FR2.1, FR2.3 |
| IT-FILTER | with `--wake addressed`: unaddressed is silent, addressed prints, important unaddressed prints | FR2.5, FR3.3 |
| IT-KICK | kicked listener exits 6 with the reason | FR1.5 |
| IT-PERM | socket mode is `0600`, directory `0700`; a connection from another uid is refused (skipped unless the test can switch uid) | FR6.3 |
| IT-DISCOVER | no listener: exit 3 with the start command in the message; one: auto-detected with a stderr notice; two: exit 3 listing both; `--as` and env select correctly | FR3.6, FR5.2, FR5.3 |
| IT-ACK | channel set to moderated so the listener cannot speak: `publish` exits 6 with the server's reason | FR3.5 |
| IT-ORDER | 20 concurrent `publish` commands, some of them split: every message's parts are contiguous in the channel | FR3.4 |
| IT-PUBLISH | `publish` is seen by another listener and by the plain client; stdin form; exit 0 | FR3.1, FR7.1 |
| IT-SEND | `send B` appears in channel as `B: ...`, flagged addressed for B only; unknown nick exits non-zero; `--private` reaches only B | FR3.2 |
| IT-IMPORTANT | `send --important` shows `[IMPORTANT]` to the plain client and `important` to listeners | FR3.3 |
| IT-MULTILINE | a 3 000-byte, 30-line message arrives as one message at a listener and as separate lines at the plain client; a 10 000-byte message arrives as several messages in order | FR3.4, FR7.3 |
| IT-WHO | two listeners and a plain client: `whos-there` lists three with correct kinds | FR4.1 |
| IT-CHANNELS | two occupied channels with messages: both listed with names; own channel has a last-activity time | FR4.2 |
| IT-CREATE | new name: listener moves and is operator; existing name: exit 6 and the listener is still in its old channel | FR4.3 |
| IT-RECONNECT | cut the connection: stderr shows backoff, the listener rejoins, presence is restored | FR1.6 |
| IT-GHOST | black-hole then restore while the old session still holds the nickname: the listener retries and gets its name back without taking another | FR1.4, FR1.6 |
| IT-CATCHUP | cut the connection, post 250 messages including multiline ones, restore: all 250 printed once, in order, flagged replayed; messages posted during catch-up are printed once after them | FR1.6 |
| IT-NOTCONN | `publish` while the listener is in backoff exits 7 | FR3.5 |
| IT-TLS | first connect to the self-signed harness pins and connects; restart of Ergo with a new certificate is refused with exit 5 and the explanation; plain-text port is never tried | FR6.2 |
| IT-CONFIG | server settings from the config file; flags override them | FR6.1 |

## 5. Acceptance

### Automated, mirroring the issue's scenario

| ID | Step | Check |
|---|---|---|
| AT-1 | Listeners `agentA` and `agentB` start on `#accept`, each with its own runtime directory to model two machines (and once sharing a runtime directory to model one machine). A plain client `human` joins | all three in the channel's names |
| AT-2 | `whos-there` as A and as B | three entries; `agentA` and `agentB` are `bot`, `human` is `human` |
| AT-3 | A: `publish "hello"` | B's stdout has exactly one new line from `agentA`, kind `bot`; the plain client received the text; A's stdout has nothing |
| AT-4 | B runs with `--wake addressed`... A runs with `--wake addressed`; B: `send --important agentA "stop"` | A's stdout has one line flagged `to-you,important`; the plain client sees `agentA: [IMPORTANT] stop` |
| AT-5 | Plain client posts `status?` | A (restarted with `--wake all`) and B each print one line with kind `human` |
| AT-6 | Cut A's connection through the proxy; B publishes 3 messages and the plain client posts 2; restore | A prints those 5 once each, in order; a further message is printed once |
| AT-7 | SIGTERM to A's listener | `whos-there` as B no longer lists `agentA`; `publish --as agentA` exits 3 |
| AT-ONBOARD | A fresh agent session given only `skills/chatterbox/SKILL.md` starts a listener, reacts to an addressed message and stays silent on an unaddressed one | observed manually |

### Manual, on tiny2 `#hof`

1. Configure `~/.config/chatterbox/config.toml` for tiny2; first connect pins the certificate and prints the fingerprint; compare it with `openssl s_client` output.
2. Start two agents (for example Claude Code and Pi) with the skill; a human joins `#hof` with a standard client.
3. Walk through steps 2–7 of the scenario. For step 6, drop the network on the agent's machine for a minute.
4. The human confirms: agent messages are readable, important ones are visibly marked, agents show as bots in the client's user list or `WHOIS`.
5. Leave both agents running on a real task for an hour and review the channel log for needless replies (FR8.1 etiquette).

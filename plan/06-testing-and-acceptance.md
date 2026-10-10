# 06. Testing and acceptance

## 1. Layers

| Layer | Runs against | Purpose |
|---|---|---|
| Unit (`UT-*`) | nothing external | parsing, splitting, classification, formatting, config, trust, state machine |
| Integration (`IT-*`) | a real Ergo 2.19 started by `test/ergo/` | every protocol interaction, one behavior per test |
| Fault injection | Ergo behind a test TCP proxy | reconnection, catch-up, timeouts |
| Acceptance (`AT-*`) | Ergo harness, the built binary as a black box | the issue's acceptance scenario |
| Manual | tiny2 `#hof`, a human on a standard IRC client | final confirmation of FR7 and FR8 |

Integration and acceptance tests run the compiled binary as subprocesses and read its stdout and stderr: they test what an agent sees. Both run in CI (T0.4).

Where the tests are:

| Tests | Location |
|---|---|
| `UT-CFG`, `UT-ID`, `UT-PIN`, `UT-CLI`, `UT-IRC`, `UT-WQ`, `UT-BATCH`, `UT-INB`, `UT-FMT`, `UT-CTL`, `UT-OUT` | next to the code, `internal/<package>/*_test.go` |
| `UT-BACKOFF`, `UT-DEDUP` | `internal/listener/units_test.go` |
| `UT-FSM`, `UT-LABEL`, `UT-MEM` | `internal/listener/listener_test.go`, against a scripted fake server (`fake_test.go`) that can show what Ergo cannot be made to: missing tokens, unlabeled failures, silence, short history pages |
| `IT-*` | `test/e2e/`: `listen_test.go`, `delivery_test.go`, `send_test.go`, `presence_test.go`, `resilience_test.go` |
| `AT-1` to `AT-7` | `test/e2e/acceptance_test.go` |

`make test` runs the unit tests with the race detector; `make test-integration` the rest (about two minutes, most of it waiting for the server's flood control).

## 2. Test helpers

- **Ergo launcher**: a private Ergo per test package, on a free port, with a fresh self-signed certificate and a temporary data directory. Test configuration, differing from Ergo's defaults only where stated:
  - flood control (fakelag) **left on**, as in the default, so timing behavior is the real one;
  - `chathistory-maxmessages` lowered to 20, so catch-up pagination is exercised with tens of messages;
  - idle timeouts lowered to a few seconds (ping after 4 s, disconnect after 8 s);
  - an operator account for the test code (to kick, set channel modes, force a nickname change);
  - variants: multiline disabled (IT-CAPS), history disabled.
- **Plain client**: a minimal IRC client in the test code that negotiates no capabilities at all. It stands in for "a human using any standard IRC client" (FR7.3).
- **Cutting proxy**: a TCP proxy between a listener and Ergo that counts accepted connections and can drop, black-hole and restore the connection.
- **Listener handle**: starts `chatterbox listen` with isolated `XDG_CONFIG_HOME` and `CHATTERBOX_RUNTIME_DIR` and a `[tuning]` section with short timings; exposes stdout lines, stderr, and waits for `joined`. The tuned keepalive is 2 s, not shorter: with flood control on, a `PING` every 400 ms is more than the server reads, and the connection never leaves the throttled state.
- Runtime directories of the tests are made directly under the temporary directory: the usual per-test directories are too long for a socket path.

## 3. Unit tests

| ID | Covers |
|---|---|
| UT-CFG | precedence, defaults, unknown keys, missing server, `--config`, `[tuning]` |
| UT-ID | nickname validation, casemapping, resolution per command class (posting commands never auto-detect), runtime dir refusal for wrong owner, mode or symlink, socket path too long |
| UT-PIN | fingerprint equals the `openssl` value; all accepted spellings; first-use pin; `ca` recorded for a valid chain; later invalid chain refused for a `ca` entry; pin match and mismatch text; configured fingerprint; `trust_on_first_use = false`; concurrent writers |
| UT-CLI | flags before, between and after positionals; `send b --important x` marks important; `publish hello --as a` uses `a`; unknown dash-argument is exit 2; `--` and `-`; repeated `--channel` rejected; error-code to exit-code mapping |
| UT-IRC | line parsing, tags and escaping, `CAP LS` continuation, `CAP DEL`, `005` parsing, over-long lines |
| UT-WQ | a batch is written contiguously while PING, PONG and queries are pending; alternation between the two queues; order within the message queue |
| UT-FSM | state transitions, fatal vs retried before and after first `ready`, keepalive timeout |
| UT-BATCH | multiline assembly, concat, nesting inside chathistory, interleaved batches, unterminated batch at disconnect |
| UT-INB | own-message drop, noise drop, `bot` tag, CTCP, formatting strip; address prefix forms; `[IMPORTANT]` with and without prefix; addressed rules including `@nick`, a bare mention not matching, `@agentA-2` not matching `agentA`, casemapped matches; both filters, including important addressed to someone else not printed under `addressed` |
| UT-FMT | golden lines for text and JSON; every escaped character; invalid UTF-8; `<>&` unescaped in JSON; `replayed`; property test that no output line contains any line terminator |
| UT-MEM | membership through join, part, quit, kick, nick change |
| UT-CTL | encoding, size limit, version check, progress lines, discovery with zero, one, several and stale sockets, probe timeout |
| UT-OUT | normalization (CRLF, trailing newline, NUL, invalid UTF-8, empty); one line; newline forces a batch; long line cut after a space, concat tag, no blank concat line; `max-bytes` and `max-lines` including prefix and marker; `(i/n)`; size cap; property test: stripping prefixes and markers and joining the parts gives the original text |
| UT-LABEL | echo, error numeric, `FAIL`, timeout scaled by unit size, unrelated labeled traffic |
| UT-BACKOFF | growth, ceiling, jitter bounds, reset |
| UT-DEDUP | `seen` bound, overlap between history and live buffer, flush order, `lastTime` never taken from the local clock |

## 4. Integration tests

| ID | Scenario | Requirement |
|---|---|---|
| IT-CAPS | against a server without multiline, the listener exits with an error naming the capability | FR1.1 |
| IT-FIRSTFAIL | unknown host, and closed port: the listener exits 9 promptly with the reason | FR1.1, D16 |
| IT-NICK | a second listener with a taken nickname exits 4 naming the nickname; no other nickname appears on the server | FR1.4 |
| IT-BOT | the plain client sees the listener flagged as a bot in `WHO` | FR1.2 |
| IT-JOIN | the listener is in the channel's names; `--channel` twice is rejected | FR1.1, FR1.3 |
| IT-PING | a black-holed connection is detected within the (shortened) idle timeout | FR1.6 |
| IT-EXIT | SIGTERM: the plain client sees the `QUIT`. SIGKILL: the plain client sees the quit. Reader closes the stdout pipe on a silent channel: the listener sends `QUIT` and exits 0 (not killed by SIGPIPE). stdout redirected to a file and the parent killed: behavior as decided by S14 | FR1.5 |
| IT-LOCK | second listener for the same nickname fails; a stale socket is replaced; 20 listeners started at once for one nickname leave exactly one running | FR5.3 |
| IT-STDOUT | during connect, join, others joining and leaving, nick and topic changes, own sends, reconnect: stdout stays empty; stderr has the diagnostics | FR2.4, FR2.6 |
| IT-BACKPRESSURE | a reader that stops reading: the listener exits 1 after the (shortened) limit and its nickname leaves | FR1.5 |
| IT-RECV | the plain client posts one line: exactly one stdout line with sender, `human`, channel, time | FR2.1, FR2.2, FR7.2 |
| IT-RECV-ML | a 5-line multiline message from another listener is one stdout line, in text and in JSON | FR2.1, FR2.3 |
| IT-FILTER | under `--wake addressed`: unaddressed is silent; `nick:` and `@nick` print; a bare mention is silent; unaddressed `[IMPORTANT]` typed by the plain client prints; important addressed to another agent is silent | FR2.5, FR3.3 |
| IT-KICK | a kicked listener exits 6 with the reason; a forced nickname change (operator) exits 6 | FR1.5 |
| IT-PERM | socket mode `0600`, directory `0700`, socket never visible with wider mode. The uid check itself is unit-tested with an injected credential function; a real cross-user attempt is part of the manual run | FR6.3 |
| IT-DISCOVER | no listener: exit 3 with the start command and the directory searched. One listener: `whos-there` auto-detects with a notice; `publish` without a nickname exits 3 and posts nothing. Two listeners: both listed. `--as` and the environment variable select correctly | FR3.6, FR5.2, FR5.3 |
| IT-WRONGAGENT | listeners A and B; B's is killed; B's `publish` without `--as` posts nothing and exits 3; with `--as agentB` exits 3 with the start command | FR5.3 |
| IT-ONECONN | with listeners behind the counting proxy, a run of every control command leaves the accepted-connection count at one per listener; the commands succeed with an empty config directory | FR3 intro |
| IT-ACK | channel made moderated so the listener cannot speak: `publish` exits 6 with the server's reason and "not sent" | FR3.5 |
| IT-ORDER | 10 concurrent `publish` commands, some split: each message's parts are contiguous and in order in the channel; a `whos-there` issued meanwhile returns before the sends finish | FR3.4 |
| IT-PUBLISH | `publish` is seen by another listener and by the plain client; stdin form with a trailing newline arrives as one line; exit 0 | FR3.1, FR7.1 |
| IT-SEND | `send B` appears in the channel as `B: ...`, flagged `to-you` at B only; unknown nickname exits 6; `--private` reaches only B | FR3.2 |
| IT-IMPORTANT | `send --important` and `publish --important` show `[IMPORTANT]` to the plain client and the `important` flag to listeners | FR3.3 |
| IT-MULTILINE | with flood control on: a 3 000-byte, 30-line message is one message at a listener and separate lines, in order, at the plain client; a 1 500-byte single line arrives as one line; a 100-line message is acknowledged; a 10 000-byte message arrives as `(1/n)`…`(n/n)` in order and the command exits 0 with progress keeping the client alive; a message over the size cap is refused; killing the client mid-way stops the remaining parts | FR3.4, FR3.5, FR7.3 |
| IT-WHO | two listeners and the plain client: `whos-there` lists three with the right kinds | FR4.1 |
| IT-CHANNELS | two occupied channels with messages, the listener in one: both names listed; own channel has a last-activity time; the other channel shows exactly what S3 established (a time, or unknown). Called three times in a row, each returns within the time budget | FR4.2 |
| IT-CREATE | free name: announcement seen in the old channel, the listener is operator in the new one, and after a forced reconnect it is back in the new one. Listed existing name: exit 6, still in the old channel. Secret existing channel (not listed): the listener returns to the old channel, prints what was posted there meanwhile, exit 6 | FR4.3, FR1.3 |
| IT-RECONNECT | connection cut: the listener rejoins and presence is restored; stderr shows it | FR1.6 |
| IT-GHOST | black-hole, then restore while the server still holds the old session: the listener gets its nickname back without ever using another | FR1.4, FR1.6 |
| IT-CATCHUP | cut; 50 messages posted (paced, some multiline, several sharing a timestamp); restore: all 50 printed once, in order, flagged `replayed`; messages posted during catch-up are printed once, after them | FR1.6 |
| IT-CATCHUP-QUIET | a listener that has seen no message since joining is cut; 3 messages; restore: all 3 printed | FR1.6 |
| IT-RESTART | Ergo restarted (history lost) while a listener is connected: it reconnects, prints nothing twice, and delivers new messages | FR1.6 |
| IT-NOTCONN | `publish` while the listener is in backoff exits 7 with "not sent" after the short wait; `publish` during a blip shorter than the wait succeeds | FR3.5 |
| IT-TLS | first connect to the self-signed harness pins and connects; Ergo restarted with a new certificate is refused with exit 5 and the explanation; with `trust_on_first_use = false` the first connect is refused; a host recorded as `ca` is refused when it presents a self-signed certificate | FR6.2 |
| IT-CONFIG | server settings from the config file; flags override them | FR6.1 |

## 5. Acceptance

### Automated, mirroring the issue's scenario

The scenario runs as one continuous session, twice: configuration **X** with agentA on `--wake all`, and configuration **Y** with agentA on `--wake addressed`. agentB is on `all` in both. Step 4 is the reason Y exists; the issue's step 5 ("both agents receive it") only holds for unaddressed text when both are on `all`.

| ID | Step | Check |
|---|---|---|
| AT-1 | Listeners agentA and agentB start on `#accept`: once with separate runtime directories (two machines), once sharing one (same machine). A plain client `human` joins | all three in the channel's names |
| AT-2 | `whos-there` as A and as B | three entries; agentA and agentB are `bot`, `human` is `human` |
| AT-3 | A: `publish "hello"` | B's stdout has exactly one new line, from agentA, kind `bot`; the plain client received the text; A's stdout has nothing |
| AT-4 | B: `send --important agentA "stop"` | A prints one line flagged `to-you,important` (X and Y); the plain client sees `agentA: [IMPORTANT] stop`. In Y additionally: the plain client posts unaddressed `[IMPORTANT] everyone stop` and A prints it, which is the case that only the importance bypass can explain |
| AT-5 | The plain client posts `status?` | X: A and B each print one line with kind `human`. Y: B prints it and A does not; the plain client then posts `agentA: status?` and A prints it with kind `human` |
| AT-6 | A's connection is cut through the proxy; B publishes 3 messages and the plain client posts 2 (in Y all five carry `agentA:` in the text; B uses `publish`, because `send` to a participant who is not in the channel is refused); the connection is restored | A prints those 5 once each, in order, flagged `replayed`; one further message is printed once |
| AT-7 | SIGTERM to A's listener | `whos-there` as B no longer lists agentA; `publish --as agentA` exits 3 |
| AT-ONBOARD | A fresh agent session given only `skills/chatterbox/SKILL.md` starts a listener, answers an addressed question, stays silent on an unaddressed remark and on a "thanks" | observed manually |

### Manual, on tiny2 `#hof`

1. Configure `~/.config/chatterbox/config.toml` for tiny2. The first connect pins the certificate and prints the fingerprint; compare it with `openssl s_client` output.
2. Start two agents (for example Claude Code and Pi) with the skill; a human joins `#hof` with a standard client.
3. Walk through steps 2–7 of the scenario. For step 6, drop the network on the agent's machine for a minute.
4. The human confirms: agent messages are readable, important ones are visibly marked, split messages are followable, agents show as bots in the client's user list or `WHOIS`.
5. End one agent's session without stopping its listener and note how long its nickname stays present (R8).
6. From a second user account on the same machine, try `publish --as <agent>`: it must fail.
7. Leave both agents on a real task for an hour and review the channel log for needless replies (FR8.1 etiquette).

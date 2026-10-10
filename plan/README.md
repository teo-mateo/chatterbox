# chatterbox implementation plan

Plan for [issue #1](https://github.com/teo-mateo/chatterbox/issues/1): `chatterbox`, a command-line IRC client that lets AI coding agents talk to each other and to a human over a standard IRCv3 server.

Status: **revision 3, implemented**. Revision 2 came out of one adversarial review ([09-review-log.md](09-review-log.md)); revision 3 folds in what the spikes and the implementation found (list below). The answers to the issue's five open questions (D1–D5) were accepted by the owner on 2026-10-10; the remaining decisions still await sign-off, and the code follows the plan's proposal for each. What is done and what is open is in [05-work-breakdown.md](05-work-breakdown.md).

## What changed in revision 3

Found by running the spikes and building the thing:

- **`draft/event-playback` is requested.** Without it Ergo's history replays joins and quits as chat lines from a pseudo-user, which the listener would have printed after every reconnect (S4; 03 §1).
- **Timers follow the server's flood control.** Ergo accepts a batch at once and then stops reading the connection for half a second per line. The acknowledgement deadline, the deadline of every query and the idle timeout are counted from an estimate of when the server has caught up, and no keepalive is sent meanwhile (S9; 03 §2, §5). The formula of revision 2 would have reported a short message sent after a long one as "possibly sent".
- **A lost history is detected.** The reference for catch-up has an id as well as a time, so "history does not reach back", including after a server restart, is reported instead of passing silently (03 §6).
- **Ergo does not label the failure of a multiline batch** (S6). It is attributed to the one message in flight.
- **Presence when the agent dies.** Claude Code gives a background command a file for stdout and stderr together, so nothing ever fails to write. The listener watches the chain of processes that started it (S14; 03 §2).
- **Last activity** is the newest chat message among a channel's last 50 history entries, since history also holds joins (03 §8).
- A third dependency, `golang.org/x/sys/unix` (02 §2).

## Files

| File | Contents |
|---|---|
| [01-scope-and-decisions.md](01-scope-and-decisions.md) | Scope, additions, answers to the issue's open questions, interpretations of ambiguous requirements, what is known about the target server |
| [02-architecture.md](02-architecture.md) | Process model, package layout, listener state machine, control socket, write queue |
| [03-irc-protocol.md](03-irc-protocol.md) | Capability negotiation, registration, receiving, sending, multiline, catch-up, presence |
| [04-cli-and-output.md](04-cli-and-output.md) | Commands, exit codes, stdout line formats, config file, identity, certificate trust |
| [05-work-breakdown.md](05-work-breakdown.md) | Milestones M0–M8 with itemized tasks and done-criteria |
| [06-testing-and-acceptance.md](06-testing-and-acceptance.md) | Test layers, harness, the acceptance scenario as automated and manual tests |
| [07-traceability.md](07-traceability.md) | Every FR mapped to design, tasks and tests, with whether it is met in full |
| [08-risks-and-spikes.md](08-risks-and-spikes.md) | Things to verify before building on them, and risks |
| [09-review-log.md](09-review-log.md) | Review findings and what was done about each |

## Identifiers

- `FRn.m`: requirement from the issue. `OQn`: open question from the issue.
- `Dn`: a decision made by this plan (01).
- `Sn`: a spike, something to verify against a real Ergo 2.19 or a real harness (08).
- `Tm.n`: a task in milestone `m` (05).
- `UT`, `IT`, `AT`: unit, integration, acceptance tests (06).

## Milestones

| # | Milestone | Delivers | Needs |
|---|---|---|---|
| M0 | Sign-off, bootstrap, spikes | Go module, CI, local Ergo harness, spike results | none |
| M1 | Config, identity, TLS | Config loading, nickname resolution, socket paths, certificate trust | M0 |
| M2 | Listener core | `listen` connects, takes the nick, sets `+B`, joins, exits cleanly | M1 |
| M3 | Inbound delivery | One line per message on stdout, text and JSON, wake filter | M2 |
| M4 | Control socket and sending | `publish`, `send`, `--important`, multiline, splitting | M2 |
| M5 | Presence and channels | `whos-there`, `channels`, `create-channel` | M4 |
| M6 | Resilience | Reconnect with backoff, catch-up, dedup | M3, M4 |
| M7 | Onboarding and packaging | Agent skill and snippet, README, release build | M5 |
| M8 | Acceptance and hardening | Automated acceptance scenario, manual run on tiny2 `#hof` | all |

M3 and M4 can run in parallel after M2; M5 and M6 after M4.

## Where the plan does not meet the issue as written

- **FR4.2**: last activity is available only for the listener's own channel (D4; confirmed by S3).
- **FR5.2**: auto-detection of the listener is limited to the read-only commands, to satisfy FR5.3 (D15).
- **FR1.6**: private messages sent during a disconnect are not recovered (confirmed by S5); channel messages are.
- **FR1.5**: an agent that dies without stopping its listener is noticed through the stdout pipe or the parent chain; this is verified for Claude Code only (S14, R8).

## Sign-off

Accepted by the owner on 2026-10-10, answering the issue's open questions:

| Decision | Answer |
|---|---|
| D1 (OQ1) | `send` is a channel message addressed to the nick; `--private` is the option |
| D2 (OQ2) | `create-channel` moves the listener into the new channel (alternative B not taken) |
| D3 (OQ3) | the nickname is always explicit |
| D4 (OQ4) | last activity is the newest history timestamp where the server exposes it, otherwise unknown |
| D5 (OQ5) | Go |

Still needed. The code implements each as the plan proposes; 01 §5 says what changes if one goes the other way:

| Decisions |
|---|
| D9 (what "accepted" means), D11 (size cap), D15 (no auto-detection for posting) |
| D7, D8, D12, D17, and the additions listed in 01 §1 |

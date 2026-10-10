# chatterbox implementation plan

Plan for [issue #1](https://github.com/teo-mateo/chatterbox/issues/1): `chatterbox`, a command-line IRC client that lets AI coding agents talk to each other and to a human over a standard IRCv3 server.

Status: **revision 2**, revised after one adversarial review ([09-review-log.md](09-review-log.md)). Awaiting owner sign-off on the decisions below. No spike has been run yet.

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

- **FR4.2**: last activity is expected to be available only for the listener's own channel (D4, S3).
- **FR5.2**: auto-detection of the listener is limited to the read-only commands, to satisfy FR5.3 (D15).
- **FR1.6**: private messages sent during a disconnect are not recovered (S5); channel messages are.
- **FR1.5**: a listener whose agent died may stay present when its output is not a pipe (S14, R8).

## Sign-off needed

| When | Decisions |
|---|---|
| Before any code | D5 (Go) |
| Before M1 | D3 (nickname always explicit) |
| Before M4 | D1 (`send` is an addressed channel message), D9 (what "accepted" means), D11 (size cap), D15 (no auto-detection for posting) |
| Before M5 | D2 (`create-channel` moves the listener) or its alternative B, D4 with the result of S3 |
| Before M8 | D7, D8, D12, D17, and the additions listed in 01 §1 |

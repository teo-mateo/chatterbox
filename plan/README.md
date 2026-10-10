# chatterbox implementation plan

Plan for [issue #1](https://github.com/teo-mateo/chatterbox/issues/1): `chatterbox`, a command-line IRC client that lets AI coding agents talk to each other and to a human over a standard IRCv3 server.

Status: **draft, revision 1** (not yet reviewed).

## Files

| File | Contents |
|---|---|
| [01-scope-and-decisions.md](01-scope-and-decisions.md) | Scope, proposed answers to the issue's open questions, interpretations of ambiguous requirements, what is known about the target server |
| [02-architecture.md](02-architecture.md) | Process model, package layout, listener state machine, control socket protocol |
| [03-irc-protocol.md](03-irc-protocol.md) | Capability negotiation, registration, receiving, sending, multiline, chathistory, presence |
| [04-cli-and-output.md](04-cli-and-output.md) | Commands, flags, exit codes, stdout line formats, config file, identity resolution, TLS pinning |
| [05-work-breakdown.md](05-work-breakdown.md) | Milestones M0–M8 with itemized tasks and done-criteria |
| [06-testing-and-acceptance.md](06-testing-and-acceptance.md) | Test layers, test harness, the acceptance scenario as automated and manual tests |
| [07-traceability.md](07-traceability.md) | Every FR mapped to design section, tasks and tests |
| [08-risks-and-spikes.md](08-risks-and-spikes.md) | Things to verify against Ergo before building on them, and risks with mitigations |

## Identifiers

- `FRn.m`: requirement from the issue.
- `OQn`: open question from the issue.
- `Dn`: a decision made by this plan (01). Decisions that answer an open question need the issue owner's sign-off before the milestone that depends on them.
- `Sn`: a spike, something to verify against a real Ergo 2.19 (08).
- `Tm.n`: a task in milestone `m` (05).
- `UT`, `IT`, `AT`: unit, integration, acceptance tests (06).

## Milestones at a glance

| # | Milestone | Delivers | Depends on |
|---|---|---|---|
| M0 | Bootstrap and spikes | Go module, CI, local Ergo test harness, spike results recorded | none |
| M1 | Config, identity, TLS | Config loading, nick resolution, socket paths, certificate pinning | M0 |
| M2 | Listener core | `listen` connects, takes the nick, sets `+B`, joins, exits cleanly | M1 |
| M3 | Inbound delivery | One line per message on stdout, text and JSON, wake filter | M2 |
| M4 | Control socket and sending | `publish`, `send`, `--important`, multiline, splitting | M2 |
| M5 | Presence and channels | `whos-there`, `channels`, `create-channel` | M4 |
| M6 | Resilience | Reconnect with backoff, chathistory catch-up, dedup | M3 |
| M7 | Onboarding and packaging | Agent skill and snippet, README, release build | M4 |
| M8 | Acceptance and hardening | Automated acceptance scenario, manual run on tiny2 `#hof` | all |

M3 and M4 can proceed in parallel after M2. M5 and M6 can proceed in parallel after M4 and M3.

## Sign-off needed before implementation

Decisions D1–D5 in [01-scope-and-decisions.md](01-scope-and-decisions.md) answer the issue's five open questions. D1 (visibility of `send`) and D2 (meaning of `create-channel`) change user-visible behavior the most.

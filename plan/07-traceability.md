# 07. Traceability

Every requirement of issue #1: where it is designed, which tasks build it, which tests prove it, and whether the plan meets it in full.

| Requirement | Summary | Design | Tasks | Tests | Met |
|---|---|---|---|---|---|
| FR1.1 | `listen` connects over TLS, takes the nick, joins | 03 §1, 04 §1 | T2.1–T2.7 | IT-CAPS, IT-FIRSTFAIL, IT-JOIN, AT-1 | yes |
| FR1.2 | marks itself `+B` | 03 §1 | T2.5 | IT-BOT, AT-2 | yes, if S10 holds |
| FR1.3 | single channel per listener | 02 §1, 04 §1, D2 | T2.6, T5.3 | UT-CLI, IT-JOIN, IT-CREATE | yes |
| FR1.4 | nick in use: exit with a clear error | 03 §1, D10 | T2.4, T6.2 | IT-NICK, IT-GHOST | yes |
| FR1.5 | stays until terminated; presence reflects reality | 03 §2, 02 §3 | T2.9, T2.12, T3.8, T3.10 | IT-EXIT, IT-BACKPRESSURE, IT-KICK, AT-7 | yes for termination; gap when the agent dies and stdout is not a pipe (R8, S14) |
| FR1.6 | reconnect with backoff; catch up through chathistory, in order, no duplicates | 02 §3, 03 §6 | T6.1–T6.5 | IT-PING, IT-RECONNECT, IT-CATCHUP, IT-CATCHUP-QUIET, IT-RESTART, UT-DEDUP, AT-6 | yes for channel messages; private messages are not recovered (S5) |
| FR2.1 | one line per message, multiline included | 03 §4, 04 §3 | T3.1, T3.6, T3.7 | UT-BATCH, UT-FMT, IT-RECV-ML | yes |
| FR2.2 | sender, bot/human, channel, time, addressed, important | 04 §3, D6, D8 | T3.2–T3.4, T3.6 | UT-INB, UT-FMT, IT-RECV | yes |
| FR2.3 | `--json` | 04 §3 | T3.7 | UT-FMT, IT-RECV-ML | yes |
| FR2.4 | own messages and server noise not printed | 03 §4 | T3.2, T2.11 | UT-INB, IT-STDOUT | yes |
| FR2.5 | wake filter | 04 §3, D7, D8, D12 | T3.5 | UT-INB, IT-FILTER, AT-4, AT-5 | yes, as interpreted by D7 and D8 |
| FR2.6 | diagnostics on stderr only | 04 §3 | T2.11, T3.8, T8.4 | IT-STDOUT | yes |
| FR3 (intro) | send commands use the listener's socket, never their own connection | 02 §1, 02 §4 | T4.1, T4.2 | IT-ONECONN | yes |
| FR3.1 | `publish` | 03 §5 | T4.7 | IT-PUBLISH, AT-3 | yes |
| FR3.2 | `send <nick>` | 03 §5, D1 | T4.8 | IT-SEND | yes, as decided by D1 |
| FR3.3 | `--important`: always printed by the recipient, visible to humans | D6, D7, 03 §5 | T3.3, T3.5, T4.7, T4.8 | IT-IMPORTANT, IT-FILTER, AT-4 | yes |
| FR3.4 | multiline for long messages; split over the limit, in order | 03 §5, 02 §5, D11 | T2.2, T4.3, T4.4, T4.6 | UT-OUT, UT-WQ, IT-MULTILINE, IT-ORDER | yes, with the added size cap (D11) |
| FR3.5 | zero only after acceptance; otherwise non-zero with an explanation | D9, 03 §5, 04 §2 | T4.5, T4.6, T4.9 | UT-LABEL, IT-ACK, IT-NOTCONN, IT-MULTILINE | yes, read strictly (D9) |
| FR3.6 | no listener: fail and say how to start one | 02 §4 | T4.2 | IT-DISCOVER | yes |
| FR4.1 | `whos-there` with bot/human | 03 §8 | T5.1 | IT-WHO, AT-2 | yes |
| FR4.2 | `channels` with names and last activity | 03 §8, D4 | T5.2 | IT-CHANNELS | **partly**: names for all; last activity for the listener's own channel, and for others only if S3 shows the server allows it |
| FR4.3 | `create-channel` | 03 §8, D2 | T5.3, T6.7 | IT-CREATE | yes, as decided by D2 |
| FR5.1 | nickname on connect, explicit with `--as` | 04 §5, D3 | T1.2 | UT-ID | yes |
| FR5.2 | locate the listener: `--as`, env, auto-detect | 02 §4, D15 | T1.2, T4.2 | UT-ID, UT-CTL, IT-DISCOVER | **narrowed**: auto-detection only for `whos-there` and `channels` (D15) |
| FR5.3 | several agents per machine, no accidental cross-posting | 02 §4, 04 §5, D15 | T1.3, T2.10, T4.2 | IT-LOCK, IT-DISCOVER, IT-WRONGAGENT, AT-1 | yes |
| FR6.1 | config file, flag overrides | 04 §4 | T1.1 | UT-CFG, IT-CONFIG | yes |
| FR6.2 | TLS required; pin on first connection; refuse a mismatch with an explanation | 04 §6 | T1.4, T2.1 | UT-PIN, IT-TLS | yes |
| FR6.3 | socket only for the owning user | 02 §4 | T1.3, T4.1 | IT-PERM, UT-CTL, manual step 6 | yes |
| FR7.1 | a human reads and posts with a standard client | 03 §7 | T4.7 | IT-PUBLISH, AT-3, manual | yes |
| FR7.2 | human messages delivered the same way, flagged human | 03 §4 | T3.2 | IT-RECV, AT-5 | yes ("human" means "not marked as a bot") |
| FR7.3 | nothing required beyond standard IRC | 03 §7, 06 §2 | constraint on T4.4, T4.7, T4.8 | every test using the plain client; IT-MULTILINE | yes |
| FR8.1 | instruction block for agents | 05 M7 | T7.1, T7.2 | AT-ONBOARD, manual step 7 | yes |

## Open questions

| Question | Decision | Settled by |
|---|---|---|
| OQ1 visibility of `send` | D1 | sign-off T0.7; S5 |
| OQ2 meaning of `create-channel` | D2, or alternative B | sign-off T0.7; S8 |
| OQ3 default nickname | D3 | sign-off T0.1 |
| OQ4 last activity | D4 | S3 and sign-off T0.5 |
| OQ5 language | D5 | sign-off T0.1 |

## Acceptance scenario

| Step | Tests | Note |
|---|---|---|
| 1 | AT-1 | same-machine and two-machine variants |
| 2 | AT-2 | |
| 3 | AT-3 | |
| 4 | AT-4 | configuration Y adds an unaddressed important message |
| 5 | AT-5 | "both agents receive it" holds in configuration X; in Y agentA receives it when addressed |
| 6 | AT-6 | |
| 7 | AT-7 | |

# 07. Traceability

Every requirement of issue #1, where it is designed, which tasks build it, which tests prove it.

| Requirement | Summary | Design | Tasks | Tests |
|---|---|---|---|---|
| FR1.1 | `listen` connects over TLS, takes nick, joins | 03 §1, 04 §1 | T2.1–T2.5 | IT-CAPS, IT-JOIN, AT-1 |
| FR1.2 | marks itself `+B` | 03 §1 | T2.4 | IT-BOT, AT-2 |
| FR1.3 | single channel per listener | 02 §1, 04 §1, D2 | T2.5 | UT-CLI, IT-JOIN |
| FR1.4 | nick in use: exit with clear error | 03 §1, D10 | T2.3, T6.2 | IT-NICK, IT-GHOST |
| FR1.5 | stays until terminated; presence reflects reality | 03 §2 | T2.8, T3.10 | IT-EXIT, IT-KICK, AT-7 |
| FR1.6 | reconnect with backoff, chathistory catch-up, ordered, no duplicates | 02 §3, 03 §6 | T6.1–T6.5 | IT-RECONNECT, IT-CATCHUP, UT-DEDUP, AT-6 |
| FR2.1 | one line per message, including multiline | 03 §4, 04 §3 | T3.1, T3.6, T3.7 | UT-BATCH, UT-FMT, IT-RECV-ML |
| FR2.2 | sender, bot/human, channel, time, addressed, important | 04 §3, D6, D8 | T3.2–T3.4, T3.6 | UT-INB, UT-FMT, IT-RECV |
| FR2.3 | `--json` | 04 §3 | T3.7 | UT-FMT, IT-RECV-ML |
| FR2.4 | own messages and server noise not printed | 03 §4 | T3.2, T2.10 | UT-INB, IT-STDOUT |
| FR2.5 | wake filter | 04 §3, D7, D12 | T3.5 | UT-INB, IT-FILTER |
| FR2.6 | diagnostics on stderr only | 04 §3 | T2.10, T3.8, T8.4 | IT-STDOUT |
| FR3 (intro) | send commands use the listener's socket, never their own connection | 02 §1, 02 §4 | T4.1, T4.2 | IT-DISCOVER, AT-7 |
| FR3.1 | `publish` | 03 §5 | T4.6 | IT-PUBLISH, AT-3 |
| FR3.2 | `send <nick>` | 03 §5, D1 | T4.7 | IT-SEND |
| FR3.3 | `--important`: always printed, visible to humans | D6, D7, 03 §5 | T3.3, T3.5, T4.7 | IT-IMPORTANT, IT-FILTER, AT-4 |
| FR3.4 | multiline for long messages, split over the limit, in order | 03 §5, D11 | T4.3, T4.5 | UT-OUT, IT-MULTILINE, IT-ORDER |
| FR3.5 | zero only after acceptance | D9, 03 §5 | T4.4, T4.8, T6.6 | UT-LABEL, IT-ACK, IT-NOTCONN |
| FR3.6 | no listener: fail and say how to start one | 02 §4 | T4.2 | IT-DISCOVER |
| FR4.1 | `whos-there` with bot/human | 03 §8 | T5.1 | IT-WHO, AT-2 |
| FR4.2 | `channels` with names and last activity | 03 §8, D4 | T5.2 | IT-CHANNELS |
| FR4.3 | `create-channel` | 03 §8, D2 | T5.3 | IT-CREATE |
| FR5.1 | nickname on connect, explicit with `--as` | 04 §5, D3 | T1.2 | UT-ID |
| FR5.2 | locate listener: `--as`, env, auto-detect | 02 §4 | T4.2 | UT-CTL, IT-DISCOVER |
| FR5.3 | several agents per machine, no accidental cross-posting | 02 §4, 04 §5 | T1.3, T2.9, T4.2 | IT-LOCK, IT-DISCOVER, AT-1 |
| FR6.1 | config file, flag overrides | 04 §4 | T1.1 | UT-CFG, IT-CONFIG |
| FR6.2 | TLS required, pin on first use, refuse mismatch | 04 §6 | T1.4, T2.1 | UT-PIN, IT-TLS |
| FR6.3 | socket only for the owning user | 02 §4 | T4.1 | IT-PERM |
| FR7.1 | human reads and posts with a standard client | 03 §7 | T4.6 | IT-PUBLISH, AT-3, manual |
| FR7.2 | human messages delivered the same way, flagged human | 03 §4 | T3.2 | IT-RECV, AT-5 |
| FR7.3 | nothing required beyond standard IRC | 03 §7, 06 §2 (plain client) | n/a (constraint) | every IT using the plain client, IT-MULTILINE |
| FR8.1 | instruction block for agents | 05 M7 | T7.1, T7.2 | AT-ONBOARD, manual step 5 |

## Open questions

| Question | Decision | Verified by |
|---|---|---|
| OQ1 visibility of `send` | D1 | sign-off T0.5; S5 |
| OQ2 meaning of `create-channel` | D2 | sign-off T0.5; S8 |
| OQ3 default nickname | D3 | sign-off T0.5 |
| OQ4 last activity | D4 | sign-off T0.5; S3, S4 |
| OQ5 language | D5 | sign-off T0.5 |

## Acceptance scenario

| Step | Tests |
|---|---|
| 1 | AT-1 |
| 2 | AT-2 |
| 3 | AT-3 |
| 4 | AT-4 |
| 5 | AT-5 |
| 6 | AT-6 |
| 7 | AT-7 |

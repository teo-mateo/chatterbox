# 09. Review log

## Review 1, 2026-10-10: adversarial review of revision 1 against issue #1

Done by a separate agent that read the issue and every plan file, with instructions to find where the plan fails the requirements. It checked the IRCv3 multiline and chathistory specifications and Ergo 2.19's default configuration; claims about Ergo's internal behavior were from memory and are treated as spikes here, not facts.

| # | Severity | Finding | Disposition in revision 2 |
|---|---|---|---|
| 1 | blocker | FR4.2 last activity is met only for the listener's own channel; the test and traceability hid that | Accepted. D4 now says FR4.2 is expected to be partly met, S3 runs first and goes to the owner with options; IT-CHANNELS asserts the non-member case; traceability says "partly" |
| 2 | major | Exported `CHATTERBOX_NICK` does not persist in Claude Code's shell; auto-detection posts as the wrong agent when one's own listener has died | Accepted. New D15: posting commands never auto-detect; instructions use `--as` everywhere; IT-WRONGAGENT. Narrows FR5.2, so it is on the sign-off list |
| 3 | major | Standard `flag` parsing turns flags placed after positionals into message text | Accepted. Interspersed flags, `--`, unknown dash-arguments rejected (04 §1); UT-CLI cases |
| 4 | major | Fixed 15 s client deadline and 10 s acknowledgement contradict server flood control; `channels` lookups can not finish | Accepted. Progress lines with an idle deadline, acknowledgement timer scaled by unit size and started after the last line, abort on client disconnect, time-budgeted lookups, harness keeps flood control on, S9 widened |
| 5 | major | Trust on first use was not limited to first use: a CA-valid server could later be replaced by a self-signed one and pinned | Accepted. Trust file records `ca` or a fingerprint per server; `trust_on_first_use` switch; fingerprint normalization; XDG path; `VerifyConnection` |
| 6 | major | `PING`, `PONG` or queries could land inside an open multiline batch | Accepted. Write queue with atomic units (02 §5), UT-WQ |
| 7 | major | Catch-up by old `msgid` can silently lose messages; "short batch" is not a safe stop; local clock mixed with server time | Accepted. Timestamp reference with overlap, paging by fresh `msgid` until empty, server times only, starting from the join echo; S13; IT-CATCHUP-QUIET, IT-RESTART |
| 8 | major | The 1 000-message replay cap narrowed FR1.6 and could not work as described | Accepted. Cap removed (D17). Whether a detected gap should also appear on stdout is left to the owner |
| 9 | major | Dead-harness detection only works for pipes; Go's default SIGPIPE handling kills the process before the clean exit | Accepted in part. SIGPIPE ignored, hang-up polling kept. The non-pipe case depends on how harnesses run background processes: new spike S14 and task T2.12, with the gap stated in traceability |
| 10 | major | `create-channel`: return path, missed messages, reconnect target, stranded peers, untested revert | Accepted in part. 03 §8 now specifies all of these and announces the move; IT-CREATE covers the secret-channel revert. The move itself (D2) is kept as the default, with the reviewer's preferred validate-only variant offered to the owner as alternative B |
| 11 | major | Removing the lock file on exit breaks mutual exclusion | Accepted. Lock file never deleted; umask before bind; IT-LOCK race case |
| 12 | minor | "Mention anywhere" as addressed is too wide and fails for backquoted names; everyone printing addressed-important messages; split parts unmarked | Accepted. D8: prefix or `@nick`; agent nicknames restricted so the boundary is unambiguous. D7: bypass only for unaddressed important messages. `(i/n)` markers |
| 13 | minor | AT-4 could pass without the importance bypass; garbled row; restart in the middle of the scenario | Accepted. Scenario runs continuously in two configurations; unaddressed important message added |
| 14 | minor | D9 is stricter than FR3.5's wording; retries can duplicate; error codes without exit codes | Accepted. D9 on the sign-off list, 5 s wait for `ready`, every failure says whether the message was sent, exit-code table completed |
| 15 | minor | The 64 KiB cap is an added requirement; arithmetic off | Accepted. D11 on the sign-off list, can be disabled, wording corrected |
| 16 | minor | Outbound text hygiene unspecified | Accepted. Normalization rules in 03 §5; concat constraints; UT-OUT |
| 17 | minor | Text/JSON inconsistencies; raw separator characters in the file; HTML escaping in JSON; "address prefix" undefined | Accepted. One text rule for both formats, examples fixed, prefix defined in D6, `SetEscapeHTML(false)` |
| 18 | minor | No back-pressure policy; first-connect retry loop never tells the agent about a typo; connection throttle | Accepted. Bounded stdout queue with exit; D16 makes first-connection failures fatal; R13 |
| 19 | minor | Runtime directory differs with environment; socket path length; serial probes; uid test never runs | Accepted. Environment-independent runtime directory with override, length check, parallel probes with timeout, uid check unit-tested and verified manually |
| 20 | minor | Tests not runnable as written (timings, pagination, operator actions); integration tests not in CI | Accepted. `[tuning]` overrides, harness configuration in 06 §2, operator account, integration suite in CI, missing tests added |
| 21 | minor | Sequencing and traceability errors | Accepted. M6 depends on M3 and M4, M7 on M5, sign-offs moved to where they gate work, stale-socket handling moved to T4.1, IT-ONECONN for the FR3 intro |
| 22 | minor | Security notes missing: unauthenticated `human`, private messages from anyone | Accepted. Stated in 03 §4, T7.1, T7.3, R11 |
| 23 | nit | Unrequested additions not listed; noisy stderr; redial vs re-`NICK`; `CAP NEW/DEL`; no version in responses | Accepted. Additions table in 01 §1, sparse diagnostics with `--verbose`, redial stated, `CAP` handling stated, `v` in responses |

### Not adopted

- Binding auto-detection to a shared ancestor process (part of finding 2). Agents started from one terminal multiplexer share ancestors, so the check would not separate them reliably. D15 removes the need.
- Making D2 validate-only by default (part of finding 10). It is offered as alternative B instead, because a command named `create-channel` that creates nothing answers OQ2 less directly. Owner's call.

## Revision 3, 2026-10-10: findings of the spikes and the implementation

Not a review: these are the places where revision 2 was wrong or silent, found by running it. Each is described where it now lives.

| # | Finding | Where |
|---|---|---|
| 1 | Without `draft/event-playback`, Ergo's history contains joins and quits as chat lines from `HistServ`; revision 2 deliberately did not request the capability | 03 §1, S4 |
| 2 | The acknowledgement formula (10 s plus 0.5 s per line of the unit) measured the wrong unit: the server's stall follows a batch, so it hits the next thing sent. Queries had a fixed deadline that the same stall would exceed | 03 §5, S9 |
| 3 | The idle timeout and the keepalive did not allow for that stall; a keepalive faster than the server's command rate keeps the connection throttled for good | 03 §2 |
| 4 | "Server restarted, history gone" was listed as undetectable. With the listener's own join in history it is detectable | 03 §6, R6 |
| 5 | Ergo's `FAIL BATCH` carries no label | 03 §5, S6 |
| 6 | `CHATHISTORY LATEST * 1` returns the newest entry, which is often a join | 03 §8 |
| 7 | Claude Code runs background commands with a file for stdout and stderr; T2.12 was conditional on this | 03 §2, S14 |
| 8 | `send` to an agent that is momentarily disconnected is refused ("not in the channel"). That is what 03 §5 specifies, and it is why AT-6 in configuration Y has agentB use `publish`; worth knowing when reading D1 | 06 §5 |
| 9 | An unquoted `#channel` is a shell comment. The CLI's usage errors and the agent instructions say so | 04 §1 |

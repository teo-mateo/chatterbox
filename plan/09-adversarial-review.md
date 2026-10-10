# 9. Adversarial review of revision 1, and what was done about it

Revision 1 of this plan was reviewed by an independent agent instructed to attack it, given the nine
plan files and the verbatim text of issue #1. It returned 4 blockers, 12 majors, 20 minors, 5 nits, and
a verdict of *build-with-changes*. This file records each finding worth recording, the disposition, and
where the fix lives — so a later reader can see the plan was changed by evidence rather than by taste,
and so nobody quietly reverts a fix.

**Nothing in this file is optional reading.** Where the disposition is *rejected*, the counter-argument
is given so the next reviewer can attack it directly.

## 9.1 Blockers — all accepted

| # | Finding | Disposition | Where fixed |
|---|---|---|---|
| **F1** | `create-channel` order was self-contradictory (`01` FR4.3 said JOIN-then-PART, `01#OQ2` said PART-then-JOIN, and `UT-164`/`IT-219` asserted the destructive order). PART-first means a refused JOIN (`473`/`474`/`471`/`405`/`403`, or a `FORWARD=f` diversion) leaves the listener channel-less, exits 12, and the abandoned channel vanishes behind it | **Accepted.** JOIN → verify the joined channel equals the requested one → PART. Refusal ⇒ exit 5, previous channel untouched | `01` FR4.3 + OQ2, `02#2.3a` (new catch-all), `04` command row + exit table, `05#T-5.5`, `06#UT-165, IT-235, E2E-10` |
| **F2** | FR3.2 says "sends a message to one named participant"; the plan's default send is a channel broadcast, and a typo'd target produced **exit 0** — `UT-142` asserted exactly that. The test defended the weakening | **Accepted with a change of shape.** Absent from NAMES ⇒ exit 18 `no_target`, nothing sent; `--force` overrides; `--dm` remains the out-of-channel route. The channel-prefixed *default* stands (it is what satisfies FR7.1), but it now fails loudly when the addressee is not there | `01` FR3.2, `04` command row + exit 18, `05#T-4.3`, `06#UT-142, IT-212` |
| **F3** | FR1.6's "in order" was broken by design: replay was buffered while live traffic printed immediately, so on any reconnect with traffic one newer line printed before older ones — and no test covered it | **Accepted.** Single hold queue for replay *and* live traffic, flushed on the end marker, ordered by first-part `time` with arrival tie-break | `03#3.6`, `01` FR1.6, `06#UT-115`, `07#R4` |
| **F4** | `05` said 124 h / 16 dev-days; the task table summed to **264 h**; headings summed to 17 days; the README said 27.5. Three numbers, one plan. And the plan's own rule ("a task is done when its tests are green in CI") was unenforceable because the fake server those tests need was scheduled for M8 | **Accepted.** Recomputed from the file: **281 h / 35 dev-days** with `fake_ircd` moved to M1 (`T-1.0`, 12 h) and `T-8.1` reduced to 4 h of extension. Headings now carry their own task sums and a heading/table mismatch is defined as a bug | `05` intro + every heading, `README#4`, `07#R13` |

## 9.2 Majors

| # | Finding | Disposition | Where fixed |
|---|---|---|---|
| **F5** | FR2.6 says diagnostics never reach stdout; `--state` records do | **Accepted as a documented exception, not a move.** The alternative (stderr) breaks the property that makes them useful: an agent reading only stdout cannot distinguish "nobody talked" from "listener dead". Rules added: opt-in, no flag token so they cannot wake, counted in the I3 buffer, `IT-208b` pins the schema. This is the one blocker/major the plan pushes back on, deliberately | `04#4.4`, `06#IT-208b, E2E-14` |
| **F6** | Ack timeout had no cancellation: exit 6 could be returned for a message that then posted, so the agent retried and the channel got it twice | **Accepted.** Cancellation on CLI disconnect, `delivery=uncertain` if already written, exit-6 hint stripped of retry advice, `--timeout` precedence defined | `03#3.4`, `03#3.9`, `04` exit 6, `05#T-4.3`, `06#IT-232`, `07#R15` |
| **F7** | The `[important] ` prefix broke the addressed matcher: `send --important pi …` yields body `[important] pi: …`, which fails an anchored `^pi` match, so FR3.3's own acceptance message arrived flagged `+` and not `@+` | **Accepted.** Strip a leading marker (and STATUSMSG prefix) before matching; E2E-4 asserts exactly `@+` | `01` FR2.5, `06#UT-137, E2E-4` |
| **F8** | FR6.2's security property was delegated to the agent: `03#3.8` printed the presented fingerprint "so the human's next step is one copy-paste" and `08` said "or read it from the error text" — i.e. a first-contact MITM is self-pinned by the victim | **Accepted.** Non-TTY error output shows `host:port` and **no** fingerprint; `trust --fingerprint` requires an explicit value; the skill tells the agent to ask; `UT-199` asserts the non-TTY output has no fingerprint | `03#3.8`, `08` §1/§6, `04` exits 9/14, `06#UT-199`, `07#R16` |
| **F9** | FR6.3 gaps: the `/tmp` fallback (the common case, since `XDG_RUNTIME_DIR` is unset in containers/CI) was unvalidated; the socket was bound before registration, so a failed second `listen --as pi` could unlink a healthy listener's socket | **Accepted.** Directory validation (`st_uid == euid`, mode 0700, not a symlink), fallback order prefers `$XDG_STATE_HOME` over `/tmp`, socket + instance file created only after READY, unlinked only on pid+inode match | `03#3.4`, `05#T-4.1`, `06#UT-193, UT-194, IT-233`, `07#R18` |
| **F10** | Startup failures (DNS/TCP/TLS-not-pin/JOIN-refused) had no exit code and no retry policy, so FR1.5 was undefined before the first READY | **Accepted.** Exit 16 with `dns`/`tcp`/`tls` sub-labels; the same backoff applies before the first READY; `--no-retry` for CI; JOIN refusal at startup is exit 12 with the numeric | `03#3.3`, `04` exit 16, `05#T-2.1`, `06#IT-234` |
| **F11** | Ghost-nick deadlock: SIGKILL (no `QUIT`) leaves the nick for 150 s; the restarted agent got exit 10, the skill said "pick another nick", and every peer address then pointed at a ghost | **Accepted.** On `433` for a case-matching nick, wait `ping_timeout + 5 s` and retry twice; `--wait-nick`; T-0.3(b) measures whether a same-host reconnect clears sooner; the skill now says keep the nick | `03#3.3`, `04` listen flags, `08` §6, `05#T-2.2`, `07#R17` |
| **F12** | R4's own fallback conceded data loss ("documented as a limitation if two messages can share a millisecond") while `02#2.14` already measured that they do; and the msgid cursor FR1.6 depends on was the one thing never exercised | **Accepted.** T-0.3 promoted to a second M0 gate; the fallback is pre-written as `time − 1000 ms` + msgid-LRU dedup (LRU 4096 > retention 2048 absorbs the overlap); `UT-118` tests the fallback path | `05#T-0.3/gates`, `03#3.6`, `06#UT-118`, `07#R4` |
| **F13** | The cursor advanced past messages dropped by I3, and a restart skipped gap fill — so the documented recovery for dropped wakes guaranteed permanent loss | **Accepted.** Cursor advances only on a successful stdout write; persisted in `instances/<nick>.json` (a cursor is a pointer into the server's copy, not a local message store); gap fill now runs on first READY too | `03#3.6`, `01` FR1.6, `06#UT-116, UT-117, UT-195b`, `07#R11` |
| **F14** | The CI-verifiable layer was circular (fake server built to the same measurements), the live layer was unreachable from hosted runners, and the nightly probe only "opened an issue" | **Accepted.** Self-hosted LAN runner stated explicitly; **a red probe blocks tagging a release**; `LIVE-1..6` given ids so DoD item 4 is satisfiable | `06#6.7, #LIVE table`, `07#R12` |
| **F15** | `UT-113` asserted same-millisecond messages arrive "in msgid order" — an invariant the measured msgids (`_p8txu58…`, `3gutycsm…`, `bnkwjgmp…`) visibly violate | **Accepted.** Rewritten as stable order by first-part `time`, arrival tie-break, "msgid never orders"; `02#2.8` now says so | `06#UT-113`, `02#2.8` |
| **F16** | FR4.2 was answered with "we don't": `-` for every channel but one, and `E2E-9` asserted the omission | **Accepted as opt-in.** `channels --probe <n>` joins the top-N briefly to fetch activity; default off, `-` otherwise; FR4.2's boundary stated in `01`, README, issue comment | `01` FR4.2 + OQ4, `04` command row, `05#T-5.4` |
| **F17** | `create-channel` offered no way to make a channel you do not immediately abandon, and the move was a silent side effect of a command agents are told to run freely | **Partially accepted.** Move stays the default (OQ2, and `--keep` would create a channel that instantly vanishes — a lie about capability), but the order is fixed, `--dry-run` exists, stderr names the abandoned channel, and the human-facing block says it | `01#OQ2`, `04`, `08` §4b |
| **F18** | macOS binaries shipped; macOS was never built or tested; `dirs` maps config to `~/Library`, so FR6.1's documented path would not be the path read; the dependency list said 13 under a "12" heading | **Accepted.** XDG resolution by hand with `$HOME` fallbacks on every platform; `dirs` and `percent-encoding` dropped (11 crates); macOS is build-only and labelled "build verified, not smoke tested" | `03#3.11, #3.12`, `05#T-0.1, T-0.4`, `07#R19` |
| **F19** | Two required capabilities (`account-notify`, `away-notify`) belonged to features v1 explicitly excludes, so a compliant server without them got exit 13; and `draft/multiline` being required made the plain-split fallback in `04#4.7` rule 6 unreachable, while a mid-session `CAP DEL` was ignored | **Accepted.** Required set trimmed to what FRs need; those two moved to warn-only; rule 6 re-scoped to the grant-then-remove case; `CAP DEL` on a required cap forces a reconnect | `02#2.2`, `03#3.3`, `04#4.7`, `06#IT-238` |
| **F20** | `02#2.2`'s "CAP ACK always → 410" claim was load-bearing, contradicted by `06`'s own note about mis-pacing, and `UT-310` used it to forbid a legal command | **Accepted.** The claim is now scoped to *this server, this state*, the transcript is the evidence, `UT-310` asserts only "REQ is the granting step", and T-0.3(d) re-runs it | `02#2.2`, `05#T-0.3`, `06#UT-310` |

## 9.3 Minors and nits — accepted unless noted

Accepted, each with a location: the exit-code table is now the single source and is cross-checked by
`UT-197`/`UT-198` (`04#4.6`, `06`); the skill's `--dm` grammar is fixed and parse-checked (`08`, F20-adjacent);
the wake-line layout, the bogus `+--` flag token, and the missing `[important] ` in the golden fixtures
are corrected (`04#4.3`); `E2E-5` addresses the real test nicks; `iterations_max` is derived rather than
magical and is a WARN (`03#3.6`); `90` vs `96` bytes and `line_bytes` vs `max_line_bytes` are unified (`02`);
etiquette says 100 lines, not 120 (`06#6.5`); `--limit` (which does not exist) is replaced by
`--max-wakes`/`--max-wakes-window` in the listener (`07#R7`); `MODE +B` refusal has a code, a status field
and a test (`06#IT-237`); exit 4 and the new 17 are distinct back-pressures; `03#3.4`'s path tree no longer
shows `pins.toml` inside the runtime dir; `cargo audit` is flagged for a maintenance check (`05#T-0.1`);
`07#7.5` no longer retracts FR1.6's ordering promise (it is scoped to cross-channel); `whos-there` is
primed from replay so it is not mostly `?` (`05#T-3.8`); the human half of FR7 finally exists as
`skill --format human` (`08#4b`); Happy Eyeballs and `TCP_NODELAY` are specified (`03#3.3`); nick
case-folding is specified for registry, socket names and mismatch detection (`05#T-5.1`, `07#R17`).

**Noted but deferred to the post-v1 backlog (`T-8.8`)** rather than fixed: an IPv6-only smoke environment,
a man page and shell completions, and `no-implicit-names`/`cap-notify`. All three are real; none is an
FR, and revision 1's sin was silence, which the backlog issue now removes.

**Rejected outright:** making `--move` opt-in (F17 above), and moving `state` records to stderr (F5).

## 9.4 What the reviewer got wrong, recorded so nobody re-pays for it

- Its per-milestone hour claims did not all match the file (it said M0 17 h, M3 43 h, M8 39 h; the file
  said 17/43/39 before revision 2, so this one was right — but its "≈ 30 h of test work" was wrong: the
  correct figure is 76 h / 27 %, now stated in `05`).
- It judged FR1.5 "genuinely met" and separately demanded a `433` retry loop; both are consistent, but
  the framing implied a contradiction that does not exist: fatal exits on configuration-class errors are
  what make FR1.4 and FR1.5 both true.
- It proposed `--limit` "on the harness side" as R7's mitigation while simultaneously criticising the plan
  for deferring behaviour to harnesses it cannot configure. The listener-side coalescing it accidentally
  described two lines later is what got implemented in the plan.
- It claimed `E2E-5` was unsatisfiable. It was not unsatisfiable — it was *wrong*, which is worse, and the
  fix is the same. Recorded because "the test fails" and "the test passes for the wrong reason" need
  different responses.

## 9.5 Verdict and what it cost

Reviewer's verdict: *build-with-changes*; the four blockers as pre-code writing tasks; the schedule at
roughly twice what was claimed. Assessment: correct on all four blockers, correct on the estimate, and
right that the weaknesses concentrated where the plan trusted itself most — the gap-fill algorithm, the
acknowledgement path, and its own arithmetic. Revision 2's additions are ~14 h of the 281 h; the
alternatives (discovering F1–F4 in code) are not 14 h.

One process lesson worth keeping: revision 1 stated three different totals in three files. Any plan that
carries numbers should compute them from the table that owns them, which is now a rule in `05`.

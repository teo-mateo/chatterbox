# chatterbox — implementation plan for issue #1

Issue: [#1 Functional requirements: chatterbox as a CLI IRC client for agents](https://github.com/teo-mateo/chatterbox/issues/1)
Branch: `plan-qwen` (branched from `main` @ `ea26afc`)
Status: **plan, revision 2** — revision 1 was written from issue #1 plus measurements against the live
Ergo 2.19.1 server on tiny2; revision 2 is the result of an adversarial review of revision 1
(`09-adversarial-review.md`), which found 4 blockers and 12 majors and was right about all of them.
Target: v1 (M0–M8), MIT licence, single Rust binary.

---

## What this plan is for

chatterbox v1 has one job: let two or more AI coding agents coordinate in an IRC channel while
a human watches and steers with any ordinary IRC client. Every design choice below is either
traced to a functional requirement (FR) in issue #1 or to a measurement taken against the real
Ergo 2.19.1 server on `tiny2` (`192.168.0.204:6697`, `server.name=irc.heapzilla.eu`,
network `HofNet`).

**The protocol facts in `02-protocol-contract.md` were measured, not assumed.** Four of them
contradict what a reasonable implementer would guess, and each one changes the design:

| # | Measured behaviour | Design consequence |
|---|---|---|
| 1 | Ergo answers `CAP ACK` with `410 Invalid CAP subcommand`; only `CAP REQ` grants capabilities | Negotiation must be REQ-based. A naive IRCv3 client connects "successfully" with **zero** capabilities: no `server-time`, no `draft/multiline`, no `draft/chathistory`, and FR1.6/FR2.2/FR3.4 silently degrade. `CAP LIST` is asserted at startup and in `doctor`. |
| 2 | Server **strips client-sent message tags** on `PRIVMSG`, and `TAGMSG` was **not relayed** to a second client | Importance (FR3.3) **cannot** rely on a message tag. It must be carried in the message body. See `07-risks-and-open-questions.md#R1`. |
| 3 | `+B` bot usermode is advertised as `BOT=B` in `RPL_ISUPPORT`, and Ergo stamps a `bot` message tag on everything a `+B` client sends | Bot/human distinction (FR1.2, FR2.2, FR4.1) has a real, human-verifiable mechanism — but `NAMES`, `WHO` and `USERHOST` do **not** expose it, so `whos-there` needs a tag cache plus `WHOIS` (numeric `335`). |
| 4 | `CHATHISTORY` replies arrive inside a `BATCH +<id> chathistory` envelope that can **nest** `BATCH +<id2> draft/multiline`, and join/part/quit history is replayed as `PRIVMSG` from `HistServ` | Gap fill (FR1.6) needs a real batch-tree parser and a `HistServ` filter, or the listener prints server noise as if it were chat and violates FR2.4. |

## How to read this plan

| File | Contents | Read first if you are… |
|---|---|---|
| `README.md` | this index, decision summary, provenance | anyone |
| `01-scope-and-decisions.md` | FR-by-FR resolution, the 5 open questions of issue #1 answered, non-goals, definition of done | starting work |
| `02-protocol-contract.md` | measured wire behaviour, hard limits, known-good frame transcripts | implementing M1–M4 |
| `03-architecture.md` | process model, modules, control protocol, config, identity, TLS pinning | implementing M2–M6 |
| `04-cli-and-formats.md` | full command surface, output formats, exit-code and error taxonomy | implementing M3–M5, writing tests |
| `05-work-plan.md` | milestones and **itemised tasks** (T-ids) with deps, acceptance checks, estimates | writing code |
| `06-test-plan.md` | unit/fake-server/live test rig, acceptance-scenario mapping, CI | writing tests, reviewing PRs |
| `07-risks-and-open-questions.md` | risk register, residual unknowns, reversal criteria | reviewing this plan |
| `08-agent-onboarding-skill.md` | FR8.1 deliverable: the agent instruction block, plus the human-facing block | implementing M7 |
| `09-adversarial-review.md` | the review of revision 1, finding by finding, with dispositions | before you disagree with a decision |

Traceability runs both ways: every task in `05-work-plan.md` carries the FR ids it satisfies, and
every FR in `01-scope-and-decisions.md` carries the task ids and test ids that satisfy it. The
authoritative matrix is `06#6.8`; the matrix in `01` is derived from it.

## Effort, gates, and the five things revision 2 changed

**281 h ≈ 35 dev-days** of focused work across 9 phases, of which 76 h (27 %) is tests and rig. Per
phase: M0 19 · M1 51 · M2 39 · M3 47 · M4 34 · M5 24 · M6 18 · M7 13 · M8 36. Two hard gates at the end
of M0: the `rustls` pin verifier, and the gap-fill cursor (whose failure has a pre-written fallback, not
a "limitation"). A red nightly probe against the live server blocks tagging a release.

Revision 1 claimed 124 h against a task table that summed to 264 h; the review caught it, and the
numbers are now computed from the table that owns them. The other four things revision 2 fixed, all of
them blockers or near-blockers:

1. **`create-channel` order.** It now JOINs, verifies, and only then PARTs. PART-first meant a refused
   JOIN left the listener channel-less and deleted the channel the human was watching. (`01#OQ2`)
2. **Gap-fill ordering and durability.** One hold queue, so a live message can never print before an
   older backfilled one; and the cursor advances only on a successful stdout write and is persisted, so a
   dropped wake comes back instead of being silently consumed. (`03#3.6`)
3. **`send` fails when the addressee is not in the channel** (exit 18). Exit 0 for a message that reached
   nobody made FR3.2 unenforceable — and the old `UT-142` asserted the broken behaviour. (`01` FR3.2)
4. **An agent can no longer self-pin a MITM.** Pin-mismatch errors show `host:port` and withhold the
   presented fingerprint unless stderr is a TTY; pins come from a human. (`03#3.8`, `08`)
5. **The test rig moved to M1.** Every IT depended on a fake server that the old plan did not build until
   M8, which is a test suite with no test runner. (`05#T-1.0`)

## Decisions at a glance

| Topic | Decision | Detail |
|---|---|---|
| Language (OQ5) | **Rust**, edition 2024, single static binary, `rustls` + custom pin verifier, `polling` event loop, no async runtime | `01#D1` |
| Capability negotiation | `CAP LS 302` → **`CAP REQ :…`** → `NICK`/`USER` → `CAP END`; assert with `CAP LIST` | `02#2.2` |
| `send` visibility (OQ1) | Default = **channel message addressed by prefix** (`nick: body`), `--dm` for a real private message | `01#OQ1` |
| `create-channel` (OQ2) | **Create-by-join, which implies the listener moves** to the new channel; permanence is out of scope (needs ChanServ accounts) | `01#OQ2` |
| Default nickname (OQ3) | `listen` **requires** a nickname (`--as` / `CHATTERBOX_NICK` / config `defaults.nick`). No auto-derived nick | `01#OQ3` |
| Last activity in `channels` (OQ4) | `CHATHISTORY LATEST <chan> * 1` for the joined channel only; everything else prints `-` | `01#OQ4` |
| Importance (FR3.3) | Body prefix `[important] ` (human-visible, survives everything); client tag is best-effort because the server strips it | `02#2.9` |
| Send acknowledgement (FR3.5) | `echo-message` + `labeled-response`: success = echo carrying our `label`; failure = error numeric carrying it | `02#2.7` |
| Reconnect gap fill (FR1.6) | `msgid` cursor (fallback `timestamp`) + `CHATHISTORY LATEST`, msgid-LRU dedup, batch-tree parse | `03#3.6` |
| TLS (FR6.2) | TLS always on; pin store `~/.local/share/chatterbox/pins.toml` (0600); `chatterbox trust`; TOFU only behind `--tofu` on a TTY | `03#3.8` |
| Control socket (FR3, FR6.3) | Unix socket per nick, dir 0700, socket 0600, `SO_PEERCRED` uid check, JSON-lines | `03#3.4` |

## Provenance of the measurements

All measurements were taken on 2026-10-10 against `192.168.0.204:6697` (TLS, Ergo 2.19.1), from
this workstation, using raw `openssl s_client` sessions driven through a FIFO with explicit
pacing, plus one pair of concurrent sessions to observe delivery between two clients. Server-side
configuration was read from `/etc/ergo/ircd.yaml` over SSH. Test traffic used the throwaway
channels `#cbtest-plan` / `#cbtest-planb` (created by joining, removed by the server on `PART`);
`#hof` received a short announcement plus capability-probe lines and nothing afterwards.

Facts that will drift and must be re-measured rather than copied are marked **DRIFT** in
`02-protocol-contract.md`: the pinned certificate fingerprint (self-signed, expires 2027-10-10),
the exact capability list, and who is currently sitting in `#hof`.

The probe pattern (FIFO + `openssl s_client` + paced `printf`, described in
`06-test-plan.md#6.4`) is the seed of the live integration harness.

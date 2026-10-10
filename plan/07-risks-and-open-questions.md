# 7. Risks, residual unknowns, reversal criteria

Likelihood/impact are judged against a v1 delivered against Ergo 2.19.1 on the operator's own LAN by
one engineer working this plan.

## 7.1 Risk register

| ID | Risk | L | I | Mitigation | Trigger / fallback |
|---|---|---|---|---|---|
| **R1** | **Importance signalling degrades if a server relays `TAGMSG` or preserves client tags.** We chose a body marker because tags were measured to be stripped and TAGMSG non-relayed on *this* server | M | M | Body marker is authoritative and works everywhere; the tag is additive and documented advisory (`02#2.9`). A server that preserves tags changes nothing for us | If a future server preserves tags, add tag-based detection as a *second* accepted signal; do not remove the body marker (it is what the human sees) |
| **R2** | **rustls custom pin verifier costs more than planned** (self-signed leaf is rejected before we can inspect it; the `danger()` API is awkward and version-sensitive) | M | H | T-0.2 is a 4-hour spike in M0, before any architecture is committed; `docs/protocol-notes.md` records the exact working verifier | Spike not green in ½ day → switch to Go with the same module layout (`01#D1`). Cost: redo M1–M2, ~4 days. This is the one risk with a pre-agreed plan change |
| **R3** | **`draft/multiline` is a draft spec.** Ergo 2.19 parameters and the batch-id rewriting are Ergo-specific; a future Ergo or another server may differ | M | M | Capability parameter is read, never hard-coded (UT-149); local pre-flight enforcement means we never depend on server rejection codes (`02#2.6`); `04#4.7` rule 6 degrades to sequential plain messages | A server rejecting our batches → log `server_reject` with the raw `FAIL` and fall back to plain split (rule 6), and record the transcript for the backlog issue |
| **R4** | **FR1.6 gap fill is the hardest correct thing in v1**: order + no-duplicates across live traffic, replay, nesting, and service noise | M | H | 5 dedicated unit tests (UT-110..114) written from the measured transcript before the implementation (T-3.7); E2E-6 with a killable relay; msgid as the only dedup key | If msgid cursors do not work (T-0.3): timestamp cursor + `time`-watermark dedup and UT-113 strengthened; documented as a limitation if two messages can share a millisecond |
| **R5** | **Ergo-specific dependence.** `bot` tag, `HistServ` replay shape, `FAIL` codes, `BOT=B` are Ergo behaviours; issue #1 says Ergo 2.19 but the human may later run another ircd | M | M | Every Ergo-ism is behind a capability or ISUPPORT check with a defined degraded mode (`?` kind, `-` activity, warn-and-continue for `+B`); T-7.4 asserts we fail with `cap_missing` rather than panic on a non-Ergo server | Portability demand appears → the backlog issue from T-8.8 becomes a v2 milestone; nothing in the design has to be thrown away |
| **R6** | **FR5.3 over-interpretation:** same-uid processes can still talk to each other's sockets, and someone may read the requirement as a security boundary | M | M | Stated plainly in `01` FR5.3, `03#3.4` and the skill: accident-proofing, not isolation. v1 has no SASL (explicit non-goal) | If multi-tenant machines become a real requirement → SASL + per-listener auth tokens, a separate issue with its own security review |
| **R7** | **Wake storms.** Two chatty agents with `--wake all` can flood a harness context window (every line may wake an agent — the issue says so) | H | M | `--wake mentions` exists and is *recommended* in the skill; documented `--limit` on the harness side; the listener's stdout-stall policy (I3) protects the connection but not the context window | Real flooding observed → add `--rate-limit <n>/min` to the listener (post-v1, it is a policy knob, not a protocol change) |
| **R8** | **FR2.5 addressed-detection false negatives.** A message saying "the pi board" marks `pi` addressed; a message saying "someone: pi look" may not | M | M | Rule set is written down and tested (UT-134..138); the skill tells agents to *start* a line with `nick:` when a reply is expected, which is also the human-readable convention | Too noisy in practice → invert the default (`--wake mentions`) in v1.1; the flag already exists, so this is a one-line default change |
| **R9** | **Shared server noise / etiquette.** Live tests and real agents share `#hof` with a human; a runaway agent can fill the channel and the 2048-message history | M | M | `06#6.5` rules; `fakelag` respected; live tests use their own channels; the queue is bounded at 64 and one batch in flight | History displacement becomes a problem → ask the operator for a dedicated `#chatterbox` channel and register it (a human one-liner) |
| **R10** | **Single-threaded poll loop is unfamiliar territory for the implementer** and `rustls` + `WouldBlock` has classic hang/loop bugs | M | M | T-2.1 is the first listener task and is tested with a "slow client" fake (IT-229); the write buffer is bounded; the alternative (threads + blocking) is written down in `03#3.3` so a swap is a decision, not a discovery | Loop stalls appear in review → switch to one-thread-per-concern with a `Selector`, keeping the same interfaces |
| **R11** | **Stdout back-pressure semantics (I3) can drop wakes**, i.e. an agent misses a message it should have acted on | L | H | Drops are loud (stderr, counted), `status` exposes `wakes_dropped`, and the 512-record buffer is generous; the alternative — blocking the IRC connection — risks ping-timeout and lost *channel* connection, which is worse | If wake loss is ever observed in normal use, raise the buffer and re-measure; a durable spool is explicitly out of scope (issue #1: no local store) |
| **R12** | **Plan drift.** This plan asserts measured facts; cert rotates 2027-10-10, Ergo upgrades, capability lists change | H | L | Nightly raw-probe job (`06#6.7`) re-measures and opens an issue; DRIFT-marked facts in `02` are never used as test fixtures | Probe job fails → update `02` first, then code, in that order |
| **R13** | **Estimate optimism:** 16 dev-days assumes no protocol surprises and one competent implementer | M | M | M0 front-loads the unknowns; PR slicing (`05`) makes slippage visible per-PR rather than at the end; M1 is all pure functions, which is where estimation is most reliable | M1 or M2 misses by > 30 % → cut `channels` activity column (OQ4 already allows `-`) and `doctor`'s scratch-connection check before cutting any FR |
| **R14** | **The `433`-after-`CAP END` race:** server may accept the nick and only then report a collision | L | M | Registration completes only after `376`; if `433` arrives post-registration the listener exits 10 with the same message (state machine handles both timings) | If a server sends `433` after JOIN we treat it as fatal and do not retry — same requirement, different numeric timing |

## 7.2 Residual unknowns (things this plan could not measure, listed so nobody mistakes them for knowns)

1. **`CHATHISTORY LATEST <chan> msgid=<id> N` semantics** — `MSGREFTYPES` advertises `msgid`, the
   spec says it means "after this id", but it was not exercised. T-0.3 settles it; the fallback is a
   timestamp cursor (`03#3.6` already supports both).
2. **`MULTILINE_MAX_LINES`** — the server aborted our 101-line batch before that specific code
   appeared, cascading into `MULTILINE_INVALID`/`Corrupt BATCH`. Harmless for us (we never send more
   than 100 lines) but the exact server behaviour under that condition is undesigned.
3. **`TAGMSG` relay** — one observation, on one server version, showed no `TAGMSG` delivery to a
   second client. It is not load-bearing (R1), but the backlog issue from T-8.8 should re-test it
   before anyone builds tag-based importance.
4. **Behaviour when the pinned server is replaced entirely** (new key, same host): expected exit 9
   with both fingerprints, tested against the fake server (IT-223) rather than against the real thing.
5. **Whether `+n` (no-external-messages) blocks `CHATHISTORY` for a non-member on other servers** —
   we treat "no history for channels we are not in" as the general case (`01#OQ4`).
6. **Real-world fakelag latency under two agents talking quickly.** The 10 s ack timeout is a guess
   with headroom; measure in E2E-4 and adjust `limits.ack_timeout_secs` if fakelag ever pushes an ack
   past it.
7. **Whether an agent harness reliably reads a long-lived stdout stream without buffering limits.**
   Outside chatterbox's control; I3 plus `--wake mentions` is the mitigation, and the skill tells
   agents to restart the listener if they see `wakes_dropped`.

## 7.3 Deliberate limitations to publish in the README and the skill

- A DM (`--dm`) is invisible to the human and not covered by gap fill. Do not use it for decisions.
- `create-channel` does not make a channel permanent; only a human with `/msg ChanServ REGISTER`
  can, and accounts are out of scope.
- Channels vanish when the last member leaves. An agent that `create-channel`s and then quits has
  created nothing.
- `channels` shows `-` for last activity on channels the listener is not in, on this class of server.
- Bot/human may read `?` briefly after a peer joins, until its first message tag or a WHOIS lands.
- Same-uid processes are not isolated from each other.
- The listener keeps no local store: if the server's 2048-message window has rolled past the gap,
  those messages are gone and chatterbox says so rather than guessing.

## 7.4 Reversal criteria for the plan's own decisions

| Decision | What evidence would reverse it | What we would do |
|---|---|---|
| Rust (`01#D1`) | T-0.2 pin spike fails after a day | Go, same architecture, ~4 days rework |
| REQ-based negotiation (`02#2.2`) | a target server that requires `CAP ACK` | negotiate both, ACK after REQ, assert with `CAP LIST` — one extra command, no design change |
| Body-marker importance (`02#2.9`) | a server that preserves client tags *and* a client-side need for invisible importance | add tag as a second signal, keep the body marker |
| Create-by-move (`01#OQ2`) | the operator registers channels and grants the agent an account | introduce `--register` using the account, and revisit the non-goal on SASL |
| Single channel (`FR1.3`) | two-channel coordination becomes a real workflow | it is an explicit non-goal in issue #1; new issue, and `03#3.6`'s per-channel cursor refactor is the work |
| Mandatory nick (`01#OQ3`) | harnesses turn out to need zero-config start | derive `<harness>-<project>` with a `-2`/`-3` collision suffix, accepting a weaker FR1.4 |
| No plaintext at all (`01#1.4`) | a site where TLS is impossible (an internal relay) | keep the ban for the public path, add an explicit `--i-know-this-is-plaintext` gate behind a warning — never by default |

## 7.5 What this plan explicitly does not defend against

Denial of service from the channel; hostile server output (a malicious ircd feeding forged history is
accepted because we trust the pinned endpoint by definition); message ordering guarantees beyond the
server's own; delivery guarantees (IRC is at-most-once, we add acknowledgement, not retry queues);
multi-user isolation on a shared machine; persistence across machine reboots.

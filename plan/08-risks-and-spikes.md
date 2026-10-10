# 08. Risks and spikes

## 1. Spikes

What the design assumes about Ergo 2.19, or about agent harnesses, that has not been verified. Each is a short experiment (T0.5, T0.6) whose result is written into the last column. S3 runs first.

Status on 2026-10-10: only the pre-registration capability list has been read from tiny2 (01 §2). Ergo defaults quoted in 01 §2 come from its shipped configuration, not from tiny2. All spikes are open.

| ID | Question | Design assumes | If the assumption is wrong | Result |
|---|---|---|---|---|
| S1 | Which `ISUPPORT` tokens are sent: `BOT`, `CHATHISTORY`, `CASEMAPPING`, `NICKLEN`, `CHANNELLEN`, `CHANTYPES`, `MSGREFTYPES`? | `BOT=B`; `CHATHISTORY` is the page limit; both `timestamp` and `msgid` references work | adjust constants; page by timestamp if `msgid` is unavailable | open |
| S2 | Is the `bot` tag on live messages from `+B` users, and on the same messages replayed through chathistory? | yes to both | for replayed messages look the sender up with `WHO` and cache; a sender no longer present gets kind `unknown`, a third value added to both formats | open |
| S3 | Does `CHATHISTORY LATEST <#chan> * 1` work for a channel the client is not in? | **no** (expected refusal) | if yes, FR4.2 is met in full and D4's caveat goes away | open |
| S4 | On tiny2: is channel history on, how much is kept, is it replayed automatically on join? | on; automatic replay, if any, comes as a `chathistory` batch | history off means FR1.6 cannot be met there: the README states the required settings | open |
| S5 | Is private-message history available to clients without accounts? | no | if yes, add a catch-up pass for the listener's own nickname | open |
| S6 | Multiline with `labeled-response` and `echo-message`: where is the label on the echo, does the echoed batch carry a `msgid`, what do failures look like? | label on the echoed opening `BATCH`; failures are labeled `FAIL BATCH ...` | correlate by batch reference or by matching echoed text | open |
| S7 | After an abrupt network loss, how long does the server hold the old session's nickname? | under 5 minutes (2m30 by default) | set the D10 window from the measured value | open |
| S8 | Does an unregistered client that joins a non-existent channel create it and become operator? Is that true on tiny2? | yes | D2's confirmation needs another signal, or alternative B applies | open |
| S9 | Flood control: how long do a 100-line batch, a 17-part message, and three consecutive `channels` calls take? Do batch lines count? Is anything rejected rather than delayed? | delayed at about 2 lines per second, nothing rejected | tune the acknowledgement formula and the lookup budget; add pacing between parts if the server disconnects for flooding | open |
| S10 | Can an unregistered client set `+B` on itself? | yes | FR1.2 cannot be met without accounts: escalate | open |
| S11 | Does tiny2 accept connections without SASL? | yes | the deployment needs reconfiguring, or accounts come into scope: escalate | open |
| S12 | How does a multiline message look to a client without the capability? Is order kept? | separate lines, in order | none needed for function; document | open |
| S13 | What does `CHATHISTORY AFTER` return for a `msgid` the server no longer has, and after a server restart? | not relied on: catch-up starts from a timestamp | none; recorded so the design choice in 03 §6 stays justified | open |
| S14 | How do Claude Code and Pi run a background command: is stdout a pipe or a file, is stderr shown to the agent, does the parent process live as long as the agent session, what happens to the child when the session ends? | stdout is a pipe at least in some harnesses; nothing else assumed | choose the detection for the non-pipe case (parent watching, or none) and settle T2.12 | open |

## 2. Risks

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | An agent posts under another agent's name | low after D15 | high | posting commands never auto-detect; instructions use `--as` everywhere; IT-WRONGAGENT. If D15 is rejected this returns to medium likelihood |
| R2 | Agents wake each other in a loop (acknowledging acknowledgements) | high | medium | etiquette in the skill; `--wake addressed`; AT-ONBOARD and the manual hour-long session |
| R3 | A human addresses an agent without `nick:` or `@nick` and the agent on `--wake addressed` does not react | medium | medium | README and skill explain addressing; default filter is `all`; D8 can be widened after the manual session |
| R4 | `draft/multiline` and `draft/chathistory` are drafts and may change in later Ergo versions | low | medium | names and limits are read from the server; integration tests pin the Ergo version and are rerun on upgrade |
| R5 | A long outage produces a large replay that floods the agent's context | medium | medium | bounded by server retention (2 048 by default); `--wake addressed` applies to replays; no client-side cap, by D17 |
| R6 | History does not reach back to the disconnect (retention exceeded, or server restarted), so messages are lost | low | medium | stderr diagnostic where detectable; owner to decide whether a stdout line is wanted (D17) |
| R7 | Trust on first use accepts an attacker's certificate on the very first connection | low | high | fingerprint printed for comparison; `tls.fingerprint` or `trust_on_first_use = false` for stricter setups; later downgrades refused |
| R8 | The listener outlives its agent, so presence lies | medium | medium | SIGPIPE handling and hang-up polling cover pipes; S14 decides the rest; skill says to stop the listener; manual step 5 measures it |
| R9 | `create-channel` leaves the agent alone in a new channel, or races with another creator | medium | medium | announcement in the old channel; operator check and return with catch-up |
| R10 | `[IMPORTANT]` can be typed by anyone | low | low | recognized only at the start of the body; documented |
| R11 | Anyone on the server can message an agent privately or pose as a human; agents may treat `human` as authority | medium | high | out of scope to solve without accounts; stated in the skill and README |
| R12 | A sign-off goes the other way after work has started | medium | medium | D1–D5 are accepted; of the open ones, D7, D8, D15 are each isolated behind one function, and sign-offs are scheduled before the milestones that depend on them (01 §5) |
| R13 | Several agents behind one address reconnect together and hit the server's connection throttle (32 per 10 minutes by default) | low | medium | 60 s backoff ceiling with jitter; fatal errors do not retry |
| R14 | A sandboxed harness blocks Unix sockets or uses a private `/tmp`, so commands cannot reach the listener | low | high | `CHATTERBOX_RUNTIME_DIR` override; the "no listener" error names the directory searched; checked in S14 |
| R15 | The server's flood control makes long messages slow enough that agents think a command hung | medium | low | progress lines keep the client alive; size cap (D11); skill advises short messages |
| R16 | Peer-credential APIs differ between Linux and macOS | medium | low | build-tagged implementations; directory `0700` and socket `0600` suffice on their own |

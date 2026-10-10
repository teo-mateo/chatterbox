# 08. Risks and spikes

## 1. Spikes

Things the design assumes about Ergo 2.19 that have not been verified. Each is a short experiment in T0.4. Results are to be written into the "Result" column.

| ID | Question | Design assumes | If the assumption is wrong | Result |
|---|---|---|---|---|
| S1 | Which `ISUPPORT` tokens are sent: `BOT`, `CHATHISTORY`, `MSGREFTYPES`, `CASEMAPPING`, `NICKLEN`, `CHANNELLEN`, `CHANTYPES`? | `BOT=B`; `CHATHISTORY` gives the page limit; `MSGREFTYPES` includes `msgid` and `timestamp` | adjust constants; without `msgid` references use timestamps only | open |
| S2 | Is the `bot` tag present on live messages from `+B` users, and on the same messages when replayed through chathistory? | yes to both | for replayed messages, look the sender up with `WHO` and cache; absent senders are reported as unknown kind | open |
| S3 | Does `CHATHISTORY LATEST <#chan> * 1` work for a channel the client has not joined? | unknown; D4 handles both answers | last activity is unknown for other channels (already the fallback) | open |
| S4 | On tiny2: is channel history enabled, what is the retention, and is history replayed automatically on join? | history enabled; automatic replay arrives in a `chathistory` batch that can be recognized | if history is disabled FR1.6 cannot be met on that server: document the required Ergo settings | open |
| S5 | Is private-message history available to clients without accounts? | no | if yes, add a catch-up pass for the listener's own nickname | open |
| S6 | Multiline with `labeled-response` and `echo-message`: where does the label appear on the echo, does the echoed batch carry a `msgid`, and what do the failure replies look like (`FAIL BATCH ...`)? | the label is on the echoed opening `BATCH`; failures are labeled `FAIL` | correlate by batch reference tag or by matching the echoed text | open |
| S7 | After an abrupt network loss, how long does the server keep the old session's nickname? | under 5 minutes (ping timeout) | raise the D10 bound to match the server's timeout | open |
| S8 | May an unregistered client create a channel by joining it, and does it get operator status? | yes | D2's confirmation step needs another signal (for example being the only member) or `create-channel` becomes validate-only | open |
| S9 | Does Ergo's flood control (fakelag) delay or reject a 16-part message? | delays, does not reject | add pacing between parts and raise the acknowledgement timeout accordingly | open |
| S10 | Can an unregistered client set `+B` on itself? | yes | FR1.2 cannot be met without accounts: escalate | open |
| S11 | Does tiny2 allow connections without SASL? | yes (accounts are out of scope) | the deployment needs reconfiguring, or accounts come into scope: escalate | open |
| S12 | How do messages sent with multiline look to a client without the capability, and do they keep order? | separate lines in order | none needed for function; document | open |

Status on 2026-10-10: only the pre-registration capability list has been read from tiny2 (01 §2). All spikes are open.

## 2. Risks

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | An agent without `CHATTERBOX_NICK` runs a send command while exactly one other agent's listener is running, and posts under that agent's name through auto-detection (FR5.2 requires auto-detection; FR5.3 forbids accidents) | medium | high | stderr notice naming the identity used; skill instructs exporting `CHATTERBOX_NICK` before anything else; consider in review whether auto-detection should additionally require the listener to share the caller's working directory |
| R2 | Agents wake each other in a loop (acknowledging acknowledgements) | high | medium | etiquette in the skill; `--wake addressed`; manual step 5 of acceptance reviews a real session |
| R3 | The addressed rule "nickname anywhere as a word" (D8) wakes agents on mere mentions | medium | low | accepted; revisit after the manual session; narrowing to prefix-only is a contained change |
| R4 | `draft/multiline` and `draft/chathistory` are draft specifications and may change in later Ergo versions | low | medium | capability names and limits are read from the server; integration tests pin the Ergo version and are rerun on upgrade |
| R5 | A long outage produces a large replay that floods the agent's context | medium | medium | replay cap of 1 000 with a stderr notice; `--wake addressed` applies to replays |
| R6 | History retention is shorter than an outage, so messages are silently lost | low | medium | stderr diagnostic when the reference message is no longer in history |
| R7 | Trust on first use accepts an attacker's certificate on the very first connection | low | high | fingerprint printed for out-of-band comparison; `tls.fingerprint` in config disables automatic pinning |
| R8 | The listener keeps running after its agent is gone, so presence lies | medium | medium | exit on closed stdout with hang-up polling (03 §2) |
| R9 | `create-channel` races with another client creating the same channel | low | low | operator-status check and revert (D2) |
| R10 | Text-embedded `[IMPORTANT]` can be typed by anyone, including by accident in quoted text | low | low | only recognized at the start of the body; documented |
| R11 | Platform differences in peer-credential checks (Linux vs macOS) | medium | low | build-tagged implementations; socket mode `0600` in a `0700` directory is sufficient on its own |
| R12 | Sign-off on D1 or D2 goes the other way after work has started | medium | medium | both are isolated behind one function each; sign-off (T0.5) is scheduled before M4 and M5 |

# 03. IRC protocol behavior

Developed against Ergo 2.19. Items marked (Sn) depend on a spike in [08-risks-and-spikes.md](08-risks-and-spikes.md).

## 1. Connecting and registering (FR1.1, FR1.2, FR1.4)

1. Dial TCP, TLS handshake with the verification described in 04 §6. Plain-text connections are not implemented.
2. `CAP LS 302`.
3. `CAP REQ` for the required capabilities, then `NICK <nick>`, `USER <nick> 0 * :chatterbox agent`, `CAP END`.
4. On `001`: record the nickname as confirmed by the server, parse `005` tokens.
5. `MODE <nick> +B`, using the mode letter from the `BOT` ISUPPORT token. Confirm through the `MODE` echo or `221`. If the server has no `BOT` token the listener exits: FR1.2 cannot be met (S1).
6. `JOIN <#channel>`. Joined when the server echoes the `JOIN` and `366` (end of names) arrives. With `no-implicit-names` not requested, names arrive automatically.
7. Record the listener's own `nick!user@host` from the `JOIN` echo. It is needed to compute how many bytes fit on a line.

### Capabilities

| Capability | Why | If missing |
|---|---|---|
| `message-tags` | `msgid`, `bot` tag, `batch` tag | exit |
| `server-time` | message time (FR2.2), history ordering | exit |
| `batch` | multiline, chathistory | exit |
| `draft/multiline` | FR2.1, FR3.4 | exit |
| `draft/chathistory` | FR1.6 | exit |
| `echo-message` | send acknowledgement (D9) | exit |
| `labeled-response` | correlate echoes and errors with requests | exit |
| `standard-replies` | readable `FAIL` reasons | continue |
| `multi-prefix` | operator check for `create-channel` | continue |

Not requested: `draft/event-playback` (would replay joins and quits), `extended-join`, `account-*`, `away-notify`, `sasl`.

Exiting when a required capability is missing is deliberate: the issue names an IRCv3 server as a prerequisite, and silent degradation would break FR1.6 or FR3.4 without anyone noticing.

### Registration errors

| Numeric | Meaning | First connect | Reconnect |
|---|---|---|---|
| 433 | nickname in use | exit 4: `nickname <nick> is already in use on <server>` | retry up to 5 min (D10), then exit 4 |
| 432 | erroneous nickname | exit 2 | exit 2 |
| 465, `ERROR` | banned, closed | exit 6 | backoff |
| 471, 473, 474, 475 | cannot join | exit 6 | exit 6 (D14) |
| `FAIL * ACCOUNT_REQUIRED` | server requires an account | exit 6 with a note that accounts are out of scope (S11) | same |

## 2. Staying connected and leaving (FR1.5)

- Answer server `PING`. Send a client `PING` every 30 s; 90 s without any incoming line is connection loss.
- On SIGINT, SIGTERM, SIGHUP: `QUIT :chatterbox listener stopped`, wait up to 2 s for the server to close, remove socket and lock, exit 0.
- On SIGKILL or a crash the kernel closes the TCP connection and the server removes the nickname immediately.
- If stdout is closed (the harness that was watching went away), the listener exits. Detection: a failed write, and additionally polling the stdout descriptor for hang-up every few seconds so a quiet channel does not keep a dead agent present.
- `KICK` naming the listener: exit 6 with the kicker and reason (D14).
- A forced nickname change of the listener (services, operator): exit 6. The socket name and the agent's idea of its name would no longer match.

## 3. Membership tracking

The core keeps the set of nicknames in the channel, from `353` names replies and from `JOIN`, `PART`, `QUIT`, `KICK`, `NICK`. It is used to reject `send` to a nickname that is not present (non-private mode) and for nothing else. Bot status is not tracked here; it is fetched on demand (§8).

## 4. Receiving (FR2.1, FR2.2, FR2.4, FR7.2)

A chat message is a `PRIVMSG` or user `NOTICE` whose target is the listener's channel or the listener's nickname. Everything else (joins, parts, quits, modes, topics, numerics, server notices, `TAGMSG`, messages for other targets) is never printed.

For each chat message:

1. **Own messages**: if the source nickname equals the listener's nickname, drop it. With `echo-message` these arrive for everything the listener sends, and they also appear in history.
2. **Multiline**: lines inside a `draft/multiline` batch are collected and combined into one message when the batch closes. Lines are joined with `\n`, except lines tagged `draft/multiline-concat`, which are appended directly. The message takes the `msgid`, `time` and source of the batch.
3. **Identity of the message**: `msgid` tag. Time: `time` tag, else the local clock.
4. **Sender kind**: `bot` if the message (or its batch) carries the `bot` tag, else `human` (S2).
5. **CTCP**: `\x01ACTION text\x01` becomes `* nick text`. Other CTCP is dropped.
6. **Importance**: strip an address prefix if present, then test for `[IMPORTANT]` (D6). Remove the token from the delivered text.
7. **Addressed**: rules of D8.
8. **Text cleanup**: strip IRC formatting codes. The encoders (04 §3) guarantee one physical line.

A human on a client without multiline support who pastes several lines sends several independent messages; each is delivered as its own line. That is inherent to FR7.3.

## 5. Sending (FR3.1–FR3.5)

### Building the body

| Command | Target | Body |
|---|---|---|
| `publish` | channel | `text` |
| `publish --important` | channel | `[IMPORTANT] text` |
| `send nick` | channel | `nick: text` |
| `send --important nick` | channel | `nick: [IMPORTANT] text` |
| `send --private nick` | nick | `text` |
| `send --private --important nick` | nick | `[IMPORTANT] text` |

The prefix (`nick: `, `[IMPORTANT] `) is part of every message produced by splitting, so each part is filtered and flagged correctly on its own.

### Line budget

An IRC line is at most 512 bytes including CRLF, and the server relays the message with the sender's prefix. The usable text per line is:

```
510 - len(":nick!user@host ") - len("PRIVMSG <target> :")
```

using the hostmask recorded at join. Tags do not count against this limit. If the hostmask is unknown the budget falls back to 300 bytes.

### Choosing the wire form

1. Text has no newline and fits the line budget: one `PRIVMSG` with a `label` tag.
2. Otherwise a multiline batch:

   ```
   @label=L BATCH +ref draft/multiline <target>
   @batch=ref PRIVMSG <target> :first line
   @batch=ref PRIVMSG <target> :second line
   @batch=ref;draft/multiline-concat PRIVMSG <target> :rest of an over-long line
   BATCH -ref
   ```

   - Logical lines longer than the budget are cut at a UTF-8 character boundary, preferring the last space, and continued with `draft/multiline-concat`.
   - Blank lines are allowed inside a batch.
3. If the combined message would exceed the server's `max-bytes` or `max-lines` (read from the capability value, 4096 and 100 on the probed server): split into several multiline messages. Split at logical line boundaries where possible, else inside a line. Each resulting message gets the prefix again and is counted against the limits with it.
4. A message of more than `max_message_bytes` (64 KiB) is refused before anything is sent (D11).

A message whose text is empty or only whitespace is refused.

### Acknowledgement

Each outgoing message carries a fresh label. It is acknowledged when the labeled echo arrives (the `PRIVMSG`, or the echoed batch for multiline). It fails on a labeled error numeric or `FAIL` (for example `404`, `401`, `FAIL BATCH MULTILINE_MAX_BYTES`), or after 10 s. Parts of a split message are sent one after another, each waiting for its acknowledgement; if one fails the command fails and reports how many parts were delivered (S6 covers how Ergo labels multiline echoes).

For private sends the server reports an unknown nickname with `401`. For addressed channel sends the listener checks membership first and fails with `unknown_nick` without sending.

## 6. Reconnection and catch-up (FR1.6)

State kept in memory: `lastMsgid` and `lastTime` of the newest channel message seen (printed or not, including own echoes), and a set of the most recent 2 000 message ids.

After a reconnect and successful join, before going `ready`:

1. Enter `catching-up`. Live channel messages that arrive now are buffered.
2. Request `CHATHISTORY AFTER <#channel> msgid=<lastMsgid> <limit>`, where `<limit>` is the `CHATHISTORY` ISUPPORT value (or 100). If no message had been seen before the disconnect, use `timestamp=<time the previous connection became ready>`.
3. The reply is a `chathistory` batch (multiline messages appear as nested batches). Process each message through the normal inbound pipeline, in the order given (oldest first), marked `replayed`.
4. If the batch contained `<limit>` messages, repeat from the newest one received. Stop on a short or empty batch.
5. Flush the buffered live messages in arrival order.
6. Every message passes the dedup set on its way out, so a message present in both history and the live buffer is printed once. Go `ready`.

Details:

- If the server answers `FAIL CHATHISTORY` for the `msgid` reference (for example the message has expired from history), fall back to the `timestamp=` form with `lastTime`. If that fails too, report on stderr that messages may have been missed and continue.
- A total cap of 1 000 replayed messages per catch-up protects the agent from a flood after a long outage. When it is hit, the newest 1 000 are kept and stderr says how the gap was truncated.
- A `chathistory` batch the listener did not ask for (some servers replay on join) is discarded. On the first connection nothing is replayed: the listener delivers what happens while it is running.
- Private messages sent to the agent while it was disconnected are not recoverable without accounts (S5). This is documented; it is one of the reasons for D1.
- The wake filter applies to replayed messages exactly as to live ones.

## 7. Human participation (FR7)

Nothing chatterbox sends requires more than `PRIVMSG` support on the receiving side:

- Addressing and importance are plain text.
- For clients without `draft/multiline` the server delivers a multiline message as separate lines.
- Bot status is visible to humans through `WHO`/`WHOIS` in their own client.

Messages from humans arrive without the `bot` tag and are flagged `human`.

## 8. Presence and channels (FR4)

### `whos-there`

`WHO <#channel>` with a label. Each `352` reply has a flags field; a participant is a bot when the flags contain the `BOT` ISUPPORT letter. The result includes the listener itself. Sorted by nickname.

### `channels`

1. `LIST` with a label: name and user count per channel. Channels hidden by server policy do not appear.
2. For up to 50 channels, most populated first: `CHATHISTORY LATEST <#chan> * 1`, and take the `time` of the returned message (D4, S3). Failure or an empty reply gives unknown.
3. Sorted by last activity, newest first, unknown last.

### `create-channel`

Per D2:

1. Validate the name: starts with a `CHANTYPES` character, at most `CHANNELLEN` bytes, no space, comma or control characters.
2. `LIST <#name>`: if it is listed, fail with `channel_exists`.
3. `PART` the current channel, `JOIN` the new one.
4. Check from the names reply (with `multi-prefix`) that the listener is an operator there. If not, `PART`, re-`JOIN` the previous channel and fail with `channel_exists`.
5. Reset catch-up state (`lastMsgid`, `lastTime`) for the new channel. Respond with the new channel name.

A secret channel is not listed, so step 2 can miss it; step 4 catches that case.

# 03. IRC protocol behavior

Developed against Ergo 2.19. Items marked (Sn) depend on a spike in [08-risks-and-spikes.md](08-risks-and-spikes.md).

## 1. Connecting and registering (FR1.1, FR1.2, FR1.4)

1. Dial TCP, TLS handshake with the verification of 04 §6. Plain-text connections are not implemented.
2. `CAP LS 302`.
3. `CAP REQ` for the capabilities below, then `NICK <nick>`, `USER <nick> 0 * :chatterbox agent`, `CAP END`.
4. On `001`: parse the `005` tokens.
5. `MODE <nick> +B`, with the mode letter from the `BOT` token. Confirm through the `MODE` echo. If the server has no `BOT` token or refuses the mode, the listener exits: FR1.2 cannot be met (S1, S10).
6. `JOIN <#channel>`. Joined when the server echoes the `JOIN` and `366` (end of names) arrives.
7. From the `JOIN` echo, record the listener's own `nick!user@host` (needed for the line budget) and the echo's `time` tag (the server-side moment the listener became present; used by catch-up).

### Capabilities

| Capability | Why | If missing |
|---|---|---|
| `message-tags` | `msgid`, `bot` tag, `batch` tag | exit |
| `server-time` | message time (FR2.2), catch-up reference | exit |
| `batch` | multiline, chathistory | exit |
| `draft/multiline` | FR2.1, FR3.4 | exit |
| `draft/chathistory` | FR1.6 | exit |
| `echo-message` | send acknowledgement (D9) | exit |
| `labeled-response` | correlating echoes and errors with requests | exit |
| `standard-replies` | readable `FAIL` reasons | continue |
| `multi-prefix` | operator check for `create-channel` | continue |

Not requested: `draft/event-playback` (would replay joins and quits), `extended-join`, `account-*`, `away-notify`, `sasl`.

Exiting when a required capability is missing is deliberate: the issue names an IRCv3 server as the prerequisite, and silent degradation would break FR1.6 or FR3.4 unnoticed. The error names the missing capability.

`CAP NEW` is ignored. `CAP DEL` of a required capability ends the listener.

### Registration errors

| Reply | Meaning | Before first `ready` | After |
|---|---|---|---|
| 433 | nickname in use | exit 4: `nickname <nick> is already in use on <server>` | redial for up to 5 min (D10), then exit 4 |
| 432 | erroneous nickname | exit 2 | exit 2 |
| 465, `ERROR` | banned, closed | exit 9 | backoff |
| 471, 473, 474, 475 | cannot join | exit 6 | exit 6 (D14) |
| `FAIL * ACCOUNT_REQUIRED` | server requires an account | exit 6, noting that accounts are out of scope (S11) | same |

## 2. Staying connected and leaving (FR1.5)

- Answer server `PING`. Send a client `PING` every 30 s; 90 s without any incoming line is connection loss.
- SIGINT, SIGTERM, SIGHUP: `QUIT :chatterbox listener stopped`, wait up to 2 s for the server to close, remove the socket, exit 0.
- SIGKILL or a crash: the kernel closes the TCP connection and the server removes the nickname at once.
- `KICK` naming the listener: exit 6 with the kicker and reason (D14).
- Forced nickname change (services, operator): exit 6. The socket name and the agent's idea of its name would no longer match.

### When the agent goes away

A listener that outlives its agent makes presence lie. Detection, in layers:

1. **SIGPIPE is ignored** at startup. By default a Go program is killed by SIGPIPE when it writes to a closed stdout or stderr, which would skip the clean exit. With the signal ignored, the write fails with an error and the listener shuts down normally. A failed stderr write is ignored.
2. **stdout hang-up**: when stdout is a pipe, the listener polls it for hang-up every 2 s, so a quiet channel does not hide a dead reader.
3. **stdout not a pipe** (a harness that redirects background output to a file): neither of the above ever fires. How the target harnesses run background processes, and whether watching the parent process is reliable there, is spike S14. Depending on its result the listener also exits when its original parent process disappears. Until then this case is a known gap (R8), and the agent instructions say to stop the listener when the work is done.

## 3. Membership tracking

The core keeps the set of nicknames in the channel, from `353` names replies and from `JOIN`, `PART`, `QUIT`, `KICK`, `NICK`. It is used to reject `send` to a nickname that is not present (non-private mode), and for nothing else. Bot status is fetched on demand (§8).

## 4. Receiving (FR2.1, FR2.2, FR2.4, FR7.2)

A chat message is a `PRIVMSG` or user `NOTICE` whose target is the listener's channel or the listener's nickname. Everything else (joins, parts, quits, modes, topics, numerics, server notices, `TAGMSG`, messages for other targets) is never printed.

For each chat message:

1. **Own messages**: if the source nickname is the listener's, drop it. With `echo-message` these arrive for everything the listener sends; they also appear in history.
2. **Multiline**: lines inside a `draft/multiline` batch are collected and combined when the batch closes. Lines are joined with a newline, except lines tagged `draft/multiline-concat`, which are appended directly. The message takes the `msgid`, `time` and source of the batch.
3. **Identity**: `msgid` tag. **Time**: `time` tag. With `server-time` negotiated every message has one; a message without it is stamped with the local clock and is not used as a catch-up reference.
4. **Sender kind**: `bot` if the message or its batch carries the `bot` tag, else `human` (S2).
5. **CTCP**: `ACTION` becomes `* nick text`. Other CTCP is dropped.
6. **Importance** and 7. **Addressed**: D6, D8.
8. **Text cleanup**: strip IRC formatting codes; remove the `[IMPORTANT]` token; keep the address prefix. The encoders (04 §3) guarantee one physical line.

A human on a client without multiline support who pastes several lines sends several independent messages; each is delivered as its own line. That is inherent to FR7.3.

Any user on the server can send a private message to an agent, and `human` only means "has not set bot mode". Without accounts neither sender identity nor humanity is authenticated. This is out of scope to fix; the agent instructions say so (T7.1).

## 5. Sending (FR3.1–FR3.5)

### Normalizing the text

Before anything else, in the command (so errors are immediate):

- Input must be valid UTF-8 and contain no NUL; otherwise exit 2.
- `\r\n` and lone `\r` become `\n`.
- One trailing newline is removed (so `echo hi | chatterbox publish -` is a one-line message).
- Text that is empty or only whitespace is refused.
- Text over `limits.max_message_bytes` is refused (D11).

### Building the body

| Command | Target | Body |
|---|---|---|
| `publish` | channel | `text` |
| `publish --important` | channel | `[IMPORTANT] text` |
| `send nick` | channel | `nick: text` |
| `send --important nick` | channel | `nick: [IMPORTANT] text` |
| `send --private nick` | nick | `text` |
| `send --private --important nick` | nick | `[IMPORTANT] text` |

When a message has to be split into `n` IRC messages, each part's body is `<prefix>(i/n) <chunk>`, with the same prefix (`nick: `, `[IMPORTANT] `) on every part. Each part is then addressed, flagged and filtered correctly on its own, and the reader can see they belong together.

### Line budget

An IRC line is at most 512 bytes including CRLF, and the server relays the message with the sender's prefix. Usable text per line:

```
510 - len(":nick!user@host ") - len("PRIVMSG <target> :")
```

using the hostmask recorded at join. Tags do not count against this limit.

### Choosing the wire form

1. The body has no newline and fits the line budget: one `PRIVMSG` with a `label` tag.
2. Otherwise one multiline batch, written as a single unit (02 §5):

   ```
   @label=L BATCH +ref draft/multiline <target>
   @batch=ref PRIVMSG <target> :first line
   @batch=ref PRIVMSG <target> :second line
   @batch=ref;draft/multiline-concat PRIVMSG <target> :rest of an over-long line
   BATCH -ref
   ```

   - A logical line longer than the budget is cut at a UTF-8 character boundary, preferring to cut after a space. The space stays at the end of the first piece, so joining the pieces reproduces the line exactly. The following piece carries `draft/multiline-concat`.
   - A concat line is never blank (the specification forbids it), and concat lines count toward `max-lines`.
   - Blank lines without the concat tag are allowed.
3. The body exceeds the server's `max-bytes` or `max-lines` (taken from the capability value; 4096 and 100 on the probed server): split into several messages as described above. Cuts fall on logical line boundaries where possible. Every part is checked against both limits including its prefix and part marker.

### Acknowledgement (D9)

Each message carries a fresh label. It is acknowledged by the labeled echo (the `PRIVMSG`, or the echoed batch). It fails on a labeled error numeric or `FAIL` (for example `404`, `401`, `FAIL BATCH MULTILINE_MAX_BYTES`).

The acknowledgement timer starts when the unit's last line has been written, and allows 10 s plus 0.5 s per line of the unit, because the server's flood control processes about two lines per second after the initial burst (S9 measures this).

Parts of a split message are sent one after another, each waiting for its acknowledgement. If one fails, the rest are not sent and the command reports how many were delivered.

For private sends an unknown nickname comes back from the server as `401`. For addressed channel sends the listener checks membership first and fails with `unknown_nick` without sending.

How Ergo labels a multiline echo and its failure replies is S6.

## 6. Reconnection and catch-up (FR1.6)

State in memory:

- `lastTime`: server time of the newest message seen in the channel, printed or not, own echoes included. Before any message has been seen it is the `time` of the listener's own `JOIN` echo. Only server-supplied times are used, never the local clock, so clock differences cannot open a gap.
- `seen`: the ids of the most recent 5 000 messages.

After a reconnect and join, before `ready`:

1. Enter `catching-up`. Live channel messages arriving now are buffered.
2. Request `CHATHISTORY AFTER <#channel> timestamp=<lastTime minus 2 s> <limit>`, with `<limit>` from the `CHATHISTORY` token. The overlap makes sure messages sharing the last timestamp are not skipped; `seen` removes the repeats.
3. The reply is a `chathistory` batch (multiline messages appear as nested batches). Each message goes through the normal inbound pipeline, in the order given, flagged `replayed`. Messages already in `seen` are skipped.
4. If the batch was not empty, request the next page: `CHATHISTORY AFTER <#channel> msgid=<id of the newest message received> <limit>`. That id was just issued by the server, so it is a safe reference. Repeat until an **empty** batch arrives. A short batch is not treated as the end; the specification lets servers return fewer than the limit.
5. Flush the buffered live messages in arrival order through the same dedup, then go `ready`.

Details:

- The reference is always a timestamp first, never an old `msgid`. What a server does with a `msgid` it no longer knows is not defined by the specification (S13), and an empty answer would be indistinguishable from "nothing missed".
- If the oldest message returned in the first page is newer than `lastTime` and the listener had seen messages before, history may not reach back to the disconnect. Likewise if the server restarted and its in-memory history is gone, nothing comes back at all. The first is reported on stderr; the second cannot be detected (R6).
- `FAIL CHATHISTORY`: reported on stderr with a note that messages may have been missed; the listener continues to `ready`.
- A `chathistory` batch the listener did not ask for (some configurations replay on join) is discarded. On the first connection nothing is replayed: the listener delivers what happens while it runs.
- There is no cap on the number of replayed messages (D17).
- Private messages sent while the agent was disconnected are not recoverable without accounts (S5). Documented; one of the reasons for D1.
- The wake filter applies to replayed messages exactly as to live ones.

The same procedure runs when `create-channel` has to return to the previous channel (§8).

## 7. Human participation (FR7)

Nothing chatterbox sends requires more than `PRIVMSG` support on the receiving side:

- Addressing, importance and part markers are plain text.
- For clients without `draft/multiline` the server delivers a multiline message as separate lines (S12).
- Bot status is visible to humans through `WHO`/`WHOIS` in their own client.

Messages from humans arrive without the `bot` tag and are flagged `human`.

## 8. Presence and channels (FR4)

### `whos-there`

`WHO <#channel>` with a label. A participant is a bot when the flags field of its `352` reply contains the `BOT` letter. The result includes the listener itself. Sorted by nickname.

### `channels`

1. `LIST` with a label: name and user count per channel. Channels the server hides do not appear.
2. Last activity (D4): `CHATHISTORY LATEST <#chan> * 1`, taking the `time` of the returned message. The listener's own channel is looked up first, then the others, most populated first, up to `limits.channels_history_lookups` (50). Lookups stop after a time budget of 10 s; whatever has no answer by then is unknown. This keeps the command usable when the server's flood control slows repeated lookups.
3. A refused or empty lookup gives unknown. After the first refusal for a channel the listener is not in, the remaining non-member lookups are skipped for that invocation.
4. Sorted by last activity, newest first, unknown last.

### `create-channel` (D2)

1. Validate the name: starts with a `CHANTYPES` character, at most `CHANNELLEN` bytes, no space, comma or control characters. Same as the current channel: fail.
2. `LIST <#name>`: if listed, fail with `channel_exists`.
3. `PRIVMSG <old> :moving to <#name>`.
4. `PART <old>`, `JOIN <#name>`.
5. Join refused (for example the server restricts channel creation, S8): go to step 7.
6. The names reply shows the listener as operator: success. The new channel becomes **the listener's channel** from here on; reconnects rejoin it, not the original `--channel`. Catch-up state is reset. Respond with the new name.
7. Otherwise (someone else created it first, typically a secret channel that `LIST` did not show): `PART <#name>` if joined, `JOIN <old>`, run catch-up for `<old>` from the saved `lastTime`, fail with `channel_exists`.
8. If rejoining the old channel is refused, the listener exits 6 (D14).

Known edges, documented rather than solved: if the listener was the last member of the old channel, that channel ceased to exist when it left and step 7 recreates it empty. After step 6 the same is true of the old channel.

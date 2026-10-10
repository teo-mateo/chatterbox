# 01. Scope and decisions

## 1. Scope

In scope: everything under FR1–FR8 of issue #1, delivered as a single binary `chatterbox` with the subcommands `listen`, `publish`, `send`, `whos-there`, `channels`, `create-channel`, plus an agent instruction block.

Out of scope, as stated in the issue:

- Running or managing the IRC server.
- Registered accounts, SASL, nickname protection.
- Listening on more than one channel at once.
- A local message store. The listener keeps only in-memory state (last seen message id, a bounded set of recent ids).
- Harness-specific hooks or extensions.

Added by this plan, small and in service of a requirement:

- `publish -` and `send <nick> -` read the message from stdin (shell quoting of long multi-line messages is otherwise error-prone; serves FR3.4).
- A `status` control operation used internally for listener discovery and by tests. Not exposed as a documented command.

## 2. What is known about the target server

Read from the Ergo instance on `tiny2` on 2026-10-10 by a `CAP LS 302` before registration (no nickname was taken and nothing was posted):

- Listens with TLS on 6697. Port 6667 is closed.
- The certificate is self-signed (`O = Ergo`). Its subject alternative names are `irc.heapzilla.eu`, `localhost`, `127.0.0.1`, `::1`. Connecting as `tiny2` or by LAN address therefore fails hostname verification as well as chain verification. FR6.2 pinning has to cover both.
- Advertised capabilities relevant here: `message-tags`, `server-time`, `batch`, `draft/multiline=max-bytes=4096,max-lines=100`, `draft/chathistory`, `draft/event-playback`, `echo-message`, `labeled-response`, `multi-prefix`, `standard-replies`, `userhost-in-names`, `extended-join`, `no-implicit-names`, `sasl=PLAIN,EXTERNAL,SCRAM-SHA-256`, `draft/account-registration=before-connect`.

Not yet verified (a post-registration probe was not run): the `ISUPPORT` tokens (`BOT`, `CHATHISTORY`, `MSGREFTYPES`, `CASEMAPPING`, `NICKLEN`), whether history is enabled and for how long, whether unregistered clients may register a connection at all, and the items listed as spikes in [08-risks-and-spikes.md](08-risks-and-spikes.md).

## 3. Decisions on the issue's open questions

Each needs the issue owner's sign-off. The plan is written so that changing one is contained.

### D1 (OQ1). `send` is a channel message addressed to the nick; private delivery is an option

- `chatterbox send <nick> <message>` posts `<nick>: <message>` to the listener's channel.
- `chatterbox send --private <nick> <message>` sends an IRC private message to the nick instead.

Why: the primary use case has a human watching and steering (FR7.1). Agent-to-agent traffic the human cannot see defeats that. An addressed channel message is also recoverable through channel history after a disconnect (FR1.6), which private messages are probably not without accounts (S5).

Consequence: with wake filter `all`, every agent in the channel is woken by a `send` to another agent. That is what the `addressed` filter is for.

Containment: the send path takes a `private` boolean. Flipping the default is a one-line change plus docs.

### D2 (OQ2). `create-channel` moves the listener into a new channel

`chatterbox create-channel <#name>`:

1. Validates the name against the server's `CHANTYPES` and `CHANNELLEN`.
2. Fails if the channel already exists (membership-count check through `LIST <#name>`).
3. Asks the listener to part its current channel and join the new one. The listener still has exactly one channel (FR1.3).
4. Confirms creation by checking that the listener holds channel-operator status in the new channel (the server grants it to the creator). If it does not, someone else got there first: the listener returns to its previous channel and the command fails.

Why: without accounts an IRC channel exists only while occupied, so "create" can only mean "be the first to join". Registering a permanent channel needs an account, which is out of scope.

Consequence: the channel disappears when the last participant leaves. The command's output says so.

Alternative if rejected: drop the command's side effect and make it validate-only, with channels created by `listen --channel <#new>`.

### D3 (OQ3). The nickname is always explicit; nothing is derived

`listen` takes its nickname from `--as`, then `CHATTERBOX_NICK`. If neither is set it exits with a usage error. No nickname is derived from harness or project name, and no `nick` default is read from the config file.

Why: FR1.4 forbids choosing another name on collision, so a derived name gains little and makes collisions between two agents in the same project likely. A config-file default would give every agent of the same user the same name. An explicit name is also what the agent has to know to recognize messages addressed to it.

### D4 (OQ4). Last activity is the newest history timestamp when the server allows it, otherwise unknown

`channels` lists channels with `LIST`. For each channel it asks the server for the newest message (`CHATHISTORY LATEST <#chan> * 1`) and uses its `server-time`.

- For the listener's own channel this always works when history is enabled.
- For other channels it depends on server policy (S3). If the server refuses, last activity is shown as `-` in text and `null` in JSON.

chatterbox does not join other channels to read their history: that would break FR1.3 and create join/part noise for everyone.

Limit: `CHATHISTORY` is issued for at most 50 channels per invocation, most populated first; the rest show unknown. Configurable.

### D5 (OQ5). Go

- Single static binary, trivial cross-compilation for the "same or different machines" case.
- TLS with custom verification, Unix sockets with peer credentials, and concurrency are all in the standard library.
- Go 1.25 is installed on the development machine.
- Dependencies: `github.com/ergochat/irc-go` (`ircmsg` for line parsing and tag escaping, written by the Ergo authors) and one TOML parser. The connection state machine is written here, not taken from `ircevent`, because the listener needs exact control over nick collision, batches, labels and reconnection.

## 4. Interpretations of ambiguous requirements

### D6. How "important" is marked (FR3.3)

The marking has to be visible to a human in a normal IRC client, so it is part of the message text: the token `[IMPORTANT]` at the start of the body, after the address prefix if there is one.

```
agentA: [IMPORTANT] the migration is failing on staging
```

- Recognition: after stripping an optional leading address prefix, the body starts with `[IMPORTANT]` (case-sensitive) followed by a space or end of text.
- No client-only message tag is used. One source of truth, works for humans who type the token by hand, and survives history replay.
- Agent-facing output reports `important` as a flag and removes the token from the text.

### D7. Who prints an important message (FR2.5, FR3.3)

FR3.3 says the *recipient's* listener always prints it. FR2.5 says the narrow filter prints "messages addressed to this agent plus important ones". Read together and with D1:

- Every listener that receives a message marked important prints it, whatever its wake filter. In a channel that means all listeners, not only the addressee.
- A private important message (`send --private --important`) reaches only its recipient.

`publish --important` follows from the same rule and is provided: it costs nothing and is the natural way to interrupt everyone.

### D8. What "addressed to this agent" means (FR2.2, FR2.5)

A message is addressed to the agent when any of these holds, comparing nicknames under the server's `CASEMAPPING`:

1. It is a private message to the agent's nickname.
2. Its text starts with an address prefix naming the agent: `nick:` or `nick,`, including lists such as `agentA, agentB: ...`.
3. The agent's nickname appears anywhere in the text as a whole word, optionally preceded by `@`. Word boundaries are any characters that are not valid nickname characters.

Rule 3 matches how IRC clients highlight and is what a human will naturally type ("can agentA look at this"). Its cost is an occasional false wake.

### D9. What "accepted" means (FR3.5)

A send command exits zero only when the server has echoed the message back to the listener. The listener uses `labeled-response` and `echo-message` to correlate. Therefore:

- Listener not connected or not joined (for example during reconnection): the command fails immediately. Nothing is queued.
- Server rejects (cannot send to channel, no such nick, multiline limit, flood): the command fails with the server's reason.
- No echo within 10 seconds: the command fails with a timeout that says the message may or may not have been delivered.

### D10. Nickname in use on reconnect (FR1.4, FR1.6)

On the first connection, "nickname in use" ends the listener immediately. On a reconnect the old connection may still hold the nickname until the server times it out. The listener then keeps retrying with backoff for up to 5 minutes, never picks another name, and exits with the FR1.4 error if the nickname is still taken.

### D11. Upper bound on one message (FR3.4)

FR3.4 has no upper bound. The plan adds one as flood protection: a message larger than 64 KiB (16 multiline messages at the probed server limit) is refused with an explanation. Configurable with `max_message_bytes`.

### D12. Default wake filter

`--wake all` is the default. Two agents coordinating through `publish` have to hear each other without extra setup. `--wake addressed` is opt-in and can be made the default in the config file.

### D13. Messages that are not plain channel text

- `NOTICE` from a user is delivered like `PRIVMSG`. Server notices are dropped.
- CTCP `ACTION` (`/me`) is delivered with the text `* <nick> <action>`. Other CTCP requests are dropped and not answered.
- IRC formatting codes (bold, color and so on) are stripped from delivered text.

### D14. Leaving the channel involuntarily

If the listener is kicked, or its join is refused on reconnect (banned, invite-only, key), it exits non-zero with the reason. It does not loop trying to rejoin a channel it was removed from.

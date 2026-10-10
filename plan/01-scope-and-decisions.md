# 01. Scope and decisions

## 1. Scope

In scope: everything under FR1–FR8 of issue #1, delivered as a single binary `chatterbox` with the subcommands `listen`, `publish`, `send`, `whos-there`, `channels`, `create-channel`, plus an agent instruction block.

Out of scope, as stated in the issue:

- Running or managing the IRC server.
- Registered accounts, SASL, nickname protection.
- Listening on more than one channel at once.
- A local message store. The listener keeps only in-memory state (time and id of the last message seen, a bounded set of recent ids).
- Harness-specific hooks or extensions.

### Additions the issue does not ask for

Each is small and serves a requirement, but none is in the issue. They are listed here so the owner can strike any of them (§5).

| Addition | Why |
|---|---|
| `publish -`, `send <nick> -` read the message from stdin | quoting long multi-line text in a shell is error-prone (FR3.4) |
| `send --private` | the private half of OQ1 (D1) |
| `publish --important` | follows from D7; the natural way to interrupt everyone |
| `replayed` flag on output lines | tells the agent a message is old (FR1.6) |
| `(i/n)` marker on the parts of a split message | lets the recipient see that several lines are one message (FR3.4) |
| `version`, `help` subcommands | ordinary CLI hygiene |
| `tls.ca_file`, `tls.trust_on_first_use` config keys | private CAs; opting out of automatic pinning (FR6.2) |
| `status` control operation | listener discovery (FR5.2); not a documented command |
| Release builds for macOS as well as Linux | "same or different machines" |

## 2. What is known about the target server

Read from the Ergo instance on `tiny2` on 2026-10-10 with a `CAP LS 302` before registration (no nickname was taken, nothing was posted):

- TLS on 6697. Port 6667 is closed.
- The certificate is self-signed (`O = Ergo`). Its subject alternative names are `irc.heapzilla.eu`, `localhost`, `127.0.0.1`, `::1`. Connecting as `tiny2` or by LAN address fails host name verification as well as chain verification. FR6.2 pinning has to cover both.
- Capabilities relevant here: `message-tags`, `server-time`, `batch`, `draft/multiline=max-bytes=4096,max-lines=100`, `draft/chathistory`, `draft/event-playback`, `echo-message`, `labeled-response`, `multi-prefix`, `standard-replies`, `userhost-in-names`, `extended-join`, `no-implicit-names`, `sasl=PLAIN,EXTERNAL,SCRAM-SHA-256`, `draft/account-registration=before-connect`.

From Ergo 2.19's shipped `default.yaml` (the deployment on tiny2 may differ; S4, S9 check):

- Flood control ("fakelag") is on: a burst of 5 commands, then 2 per second. Some commands have a per-session budget before that applies (`CHATHISTORY` 16, `WHO` 4).
- Channel history is kept in memory, 2 048 messages per channel; one `CHATHISTORY` request returns at most 1 000.
- Idle clients are pinged after 1m30 and disconnected after 2m30. Registration times out after 60 s.
- Per IP address: 16 concurrent connections, 32 new connections per 10 minutes.
- Any client may create a channel by joining it (`operator-only-creation: false`).

Not yet verified: everything in the spike list of [08-risks-and-spikes.md](08-risks-and-spikes.md). A post-registration probe of tiny2 was not run.

## 3. Decisions on the issue's open questions

D1–D5 were accepted by the issue owner on 2026-10-10.

### D1 (OQ1). `send` is a channel message addressed to the nick; private delivery is an option

- `chatterbox send <nick> <message>` posts `<nick>: <message>` to the listener's channel.
- `chatterbox send --private <nick> <message>` sends an IRC private message instead.

Why: the primary use case has a human watching and steering (FR7.1). Agent-to-agent traffic the human cannot see defeats that. An addressed channel message is also recoverable from channel history after a disconnect (FR1.6); private messages probably are not without accounts (S5).

Consequences:

- With wake filter `all`, every agent in the channel is woken by a `send` to another agent.
- FR3.3's "printed regardless of the wake filter" adds nothing for an addressed `send`: an addressed message is printed under either filter anyway. The `important` flag still reaches the recipient and the human; the filter bypass only matters for unaddressed important messages (D7).

Containment: the send path takes a `private` boolean. Flipping the default is a one-line change plus docs.

### D2 (OQ2). `create-channel` moves the listener into a new channel

Without accounts an IRC channel exists only while occupied, so "create" can only mean "be the first to join". Registering a permanent channel needs an account, which is out of scope.

`chatterbox create-channel <#name>`:

1. Validates the name.
2. Fails if the channel already exists.
3. Posts one line in the current channel, `moving to <#name>`, so peers and the human know where the agent went.
4. The listener parts its current channel and joins the new one. It still has exactly one channel (FR1.3).
5. Confirms it created the channel by checking that the server made it channel operator. If not, someone else got there first: the listener goes back to its previous channel, catches up on what it missed there, and the command fails.

Full behavior, including what happens when going back fails and which channel a reconnect uses, is in 03 §8.

Consequences:

- The channel disappears when its last participant leaves. The command's output says so.
- Other agents do not follow automatically. Each has to restart its listener with the new `--channel`.

Alternative B, not taken: `create-channel` only validates the name and checks that it is free, changes nothing, and tells the agent to run `listen --channel <#name>`. It remains the fallback if S8 shows that joining does not create a channel on the target server.

### D3 (OQ3). The nickname is always explicit; nothing is derived

`listen` takes its nickname from `--as`, then `CHATTERBOX_NICK`. With neither it exits with a usage error. No nickname is derived from harness or project name, and there is no `nick` key in the config file.

Why: FR1.4 forbids choosing another name on collision, so a derived name gains little and makes collisions between two agents in the same project likely. A config-file default would give every agent of the same user the same name. An explicit name is also what the agent must know to recognize messages addressed to it.

### D4 (OQ4). Last activity is the newest history timestamp where the server exposes it

`channels` lists channels with `LIST`. For each it asks for the newest message (`CHATHISTORY LATEST <#chan> * 1`) and uses its server time.

- For the listener's own channel this works whenever history is enabled.
- For other channels it depends on server policy. Ergo is expected to refuse history to non-members (S3). Then last activity is shown as `-` in text and `null` in JSON.

**This means FR4.2 is expected to be only partly met**: names for all channels, last activity for the listener's own channel only. chatterbox will not join other channels to read their history: that breaks FR1.3 and makes join/part noise for everyone. The owner accepted this on 2026-10-10. S3 still runs first in M0, to establish which of the two cases applies and to fix what IT-CHANNELS asserts. If the server turns out to allow the lookup, FR4.2 is met in full.

### D5 (OQ5). Go

- Single static binary, trivial cross-compilation for the "same or different machines" case.
- TLS with custom verification, Unix sockets with peer credentials, and concurrency are in the standard library.
- Go 1.25 is installed on the development machine.
- Dependencies: `github.com/ergochat/irc-go/ircmsg` (line parsing and tag escaping, from the Ergo authors) and one TOML parser. The connection state machine is written here, because the listener needs exact control over nick collision, batches, labels and reconnection.

## 4. Interpretations and further decisions

### D6. How "important" is marked (FR3.3)

The marking has to be visible to a human in a normal IRC client, so it is part of the text: the token `[IMPORTANT]` at the start of the body, after the address prefix if there is one.

```
agentA: [IMPORTANT] the migration is failing on staging
```

- An address prefix is, syntactically: one or more nickname-shaped tokens separated by `,` and optional spaces, ended by `:` or `,` and a space. Whether the names are present in the channel does not matter.
- The message is important when the text after that optional prefix starts with `[IMPORTANT]` (case-sensitive) followed by a space or the end of the text.
- No client-only message tag is used. One source of truth, works for a human who types the token, survives history replay.
- Agent-facing output reports `important` as a flag and removes the token from the text.

### D7. Who prints an important message (FR2.5, FR3.3)

Under `--wake addressed` a listener prints an important message when:

- it is addressed to this agent (which prints anyway), or
- it has no addressee at all (`publish --important`, or a human typing `[IMPORTANT] ...`).

An important message addressed to somebody else is not printed under `addressed`. This follows FR3.3's wording, "the recipient's listener", and keeps wake-ups down.

### D8. What "addressed to this agent" means (FR2.2, FR2.5)

Nicknames are compared under the server's `CASEMAPPING`. A message is addressed to the agent when:

1. it is a private message to the agent's nickname; or
2. its address prefix (D6) names the agent: `agentA: ...`, `agentA, agentB: ...`; or
3. it contains `@<nick>` anywhere, not followed by another nickname character.

A bare mention ("agentA finished the migration") is not an address. Agent nicknames are restricted to letters, digits, `_` and `-` (04 §5), which makes the boundary in rule 3 unambiguous.

### D9. What "accepted" means (FR3.5)

FR3.5 says a send command exits zero "only after the listener has accepted the message". The plan reads this strictly: the listener accepts a message when the server has echoed it back (`echo-message` with `labeled-response`). So:

- Listener not `ready`: the command waits up to 5 s for it to become ready (covers a short blip), then fails with "not sent". Nothing is queued beyond that.
- Server rejects: the command fails with the server's reason. "Not sent".
- No echo in time: the command fails with exit 8 and says delivery is unknown, so the agent should check the channel before repeating itself.
- A split message that fails part-way reports how many parts were delivered.

A looser reading (exit zero once the listener has queued the message) is simpler but lets a command succeed for a message that never arrives. Owner's call (§5).

### D10. Nickname in use on reconnect (FR1.4, FR1.6)

On the first connection, "nickname in use" ends the listener immediately. On a reconnect the server may still hold the nickname for the old connection until its idle timeout (2m30 by default). The listener then keeps redialing with backoff for up to 5 minutes, never picks another name, and exits with the FR1.4 error if the nickname is still taken. The bound is confirmed by S7.

### D11. Upper bound on one message (FR3.4)

FR3.4 has no upper bound. The plan adds one as flood protection: text larger than 64 KiB is refused with an explanation before anything is sent. At the probed limits that is roughly 17 multiline messages, which the server's flood control spreads over more than a minute. Configurable with `limits.max_message_bytes`; `0` disables it.

### D12. Default wake filter

`--wake all` is the default: two agents coordinating through `publish` must hear each other without extra setup (acceptance step 3). With three or more agents and D1 this gets noisy; `--wake addressed` is then the better setting and can be made the default in the config file.

### D13. Messages that are not plain channel text

- `NOTICE` from a user is delivered like `PRIVMSG`. Server notices are dropped.
- CTCP `ACTION` (`/me`) is delivered with the text `* <nick> <action>`. Other CTCP requests are dropped and not answered.
- IRC formatting codes (bold, color and so on) are stripped from delivered text.

### D14. Leaving the channel involuntarily

If the listener is kicked, or its join is refused (banned, invite-only, key), it exits non-zero with the reason. It does not loop trying to rejoin.

### D15. Auto-detection does not apply to commands that post (FR5.2 vs FR5.3)

FR5.2 asks for auto-detection "when exactly one listener is running for the current user". FR5.3 asks that agents cannot post as one another by accident. They collide in an ordinary situation: agent B's listener has exited (kicked, crashed, name taken) while agent A's is running; B's next `publish` finds exactly one listener and posts as A.

An exported `CHATTERBOX_NICK` does not reliably prevent this: Claude Code's shell tool does not keep environment variables between commands.

Decision:

- `whos-there` and `channels` auto-detect as FR5.2 says.
- `publish`, `send` and `create-channel` require an explicit nickname (`--as` or `CHATTERBOX_NICK`). Without one they fail and list the running listeners.
- The agent instructions use `--as <nick>` on every command.

This narrows FR5.2 and needs sign-off. If rejected, auto-detection applies to all commands with a stderr notice naming the identity used, and FR5.3 rests on the instructions alone.

### D16. Failures before the first successful connection are fatal

If the first connection attempt fails (name resolution, refused, TLS, registration), the listener exits non-zero with the reason. An agent that mistyped the server gets an answer instead of a silent retry loop. Reconnection with backoff (FR1.6) starts once the listener has been `ready`.

### D17. No cap on catch-up

After a reconnect the listener delivers everything the server returns for the gap, as FR1.6 says. The server's own retention bounds it. If the listener can tell that history does not reach back to where it left off, it says so on stderr; whether the agent should also see a line on stdout for that is a question for the owner (it would be the one exception to FR2.6).

## 5. Sign-off

Accepted by the issue owner on 2026-10-10: **D1**, **D2**, **D3**, **D4**, **D5**.

Still needed:

- Before M4: **D9**, **D11**, **D15**.
- Any time before M8: D7, D8, D12, D17's stdout question, and the additions in §1.

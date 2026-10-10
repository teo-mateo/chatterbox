# chatterbox

A command-line IRC client that lets AI coding agents (Claude Code, Pi, others) talk to each other, and to a human, over a standard IRC server. The human joins with any ordinary IRC client and is a full participant.

An agent runs one long-lived process, the **listener**, in the background. It holds the agent's single connection and prints one line per incoming message, which is what wakes the agent. Short commands (`publish`, `send`, `whos-there`, ...) talk to the listener over a local socket; they never connect to the server themselves.

```
 agent harness                               IRC server (TLS)
 ┌────────────────────────────────┐                  ▲
 │ background:                    │  one connection  │
 │   chatterbox listen ───────────┼──────────────────┘
 │     │ stdout: one line/message │
 │     └ control socket ◄─────────┼── chatterbox publish / send / whos-there / ...
 └────────────────────────────────┘
```

chatterbox does not include a server. It is developed against [Ergo](https://ergo.chat) 2.19 and needs an IRCv3 server ([requirements](#server-requirements)).

## Features

**Receiving**

- One line of output per incoming message, so any harness that can watch a background process can be woken by it. Multi-line messages stay on one line (`\n` escaped).
- Each line says when, where, who, whether the sender is a bot or a human, and whether the message is addressed to the agent, important, or replayed.
- Text or JSON output (`--json`).
- A wake filter: `--wake all` prints everything, `--wake addressed` only what is addressed to the agent, private messages, and important broadcasts.
- Nothing but messages on standard output: no joins, quits, topic changes, server notices, or the agent's own messages.

**Sending**

- `publish` to the whole channel, `send` to one participant (in the channel, so the human can follow, or `--private`).
- `--important` marks a message so that it gets through the wake filter.
- A command succeeds only after the server has confirmed the message. A failure says whether the message was not sent, partly sent, or possibly sent.
- Long and multi-line messages are sent as one IRCv3 multiline message; text over the server's limit is split into numbered parts `(i/n)`. Messages can be piped in on standard input.
- Messages from one agent arrive in the order they were sent.

**Presence and discovery**

- `whos-there` lists the participants of the channel and whether each is a bot or a human.
- `channels` lists the channels on the server with user counts and last activity.
- `create-channel` moves the agent into a new channel.
- Agents mark themselves as bots (`+B`); the human needs nothing but an ordinary IRC client.
- An agent is in the channel exactly as long as its listener runs. The listener leaves when it is stopped, when its output is no longer read, and when the process that started it disappears.

**Resilience**

- Automatic reconnection with backoff after a lost connection, including connections that die silently.
- Messages missed while disconnected are fetched from the server's history and delivered once each, in order, flagged `replayed`. A history that does not reach back far enough is reported.
- The nickname is never changed: if it is taken the listener says so and exits, rather than appearing under another name.
- Timeouts that follow the server's flood control, so a slow answer after a long message is not mistaken for a lost one.

**Identity and safety**

- Several agents on one machine, each with its own listener and nickname; one listener per nickname, enforced by a lock.
- Commands that post require the agent's name (`--as`), so an agent cannot speak under another's name by accident.
- TLS always; self-signed servers are pinned on first use and a changed certificate is refused.
- The control socket is private to the user (file mode and peer check).
- A size cap on single messages (64 KiB by default).

**Packaging**

- One static binary for Linux and macOS (amd64, arm64); one TOML configuration file, all of it optional except the server.
- Instructions for agents as a Claude Code skill and as a paste-in block.
- Distinct exit codes for every kind of failure.

## Install

On Linux (amd64 or arm64), this installs the latest release into `~/.local/bin`, after verifying its checksum:

```
curl -fsSL https://raw.githubusercontent.com/teo-mateo/chatterbox/main/scripts/install.sh | sh
```

Run it again to update. Options, as environment variables of the `sh`:

- `CHATTERBOX_VERSION=v0.1.0` installs that release; `CHATTERBOX_VERSION=edge` installs the newest build of `main`, which is replaced on every push.
- `CHATTERBOX_INSTALL_DIR=/some/dir` installs elsewhere.

The binaries themselves are on the [releases page](https://github.com/teo-mateo/chatterbox/releases). `chatterbox version` prints what is installed: `v0.1.0` for a release, `v0.1.0-3-gabc1234` for a build three commits past it.

From source, with Go 1.25 or newer:

```
make build          # produces bin/chatterbox
make release        # linux and macOS, amd64 and arm64, in dist/
```

The result is a single static binary. Put it on the `PATH` of every agent.

## Configure

`~/.config/chatterbox/config.toml` (or `$XDG_CONFIG_HOME/chatterbox/config.toml`):

```toml
[server]
host = "irc.example.org"
port = 6697

[defaults]
channel = "#hof"
wake = "all"        # or "addressed"
format = "text"     # or "json"

[tls]
# Accept exactly this certificate (SHA-256 of the certificate) and never pin automatically.
# fingerprint = "44:0D:EE:..."
# Extra CA bundle for a private CA.
# ca_file = "/path/to/ca.pem"
# Set to false to refuse unverifiable certificates instead of pinning them on first use.
trust_on_first_use = true

[limits]
max_message_bytes = 65536        # larger messages are refused; 0 disables the cap
channels_history_lookups = 50
```

Everything is optional except the server, and `--server`, `--port`, `--channel`, `--wake`, `--json` override the file. Only `listen` reads it. There is no nickname setting: every agent is given its name explicitly.

### TLS

TLS is always on. A certificate that verifies against the system roots is simply accepted. One that does not (a self-signed server) is **pinned on the first connection**: its fingerprint is printed, recorded in `~/.config/chatterbox/known_servers`, and from then on only that certificate is accepted. If it changes, the listener refuses to connect and explains how to accept the new one. Compare the printed fingerprint with the server's own:

```
openssl s_client -connect irc.example.org:6697 </dev/null 2>/dev/null | openssl x509 -noout -fingerprint -sha256
```

## Quick start: two agents and a human

**The human** joins `#hof` with any IRC client (TLS, port 6697).

**Each agent** gets a distinct nickname and the instructions in [`skills/chatterbox/SKILL.md`](skills/chatterbox/SKILL.md) (a Claude Code skill; the same text as a paste-in block is in [`docs/agent-snippet.md`](docs/agent-snippet.md)). In short, the agent starts its listener in the background and watches its output:

```
chatterbox listen --as agentA --channel '#hof'
```

and then uses, always with its own name:

```
chatterbox whos-there --as agentA
chatterbox publish --as agentA "tests are green on the plan branch"
chatterbox send --as agentA agentB "can you review the parser change?"
chatterbox send --as agentA --important agentB "stop: the migration is failing on staging"
```

What agentB's listener prints:

```
2026-10-10T12:34:56Z #hof agentA bot - | tests are green on the plan branch
2026-10-10T12:35:10Z #hof agentA bot to-you | agentB: can you review the parser change?
2026-10-10T12:35:40Z #hof agentA bot to-you,important | agentB: stop: the migration is failing on staging
```

What the human sees in their client is ordinary IRC:

```
<agentA> tests are green on the plan branch
<agentA> agentB: can you review the parser change?
<agentA> agentB: [IMPORTANT] stop: the migration is failing on staging
```

**Addressing an agent as a human.** Start the message with the agent's nickname and a colon or comma, or mention it with `@`:

```
agentB: please rebase first
agentA, agentB: stand-up in five minutes
can you look at this, @agentB?
```

A bare mention (`agentB finished the migration`) is not an address. This matters when an agent listens with `--wake addressed`: it is then only woken by messages addressed to it, private messages, and messages that start with `[IMPORTANT]` and are addressed to nobody. Type `[IMPORTANT] everyone stop` to interrupt every agent.

Quote channel names in a shell: an unquoted `#` starts a comment.

## Commands

| Command | What it does |
|---|---|
| `chatterbox listen --as <nick> --channel '<#name>' [--wake all\|addressed] [--json] [--server h] [--port n] [--config path] [--verbose]` | Connects, takes the nickname, marks itself as a bot (`+B`), joins the one channel and stays until stopped. If the nickname is in use it exits; it never picks another. |
| `chatterbox publish --as <nick> [--important] <message...\|->` | Posts to the listener's channel. |
| `chatterbox send --as <nick> [--important] [--private] <nick> <message...\|->` | Posts `<nick>: <message>` in the channel; with `--private`, an IRC private message instead. |
| `chatterbox whos-there [--as <nick>] [--json]` | Who is in the channel: `<nick> <bot\|human>`. |
| `chatterbox channels [--as <nick>] [--json]` | Channels on the server: `<#name> <users> <last-activity\|->`. |
| `chatterbox create-channel --as <nick> '<#name>'` | Moves the listener into a new channel (see [limitations](#limitations)). |
| `chatterbox help [command]`, `chatterbox version` | |

- A lone `-` as the message reads it from standard input. Put `--` before text that starts with a dash.
- A command finds its listener by `--as`, then the `CHATTERBOX_NICK` environment variable. `whos-there` and `channels` also pick the only running listener when neither is given. Commands that post never guess: an agent whose listener has died must not speak under another agent's name.
- Several agents can run on one machine, each with its own listener; one listener per nickname.

### Output

Text (default), one line per message:

```
<time> <channel> <sender> <bot|human> <flags> | <text>
```

`channel` is `private` for a private message. `flags` is `-` or a comma-separated subset of `to-you`, `important`, `replayed`. In the text, a backslash is doubled and line breaks, carriage returns and tabs are written `\n`, `\r`, `\t`, so a multi-line message is still one line. The five fields before ` | ` never contain spaces.

`--json`, one object per line:

```json
{"time":"2026-10-10T12:35:40Z","channel":"#hof","from":"agentA","kind":"bot","addressed":true,"important":true,"private":false,"replayed":false,"msgid":"abc123","text":"agentB: stop: the migration is failing on staging"}
```

Only messages go to standard output. The agent's own messages, joins, quits, topic changes and other server noise are never printed. Diagnostics go to standard error, each line starting with `chatterbox:`.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | success; `listen`: stopped by a signal, or its reader went away |
| 1 | internal error; `listen`: its output was not being read |
| 2 | usage, input or configuration error |
| 3 | no listener for the nickname, or a nickname is required and none was given |
| 4 | nickname already in use |
| 5 | server certificate not trusted, or changed |
| 6 | refused: cannot join, kicked, cannot send, addressee not in the channel, channel exists |
| 7 | the listener is not connected to the server; message not sent |
| 8 | timed out; the message may or may not have been sent |
| 9 | `listen`: could not reach or register with the server on the first attempt |

A send command exits 0 only after the server has echoed the message back. Every failure says whether the message was not sent, partly sent, or possibly sent.

## Behavior worth knowing

- **Long messages.** Text with line breaks, or longer than one IRC line, is sent as one IRCv3 multiline message. Text over the server's multiline limit (4096 bytes or 100 lines on Ergo) is split into several messages, each starting with `(i/n)`. The server's flood control then slows things down: about half a second per line sent. A 10 000-byte message takes ten seconds or more; the command waits and reports when it is done.
- **Reconnection.** After an unexpected disconnect the listener reconnects with backoff and fetches what it missed from the server's history. Those messages are printed once each, in order, flagged `replayed`. If the very first connection fails, the listener exits instead of retrying, so a mistyped server name gets an answer.
- **Presence reflects reality.** The listener leaves the channel when it is stopped (SIGINT, SIGTERM, SIGHUP), when the reader of its output goes away, when its output is not read for a minute, and when the process that started it disappears. Start it with `nohup` to make it outlive its parent.
- **Kicked** or refused by the channel: the listener exits; it does not try to get back in.

## Server requirements

An IRC server with the IRCv3 capabilities `message-tags`, `server-time`, `batch`, `draft/multiline`, `draft/chathistory`, `echo-message`, `labeled-response`, and a bot mode advertised in `ISUPPORT` (`BOT=B`). The listener exits with an error naming what is missing. Ergo 2.19 with its default configuration meets all of this. In particular:

- **History on** (`history.enabled: true`), otherwise missed messages cannot be recovered after a disconnect.
- **No forced accounts** (`accounts.require-sasl.enabled: false`). chatterbox does not log in.
- Unregistered clients may create channels (`channels.operator-only-creation: false`) if `create-channel` is to work.

## Limitations

- **No authentication.** There are no accounts or passwords. Nicknames are first come, first served; `bot` and `human` only say whether the participant set the bot flag; anyone on the server can send an agent a private message. Run it on a server you control.
- **Private messages are not recovered** after a disconnect; channel messages are. That is one reason `send` posts in the channel by default. While the addressee is disconnected, a private `send` fails with "no such nick".
- **Channels are ephemeral.** Without accounts a channel exists only while someone is in it. `create-channel` therefore moves the listener into the new channel; other agents have to restart their listeners there.
- **Last activity** in `channels` is known only for the listener's own channel. The server does not expose the history of channels the client has not joined, and a listener is in exactly one channel.
- **One channel per listener.**
- Linux and macOS only.

## Development

```
make test               # unit tests, nothing external
make test-integration   # integration and acceptance tests against a private Ergo
make lint
make ergo-up            # a local Ergo on 127.0.0.1:16697 for manual work; make ergo-down stops it
```

### Releasing

Versions follow [semantic versioning](https://semver.org); before 1.0 the output format and the commands may still change between minor versions. To release, tag a commit of `main` that CI has passed and push the tag:

```
git tag -a v0.2.0 -m "chatterbox v0.2.0"
git push origin v0.2.0
```

CI runs the tests again and publishes a release of that name with the Linux binaries and their checksums.

The integration tests download a pinned Ergo release on first use (into `~/.cache/chatterbox-test/ergo`, or `$CHATTERBOX_ERGO_CACHE`), verify its checksum, and start private servers on free ports. The design is in [`plan/`](plan/README.md).

## License

MIT, see [LICENSE](LICENSE).

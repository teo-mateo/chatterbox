# 04. Commands, output, configuration

## 1. Commands

```
chatterbox listen --as <nick> --channel <#name> [--wake all|addressed] [--json]
                  [--server <host>] [--port <n>] [--config <path>]
chatterbox publish [--important] [--as <nick>] <message...|->
chatterbox send [--important] [--private] [--as <nick>] <nick> <message...|->
chatterbox whos-there [--json] [--as <nick>]
chatterbox channels [--json] [--as <nick>]
chatterbox create-channel [--as <nick>] <#name>
chatterbox version
chatterbox help [command]
```

Common rules:

- Several message arguments are joined with single spaces. A lone `-` reads the message from stdin. `--` ends flag parsing, for messages that start with `-`.
- `--as` on a control command selects the listener (identity). It never opens a connection.
- Control commands do not read the server settings; they only need the socket.

### `listen`

| Flag | Default | Notes |
|---|---|---|
| `--as <nick>` | `CHATTERBOX_NICK` | required one way or the other (D3) |
| `--channel <#name>` | config `defaults.channel` | exactly one; repeating the flag or passing a comma-separated list is a usage error (FR1.3) |
| `--wake all\|addressed` | config `defaults.wake`, else `all` | FR2.5 |
| `--json` | config `defaults.format = "json"` | FR2.3 |
| `--server`, `--port` | config `server.host`, `server.port` (6697) | FR6.1 |
| `--config <path>` | `$XDG_CONFIG_HOME/chatterbox/config.toml`, else `~/.config/chatterbox/config.toml` | |

### Output of control commands

| Command | stdout on success | 
|---|---|
| `publish`, `send` | nothing (when the message was split, stderr says `sent as N messages`) |
| `whos-there` | one participant per line: `<nick> <bot\|human>`; with `--json`, one object per line `{"nick":"...","kind":"bot"}` |
| `channels` | one channel per line: `<#name> <users> <last-activity\|->`; with `--json`, `{"name":"...","users":3,"last_activity":"..."\|null}` |
| `create-channel` | `now listening on <#name> (the channel exists only while someone is in it)` |

Errors go to stderr as `chatterbox: <explanation>`.

## 2. Exit codes

| Code | Meaning |
|---|---|
| 0 | success; for `listen`, terminated by signal or closed stdout |
| 1 | unexpected internal error |
| 2 | usage or configuration error (bad flags, no nickname, invalid nickname or channel name, missing server setting) |
| 3 | no listener running for the nickname, or several listeners and no nickname given (FR3.6, FR5.2) |
| 4 | nickname already in use (FR1.4) |
| 5 | TLS failure: certificate pin mismatch or untrusted certificate (FR6.2) |
| 6 | refused by the server (cannot join, kicked, cannot send, channel exists) |
| 7 | listener is not connected to the server right now |
| 8 | timed out waiting for the listener or the server |

## 3. Listener output (FR2)

stdout carries message lines and nothing else. No banner, no connection notices, no blank lines. stderr carries diagnostics, prefixed `chatterbox:`.

### Text format (default)

```
<time> <channel> <sender> <kind> <flags> | <text>
```

| Field | Value |
|---|---|
| time | server time in UTC, `2026-10-10T12:34:56Z` |
| channel | channel name, or `private` for a private message |
| sender | nickname |
| kind | `bot` or `human` |
| flags | comma-separated subset of `to-you`, `important`, `replayed`; `-` when none |
| text | message text, escaped |

Examples:

```
2026-10-10T12:34:56Z #hof agentB bot - | tests are green on the plan branch
2026-10-10T12:35:10Z #hof teo human to-you | agentA please rebase first
2026-10-10T12:35:40Z #hof agentB bot to-you,important | staging deploy is failing
2026-10-10T12:36:02Z #hof agentB bot - | line one\nline two
2026-10-10T12:36:30Z private agentB bot to-you | only you can see this
```

The first five fields never contain spaces, so the line can be split on the first ` | `.

Escaping of text, so that one message is exactly one line (FR2.1):

- `\` becomes `\\`, newline becomes `\n`, carriage return `\r`, tab `\t`.
- Other C0 control characters, DEL, U+0085, U+2028 and U+2029 become `\u{XXXX}`.
- Invalid UTF-8 bytes are replaced with U+FFFD.

### JSON format (`--json`)

One object per line, keys in this order, no embedded newlines (the JSON encoder escapes them):

```json
{"time":"2026-10-10T12:35:40Z","channel":"#hof","from":"agentB","kind":"bot","addressed":true,"important":true,"private":false,"replayed":false,"msgid":"abc123","text":"staging deploy is failing"}
```

For a private message `channel` is `null` and `private` is `true`. `text` is the cleaned text (formatting codes stripped, `[IMPORTANT]` removed, address prefix kept). U+2028 and U+2029 are written as ` ` and ` `.

### Wake filter (FR2.5)

| `--wake` | Printed |
|---|---|
| `all` | every chat message in the channel and every private message to the agent |
| `addressed` | messages addressed to the agent (D8), plus every important message (D7) |

Never printed under either setting: the agent's own messages, joins, parts, quits, nick changes, modes, topics, server notices (FR2.4).

### Diagnostics on stderr (FR2.6)

Connection state changes (`connecting to tiny2:6697`, `joined #hof as agentA`, `connection lost: ...; reconnecting in 4s`, `caught up: 12 messages replayed`), certificate pinning notices, and errors. One line each.

## 4. Configuration file (FR6.1)

`~/.config/chatterbox/config.toml`:

```toml
[server]
host = "tiny2"
port = 6697

[tls]
# Optional. When set, this is the only certificate accepted and nothing is
# pinned automatically. SHA-256 of the certificate in DER form, hex.
# fingerprint = "44:0D:..."
# Optional extra CA bundle for a private CA.
# ca_file = "/path/to/ca.pem"

[defaults]
channel = "#hof"
wake = "all"        # or "addressed"
format = "text"     # or "json"

[limits]
max_message_bytes = 65536
channels_history_lookups = 50
```

Precedence: flag, then environment (`CHATTERBOX_NICK` only), then config file, then built-in default.

- The file is optional if `--server` is given. With neither, `listen` exits 2 and names the file to create.
- Unknown keys are an error naming the key, to catch typos.
- There is no `nick` key (D3).

## 5. Identity (FR5)

- Valid nickname for chatterbox: 1 to `NICKLEN` characters (checked against 32 before connecting, against the server value after), from `A–Z a–z 0–9 _ - [ ] { } \ | ^` and backquote, not starting with a digit or `-`. This is stricter than Ergo allows and keeps socket file names safe. The server has the final say (432).
- `listen`: `--as`, then `CHATTERBOX_NICK`, else exit 2.
- Control commands: `--as`, then `CHATTERBOX_NICK`, then auto-detection (02 §4).
- Protection against posting as another agent (FR5.3):
  - one socket per nickname, so a command reaches exactly the identity it names;
  - auto-detection only ever succeeds with exactly one live listener and announces the nickname it picked on stderr;
  - with two or more listeners and no nickname the command fails rather than guess;
  - the agent instructions tell each agent to export `CHATTERBOX_NICK` in its own environment;
  - sockets are per user (`0600`, peer uid check), so other users cannot post at all.

## 6. TLS and certificate pinning (FR6.2)

TLS 1.2 or newer is always used. Verification, in order:

1. `tls.fingerprint` set in config: accept exactly that certificate. Anything else: exit 5.
2. A pin exists for `host:port` in the pin file: accept exactly that certificate. Anything else: exit 5 with

   ```
   chatterbox: certificate for tiny2:6697 has changed.
     pinned:    SHA256:44:0D:...
     presented: SHA256:9A:11:...
   If the server's certificate was deliberately replaced, remove the line for
   tiny2:6697 from ~/.config/chatterbox/known_servers and connect again.
   Otherwise someone may be intercepting the connection.
   ```

3. No pin: verify against the system roots (plus `tls.ca_file`) including the host name. Success: connect, pin nothing.
4. Verification failed and no pin: trust on first use. Record the fingerprint, print it on stderr so it can be compared out of band, and connect.

Pin file: `~/.config/chatterbox/known_servers`, one `host:port SHA256:<hex>` per line, mode `0600`, written atomically (temporary file and rename) under a file lock so two listeners starting at once do not corrupt it.

The fingerprint is the SHA-256 of the leaf certificate in DER form, the value `openssl x509 -fingerprint -sha256` prints.

A pinned certificate is accepted regardless of host name and expiry. This is what makes the probed server usable: its certificate does not name `tiny2`.

During reconnection a pin mismatch is fatal like on first connect.

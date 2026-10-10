# 04. Commands, output, configuration

## 1. Commands

```
chatterbox listen --as <nick> --channel <#name> [--wake all|addressed] [--json]
                  [--server <host>] [--port <n>] [--config <path>] [--verbose]
chatterbox publish --as <nick> [--important] <message...|->
chatterbox send --as <nick> [--important] [--private] <nick> <message...|->
chatterbox whos-there [--as <nick>] [--json]
chatterbox channels [--as <nick>] [--json]
chatterbox create-channel --as <nick> <#name>
chatterbox version
chatterbox help [command]
```

Argument rules:

- Flags may appear before, between or after positional arguments: `send agentB --important "x"` and `send --important agentB "x"` are the same.
- `--` ends flag parsing. Everything after it is positional, so a message starting with `-` is written `publish --as a -- "-1 is wrong"`.
- An argument before `--` that starts with `-` and is not a known flag is a usage error (exit 2). It is never sent as text.
- Several message arguments are joined with single spaces. A lone `-` reads the message from stdin.
- `--as` on a control command names the listener to use (the identity). `CHATTERBOX_NICK` is the alternative. `publish`, `send` and `create-channel` need one of them (D15).

### `listen`

| Flag | Default | Notes |
|---|---|---|
| `--as <nick>` | `CHATTERBOX_NICK` | required one way or the other (D3) |
| `--channel <#name>` | config `defaults.channel` | exactly one; repeating the flag or giving a comma-separated list is a usage error (FR1.3). Must start with `#` or `&`. In a shell the name has to be quoted, since an unquoted `#` starts a comment; the usage errors say so |
| `--wake all\|addressed` | config `defaults.wake`, else `all` | FR2.5 |
| `--json` | config `defaults.format = "json"` | FR2.3 |
| `--server`, `--port` | config `server.host`, `server.port` (6697) | FR6.1 |
| `--config <path>` | `$XDG_CONFIG_HOME/chatterbox/config.toml`, else `~/.config/chatterbox/config.toml` | |
| `--verbose` | off | more diagnostics on stderr |

### Output of control commands

| Command | stdout on success |
|---|---|
| `publish`, `send` | nothing (when the message was split, stderr says `sent as N messages`) |
| `whos-there` | one participant per line: `<nick> <bot\|human>`; with `--json`, one object per line `{"nick":"...","kind":"bot"}` |
| `channels` | one channel per line: `<#name> <users> <last-activity\|->`; with `--json`, `{"name":"...","users":3,"last_activity":"..."}` with `null` for unknown |
| `create-channel` | `now listening on <#name>; the channel exists only while someone is in it` |

Errors go to stderr as `chatterbox: <explanation>`. For `publish` and `send` the explanation always says whether the message was not sent, partly sent, or possibly sent.

## 2. Exit codes

| Code | Meaning | Control error codes mapped here |
|---|---|---|
| 0 | success; for `listen`, terminated by signal or by loss of its reader | |
| 1 | unexpected internal error; `listen`: output not consumed (02 §3) | |
| 2 | usage, input or configuration error | `bad_request`, `too_large`, `unsupported_version` |
| 3 | no listener for the nickname; or a nickname is required and none was given | |
| 4 | nickname already in use (FR1.4) | |
| 5 | certificate not trusted or changed (FR6.2) | |
| 6 | refused: cannot join, kicked, cannot send, addressee not in the channel, channel exists | `rejected`, `unknown_nick`, `channel_exists` |
| 7 | listener is not connected to the server; message not sent | `not_connected` |
| 8 | timed out; delivery unknown | `timeout` |
| 9 | `listen`: could not reach or register with the server on the first attempt (D16); at any time: a required capability or bot mode is missing | |

## 3. Listener output (FR2)

stdout carries message lines and nothing else: no banner, no connection notices, no blank lines. stderr carries diagnostics, prefixed `chatterbox:`.

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
| flags | comma-separated subset of `to-you`, `important`, `replayed`, in that order; `-` when none |
| text | message text, escaped |

```
2026-10-10T12:34:56Z #hof agentB bot - | tests are green on the plan branch
2026-10-10T12:35:10Z #hof teo human to-you | agentA: please rebase first
2026-10-10T12:35:40Z #hof agentB bot to-you,important | agentA: staging deploy is failing
2026-10-10T12:36:02Z #hof agentB bot - | line one\nline two
2026-10-10T12:36:30Z private agentB bot to-you | only you can see this
```

The five fields before the bar never contain spaces, so a line splits on the first ` | `.

The text is the same in both formats: IRC formatting codes stripped, the `[IMPORTANT]` token removed, the address prefix and any part marker kept.

Escaping in the text format, so that one message is exactly one line (FR2.1):

- a backslash becomes two backslashes; newline, carriage return and tab become a backslash followed by `n`, `r`, `t`;
- every other C0 control character, DEL, and the Unicode line terminators NEL, LINE SEPARATOR and PARAGRAPH SEPARATOR become a backslash, `u`, `{`, the hex code point, `}`;
- invalid UTF-8 bytes are replaced with the replacement character.

### JSON format (`--json`)

One object per line, keys in this order:

```json
{"time":"2026-10-10T12:35:40Z","channel":"#hof","from":"agentB","kind":"bot","addressed":true,"important":true,"private":false,"replayed":false,"msgid":"abc123","text":"agentA: staging deploy is failing"}
```

- For a private message `channel` is `null` and `private` is `true`.
- Newlines are escaped by the JSON encoder. LINE SEPARATOR and PARAGRAPH SEPARATOR are also written as JSON unicode escapes.
- HTML escaping is turned off (`SetEscapeHTML(false)`), so `<`, `>` and `&` in code snippets stay readable.

### Wake filter (FR2.5)

| `--wake` | Printed |
|---|---|
| `all` | every chat message in the channel and every private message to the agent |
| `addressed` | messages addressed to the agent (D8), plus important messages with no addressee (D7) |

Never printed under either setting: the agent's own messages, joins, parts, quits, nick changes, modes, topics, server notices (FR2.4).

### Diagnostics on stderr (FR2.6)

Some harnesses show an agent stderr together with stdout, so diagnostics are kept to state changes and errors, one line each: `joined #hof as agentA`, `connection lost: <reason>; reconnecting`, `reconnected; 12 messages replayed`, certificate notices, fatal errors. Individual retry attempts are logged only with `--verbose`.

## 4. Configuration file (FR6.1)

`$XDG_CONFIG_HOME/chatterbox/config.toml`, by default `~/.config/chatterbox/config.toml`:

```toml
[server]
host = "tiny2"
port = 6697

[tls]
# Accept exactly this certificate and never pin automatically.
# SHA-256 of the certificate in DER form.
# fingerprint = "44:0D:EE:..."
# Extra CA bundle for a private CA.
# ca_file = "/path/to/ca.pem"
# Set to false to refuse unverifiable certificates instead of pinning them.
trust_on_first_use = true

[defaults]
channel = "#hof"
wake = "all"        # or "addressed"
format = "text"     # or "json"

[limits]
max_message_bytes = 65536
channels_history_lookups = 50
```

Precedence: flag, then environment (`CHATTERBOX_NICK`), then config file, then built-in default.

- The file is optional when `--server` is given. With neither, `listen` exits 2 and names the file to create.
- Unknown keys are an error naming the key.
- There is no `nick` key (D3).
- Control commands do not read this file. The size cap of `limits.max_message_bytes` is therefore applied by the listener, which answers `too_large`; the other checks on the text are made by the command first.

## 5. Identity (FR5)

- An agent nickname is 1 to 32 characters (and at most the server's `NICKLEN`), letters, digits, `_` and `-`, starting with a letter. Stricter than IRC allows; it keeps socket file names safe and makes `@nick` matching unambiguous. The addressee of `send` may be any nickname the server accepts.
- `listen`: `--as`, then `CHATTERBOX_NICK`, else exit 2.
- `publish`, `send`, `create-channel`: `--as`, then `CHATTERBOX_NICK`, else exit 3 listing the running listeners (D15).
- `whos-there`, `channels`: `--as`, then `CHATTERBOX_NICK`, then auto-detection when exactly one listener is running.

What keeps agents from posting as one another by accident (FR5.3):

- one socket per nickname, so a command reaches exactly the identity it names;
- no posting command ever guesses an identity;
- one listener per nickname per machine (lock file);
- the agent instructions put `--as <own nick>` on every command;
- sockets are per user, so other users cannot post at all.

It does not stop an agent from deliberately naming another agent's nickname. The requirement is about accidents.

## 6. TLS and certificate trust (FR6.2)

TLS 1.2 or newer, always. Verification is done in the TLS library's `VerifyConnection` hook, which also runs on resumed sessions.

Trust file: `$XDG_CONFIG_HOME/chatterbox/known_servers`, one line per `host:port`, mode `0600`, written atomically (temporary file, rename) under a file lock. An entry is either `ca` or a fingerprint.

For a connection to `host:port`:

1. `tls.fingerprint` is set: accept exactly that certificate, else exit 5. The trust file is not consulted or written.
2. The trust file has a fingerprint for it: accept exactly that certificate, else exit 5 with

   ```
   chatterbox: the certificate of tiny2:6697 has changed.
     trusted:   SHA256:44:0D:EE:...
     presented: SHA256:9A:11:02:...
   If the server's certificate was replaced on purpose, remove the tiny2:6697
   line from ~/.config/chatterbox/known_servers and connect again.
   Otherwise someone may be intercepting the connection.
   ```

3. The trust file says `ca`: the certificate must verify against the system roots (plus `tls.ca_file`) for this host name, else exit 5. A server that once presented a valid certificate is never silently downgraded to a pinned one.
4. No entry (first connection): verify against the system roots and host name.
   - Valid: record `ca`, connect.
   - Not valid and `trust_on_first_use` is true: record the fingerprint, print it on stderr for comparison out of band, connect.
   - Not valid and `trust_on_first_use` is false: exit 5, printing the fingerprint and how to set `tls.fingerprint`.

Fingerprints are the SHA-256 of the leaf certificate in DER form, the value `openssl x509 -fingerprint -sha256` prints. They are accepted in any of the usual spellings (with or without colons, either case, optional `SHA256:` prefix) and stored as upper-case hex with colons.

A pinned certificate is accepted regardless of host name and expiry. That is what makes the probed server usable: its certificate does not name `tiny2`.

The same rules apply on every reconnect; a trust failure is always fatal.

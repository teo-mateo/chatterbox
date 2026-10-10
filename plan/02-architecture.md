# 02. Architecture

## 1. Process model

```
 agent harness                               IRC server (Ergo, TLS 6697)
 ┌────────────────────────────────┐                      ▲
 │ background:                    │   one TLS connection │
 │   chatterbox listen ───────────┼──────────────────────┘
 │     │ stdout: one line/message │
 │     │ stderr: diagnostics      │
 │     └ control socket (unix) ◄──┼── chatterbox publish / send /
 │                                │   whos-there / channels / create-channel
 └────────────────────────────────┘   (short-lived, no IRC connection)
```

- `chatterbox listen` is the only process that talks IRC. It holds one connection and one channel.
- Every other subcommand is a control client: it connects to the listener's Unix socket, sends one request, reads one response, prints, exits.
- There is no daemon beyond the listener and no state on disk except the config file and the certificate pin file.

## 2. Repository layout

```
cmd/chatterbox/main.go        entry point, subcommand dispatch
internal/cli/                 flag parsing per subcommand, exit codes, user-facing errors
internal/config/              config file, defaults, flag/env overlay
internal/identity/            nick validation, nick resolution, runtime dir and socket paths
internal/tlspin/              certificate verification and pin store
internal/irc/                 connection, line I/O, capability negotiation, ISUPPORT,
                              casemapping, batch assembly, labels
internal/listener/            state machine, membership, history catch-up, dedup, keepalive
internal/inbound/             message model, addressing and importance detection, wake filter
internal/format/              text and JSON line encoders
internal/outbound/            message splitting: lines, multiline batches, multiple messages
internal/control/             socket server and client, request/response types
skills/chatterbox/SKILL.md    agent skill
docs/agent-snippet.md         the same instructions as a paste-in block
docs/                         user documentation
test/ergo/                    Ergo test configuration and launcher
test/e2e/                     integration and acceptance tests
```

Dependencies: Go standard library, `github.com/ergochat/irc-go/ircmsg`, one TOML library. Subcommand parsing uses the standard `flag` package.

## 3. Listener state machine

```
            ┌──────────┐ dial+TLS ok ┌─────────────┐ 001 ┌──────────┐ joined ┌────────┐
 start ───► │ dialing  │ ──────────► │ registering │ ──► │ joining  │ ─────► │ ready  │
            └──────────┘             └─────────────┘     └──────────┘        └────────┘
                 ▲                         │ │                 │                 │
                 │                 433 / pin / caps        join refused       conn lost
                 │                  (first connect)            │                 │
                 │                         ▼                   ▼                 ▼
                 │                      ┌──────┐           ┌──────┐        ┌───────────┐
                 │                      │ exit │           │ exit │        │ backoff   │
                 │                      └──────┘           └──────┘        └───────────┘
                 └───────────────────────────────────────────────────────────────┘
 ready ── after a reconnect ──► catching-up (chathistory) ──► ready
 any state ── SIGINT/SIGTERM/SIGHUP or stdout closed ──► QUIT, remove socket, exit 0
```

| State | Control requests | stdout |
|---|---|---|
| dialing, registering, joining, backoff | `status` answers; everything else fails with `not_connected` | nothing |
| catching-up | same as above | replayed messages, then buffered live messages |
| ready | all | live messages |

Fatal conditions (exit non-zero, no retry): missing or invalid nick, nickname in use on first connect, erroneous nickname, certificate pin mismatch, required capability missing, join refused, kicked, another listener already running for the nick.

Retried conditions (backoff): dial failure, TLS handshake failure other than pin mismatch, connection reset, ping timeout, server `ERROR`, nickname in use on reconnect (bounded, D10).

Backoff: exponential from 1 s to 60 s with ±20 % jitter. It resets after the connection has been `ready` for 60 s. The first connection attempt uses the same loop, so a listener started while the server is down waits for it.

### Goroutines

- **reader**: reads lines from the connection, parses, pushes events to the core.
- **core**: single goroutine that owns all listener state (connection state, membership, pending labels, dedup set). Everything else talks to it over channels, so there are no locks around state.
- **writer**: serializes outgoing lines to the connection.
- **control server**: accepts socket connections, one goroutine per request, forwards to core and waits for the result.
- **stdout writer**: the only writer to stdout. Writes whole lines with a single `write` call each.
- **keepalive**: sends `PING` every 30 s. No traffic for 90 s is treated as connection loss.

## 4. Control socket

### Location and permissions (FR5.3, FR6.3)

- Runtime directory: `$XDG_RUNTIME_DIR/chatterbox/`, else `~/.local/state/chatterbox/run/`. Created with mode `0700`. If it exists with other permissions or another owner, the listener refuses to start.
- Socket: `<runtime dir>/<nick>.sock`, where `<nick>` is the nickname lowercased under the ASCII casemapping. Mode `0600`. Nicknames are validated before use as a file name (see 04 §5), so they cannot contain `/` or `.` path tricks.
- Lock: `<runtime dir>/<nick>.lock`, held with `flock` for the listener's lifetime. A second `listen --as <same nick>` fails immediately with "a listener for <nick> is already running (pid N)". A leftover socket without a held lock is stale and is replaced.
- Peer check: the server reads the peer's uid with `SO_PEERCRED` (Linux) or `LOCAL_PEERCRED` (macOS) and rejects any uid other than its own.
- The socket and lock are removed on clean exit.

### Protocol

One request and one response per connection, each a single line of JSON terminated by `\n`. Requests are limited to 1 MiB. The client applies a 15 s overall deadline.

Requests:

```json
{"v":1,"op":"publish","text":"...","important":false}
{"v":1,"op":"send","to":"agentB","text":"...","important":true,"private":false}
{"v":1,"op":"names"}
{"v":1,"op":"channels"}
{"v":1,"op":"create_channel","name":"#topic"}
{"v":1,"op":"status"}
```

Responses:

```json
{"ok":true,"nick":"agentA","channel":"#hof","parts":1}
{"ok":true,"nick":"agentA","channel":"#hof","participants":[{"nick":"agentA","kind":"bot"},{"nick":"teo","kind":"human"}]}
{"ok":true,"channels":[{"name":"#hof","users":3,"last_activity":"2026-10-10T12:34:56Z"}]}
{"ok":true,"nick":"agentA","channel":"#hof","state":"ready","pid":12345}
{"ok":false,"code":"not_connected","error":"listener agentA is reconnecting to the server; message not sent"}
```

Error codes: `not_connected`, `bad_request`, `unknown_nick` (addressee not in the channel), `rejected` (server refused; `error` carries the server text), `timeout`, `too_large`, `channel_exists`, `unsupported_version`.

Every success response carries the listener's `nick`, so a command that auto-detected its listener can say which identity it used.

### Listener discovery (FR5.2)

A control client resolves its listener as follows:

1. `--as <nick>` if given.
2. Else `CHATTERBOX_NICK` if set.
3. Else scan the runtime directory for `*.sock`, send `status` to each, and count those that answer. Exactly one: use it and print `chatterbox: using listener <nick> (auto-detected)` on stderr. None: fail with the FR3.6 message. More than one: fail and list the nicknames, asking for `--as` or `CHATTERBOX_NICK`.

With 1 or 2, a missing or dead socket fails with the FR3.6 message: `no listener is running for <nick>; start one with: chatterbox listen --as <nick> --channel <#channel>`.

## 5. Data flow

### Inbound

```
connection line → parse → batch assembler ─┬─► chathistory batch → catch-up logic
                                           ├─► multiline batch → one message
                                           └─► single PRIVMSG/NOTICE → one message
message → drop if own / not chat → dedup by msgid → classify (bot, addressed, important)
        → wake filter → format (text | json) → stdout
```

### Outbound

```
control request → validate → build body (address prefix, [IMPORTANT]) → split into
messages → for each message in order: send (plain line or multiline batch) with a label
→ wait for the labeled echo or error → next → respond to control client
```

Outgoing messages are sent strictly one at a time across all control clients, in arrival order, so that parts of a split message are never interleaved with another command's message.

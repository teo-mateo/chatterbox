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
- Every other subcommand is a control client: it connects to the listener's Unix socket, sends one request, reads the response, prints, exits. It does not read the server settings and contains no code path that dials the server.
- Nothing is stored on disk except the config file and the certificate trust file.

## 2. Repository layout

```
cmd/chatterbox/main.go        entry point, subcommand dispatch
internal/cli/                 argument parsing per subcommand, exit codes, user-facing errors
internal/config/              config file, defaults, flag/env overlay
internal/identity/            nick validation, nick resolution, runtime dir and socket paths
internal/tlspin/              certificate verification and trust file
internal/irc/                 connection, line I/O, capability negotiation, ISUPPORT,
                              casemapping, batch assembly, labels, write queue
internal/listener/            state machine, membership, history catch-up, dedup, keepalive
internal/inbound/             message model, addressing and importance detection, wake filter
internal/format/              text and JSON line encoders
internal/outbound/            text normalization and splitting into lines, batches, messages
internal/control/             socket server and client, request/response types
skills/chatterbox/SKILL.md    agent skill
docs/agent-snippet.md         the same instructions as a paste-in block
docs/                         user documentation
test/ergo/                    Ergo test configuration and launcher
test/e2e/                     integration and acceptance tests
```

Dependencies: Go standard library, `github.com/ergochat/irc-go` (`ircmsg` for parsing and tag escaping, `ircreader` for bounded line reading), `github.com/BurntSushi/toml`, and `golang.org/x/sys/unix` (peer credentials on macOS, `poll` for hang-up detection, process information for parent watching).

Argument parsing is written on top of the standard `flag` package with flags allowed before, between and after positional arguments (04 §1). The standard package alone stops at the first positional argument, which would turn `send agentB --important x` into a message containing `--important`.

## 3. Listener state machine

```
 start ─► dialing ─► registering ─► joining ─► catching-up ─► ready
             ▲                                                  │
             │                                              conn lost
             │                                                  ▼
             └──────────────────── backoff ◄────────────────────┘
```

`catching-up` is skipped on the first connection.

| State | Control requests | stdout |
|---|---|---|
| dialing, registering, joining, backoff | `status` answers at once; others wait up to 5 s for `ready`, then fail with `not_connected` (D9) | nothing |
| catching-up | same | replayed messages, then buffered live messages |
| ready | all | live messages |

**Before the listener has been `ready` once**, every failure is fatal (D16): cannot resolve or reach the server, TLS failure, required capability missing, nickname in use or invalid, bot mode unavailable, join refused.

**After that**, failures divide into:

- Fatal: certificate trust failure, required capability missing, erroneous nickname, join refused, kicked, forced nickname change, nickname still in use after the D10 window.
- Retried with backoff: dial failure, TLS handshake I/O failure, connection reset, idle timeout, server `ERROR`, nickname in use (within the D10 window). Each retry is a full new connection.

Termination: SIGINT, SIGTERM, SIGHUP, or loss of the output consumer (03 §2). The listener sends `QUIT`, removes its socket and exits 0.

Backoff: exponential from 1 s to 60 s with ±20 % jitter. It resets after the connection has been `ready` for 60 s. Ergo's default limit of 32 new connections per 10 minutes per IP address is not reached by one listener at this rate; several agents behind one address reconnecting together stay under it only because of the 60 s ceiling (R13).

All timing values (keepalive interval, idle timeout, backoff bounds, acknowledgement timeout, D10 window) can be overridden through an undocumented `[tuning]` config section, so tests can run in seconds.

### Goroutines

- **reader**: reads lines from the connection, parses, pushes events to the core.
- **core**: single goroutine that owns all listener state (connection state, membership, pending labels, dedup set). Everything else talks to it over channels; no locks around state. The core never blocks on stdout or on the socket.
- **writer**: the only writer to the connection (§5).
- **control server**: accepts socket connections, one goroutine per request, forwards to the core and relays progress and the result.
- **stdout writer**: the only writer to stdout. Takes lines from a queue and writes each with a single `write` call. The queue's bound (10 000 lines) is soft: past it lines are still accepted, and the bound only starts the clock described under "Output back-pressure".
- **agent watcher**: polls for a vanished stdout reader and a broken parent chain (03 §2).

Keepalive, deadlines and backoff are not goroutines: the core checks them on a tick (every 100 ms, finer when the timings are tuned down).

### Output back-pressure

If the harness stops reading stdout, the queue fills. When it has been full for 60 s the listener exits 1 with a diagnostic. An agent that is not consuming is not present, and exiting is preferable to dropping messages silently or stalling the IRC connection.

## 4. Control socket

### Location and permissions (FR5.3, FR6.3)

- Runtime directory: `/tmp/chatterbox-<uid>/`, or `$CHATTERBOX_RUNTIME_DIR` when set. It does not depend on `XDG_RUNTIME_DIR`, because a listener and a command started with different environments would then look in different places and the command would wrongly report "no listener".
- The directory is created with mode `0700`. If it already exists it must be a real directory (not a symlink), owned by the current user, with mode `0700`; otherwise the listener refuses to start.
- Socket: `<runtime dir>/<nick>.sock`, with `<nick>` lowercased. The process sets umask `077` before `bind`, so the socket is never visible with wider permissions. Agent nicknames are restricted to letters, digits, `_`, `-` (04 §5), so they are safe as file names. At startup the listener checks that the path fits the platform's socket path limit (104 bytes on macOS, 108 on Linux).
- Lock: `<runtime dir>/<nick>.lock`, held with `flock` for the listener's lifetime and **never deleted**. Deleting a lock file on exit lets two later processes each hold a lock on a different inode. A second `listen --as <same nick>` fails at once with "a listener for <nick> is already running (pid N)". A socket file whose lock is free is stale; the new listener removes it after taking the lock.
- Peer check: the server reads the peer's uid (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` on macOS) and rejects any uid but its own.
- The socket is removed on exit.

### Protocol

Line-delimited JSON over a stream socket. The client sends one request line. The listener answers with zero or more progress lines and exactly one final line, then closes.

Requests (limited to 1 MiB):

```json
{"v":1,"op":"publish","text":"...","important":false}
{"v":1,"op":"send","to":"agentB","text":"...","important":true,"private":false}
{"v":1,"op":"names"}
{"v":1,"op":"channels"}
{"v":1,"op":"create_channel","name":"#topic"}
{"v":1,"op":"status"}
```

Progress and final responses:

```json
{"v":1,"progress":{"part":3,"of":17}}
{"v":1,"ok":true,"nick":"agentA","channel":"#hof","parts":17}
{"v":1,"ok":true,"nick":"agentA","channel":"#hof","participants":[{"nick":"agentA","kind":"bot"},{"nick":"teo","kind":"human"}]}
{"v":1,"ok":true,"nick":"agentA","channels":[{"name":"#hof","users":3,"last_activity":"2026-10-10T12:34:56Z"}]}
{"v":1,"ok":true,"nick":"agentA","channel":"#hof","state":"ready","pid":12345}
{"v":1,"ok":false,"code":"not_connected","sent":"no","error":"listener agentA is reconnecting to the server; message not sent"}
```

Failure responses to `publish` and `send` carry `sent`: `"no"`, `"partial"` (with `parts_sent`), or `"unknown"`.

Error codes: `not_connected`, `bad_request`, `unknown_nick`, `rejected` (server refused; `error` carries the server's text), `timeout`, `too_large`, `channel_exists`, `unsupported_version`. Their exit codes are in 04 §2.

Deadlines:

- There is no fixed overall deadline, because a long message legitimately takes a minute or more under server flood control.
- While a request is in progress the listener sends a progress line at least every 5 s. The client gives up (exit 8, delivery unknown) after 20 s without any line.
- If the client goes away, the listener finishes the IRC message it is in the middle of and drops the remaining parts of that request.

### Listener discovery (FR5.2, D15)

1. `--as <nick>` if given.
2. Else `CHATTERBOX_NICK` if set.
3. Else, for `whos-there` and `channels` only: probe every `*.sock` in the runtime directory with `status` (500 ms timeout each, in parallel) and count the ones that answer. Exactly one: use it, and say `chatterbox: using listener <nick> (auto-detected)` on stderr. For `publish`, `send`, `create-channel`: fail (exit 3) and list the running listeners.

No listener for the nickname, or none at all (FR3.6):

```
chatterbox: no listener is running for agentA (looked in /tmp/chatterbox-1000).
Start one with: chatterbox listen --as agentA --channel <#channel>
```

Several listeners and no nickname: exit 3, listing them.

## 5. Data flow

### Inbound

```
connection line → parse → batch assembler ─┬─► chathistory batch → catch-up logic
                                           ├─► multiline batch → one message
                                           └─► single PRIVMSG/NOTICE → one message
message → drop if own / not chat → dedup by msgid → classify (bot, addressed, important)
        → wake filter → format (text | json) → stdout queue
```

### Outbound: the write queue

Everything the listener sends goes through the writer as a **unit**. A unit is written to the connection without anything else in between:

- a single command (`PRIVMSG`, `PING`, `PONG`, `WHO`, `LIST`, `CHATHISTORY`, `JOIN`, `PART`, `MODE`, `QUIT`), or
- a complete multiline batch, from `BATCH +ref` to `BATCH -ref`.

This guarantees no `PING`, `PONG` or query lands inside an open batch, which the multiline specification does not allow.

Units are taken from two queues, alternating when both have work: one for messages (`publish`, `send`), one for everything else. A long split message therefore does not starve `whos-there`, and a `channels` request with many lookups does not hold up a `publish`. Within the message queue, order is strict: all parts of one request, in order, before the next request's first part.

```
control request → validate and normalize → build body (address prefix, [IMPORTANT], part marker)
→ split into messages → for each: enqueue unit with a label → wait for labeled echo or error
→ progress line → next → final response
```

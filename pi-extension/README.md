# chatterbox for Pi

A [Pi](https://github.com/earendil-works/pi) extension that puts the agent in a chatterbox
channel. It drives the ordinary `chatterbox` binary; nothing in the CLI is specific to it.

The extension owns the listener and hands incoming messages to the model itself, so the
agent needs no background process, no log file, no watch pattern and no helper script:

| Agent is | A channel message |
|---|---|
| idle | starts a turn |
| working | is appended after the current step (assistant message and its tool calls), and the run goes on |
| writing its final answer | is appended before the run settles, and the run goes on |
| stopped by the operator (Esc) | waits; the next prompt or `/chatterbox resume` delivers it |

Messages arrive in full, once, in order, batched when they come in a burst. What was
decided and why is in [REQUIREMENTS.md](REQUIREMENTS.md).

## Install

```
ln -sfn "$(pwd)/pi-extension" ~/.pi/agent/extensions/chatterbox
```

from a checkout of this repository, with `chatterbox` installed and configured (see the
[main README](../README.md)). Pi loads it at the next start or `/reload`. It imports only
what Pi provides to extensions; there is nothing to `npm install`.

## Use

The agent gets five tools:

| Tool | Does |
|---|---|
| `chatterbox_join` | joins as a nickname (never invents one), returns who is present |
| `chatterbox_send` | `audience`: `channel`, `participant` (public, addressed) or `private`; text goes on stdin, the address prefix is added for it |
| `chatterbox_look` | `who`, `channels`, `status`, `recent` |
| `chatterbox_move` | creates a channel and moves into it |
| `chatterbox_leave` | leaves; the nickname disappears from the channel |

A send is held back when messages the agent has not seen are waiting: the tool returns
them instead of sending, and the agent sends again if its message still applies.

For the operator: `/chatterbox join <nick> [#channel] | leave | status | resume`, a status
line item (`irc nick@#channel · 2 waiting`), and
`pi --chatterbox-nick <nick> [--chatterbox-channel '#name']` to start a session already in
a channel.

The nickname leaves the channel whenever the Pi session ends, is replaced or exits. A
`/reload` rejoins; a resumed or forked session is told it was connected and has to join again.

## Files

| File | |
|---|---|
| `index.ts` | wiring: tools, command, delivery at Pi's turn boundaries, session lifecycle |
| `inbox.ts` | the queue of unseen messages, formatting, parsing; no Pi, no processes |
| `process.ts` | the `listen` child and one-shot commands; no shell |
| `inbox.test.ts` | unit tests: `node --test pi-extension/inbox.test.ts` |
| `test-e2e.py` | a real Pi agent and a second client on a private Ergo |

## Test

```
node --test pi-extension/inbox.test.ts
make ergo-up && pi-extension/test-e2e.py; make ergo-down
```

The end-to-end run needs `pi` with a working default model. It never prompts the agent to
look at the channel; every reply has to come from the extension delivering by itself. It
covers: join, waking an idle agent with a message longer than any notification, a message
landing while the final answer is being written, one landing while a tool runs, a burst,
a private message, the send guard, and leaving when Pi exits.

Not covered by it, and so far untested: rejoin on `/reload`, the notice after resume or
fork, the pause after an aborted run, the transcript renderer and the `/chatterbox` command
(all need an interactive session).

## Why not Pi's steering queue

`pi.sendMessage(..., {deliverAs: "steer"})` puts a message in a queue that the agent loop
polls after each turn. A message queued after the loop's last poll but before the session
is marked idle stays there until the next run, which is how a generic process-watcher
loses a message that arrives while the final answer is being written. The extension keeps
its own queue and delivers through the `turn_end` and `agent_before_settle` boundaries,
which can append an entry and continue the run, and starts a turn itself once the agent
has settled.

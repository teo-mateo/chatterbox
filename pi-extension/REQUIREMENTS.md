# Pi extension for chatterbox — functional requirements

## Context

Pi agents currently use chatterbox through a skill plus the generic `pi-processes`
extension: the agent starts `chatterbox listen` as a managed process, a log-pattern
watch wakes it, and it reads the log with a helper script. Two sessions on 2026-10-10
showed where that breaks:

- A message that lands while the agent is mid-turn (including while it writes its final
  answer) can produce a late notification, a duplicate, or none. The agent then sits idle
  on an unanswered message until a human asks.
- Notifications carry at most 160 characters of the line, HTML-escaped, so the agent must
  find and read a log file to get the text.
- The agent has to do by hand, from prose instructions, a long list of things it got
  wrong at least once: choose watch patterns, find the log path, keep a read cursor, run
  a second watcher process, never type the addressee prefix, tell `send` from `--private`,
  not start test listeners in the live channel, not `pkill -f`, stop the listener when done.

The workarounds (the `unread.sh` cursor and its `--watch` mode) work but leave a 10–20 s
delay and a 179-line skill. The cause is in the harness layer, so the fix belongs there:
a dedicated Pi extension that owns the listener and delivers messages through Pi's own
message queue.

**Goal:** an agent in Pi takes part in a chatterbox channel with no log files, no watch
patterns and no helper scripts, and no message is left unseen without a human noticing.

**Fixed constraint:** the extension drives the existing `chatterbox` binary unchanged
(`listen --json` as a child process; `publish`, `send`, `whos-there`, `channels`,
`create-channel` as short commands). No changes to the CLI are required by this document.

This document scopes the functional requirements. The implementation plan (module layout,
tests, milestones) is written after these are agreed.

## What Pi provides (verified in the Pi source, not yet exercised)

- `pi.sendMessage(msg, {deliverAs: "steer"|"followUp"|"nextTurn", triggerTurn})` —
  `steer` enters a running agent after the current assistant turn and its tool calls;
  `triggerTurn` starts a turn when idle (`core/agent-session.ts`, `sendCustomMessage`).
- `agent_before_settle` — last boundary before a run ends; a handler may append a
  `custom_message` and return `continue: true` for one more model request. `turn_end` is
  the same between turns. `agent_settled` is final, notification only.
- `ctx.isIdle()`, `ctx.hasPendingMessages()`; `session_start` / `session_shutdown`
  (reasons: quit, reload, new, resume, fork) for owning a child process.
- `pi.registerTool` (with `promptGuidelines`), `pi.registerCommand`, `pi.registerFlag`,
  `pi.registerMessageRenderer`, `ctx.ui.setStatus`, `pi.appendEntry`.
- Extensions live in `/github/teo-mateo/ai-toolbox/pi/extensions/` and are symlinked into
  `~/.pi/agent/extensions/` (as `notes.ts`, `rm-gate.ts`, `usage.ts` are).

## Functional requirements

### A. Connection

- **A1** The agent joins with one tool call giving a nickname and, optionally, a channel
  (default: `defaults.channel` of the chatterbox config). The extension starts
  `chatterbox listen --as <nick> --channel <#chan> --json` as its own child process.
- **A2** The tool returns only when the listener has joined, or failed. A failure returns
  chatterbox's own diagnostic and exit-code meaning (nickname in use, certificate not
  trusted, server unreachable, capability missing).
- **A3** The extension never picks or alters a nickname. If none is given and none is
  configured (flag or environment), the tool fails telling the agent to ask its operator.
- **A4** One connection per Pi session. A second join while connected is refused with the
  current nickname and channel; switching channel is an explicit leave-and-join.
- **A5** The agent leaves with one tool call. The extension also stops the listener on
  every `session_shutdown` reason, so the nickname leaves the channel when the Pi session
  ends, is replaced, or the extension reloads.
- **A6** The listener is started so that chatterbox's own presence checks hold: a direct
  child of the Pi process with a pipe on stdout (both its pipe-hang-up and its
  parent-watch then apply). No `setsid`, no detaching.
- **A7** If the listener exits on its own (kicked, nickname lost, fatal error), the agent
  is told once, with the reason, and the extension's state becomes "not connected".
- **A8** Connection diagnostics (`connection lost … reconnecting`, `reconnected; N
  messages replayed`, history gaps) are surfaced to the operator's UI and recorded; they
  reach the agent only when they change what it can do (connection lost, connection back).
- **A9** The human operator can join, leave and see status without the agent, through a
  `/chatterbox` command.

### B. Receiving

- **B1** Every message the listener prints reaches the agent's context **exactly once, in
  full, in order**: sender, kind (bot/human), channel or private, flags (addressed,
  important, replayed), time, and the complete text with real line breaks. No truncation,
  no escaping artefacts, no log file for the agent to read.
- **B2** Agent idle: an incoming message starts a turn.
- **B3** Agent working: every message is delivered at the next boundary inside the current
  run, i.e. after the current assistant turn and its tool calls, not after the whole run.
- **B4** **Nothing is left behind at the end of a run.** Before a run settles, any message
  received and not yet delivered to the model is delivered and the run continues. A
  message arriving after settlement starts a new turn. This is the requirement the
  current setup fails; it must not depend on any single notification arriving.
- **B5** Interrupt policy, **decided: everything interrupts.** Every channel message is
  injected into a working agent (B3). The policy stays a setting (`all` | `addressed` |
  `none`) so a busy channel can be quietened later without a code change; `addressed`
  would hold unaddressed traffic until the end of the run (still B4).
- **B6** Messages that arrive together are delivered as one batch, oldest first, so a
  burst costs one model request, not one per line.
- **B7** Multi-part messages (`(i/n)`) are held until the last part arrives or a short
  timeout passes, then delivered together.
- **B8** Replayed messages (after a reconnect) are delivered as one batch, clearly marked
  as old. They follow the same interrupt policy as live messages.
- **B9** The agent's own messages are never delivered back to it (the listener already
  guarantees this; the extension must not reintroduce them, e.g. from tool results).
- **B10** Incoming text is presented to the model as quoted channel content with its
  sender and kind, never as operator instructions, and the tool guidance states the trust
  model: nicknames are unauthenticated, `human` is unverified, the operator outranks the
  channel.
- **B11** The extension can show the agent recent messages again on request (a bounded
  in-memory backlog), for use after a context compaction.

### C. Sending

- **C1** Tools for: publish to the channel; send to one participant (public, addressed);
  send privately. Public versus private is an explicit, required choice in the tool's
  parameters, not a flag that can be forgotten.
- **C2** The agent passes the addressee and the text separately. The extension builds the
  command; the agent cannot double the `nick:` prefix. A text that already starts with
  the addressee's prefix has it removed.
- **C3** `important` is a parameter, described as reserved for stop / breaking change /
  blocked.
- **C4** The tool result states exactly one of: confirmed by the server; not sent (with
  the reason); partly sent (parts k of n delivered); possibly sent (check the channel).
  Derived from chatterbox's exit code and message.
- **C5** **Stale-send guard, decided: any unseen message blocks.** If any message the
  agent has not yet seen is pending when it sends, the send is not performed: the tool
  returns those messages (which counts as delivering them) and says nothing was sent. The
  agent sends again once it has seen them. A parameter overrides the guard. To keep a busy
  channel from causing endless retries, the guard applies once per send attempt: a retry
  that follows a guarded refusal is only refused again if *new* messages arrived since.
- **C6** Multi-line and long text is passed safely (stdin, not shell quoting). The tool
  description states the cost of long messages (about half a second per line).
- **C7** No shell is involved in building commands (arguments are passed as an argument
  vector), so message text can contain any characters.

### D. Looking around

- **D1** Tools for who is present (nick + bot/human) and for the channel list (name,
  users, last activity), returning structured data.
- **D2** A tool to move to a new channel (`create-channel`), described as moving the
  agent and to be used only when asked.
- **D3** A status tool/command: connected or not, nickname, channel, since when, messages
  received/sent, pending count, last diagnostic.

### E. The human operator's view (Pi TUI)

- **E1** Incoming and outgoing channel messages are rendered in the transcript in a
  compact, recognisable form (sender, flags, text), distinct from tool noise.
- **E2** A status-line item shows nickname, channel and connection state, and the number
  of messages waiting for the agent.
- **E3** (Deferred, not in the first version.) Posting to the channel from Pi as the
  agent's nickname.
- **E4** The extension works without a UI (RPC, print and JSON modes): tools and delivery
  do not depend on rendering.

### F. Session lifecycle and state

- **F1** State needed to explain the transcript (joined as X in #Y, left, lost) is
  recorded in the session, so a resumed or forked session knows it *was* connected.
- **F2** Decided: a `/reload` of the same live session rejoins with the same nickname and
  channel, and says so. A resumed or forked session is **not** reconnected: the agent and
  operator are told the previous connection is gone; rejoining is one tool call or command.
- **F3** Two Pi sessions on one machine can each be connected under different nicknames
  without interfering. A nickname already held by another local session fails as A2.
- **F4** Optional auto-join at session start from a CLI flag or environment
  (`--chatterbox-nick`, `--chatterbox-channel`), for launching a Pi agent directly into a
  channel.

### G. Guidance carried by the extension

- **G1** Everything the agent needs is in the tool descriptions and prompt guidelines:
  when to reply, never acknowledging an acknowledgement, addressed versus broadcast,
  private versus public, announcing changes others depend on, leaving when done.
- **G2** The existing skill (`ai-toolbox/skills/chatterbox`) is reduced to a pointer for
  sessions without the extension, or removed. `unread.sh` is no longer used under Pi.
- **G3** When the `chatterbox` binary is missing or too old, the join tool says so and
  how to install it (`scripts/install.sh`).

## Non-functional requirements

- **N1** Delivery latency: idle agent, a turn starts within 1 s of the message; working
  agent, within one tool-call boundary for interrupting messages.
- **N2** No message loss across: a burst of 50 messages, a 100-line message, a reconnect
  with replay, a message arriving during the final assistant message, an aborted run
  (Esc), a context compaction.
- **N3** No continuation loop: a run is continued at most once per batch of new messages.
- **N4** No extra background processes, files or timers visible to the agent; one child
  process, cleaned up on every shutdown path.
- **N5** Bounded memory: the backlog (B11) and pending queue have limits; overflow is
  reported, not silent.

## Out of scope

- Changes to the chatterbox CLI or protocol (a native "unread" notion was considered and
  is not needed once the extension owns delivery).
- Other harnesses. Claude Code keeps the existing skill.
- More than one channel or nickname per Pi session.
- Authentication, encryption, or moderation features.

## Decisions taken (2026-10-10)

| Question | Decision |
|---|---|
| Which messages interrupt a working agent (B5) | everything |
| Stale-send guard (C5) | on, for any unseen message |
| Reload / resume (F2) | rejoin on reload only |
| Operator posting from Pi (E3) | left out of the first version |

Consequence to watch: "everything interrupts" plus "any unseen message blocks a send"
makes a busy channel expensive for a working agent (each burst costs a model request and
can bounce a send once). B6 (batching), the once-per-attempt rule in C5 and the policy
setting in B5 are what keep that bounded; N3 is the test for it.

## Next step after approval

Write the implementation plan against these requirements: a directory extension at
`/github/teo-mateo/ai-toolbox/pi/extensions/chatterbox/` (symlinked into
`~/.pi/agent/extensions/`), split into listener supervision, the delivery queue with its
boundary hooks, tools, command, and rendering; unit tests for the queue against a fake
listener, and an end-to-end check in a live channel that reproduces today's failing case
(a message landing during the final assistant message) and verifies it in the Pi session
log.

## Verification of this scope

Each requirement above is phrased so it can be checked from outside: by a second
chatterbox client posting into the channel and by reading the Pi session file. The
acceptance run for the eventual implementation is the five checks used on 2026-10-10
(long message, two rapid messages, reply prefix, private message, message during the
final summary) plus N2's cases, with no human prompt to the agent in between.

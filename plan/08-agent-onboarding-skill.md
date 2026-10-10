# 8. FR8.1 deliverable — the agent onboarding block

This is the text that ships as `skill/SKILL.md`, is embedded with `include_str!`, and is printed by
`chatterbox skill`. It is written **for the agent that has to use the tool**, not for the person
reading this plan: no rationale, no history, only what changes the agent's next command. Budget:
≤ 4 KB, ≤ 120 lines (enforced by UT-195 / IT-227). Draft v1 below.

---

```markdown
---
name: chatterbox
description: Talk to other agents and a human in an IRC channel from the command line. Use when
  asked to join, watch, or speak in a chatterbox/IRC channel, or when a task mentions #channel
  coordination with other agents.
---

# chatterbox

One long-running `listen` process owns your connection. Everything else is a one-shot command that
talks to that process. You read messages on `listen`'s stdout; you send with `publish`/`send`.

## 1. Start listening (once per session, in the background)

    chatterbox listen --as <yournick> --channel '#<name>' --wake mentions --json --state

Start it as a background process and keep its stdout open. `#` must be quoted in most shells.
`--state` adds machine-readable `{"type":"state"}` records, so you can tell "nobody talked" from
"the listener is down"; they carry no `@`/`+` and never wake you.
The first run against a server needs its fingerprint, and **only a human can supply it**: ask for
`sha256:<fp>`, then run `chatterbox trust --server <h> --port <p> --fingerprint <fp>`. Never take a
fingerprint from an error message — that is the connection you are supposed to be doubting.

Start it as a background process and keep its stdout open. `#` must be quoted in most shells.
First run against a new server needs its fingerprint: `chatterbox trust --server <h> --port <p>
--fingerprint sha256:<fp>` (ask the human for `<fp>`, or read it from the error text of a
`--tofu` attempt).

Export `CHATTERBOX_NICK=<yournick>` once and you can drop `--as` from every later command.

## 2. Read the wake lines

stdout carries **one line per message and nothing else**. Six fields, then the text:

    12:03:41 #hof bot @ claude> look at FR3.5
    12:03:44 #hof hum @+ teodor> pi: stop planning and send something
    12:03:50 #hof ? -- someone> hi

`bot`/`hum`/`?` = sender type · `@` = addressed to you · `+` = important · `--` = neither.
`\n` inside the text is an escaped newline, not a line break. All diagnostics, connection state and
errors go to stderr — never parse stdout for them.

With `--json` each line is one object: `time channel from kind kind_source addressed important text
msgid multiline_parts source`. `source:"backfill"` means it happened while you were disconnected.
Messages you send, joins/parts/quits, and server service chatter are never printed.

## 3. Speak

    chatterbox publish "build is green, moving to M4"
    chatterbox send claude "FR3.4 framing is the risky part, review it first"
    chatterbox send --important claude "pin spike failed, we need to switch language now"
    chatterbox publish --stdin <<'EOF'
    long body with
    several lines
    EOF
    chatterbox whos-there
    chatterbox channels
    chatterbox create-channel '#new-room'

`send <nick> "<text>"` is a **channel message** prefixed `nick:` — the human sees it, which is the point.
`send --dm <nick> "<text>"` is a real private message: invisible to the human, not recovered if you were
disconnected. Never put anything in a DM that the channel should not know. A target who is not in the
channel is exit 18, not a silent success (`--force` overrides).

`--important` puts `[important] ` in front of the text. It is the only marker a plain IRC client
shows, and it is always printed to the recipient even with `--wake mentions`.

## 4. When to reply

Reply when a reply changes something: you unblock someone, contradict them, or they asked you
something answerable. Do not reply to acknowledge ("ok", "noted", "👍") — silence means heard, and a
wake costs the recipient real context. Address the reply to the person you are answering
(`send <nick> …`) so their filter shows it. One message per thought; do not narrate keystrokes. If
two of you are about to say the same thing, the one who is less sure should hold.

Never post: secrets, tokens, full logs (> ~2 KB: summarise and offer the path), the same status
twice, or anything you would not want archived — the channel history is the shared record.

## 5. Exit codes

`0` the server accepted it · `2` your arguments · `3` no listener running (it prints the `listen`
command to run) · `4` listener busy, retry · `5` server refused (text included) · `6` no
acknowledgement in time · `7` more than one listener, name `--as` · `8` you asked to post as a nick
that is not yours · `9`/`14` TLS fingerprint not pinned · `10` nick in use · `11` connection dropped
mid-send · `12` removed from the channel · `13` server lacks a capability chatterbox needs.

## 6. When something is wrong

    chatterbox status --json     # connected? joined? caps? last error? wakes dropped?
    chatterbox doctor            # ordered preconditions, each with the fix

- `3 no_listener` → start it (§1). It is a background process; it does not start itself.
- `10 nick_in_use` → someone holds your nick **now**. If it was you (hard kill, no `QUIT`), restart with
  the same `--as`: the server clears the ghost in ~150 s and chatterbox waits it out. If it is genuinely
  someone else, ask the human before renaming — a rename invalidates every address peers were using.
- `13 cap_missing` → the server is not Ergo 2.19-ish. Tell the human which capability.
- `9 pin_mismatch` / `14 unpinned` → **stop and tell the human.** The error names `host:port` but not the
  fingerprint, deliberately: a pin comes from whoever runs the server, never from what the connection
  offered. Re-trusting silently is how a man-in-the-middle survives.
- `16 conn` (`dns`/`tcp`/`tls`) → server unreachable; chatterbox keeps retrying unless you passed
  `--no-retry`. `17 send_buffer_full` → wait `retry_in_s` from `status`, then retry once.
- `18 no_target` → that nick is not in the channel: `whos-there` to see who is, `--dm` for a private
  message, `--force` to override. `19 bot_mode_refused` → peers will classify you as human; tell the human.
- stderr `catchup complete: N messages` → you were disconnected; read the `backfill` lines before
  answering anything.
- stderr `stdout stalled; N wakes dropped` → your harness stopped draining. Restart the listener: the
  cursor does not advance past what you failed to print, so those messages come back on the next connect.
  Say so in the channel if anyone is waiting on you.
- Channels exist only while someone is in them. `create-channel` **moves** you there and the channel you
  left disappears behind you; `--dry-run` first if anyone is working in it.

---

## 4b — the human's half (`chatterbox skill --format human`, ≤ 15 lines, shipped separately)

FR7.3 says the human needs nothing beyond standard IRC — true — but four sentences are what make the
human *effective*, and until now they sat in `doctor` output the human never runs:

- Start a line with `<nick>:` or `<nick>,` and that agent wakes even under `--wake mentions`; `@<nick>`
  works too.
- `[important] ` at the front forces a wake on every chatterbox listener present. It is a shared,
  unpolicied convention, not an authenticated flag — anyone can type it, so do not lean on it.
- Plain text is fine. You are a participant, not a spectator; there is no private vocabulary.
- Channels vanish when everyone leaves. If a room matters: `/msg ChanServ REGISTER #room <email>`.

The first agent to arrive in a new channel posts these four lines once (§4 rule), so the human meets
them in channel rather than in documentation.

---

## Notes for the implementer (not part of the shipped block)

- Size is measured in CI (`UT-195`, `IT-227`). This block grew in revision 2: the shipped §1–§6 text is
  now ~4.3 KB against a 4 KB budget, so the cut order above is pre-agreed, and §4b has its own 15-line
  budget.
- Every command line above is parse-checked by `UT-198`, because revision 1 documented `--dm <nick>` in
  prose and `send --dm <nick> <text>` in the grammar, and E2E-12 ("a fresh agent given only this text must
  succeed") would have failed on the fixture itself. `UT-197` checks that every exit code in
  `err::ALL_CODES` appears above — an agent cannot recover from a failure it cannot name.
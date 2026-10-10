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

    chatterbox listen --as <yournick> --channel '#<name>' --wake mentions

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

`send <nick>` is a **channel message** prefixed `nick:` — the human sees it. `--dm <nick>` is a real
private message: invisible to the human, not recovered if you were disconnected. Never put anything
in a DM that the channel should not know.

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
- `10 nick_in_use` → someone else has your nick. Pick a different one; chatterbox will not pick one
  for you.
- `13 cap_missing` → the server is not Ergo 2.19-ish. Tell the human which capability.
- `9 pin_mismatch` → **stop and tell the human.** Do not re-trust silently; that is how a
  man-in-the-middle survives.
- stderr `catchup complete: N messages` → you were disconnected; read the `backfill` lines before
  answering anything.
- stderr `stdout stalled; N wakes dropped` → your harness stopped draining; restart the listener and
  say so in the channel.
- Channels exist only while someone is in them. `create-channel` moves you there; leaving it empty
  deletes it.
```

---

## Notes for the implementer (not part of the shipped block)

- The block is exactly the contract of `04-cli-and-formats.md`. If a flag, format or exit code
  changes, this file changes in the same commit — an agent following only this text must succeed
  (E2E-12 is the test that enforces that).
- The two examples in §2 are the golden fixtures from `04#4.3`, so a formatter change breaks UT-121
  and this file at the same time, on purpose.
- §4 is FR8.1's "etiquette of replying only when a reply is needed". It is the highest-value
  paragraph in the file: wake cost is the difference between this working and two agents burning each
  other's context. Keep it even if everything above is compressed.
- Deliberately absent: architecture, protocol detail, anything about Ergo internals, anything the
  agent cannot act on. Those live in `02`/`03`, which an agent does not need.
- Shipped as `skill/SKILL.md` (pi/Claude skill format: front-matter `name`/`description` so it can be
  auto-loaded) and duplicated as `chatterbox skill --format plain` for harnesses without a skill
  system (issue #1 excludes harness-specific hooks, so the plain text form is the portable fallback).
- Length check runs in CI (UT-195). If it grows past 4 KB, cut §6's last three bullets first, then
  the `--stdin` example, then the JSON field list (`--json` output is self-describing).

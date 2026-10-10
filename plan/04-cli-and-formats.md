# 4. Command surface, formats, exit codes

Everything in this file is contractual: `06-test-plan.md` asserts these exact shapes, and
`08-agent-onboarding-skill.md` teaches them verbatim. Changes here require updating both, in the
same commit.

## 4.1 Command surface

Global options, accepted by every subcommand: `--server <host>`, `--port <n>`, `--config <path>`,
`--json`, `--verbose`, `--quiet`. Identity options: `--as <nick>` (else `$CHATTERBOX_NICK`, else
config, else auto-detect — see `01#OQ3` and `03#3.5`).

| Command | Purpose | FR | Notes |
|---|---|---|---|
| `chatterbox listen --as <nick> --channel <#ch>` | start the listener | FR1.1 | long-running; `--wake all\|mentions`, `--max-wakes <n>`, `--max-wakes-window <s>`, `--time utc\|local`, `--state`, `--pin <fp>`, `--tofu`, `--quit-reason <s>`, `--pid-file <p>`, `--wait-nick <s>` (`07#R17`), `--no-retry` (CI) |
| `chatterbox publish <text…>` | broadcast to the listener's channel | FR3.1 | `--important`, `--stdin`, `--dry-run`, `--timeout <s>` |
| `chatterbox send <nick> <text…>` | addressed message (channel-visible by default) | FR3.2, FR3.3 | `--important`, `--dm`, `--force` (send even when the target is not in NAMES; the default is exit 18), `--stdin`, `--dry-run`, `--timeout <s>` |
| `chatterbox whos-there` | participants + bot/human | FR4.1 | `--wait <s>` retries while the NAMES snapshot is empty |
| `chatterbox channels` | channels, members, last activity | FR4.2 | `--joined` limits to the channel we occupy; `--probe <n>` (default 0) joins the top-N briefly to fetch last activity; `-` when unknown unknown |
| `chatterbox create-channel <#name>` | create by joining, then move (JOIN, verify, then PART) | FR4.3 | `--topic <s>`, `--dry-run`; `--keep` rejected with an explanation (`01#OQ2`) |
| `chatterbox status` | listener health | FR2.6, ops | `--json` for the `03#3.4` payload |
| `chatterbox doctor` | ordered precondition check | ops, FR6 | exit 0 only when all PASS |
| `chatterbox trust [--list\|--forget <host:port>\|--fingerprint <fp>]` | pin store | FR6.2 | the only way to change a pin |
| `chatterbox skill` | print the onboarding block | FR8.1 | `--format markdown\|plain\|human` — `human` is ≤ 15 lines for the person steering from an ordinary client (`07#R18`) |
| `chatterbox init` | write a commented config template, `0600` | FR6.1 | refuses to overwrite |

Message text rules: multiple positional args are joined with a single space; `--stdin` reads the
whole of stdin (UTF-8) so an agent can send a multi-paragraph block without shell quoting; a
single `-` positional also means stdin. `publish`/`send` with no text is a usage error (exit 2),
never an empty PRIVMSG.

There is deliberately **no** `chatterbox listen --daemonize`, no `join`/`part`/`topic`/`quit`
subcommands, and no generic `raw` passthrough: FR1.3 and the Concepts section make the listener the
sole owner of the connection, and a raw passthrough would let an agent do anything except the one
thing the requirement list describes. If a future need appears, it becomes a named command.

`--dry-run` on sends prints the framed output (line count, byte sizes, chunk boundaries) and exits 0
without contacting the server — the cheapest way to test FR3.4 and the one an agent can run without
permissions.

## 4.2 Help text

`--help` on every subcommand carries a one-line purpose, a copy-pasteable example, and the exit
codes it can produce. `chatterbox listen --help` additionally states the stdout/stderr contract,
because that is the interface an agent breaks by accident:

```
stdout carries one line per delivered message and nothing else.
diagnostics go to stderr. read both if you need them; never let stdout fill up.
```

## 4.3 Wake line — plain format (default)

Exactly six space-separated fields, then the message text to end of line:

```
HH:MM:SS <channel> <kind> <flags> <sender> > <text>
```

```
12:03:41 #hof bot @ claude> look at FR3.5 before you touch the framing code
12:03:44 #hof hum @+ teodor> pi: stop planning and send something
12:03:50 #hof ? -- someone> hi
12:04:07 #hof bot +-- claude> line one\nline two joined by a multiline batch
```

| Field | Values | Notes |
|---|---|---|
| time | `HH:MM:SS` UTC (server time via the `time` tag; `--time local` for local) | always 8 chars, so `cut -d' ' -f…` is stable |
| channel | `#name` | FR2.2; included even though the listener has one channel, so a log of several listeners parses the same way |
| kind | `bot` \| `hum` \| `?` | `?` = not yet determined (`02#2.10`); never a silent `hum` |
| flags | `@` addressed to me, `+` important, both as `@+`, neither as `--` | fixed order `@` then `+` so `grep ' @+ '` finds pings-that-matter |
| sender | nick, raw | `>` is not legal in an IRC nick, so ` > ` is unambiguous |
| text | verbatim, as received | real newlines escaped to the two characters `\n`; a literal backslash to `\\`, so it is reversible; trailing whitespace trimmed |

The `[important] ` body marker is **not** stripped from `text`: the line shows what a human in the
channel sees. The `+` flag is the machine-readable duplicate of it.

`--wake mentions` prints only lines where flags contain `@` or `+` (FR2.5, FR3.3). Colour is off
unless stdout is a TTY and `--color=auto`/`always`; with `--json` colour is never emitted.

## 4.4 Wake record — JSON format (`--json`, FR2.3)

One object per line. `v` is the schema version; consumers ignore unknown fields and must tolerate
`null` in every optional field.

```json
{"v":1,"type":"message","time":"2026-10-10T12:03:41.226Z","tz":"server",
 "channel":"#hof","from":"claude","kind":"bot","kind_source":"tag",
 "addressed":false,"important":true,"text":"look at FR3.5\nsecond line",
 "msgid":"9pk4p499g9x8k29qedc224cge2","label":null,"sender_account":null,
 "multiline_parts":2,"source":"live"}
```

| Field | Type | Nullability / meaning |
|---|---|---|
| `v` | int | always `1` in v1 |
| `type` | `"message"` \| `"state"` | `state` only with `--json --state` |
| `time` | RFC 3339 UTC, ms | from the `time` tag (`server-time`); `tz":"local"` if synthesised |
| `channel` | string | `#name` |
| `from` | string | sender nick, PREFIX-stripped |
| `kind` | `"bot"\|"hum"\|"?"` | FR2.2 |
| `kind_source` | `"tag"\|"whois"\|"cache"\|"unknown"` | why `kind` is what it is; lets a reviewer audit FR2.2 |
| `addressed` | bool | FR2.5 matcher (`01` FR2.5) |
| `important` | bool | `[important] ` body marker |
| `text` | string | verbatim body; embedded newlines are JSON-escaped, so one record = one line |
| `msgid` | string \| null | dedup key; null only if the server omitted it |
| `label` | string \| null | set only on echoes of our own sends, which are suppressed from stdout; present for `--verbose` traces |
| `sender_account` | string \| null | from `account-notify`/`extended-join`; null when the sender is unregistered (the normal case: SASL is out of scope) |
| `multiline_parts` | int | 1 for a single-line message; N when a `draft/multiline` batch was coalesced (FR2.1 evidence) |
| `source` | `"live"\|"backfill"` | `backfill` = delivered during FR1.6 gap fill |

`state` records (`--json --state`):
`{"v":1,"type":"state","state":"reconnecting|connected|joined|catchup|closing|conn","reason":"dns|tcp|tls|eof|pong_timeout|killed|…","retry_in_s":4,"wakes_dropped":0}`.
They exist so an agent can distinguish "nobody talked" from "we are offline", which plain wake
silence cannot express. Default off, so the minimal-output promise holds by default.

**FR2.6 exception, stated as a decision (reviewed).** FR2.6 says diagnostics go to stderr and they do
— every log line does. `state` records are not logs but a machine-readable status channel on the
stream the agent is already reading, because "no lines for ten minutes" is otherwise ambiguous in a
way that costs a wasted restart or a missed deadline. Four rules keep the exception small: emitted
only with `--json --state`; they carry no `@`/`+` flag token, so a harness that wakes on flagged lines
ignores them while `--wake all` readers see them; they are counted against the I3 buffer, so a state
flood cannot evict a real wake; and `IT-208b` pins their schema. The strict alternative (stderr,
`#`-prefixed) was considered and rejected because it breaks the property that makes them worth
reading — that is a decision, recorded in `09`, not an oversight.

## 4.5 Read-only command output

`whos-there` (plain, FR4.1):

```
#hof  3 participants (2 bots, 1 human)
  NICK      KIND     FLAGS  ACCOUNT  SINCE
  teodor    hum      @      -        2026-10-10T09:58:11Z
  pi        bot      -      -        2026-10-10T10:02:33Z
  claude    bot      -      -        2026-10-10T10:04:02Z
```

JSON: `{"v":1,"channel":"#hof","participants":[{"nick":"teodor","kind":"human","kind_source":"whois","operator":true,"account":null,"since":"…"}],"unknown_count":0}`.
`kind` is `bot|human|unknown` here (verbose words, not `bot|hum|?`) because this output is read by
humans too; the mapping is documented in `08`.

`channels` (plain, FR4.2):

```
NAME            MEMBERS  LAST_ACTIVITY            SOURCE
#hof            3        2026-10-10T10:34:14Z     chathistory
#cbtest-plan    2        -                        unavailable
```

`status`, `doctor`, `trust --list`, `skill`, `--dry-run` framings are specified in
`03#3.4`/`03#3.10` and asserted by IT-225..228.

## 4.6 Exit codes and error taxonomy

`--json` on any error path prints `{"v":1,"ok":false,"error":{"code":…,"message":…,"hint":…}}` on
**stdout** (the command's own result, not a wake line) and the human-readable form on stderr.

| Code | `code` | Meaning | FR | Fix hint printed |
|---|---|---|---|---|
| 0 | – | accepted by the server / command succeeded | FR3.5 | – |
| 1 | `io` | local I/O failure that is not a config or TLS problem: state dir unwritable, pin store unreadable, disk full, socket accept failure | – | what `chatterbox doctor` says about that path |
| 2 | `usage` | bad flags, missing nick, bad nick/channel shape, `create-channel --keep` | FR1.3, FR5.1 | the corrected command line |
| 3 | `no_listener` | no live listener for this nick | FR3.6 | `chatterbox listen --as <nick> --channel <#ch>` |
| 4 | `queue_full` | send queue saturated (64 pending) — a caller pacing problem | FR3.5 | slow down; `status` shows `queued` |
| 5 | `server_reject` | server refused (numeric or `FAIL`), text included | FR3.5 | the numeric and its meaning |
| 6 | `ack_timeout` | no label echo within `--timeout` (caller) or `limits.ack_timeout_secs` (listener; the binding one — `--timeout` may only shorten it) | FR3.5 | `status` first; **do not re-send blind** — the listener may still hold it queued or have written it (`03#3.4` cancellation, `07#R15`) |
| 7 | `ambiguous` | ≥ 2 live listeners, none named | FR5.2 | the candidate nicks |
| 8 | `nick_mismatch` / `config` | command asked to post as a nick that is not this listener's; or config error | FR5.3, FR6.1 | the nick to use |
| 9 | `pin_mismatch` | pinned fingerprint ≠ presented | FR6.2 | `<host:port>`, the stored fingerprint, and the `trust --fingerprint` form to run; **the presented fingerprint is printed only when stderr is a TTY**, so an agent cannot self-pin a MITM by copying the error (`07#R16`, `UT-199`) |
| 10 | `nick_in_use` | `433` at registration | FR1.4 | choose another `--as` |
| 11 | `conn_lost` | connection dropped before acceptance | FR3.5, FR1.6 | retry after `catchup complete` |
| 12 | `channel_lost` | kicked/removed and re-JOIN failed | FR1.3 | re-`listen` with a channel you can join |
| 13 | `cap_missing` | server did not grant a required capability | FR1.1, FR1.6, FR2.2, FR3.4 | which cap, and why chatterbox needs it |
| 14 | `unpinned` | no pin stored, non-interactive | FR6.2 | `<host:port>` + `trust --fingerprint <fp>`, with `<fp>` shown **only on a TTY**; otherwise it says "ask the human who runs the server for the fingerprint" (`07#R16`) |
| 15 | `peer_uid` | control socket rejected the caller's uid | FR6.3 | run as the user who started the listener |
| 16 | `conn` | initial connect failed: `dns`, `tcp` or `tls` sub-label, none of them a pin mismatch | FR1.1, FR1.5 | is the server up? firewall? the config line to check. **Only reached with `--no-retry`** (CI); otherwise the listener retries on the same backoff as any drop, so FR1.5 holds from process start |
| 17 | `send_buffer_full` | TLS write buffer past its high-water mark — the socket is the limit, not the queue | FR3.5 | retry after `status.retry_in_s`; deliberately distinct from 4 so a caller does not retry a queue overflow at socket-backpressure cadence |
| 18 | `no_target` | the `send` target is not in the channel's NAMES snapshot, so a channel-addressed message would succeed while reaching nobody | FR3.2, FR3.5 | `whos-there`, or `--dm <nick>` for a real private message, or `--force` if you know better than the cache |
| 19 | `bot_mode_refused` | `BOT=B` advertised but `MODE +B` refused (`674` reserved, `491` no privilege) | FR1.2 | ask the operator; until fixed, peers classify this listener as human and `status` reports `server.bot_mode: refused` |

**The whole set is the API.** `src/err.rs` owns `err::ALL_CODES` as a single list; the exit table, the
IPC error codes (`03#3.4`) and the skill's agent-facing table are all generated or checked against it.
`UT-197` fails if any code is missing from `skill/SKILL.md` §6, because a reader who cannot name a
failure cannot recover from one, and `UT-198` parses every command example in the skill against the
real argument parser so the documented grammar and the enforced grammar cannot drift apart.`

`401 No such nick`, `FAIL BATCH MULTILINE_MAX_BYTES 4096` and friends are surfaced inside
`server_reject.message` with the raw server line preserved in `server_detail`, so an agent can act
without parsing IRC numerics.

## 4.7 Message framing rules (FR3.4, normatively)

1. Compute the serialized line length **including** the tag skeleton the server will echo back
   (worst case ≈ 96 bytes for `msgid`+`time`+`bot`+`label`), against `limits.line_bytes = 450`.
2. If the text fits in one line → single `PRIVMSG`, no batch.
3. Else split: prefer line breaks at existing `\n`; then at word boundaries; then at a UTF-8 char
   boundary as last resort. Lines that continue a paragraph get `draft/multiline-concat` with the
   separating space at the end of the *previous* line (measured concatenation behaviour, `02#2.6`).
4. Wrap the parts in one `draft/multiline` batch if `batch_bytes` (default `min(config, 4096)`) and
   `batch_lines = 100` are both respected.
5. If total > `batch_bytes`, emit **N sequential batches in order**, each self-contained and
   separately acked; stop at the first failure and report `chunk <i>/<N> failed: <server text>`.
6. If `draft/multiline` is **lost mid-session** — a granted capability removed by `CAP DEL`, which `03#3.3` treats as an unhealthy connection and reconnects — the retry path degrades to N plain PRIVMSGs in order with one loud stderr WARN. Revision 1 also used this rule for "capability absent", which was dead code: `draft/multiline` is in the **required** set (FR3.4 names it), so absence is exit 13, never a fallback. Rule 6 exists for the shrink-underfoot case and for servers that grant it then take it away.
   warning (FR3.4's fallback), rather than failing the send.
7. Never send `PING`, `QUIT`, `CAP`, or `MODE` through this path — the framing rules apply to
   message text only.

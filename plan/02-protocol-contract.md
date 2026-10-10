# 2. Protocol contract (measured)

Target server: Ergo 2.19.1 (`NETWORK=HofNet`, `server.name=irc.heapzilla.eu`), issue #1 says
"developed against Ergo 2.19". Everything here was observed on the live server, not read from a
spec, unless labelled *spec*. Sections marked **CONSTRAINT** must be implemented; **DRIFT** means
re-measure before relying on it.

This file is the implementer's cheat sheet for M1–M4. Its purpose is that nobody rediscovered in
M4 what was measured in planning.

## 2.1 Endpoint and TLS

| Item | Value |
|---|---|
| Address | `192.168.0.204:6697` (tiny2, LAN). TLS listener binds `:6697`; plaintext `6667` is loopback-only by design, so chatterbox must not be pointed at it |
| TLS | min TLS 1.2 (server config), self-signed leaf `O=Ergo`, self-issued, notBefore 2026-10-10, notAfter 2027-10-10 |
| Leaf SHA-256 (DRIFT) | `44:0D:EE:9D:D4:B1:BE:62:C6:6A:5F:DF:51:69:9E:B2:6F:89:07:40:5D:FE:48:0F:3B:54:36:30:71:0C:E0:BA` |
| Consequence | certificate validity cannot be checked against a CA ⇒ FR6.2 pinning is not optional polish, it is the only authentication. chatterbox pins the leaf fingerprint and refuses anything else. |

## 2.2 Capability negotiation — **CONSTRAINT: REQ, not ACK**

The server rejects `CAP ACK` and grants capabilities only through `CAP REQ`:

```
C: CAP LS 302
S: :irc.heapzilla.eu CAP * LS * :account-notify account-tag away-notify batch cap-notify chghost \
   draft/account-registration=before-connect draft/channel-rename draft/chathistory \
   draft/event-playback draft/extended-isupport draft/languages=17,en,… \
   draft/multiline=max-bytes=4096,max-lines=100 draft/persistence
S: :irc.heapzilla.eu CAP * LS :draft/pre-away draft/read-marker draft/relaymsg=/ draft/whoami \
   echo-message ergo.chat/nope extended-join extended-monitor invite-notify labeled-response \
   message-tags multi-prefix no-implicit-names sasl=PLAIN,EXTERNAL,SCRAM-SHA-256 server-time \
   setname standard-replies userhost-in-names znc.in/playback znc.in/self-message

C: CAP ACK :server-time …            → S: 410 * ACK :Invalid CAP subcommand      (rejected)
C: CAP REQ :server-time              → S: @time=… CAP * ACK server-time          (granted)
```

**Observed on this server, in this state:** both `CAP ACK :a b` and `CAP ACK a b` produced `410`,
pre- and post-registration. Two honesty notes, because this claim is load-bearing and was the most
disputed line in the plan's review. (i) One probe that produced the same `410` was later traced to
sending the request before the `LS` pages had drained, so the transcript in
`docs/protocol-notes.md` is the evidence, not this sentence; T-0.3(d) re-runs it with the pages fully
drained. (ii) The IRCv3.2 spec does allow a client to `CAP ACK` a server-initiated `CAP NEW`/`CAP DEL`
(cap-notify), so chatterbox's rule is **"we never use `CAP ACK` as a granting step"**, not "`CAP ACK`
is illegal" — `UT-310` asserts the former. The design is unaffected either way, because step 5
asserts the grant with `CAP LIST` rather than trusting any particular grant message.

**Why this is the most dangerous finding in the file:** a client that follows the IRCv3 cheat sheet
(`LS` → `ACK` → register) receives no error, registers fine, joins fine, and ends up with **zero**
capabilities — no `server-time` (so timestamps must be guessed locally), no `draft/multiline` (FR3.4
silently impossible), no `draft/chathistory` (FR1.6 impossible), no `message-tags` (FR2.2 bot/human
lost). Every symptom appears in a different requirement than the cause.

**CONSTRAINT — chatterbox's negotiation sequence:**

1. `CAP LS 302`, consume the paged replies (`CAP * LS *` = continuation, `CAP * LS` = last page).
2. `CAP REQ :<space-separated list>` for the caps we want, from the advertised set only.
3. `NICK` + `USER`.
4. `CAP END`.
5. **Assert** with `CAP LIST`; compare against the required set. If a required cap is missing,
   exit 13 with `server did not grant <cap>; refusing to start` rather than degrading quietly.
   `doctor` re-checks. **Required:** `server-time message-tags batch draft/multiline
   draft/chathistory echo-message labeled-response standard-replies`. **Wanted, warn-only:**
   `extended-join userhost-in-names multi-prefix account-notify away-notify`.

   Two changes from revision 1, both from the review. `account-notify`/`away-notify` moved out of the
   required set: accounts and SASL are an explicit non-goal (`01#1.4`), `sender_account` is always
   `null` without them, and requiring them meant a spec-compliant server without those two
   extensions got exit 13 for features v1 does not use. `draft/multiline` stays required, because
   FR3.4 names it — but that makes the plain-split path in `04#4.7` rule 6 unreachable by
   *absence*, so rule 6 is re-scoped to the two cases that can actually occur: a granted-then-removed
   capability (a mid-session `CAP DEL`, which now forces a reconnect per `03#3.3`, and degrades to
   plain splits with a loud WARN if the server keeps doing it) and a message that exceeds the batch
   limits. `extended-join` is wanted because its realname is display-only; `userhost-in-names` and
   `multi-prefix` are display niceties; losing any of them costs no FR.
6. `MODE <self> +B` (FR1.2), then `JOIN`.
7. Also request `draft/extended-isupport` when advertised, and re-read `RPL_ISUPPORT` after
   registration; a second `RPL_ISUPPORT` can add tokens mid-session.

Requesting `no-implicit-names` is deliberately **not** done: without it the server sends `353`/`366`
on JOIN (measured), which is the cheapest way to seed the NAMES snapshot for FR4.1.

## 2.3 `RPL_ISUPPORT` (005) — the tokens chatterbox must honour

Observed verbatim:

```
AWAYLEN=390 BOT=B CASEMAPPING=ascii CHANLIMIT=#:100 CHANMODES=Ibe,k,fl,CEMRUimnstu
CHANNELLEN=64 CHANTYPES=# CHATHISTORY=1000 ELIST=U EXCEPTS EXTBAN=,m FORWARD=f INVEX
KICKLEN=390 MAXLIST=beI:100 MAXTARGETS=4 MODES MONITOR=100 MSGREFTYPES=msgid,timestamp
NETWORK=HofNet NICKLEN=32 PREFIX=(qaohv)~&@%+ SAFELIST SAFERATE STATUSMSG=~&@%+
TARGMAX=NAMES:1,LIST:1,KICK:,WHOIS:1,USERHOST:10,PRIVMSG:4,TAGMSG:4,NOTICE:4,MONITOR:100
TOPICLEN=390 UTF8ONLY WHOX draft/CHATHISTORY=1000
```

| Token | Value | Use |
|---|---|---|
| `BOT` | `B` | FR1.2: the usermode char to set. Absent ⇒ warn and continue |
| `NICKLEN` | 32 | validate `--as` length before connecting (exit 2, not a server error) |
| `CHATHISTORY` / `draft/CHATHISTORY` | 1000 | max messages per gap-fill request ⇒ the page size in `03#3.6` |
| `MSGREFTYPES` | `msgid,timestamp` | gap-fill cursor kinds; `msgid` is exact, `timestamp` is the fallback |
| `CASEMAPPING` | `ascii` | nick comparison is ASCII-case-insensitive; the addressed-pattern must match it |
| `STATUSMSG` | `~&@%+` | part of the addressed-detection rules (FR2.5) |
| `PREFIX` | `(qaohv)~&@%+` | strip before matching names in `whos-there`; `@` = channel operator |
| `CHANNELLEN`/`CHANTYPES` | 64, `#` | validate `--channel` shape before connecting |
| `MAXTARGETS`/`TARGMAX` | 4 PRIVMSG | chatterbox sends one target per message; never multi-targets |
| `WHOX` | present | available, but see `02#2.10`: WHOX cannot expose usermodes, so it does not help FR4.1 |
| `UTF8ONLY` | present | messages are UTF-8; chatterbox must not truncate mid-codepoint (`02#2.5`) |
| `FORWARD` | `f` (mode exists) | a forwarded channel can redirect a JOIN somewhere else; the JOIN handler compares the joined channel against the requested one and treats a mismatch as fatal (`02#2.3a`) |

### 2.3a JOIN failure — one catch-all, because the list is not enumerable from here

`JOIN` can be refused for reasons this plan has not individually reproduced on this server
(`473` invite-only, `474` banned, `471` full, `405` `CHANLIMIT`, `403` no such channel and creation
forbidden, `460`/`719`/`690` variants, plus `FORWARD=f` diversion). **Design rule, so no implementer
has to guess:** any numeric in reply to our `JOIN` is fatal to that channel — at startup, exit 12
with the server text; mid-session, exit 11; for `create-channel`, exit 5 **and the previous channel
is not parted** (`01#OQ2`). T-0.3(c) enumerates which of these this server can actually produce and
turns this paragraph into a table; until then the catch-all is the contract.

## 2.4 Registration, nick failure, `+B`

- Registration completion is `001`…`376`; `MODE <self>` returns `221 +Zi` (`+Z` = TLS, `+i` =
  invisible).
- **Nick taken:** `:irc.heapzilla.eu 433 * <nick> :Nickname is already in use`. FR1.4 ⇒ exit 10,
  no retry, no fallback name.
- **`+B` is real and observable:** `MODE cbver2 +B` accepted, `221 +BZi`, and
  `379 cbver1 cbver1 :is using modes +BZi` + `335 cbver1 cbver1 :is a Bot on HofNet`.
- **`bot` message tag:** once `+B` is set, every delivered line from that client carries `bot`:

```
@msgid=9pk4p499g9x8k29qedc224cge2;bot;time=2026-10-10T10:33:59.226Z \
  :cbver3!~u@… PRIVMSG #cbtest-plan :hello from the bot
```

  This is the mechanism behind FR2.2 and part of FR4.1. It is an Ergo behaviour, not an IRCv3
  standard, so the parser treats a missing `bot` tag as "unknown", never as "human", until WHOIS
  agrees.

## 2.5 Sending a message — line limits are per line, not per message

- A single `PRIVMSG` whose line exceeds 512 bytes (tags included) is refused with
  `:irc.heapzilla.eu 417 cbver1 :Input line too long` (measured with a 620-byte payload).
- **CONSTRAINT:** chatterbox computes the full serialized line — `@tags :nick!user@host PRIVMSG #chan :text`
  — and keeps it ≤ `limits.line_bytes`, default **450**, floor **200** (`03#3.7`). The tag budget is
  not free: `msgid=…;time=…;label=…` costs **96 bytes** worst case on echo (measured overhead ≈ 90),
  so the client-side budget has to cover what the server adds on the way back. **96 and `line_bytes`
  are the only spellings in this plan** — revision 1 had 90 here, 96 in `04`, `max_line_bytes` in two
  places and `line_bytes` in three, which is how a budget constant becomes a debate.
- UTF-8 must be truncated at a char boundary; `UTF8ONLY` is advertised and a split codepoint would
  be a wire-level corruption bug.
- Splitting is done at word boundaries where possible, keeping the separating space at the end of
  the preceding line (the multiline spec's recommendation, and confirmed by measurement: Ergo
  concatenates `draft/multiline-concat` lines with no separator — our `"  multi two joined"` arrived
  with both spaces intact).

## 2.6 `draft/multiline` — exact format, and two traps

Server advertises `draft/multiline=max-bytes=4096,max-lines=100`.

Working format (measured accepted, then echoed and relayed):

```
C: BATCH +t1 draft/multiline #cbtest-plan
C: @batch=t1 PRIVMSG #cbtest-plan :MLV line one
C: @batch=t1 PRIVMSG #cbtest-plan :line two is here
C: @batch=t1;draft/multiline-concat PRIVMSG #cbtest-plan : (concat appended to two)
C: BATCH -t1
```

Trap 1 — **batch ids are rewritten in transit.** We opened `+t1`; the echo came back `+1`; a second
client saw `+hqvpzrxhd2bvj`. Therefore: never assume a batch id survives, never derive meaning from
one, and key the coalescing state purely on the `batch=` tag of the line currently being read.
Correlation for acknowledgement is by `label` (`02#2.7`), never by batch id.

Trap 2 — **the delivered form differs from the sent form.** A third-party client receives:

```
@time=…;msgid=…;bot :cbver3!~u@… BATCH +hqvpzrxhd2bvj draft/multiline :#cbtest-plan
@batch=hqvpzrxhd2bvj :cbver3!~u@… PRIVMSG #cbtest-plan :multi one
@batch=hqvpzrxhd2bvj;draft/multiline-concat :cbver3!~u@… PRIVMSG #cbtest-plan :  multi two joined
:cbver3!~u@… BATCH :-hqvpzrxhd2bvj
```

Note `BATCH +<id> draft/multiline :#channel` (colon-prefixed target param) and `BATCH :-<id>` for
the close. The parser must accept `+id`/`-id` and `:-id` forms, quoted and unquoted target, and
must not assume the type token is exactly `draft/multiline` (accept `multiline` too, for a future
finalised spec).

Over-limit behaviour (measured):

```
C: BATCH +ovf draft/multiline #cbtest-plan   + 10 × 470-byte lines
S: FAIL BATCH MULTILINE_MAX_BYTES 4096 :Multiline batch byte limit 4096 exceeded
```

and, when the server aborts a batch mid-flight, the remaining `@batch=…` lines cascade into
`FAIL BATCH MULTILINE_INVALID :Incorrect batch tag sent` and `FAIL BATCH INVALID_PARAMS :Corrupt
BATCH`. **CONSTRAINT:** never use the server as a validator — pre-flight the byte and line budgets,
and treat any `FAIL BATCH` as a chatterbox bug to report on stderr with the offending chunk size.
`MULTILINE_MAX_LINES` was not observed cleanly (the server aborted earlier); the design does not
depend on it, since we enforce 100 lines locally.

**Client-sent message tags are stripped.** We sent
`@chatterbox/important=1 PRIVMSG #cbtest-plan :…`; both the echo and the third client's copy showed
`@time=…;msgid=…;bot` and **no** `chatterbox` tag. `TAGMSG` is additionally not relayed to other
clients (we saw no `TAGMSG` line on the receiver). Consequences in `02#2.9`.

## 2.7 Acknowledgement: `echo-message` + `labeled-response` (FR3.5)

Both caps are granted by REQ and work. Measured:

```
C: @label=L-send-1 PRIVMSG #cbtest-plan :labelled-message-one
S: @msgid=…;bot;time=…;label=L-send-1 :cbver2!~u@… PRIVMSG #cbtest-plan :labelled-message-one

C: @label=L-send-2 PRIVMSG nosuchnick9 :hi
S: @time=…;label=L-send-2 :irc.heapzilla.eu 401 cbver2 nosuchnick9 :No such nick

C: @label=L-send-3 BATCH +ml1 draft/multiline #cbtest-plan
S: @time=…;msgid=…;bot;label=L-send-3 :cbver2!~u@… BATCH +1 draft/multiline #cbtest-plan
```

**CONSTRAINT:** the definition of "accepted" for FR3.5 is *the echo or the error carrying our
label*. Rules: labels are `cb<process-start-id>-<monotonic-counter>`, unique per connection, never
reused; a send command blocks on its label; a pending send is failed if the connection drops while
waiting (exit 11, "connection lost before the server accepted the message"), never reported as
success; and because `echo-message` is on, FR2.4's self-filter is mandatory or every send would
echo as a wake.

Alternative rejected: "no error within N seconds means success". Rejected because it reports
success on a 401/404 race, and FR3.5 is explicit that exit 0 requires acceptance.

## 2.8 `chathistory` (FR1.6)

Syntax that works on this server (`MSGREFTYPES=msgid,timestamp`):

| Query | Meaning | Measured |
|---|---|---|
| `CHATHISTORY LATEST <#chan> * 20` | newest ≤20, all-versions-ok | ✔ (a `*` must be a bare param; `timestamp=*` is **not** accepted → `FAIL CHATHISTORY INVALID_TARGET … :Messages could not be retrieved`) |
| `CHATHISTORY LATEST <#chan> timestamp=<ISO8601> 20` | messages strictly after that time | ✔ |
| `CHATHISTORY LATEST <#chan> msgid=<id> 20` | messages after that msgid | **not yet exercised — T-0.3(a) is an M0 gate (`05`), not a to-do.** `MSGREFTYPES` advertises `msgid` and the spec says "after"; the fallback is `timestamp=<cursor.time − 1000 ms>` + msgid-LRU dedup, pre-written in `03#3.6` |
| `CHATHISTORY BEFORE <#chan> timestamp=<ISO8601> 20` | messages before that time | ✔ |

Reply shape — **CONSTRAINT, this is where naive parsers break:**

```
S: @draft/chathistory-end;time=… :irc.heapzilla.eu BATCH +2 chathistory #cbtest-plan
S: @msgid=_p8txu58…;time=…;batch=2 :HistServ!HistServ@localhost PRIVMSG #cbtest-plan :cbver1 joined the channel
S: @time=…;msgid=3gutycsm…;bot;batch=2 :cbver1!~u@… BATCH +3 draft/multiline #cbtest-plan
S: @batch=3 :cbver1!~u@… PRIVMSG #cbtest-plan :MLV line one
S: @batch=3;draft/multiline-concat :cbver1!~u@… PRIVMSG #cbtest-plan : (concat appended to two)
S: @batch=2 :cbver1!~u@… BATCH -3
S: @msgid=bnkwjgmp…;time=…;batch=2 :cbver1!~u@… PRIVMSG #cbtest-plan :echo-check-unique-token
S: @time=… :irc.heapzilla.eu BATCH -2
```

Five constraints follow:

1. Results are delimited by a `BATCH +<id> chathistory <chan>` … `BATCH -<id>` envelope, **nested**
   with any multiline batches inside it. The parser must keep a stack, not a single slot.
2. `+draft/chathistory-end` on the opening line = nothing older to fetch; that, or the matching
   `BATCH -<id>`, ends the replay.
3. Every replayed line carries `msgid` and `time` ⇒ the dedup key is `msgid` (join/quit entries get
   `_`-prefixed msgids; treat the msgid as opaque, `=` and `,` are escaped in tag values — decode
   before comparing). **`msgid` is never an ordering key.** The three msgids in the transcript above
   (`_p8txu58…`, `3gutycsm…`, `bnkwjgmp…`) are opaque and not monotonic, which is why `time` sorts and
   msgid dedups; a test that asserts "msgids sort chronologically" would encode a false invariant
   (UT-113).
4. Join/part/quit history is replayed as **`PRIVMSG` from `HistServ!HistServ@localhost`**, i.e. it
   looks exactly like chat. FR2.4 requires suppressing it, and the gap-fill cursor must still advance
   over it. Filter = `nick == HistServ` and/or `host == localhost` service users, config-overridable.
5. The listener's **own** messages appear in replay (measured), so FR2.4's self-filter applies to
   replay too, not only to live echo.

Server-side history limits (tiny2 config): `history.enabled: true`, `channel-length: 2048`
messages, `chathistory-maxmessages: 1000`, `autoreplay-on-join: 0` ⇒ no automatic replay, so
CHATHISTORY must be asked explicitly; a long outage older than 2048 messages is simply gone, and the
listener says so on stderr rather than pretending the gap was filled.

## 2.9 Importance marking (FR3.3) — the tag route is closed

Measured: client tags stripped on PRIVMSG (`02#2.6`), `TAGMSG` not relayed, so a tag-based marker
would be dropped in transit and would be invisible to a human client anyway. Ergo also ignores
client attempts to set server-namespace `+tag`s on PRIVMSG (one observation showed a `+chatterbox/…`
tag preserved on a TAGMSG echo and stripped on a PRIVMSG — inconsistent, and not something to build
on).

**CONSTRAINT:** importance travels **in the body**, as the literal prefix `[important] `. It is
visible in any IRC client (FR7.1/FR7.3), survives history replay, survives a multiline fallback to
plain lines, and is matchable by the receiving listener. chatterbox additionally attaches
`chatterbox/important=1` as a client tag for a future server that preserves it, logs once on stderr
that the tag is advisory, and never treats a missing tag as "not important".

Rejected alternatives: TAGMSG-only (not relayed); a dedicated `[cbi]` acronym (opaque to humans);
channel mode or nickname convention (not per-message).

**One consequence the first edition did not state:** the marker is **unauthenticated**. Anyone in the
channel — a human, a buggy agent, a non-chatterbox client — can type `[important] ` and force an
unfiltered wake on every chatterbox listener present (`--wake mentions` does not suppress it, by
FR3.3's own words). That makes it a Denial-of-attention vector, bounded by `--max-wakes` coalescing
(`07#R7`), and it is why the skill tells the human the marker is a shared, unpolicied convention.

## 2.10 Presence: `NAMES`, `WHO`, `USERHOST`, `LIST`

- With `userhost-in-names` negotiated, JOIN yields `353 … :@cbver3!~u@host cbwatch!~u@host` and
  `366`. Operator prefix `@` per `PREFIX=(qaohv)~&@%+`.
- `extended-join` adds account + realname: `JOIN #chan * v3`.
- `WHO #chan %cuynhah` returns `354` rows of channel/userhost/host/nick/hops — **no usermode
  column exists in WHOX**, so WHO cannot answer "is this a bot".
- `USERHOST a b` → `nick=+~u@host` (`+` = online only, no modes).
- `WHOIS <nick>` for a bot ends with `335 <me> <nick> :is a Bot on HofNet`; for the human `teodor`,
  no `335` at all. Confirmed from a *different* client than the subject, which is what FR4.1 needs.
- `LIST` → `322 <chan> <members> :` + `323`; no timestamps, no modes. `LIST U` accepted (ELIST=U)
  but returns the same fields.
- **CONSTRAINT:** FR4.1's bot/human column = NAMES ∪ bot-tag cache ∪ `WHOIS 335`, with `unknown`
  permitted. FR4.2's activity column = chathistory for the occupied channel, `-` elsewhere.

## 2.11 Errors, failures, and channel behaviour

| Situation | Server response (measured) | chatterbox behaviour |
|---|---|---|
| nick in use | `433 * <nick> :Nickname is already in use` | exit 10 (FR1.4) |
| target absent | `401 <me> <target> :No such nick` | send exits 5 with server text |
| line too long | `417 <me> :Input line too long` | bug in framing; exit 5 and log chunk size |
| batch/cap failures | `FAIL <CMD> <CODE> [params] :<text>` (standard-replies) | surfaced verbatim in the send error |
| history for a channel we are not in | `FAIL CHATHISTORY INVALID_TARGET LATEST #chan :Messages could not be retrieved` | `channels` prints `-` |
| JOIN a non-existent channel | JOIN accepted, we become `@`, modes `+Cnt` | FR4.3 mechanism |
| empty unregistered channel | removed by the server (3 test channels disappeared after PART) | OQ2 rationale; documented to users |
| second JOIN | accepted by the server | blocked by chatterbox (FR1.3) |

Default channel modes on creation: `+Cnt` (`c` no-CTCP, `n` no-external-messages, `t` ops-only
topic) from `channels.default-modes: +ntC`.

## 2.12 Keepalive and liveness

Server ping timeout is **2 m 30 s** (observed in history strings: `tao quit (Ping timeout: 2m30s)`).
**CONSTRAINT:** the listener sends `PING chatterbox-<n>` every 50 s, treats 120 s of total silence
as a dead connection (force reconnect), and does not consider a connection healthy if no `PONG`
followed within 50 s. Ergo's fakelag is enabled, so rapid-fire sends can be delayed; sends are
serialised with one outstanding labelled batch at a time and the queue is bounded (`03#3.4`).

## 2.13 Hard-limit summary table (the numbers that go into code and tests)

| Limit | Value | Source |
|---|---|---|
| IRC line, serialized | 512 bytes, tags included | server-enforced (`417`) |
| chatterbox line budget | **450 bytes** (floor 200) | `limits.line_bytes`, headroom for echo tags |
| worst-case tag budget | 96 bytes | `msgid`+`time`+`bot`+`label`, measured ≈ 90 (`02#2.5`) |
| Multiline batch bytes | 4096 | CAP param, measured `FAIL … MULTILINE_MAX_BYTES 4096` |
| Multiline batch lines | 100 | CAP param |
| Nick length | 32 | `NICKLEN` |
| Channel name length | 64 | `CHANNELLEN` |
| CHATHISTORY page | 1000 messages | `CHATHISTORY=1000` |
| Channel history retention | 2048 messages | server config |
| One channel per listener | 1 | FR1.3, local rule |
| Ping timeout | 150 s | measured, drives the 50/120 s keepalive |
| Pending send queue | 64 | `limits.queue_capacity` |
| Send ack timeout | 10 s | `limits.ack_timeout_secs`, overridable per call by `--timeout` |
| In-flight multiline batches | 1 | measured (`02#2.12`), serialise-or-reject |
| Catch-up pages per gap | 3 | `ceil(channel-length/CHATHISTORY)+1` = `ceil(2048/1000)+1` |
| Stdout wake buffer | 512 records | I3 (`06#6.1`); a drop does **not** advance the cursor (`03#3.6`) |
| TLS write buffer / high water | 256 KiB / 220 KiB | `03#3.3`; high water ⇒ exit 17 |

## 2.14 Anti-pattern list (each item is a measured trap, for PR review)

1. Do not send `CAP ACK` as the granting step. Request. Assert with `CAP LIST`.
2. Do not assume a batch id you generated is the one you will receive.
3. Do not use a message tag as a transport for anything required. Body only.
4. Do not parse `CHATHISTORY` output with a single-slot batch tracker, and do not print `HistServ`
   lines.
5. Do not report success for a send without a matching `label` echo.
6. Do not close the connection to stop; send `QUIT` and flush.
7. Do not trust `time` alone for ordering (millisecond collisions) — msgid is the key, `time` is
   the cursor fallback.
8. Do not derive "human" from the absence of a tag; derive "unknown", then WHOIS.

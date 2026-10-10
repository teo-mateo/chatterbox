# 5. Work plan — milestones and itemised tasks

Estimates are hours of focused work by one engineer who knows Rust, and the task table is the
authoritative number in this plan. Total **281 h ≈ 35 dev-days**, of which 76 h (27 %) is test and
rig work. Each heading below carries its own task sum; if a heading and its table ever disagree,
the table wins and the heading is a bug to fix in the same PR (`07#R11`). The first edition of this
file claimed 124 h against a task table that summed to 264 h — an adversarial review caught it, and
the reviewer was right.

Rules for the whole plan: a task is done when its named test ids are green and wired into CI (not
"will be tested in M8"); no task closes two FRs without naming both; every PR is ≤ ~500 changed
lines of non-test code so review stays real.

## M0 — Spike the risky assumptions first (19 h)

The point of M0 is to fail fast on the things that would invalidate the plan, not to build
anything. All four spikes throw away their code except the findings.

| ID | Task | Done when | Deps | Est |
|---|---|---|---|---|
| T-0.1 | Scaffold: `Cargo.toml` (edition 2024, MSRV 1.85, `lto`, `strip`), `src/main.rs` with `--version`, `clippy` (`-D warnings`), `rustfmt`, `cargo deny` + advisory scanning wired (**the plan's original choice was `cargo audit` — verify it is still maintained at spike time, else `cargo vet`/`osv-scanner`**), CI skeleton running fmt/clippy/test, plus a macOS **build-only** job (`03#3.12`) | CI green on an empty binary; `cargo build --target x86_64-unknown-linux-musl` links | – | 3 h |
| T-0.2 | **Pin spike (decides the language, `01#D1`).** Working `rustls` `danger()` + custom `ServerCertVerifier` that accepts tiny2's self-signed leaf by SHA-256 and rejects a different one. No chatterbox code involved | `cargo run --example pinprobe -- --host 192.168.0.204 --port 6697 --pin sha256:44:0D:…` prints `PIN OK`; a wrong pin exits non-zero naming both fingerprints; the code is ≤ 100 lines | T-0.1 | 4 h |
| T-0.3 | **Gap-fill cursor spike — the one query `02#2.8` never exercised — plus three smaller probes.** (a) `CHATHISTORY LATEST <#chan> msgid=<id> N` returns only messages after that msgid and paging by msgid terminates; (b) does this server clear a ghost of a same-`nick!user@host` connection faster than `ping_timeout` (R14)?; (c) enumerate the `JOIN` refusals this server actually produces (473/474/471/405/403/690) and whether `FORWARD=f` diverts the join; (d) re-run the `CAP ACK` probe with the `CAP LS` pages fully drained, so the `410` in `02#2.2` is reproduced or exposed as pacing | all four recorded verbatim with raw transcripts in `docs/protocol-notes.md`; **(a) failure ⇒ `03#3.6` adopts the timestamp cursor with a 1000 ms overlap window + msgid-LRU dedup in this same PR — not a "documented limitation"**; **(c) ⇒ `02#2.3` gains a catch-all row**; **(d) contradiction ⇒ `02#2.2` rescoped and `UT-310` weakened**, same PR | T-0.2 | 5 h |
| T-0.4 | Dependency decision: fix the crate list from `03#3.11`, pick `time` vs `chrono` by tree size, commit `deny.toml` with a licence allow-list (MIT/Apache-2.0/ISC/BSD-0-9) and advisories as errors | `cargo deny check` green in CI; non-transitive count ≤ 11 — `dirs` and `percent-encoding` are dropped, XDG paths are resolved by hand so FR6.1's literal paths hold on macOS too (`03#3.11`) | T-0.1 | 2 h |
| T-0.5 | **Fake-server feasibility spike.** `rcgen`-signed TLS listener that completes registration and echoes one labelled PRIVMSG; **measure the wall time and extrapolate to `T-1.0` scope** | an IT test connects over TLS with the real pin-store path exercised; the measured time and the extrapolation are pasted into the PR, and if the extrapolation exceeds 14 h the `T-1.0` scope is cut in that PR, before M1 | T-0.2 | 3 h |
| T-0.6 | Plan reconciliation: any spike result that contradicts `plan/02` or `plan/03` is written back into those files in this PR; the residual-unknown list in `07` is updated | `plan/` diff in the same PR as the spike code; issue #1 gets a comment with the three findings that changed the design | T-0.2..T-0.5 | 2 h |

**Gates M0→M1 — two, both hard:**
1. **T-0.2 green.** If the pin verifier is not clean at ≤ 150 lines, stop and re-decide the language
   (`07#R2`) before writing 4 kLoC.
2. **T-0.3(a) green, or its fallback already implemented.** Acceptance step 6 depends on the
   gap-fill cursor; an unconfirmed cursor is a gate, not a note (`07#R4`).

## M1 — IRC core plus the test rig (51 h)

Everything but `T-1.0` is pure and unit-testable without a socket. `T-1.0` is the rig the rest of
the plan leans on, and it lives here because every IT test in M2-M8 depends on it existing.

| ID | Task | FR | Done when | Deps | Est |
|---|---|---|---|---|---|
| T-1.0 | **`fake_ircd` core, moved here from M8 so every IT from M1 onward has a rig.** `rcgen` TLS; registration; `CAP LS`/`REQ`/`LIST` with a switchable REQ-vs-ACK dialect; `433`/`401`/`473` modes; `MODE +B` accept/refuse switch; labelled `echo-message`; nested `BATCH` (chathistory envelope, multiline children, HistServ shape); `FAIL` codes; a 2048-entry store serving `LATEST`/`BEFORE` by cursor; token-bucket fakelag | `UT-400` walks registration → join → labelled echo → nested replay → `401`-with-label; behaviour switched by env var; the fixture store is shared with `UT-322` | T-0.5 | 12 h |
| T-1.1 | `irc/line.rs`: parse/serialize a wire line (tags, prefix, command, middle, trailing), CRLF framing, partial-line buffering, 512-byte awareness, `…` continuation params | FR1.1 (enabler) | UT-301..304 (incl. the exact lines captured in `02`) | T-0.1 | 5 h |
| T-1.2 | `irc/tags.rs`: tag codec with `\: \r \n \s \\` escapes and hex `\\x##`, empty-value and valueless tags, `+` server-namespace tags preserved on parse | FR2.2 (enabler) | UT-305, UT-306 (round-trip property test) | T-1.1 | 4 h |
| T-1.3 | `irc/cap.rs`: LS paging, REQ list, granted/denied diff, required-set assertion producing `cap_missing` with the FR that breaks without it | FR1.1 | UT-307..309; UT-310 asserts our serializer can never emit `CAP ACK` as a granting step | T-1.1 | 4 h |
| T-1.4 | `irc/isupport.rs`: 005 store, multiple 005 lines, mid-session re-read, typed accessors (`bot_mode()`, `nick_len()`, `chathistory_max()`), `draft/multiline=max-bytes,max-lines` parsing | FR1.2, FR3.4, FR5.1 | UT-311..313 against the captured 005 in `02#2.3` | T-1.1 | 3 h |
| T-1.5 | `irc/batch.rs`: batch tree (stack), `chathistory` envelope, nested `draft/multiline`, coalescing with `draft/multiline-concat`, tolerance of `+id`/`-id`/`:-id` and id rewriting, `+draft/chathistory-end` | FR1.6, FR2.1 | UT-314..317 replay the transcripts in `02#2.6`/`02#2.8` verbatim; UT-318 an unmatched `BATCH -`; UT-319 a batch that never closes (flush after 5 s) | T-1.2 | 7 h |
| T-1.6 | `irc/frame.rs`: outbound framing per `04#4.7` — budget check, word/UTF-8-safe split, `draft/multiline` batching, multi-batch chunking, `--dry-run` report | FR3.4 | UT-144..149, i.e. the measured `417` case (620 B single line) must **split** rather than fail (UT-146), and one 5 000-char token hard-splits at the budget on a UTF-8 boundary (UT-147) | T-1.4 | 6 h |
| T-1.7 | `irc/nicks.rs`: ASCII case mapping, `PREFIX` stripping, addressed-pattern matcher with the three rules of FR2.5, STATUSMSG prefixes | FR2.2, FR2.5 | UT-134..138, incl. `@pi`, `pi:`, `pi,`, `PI:`, `pipe` must **not** match, `@pi` inside a URL host must not match | T-1.4 | 4 h |
| T-1.8 | `ipc/proto.rs`: request/response enums, `v` field, error codes from `04#4.6`, JSON round-trip | FR3, FR3.5 | UT-150..153 (serde round-trip + unknown-field tolerance) | T-0.1 | 3 h |
| T-1.9 | `err.rs`: `Error` ↔ exit code ↔ hint mapping, single place where user-facing text is produced | all | UT-320: every exit code in `04#4.6` is produced by ≥ 1 test somewhere in the suite (`err::ALL_CODES` iterated) | T-1.8 | 3 h |

## M2 — Listener lifecycle (39 h)

| ID | Task | FR | Done when | Deps | Est |
|---|---|---|---|---|---|
| T-2.1 | `conn.rs`: non-blocking TCP + `rustls` + pin verifier integration over the poll loop, read buffer, write buffer, WouldBlock handling both directions | FR1.1 | IT-201 against the fake server: connects, registers, survives 3 PING/PONG cycles | T-0.2, T-1.1 | 8 h |
| T-2.2 | Negotiation sequence from `02#2.2` (LS → REQ → NICK/USER → CAP END → assert) + required-cap gate | FR1.1 | IT-202: fake server that grants nothing → exit 13 naming the caps; grants all → READY | T-1.3, T-2.1 | 4 h |
| T-2.3 | `433` handling and the whole fatal-exit table (`03#3.6`), exit 10, no retry | FR1.4 | UT-107, IT-204 | T-2.2 | 2 h |
| T-2.4 | `MODE <self> +B` from `BOT=` before JOIN; warn-if-absent | FR1.2 | IT-203: fake server asserting the MODE line arrives before JOIN | T-1.4, T-2.2 | 2 h |
| T-2.5 | JOIN, single-channel enforcement, KICK → one re-JOIN → exit 12, inbound events for other channels ignored with stderr note | FR1.3 | UT-106, IT-205 (server sends `KICK`, listener re-JOINs once then exits 12) | T-2.2 | 4 h |
| T-2.6 | Reconnect + backoff + jitter + PING/liveness timers + `--quit-reason`, signal handling with QUIT flush (250 ms) and unlink-on-exit | FR1.5, FR1.6 | UT-108 (backoff sequence is exactly `1,2,4,8,16,32,60,60…` with jitter ∈ ±20 %), E2E-7 | T-2.1 | 6 h |
| T-2.7 | Diagnostics channel: `chatterbox[nick]: …` to stderr, `--verbose` protocol trace with truncated tag values, never to stdout | FR2.6 | UT-139, IT-210 (assert stdout is byte-identical when only diagnostics fire) | T-2.1 | 3 h |
| T-2.8 | Poll loop assembly (`listen/mod.rs`): self-pipe, timer wheel, write-buffer high-water backpressure, stdout-stall policy (I3) | – | IT-229: fake server + a consumer that stops reading → listener keeps answering PINGs for 180 s, then reports dropped wakes | T-2.1, T-2.6 | 6 h |
| T-2.9 | `listen` CLI wiring, `--pid-file`, `status` round-trip | FR1.1 | IT-230: `listen` starts, `status --json` returns `connected=true`, `kill -TERM` → clean exit 0, socket gone | T-2.6, T-4.1 | 4 h |

## M3 — Inbound pipeline and output (47 h)

| ID | Task | FR | Done when | Deps | Est |
|---|---|---|---|---|---|
| T-3.1 | `Message` model (fields = `04#4.4`) and the pipeline seam: parse → coalesce → suppress → filter → format | FR2.1..2.4 | UT-125 (model round-trip) | T-1.5 | 3 h |
| T-3.2 | Suppression list: services (`HistServ` + configurable), non-PRIVMSG commands, unconsumed numerics, self | FR2.4 | UT-129..133 replaying the `02#2.8` transcript and asserting stdout would be empty; IT-209 | T-3.1 | 4 h |
| T-3.3 | bot/human resolution: `bot` tag → cache → WHOIS queue (`335`), `kind_source` recorded, `?` when unknown | FR2.2, FR4.1 | UT-124, UT-126, UT-127; IT-217 | T-3.1 | 5 h |
| T-3.4 | Wake filter + `[important] ` detection (body marker authoritative, tag advisory) + `--max-wakes` coalescing (`07#R7`) | FR2.5, FR3.3 | UT-134..139, UT-143 | T-1.7, T-3.1 | 5 h |
| T-3.5 | Plain formatter: six-field layout of `04#4.3`, escaping, TTY colour, `--time local` | FR2.1, FR2.2 | UT-120..123 golden-line tests, snapshot of the four example lines in `04#4.3` | T-3.1 | 4 h |
| T-3.6 | JSON formatter + `--state` records + schema version | FR2.3 | UT-128, IT-208 (NDJSON parsed with `serde_json::from_str` line by line) | T-3.5 | 3 h |
| T-3.7 | `gapfill.rs`: cursor advanced **only on a successful stdout write**, cursor persisted in the instance file, gap fill on restart, single hold queue covering live traffic during replay (`03#3.6`), msgid paging with a 1000 ms timestamp fallback, `seen` LRU 4096, retention-truncation WARN, `iterations_max = ceil(retention/page)+1`, `catchup complete` accounting | FR1.6 | UT-110..117 (out-of-order replay, duplicate replay, replay overlapping live traffic, dropped-wake cursor, restart gap fill, 2048-message retention cut, empty reply) | T-1.5, T-2.6 | 10 h |
| T-3.8 | `presence.rs`: NAMES snapshot (`userhost-in-names`, PREFIX strip), bot cache with TTL **primed from the chathistory replay on first join so `whos-there` is not mostly `?`**, WHOIS queue limited to 1 concurrent under `TARGMAX=WHOIS:1`, `since` timestamps, `kind_pending` in JSON | FR4.1, FR2.2 | UT-160..162, IT-217 | T-3.3 | 6 h |
| T-3.9 | `LIST` client for `channels` + `CHATHISTORY LATEST … * 1` activity + `-`/`unavailable` semantics | FR4.2 | UT-163, IT-218 | T-3.8 | 4 h |
| T-3.10 | Wire stdout/stderr through the pipeline in the listener; end-to-end "line in → line out" test | FR2.1, FR2.6 | IT-207 (multiline batch in → exactly one stdout line out), IT-210 | T-3.5..T-3.7 | 3 h |

## M4 — Outbound sends (34 h)

| ID | Task | FR | Done when | Deps | Est |
|---|---|---|---|---|---|
| T-4.1 | `ipc.rs`: control socket (0600, `SO_PEERCRED` uid check) and instance file, **created only after registration reaches READY, unlinked only when pid and socket inode match ours**, runtime-dir fallback validated (`st_uid == euid`, mode `0700`, not a symlink) | FR3.5, FR5.2, FR5.3, FR6.3 | UT-176, UT-177, UT-190..194, IT-224, IT-233 | T-2.8 | 5 h |
| T-4.2 | Instance registry: write/unlink/staleness probe, `doctor --repair` | FR5.2, FR3.6 | UT-175..177, IT-216 | T-4.1 | 4 h |
| T-4.3 | Send state machine + label allocator + `publish`/`send` + ack/timeout/reject + **cancel on client disconnect** (`03#3.4`, `07#R15`) | FR3.1, FR3.2, FR3.5 | UT-150..153, UT-157, IT-211, IT-212, IT-215, IT-232 | T-1.6, T-2.8, T-4.1 | 8 h |
| T-4.4 | Important marking on the send side (`[important] ` prefix + advisory tag + one-time stderr note); `create-channel` move semantics | FR3.3, FR4.3 | UT-143, UT-164, IT-213, IT-219 | T-4.3 | 3 h |
| T-4.5 | Multi-chunk sends (FR3.4), `--dry-run`, queue capacity + `queue_full`, serialisation with one in-flight batch | FR3.4, FR3.5 | UT-144..149 extended to chunking, IT-214 (8 KB message → 3 batches, all acked in order) | T-4.3 | 5 h |
| T-4.6 | Send-command CLI front ends: arg/stdin handling, `--json` error object, exit code mapping, hint lines | FR3.1..3.6 | IT-211..216 via `assert_cmd`; `--dry-run` golden output | T-4.3 | 4 h |
| T-4.7 | `send` with no listener → exit 3 with the literal `listen` command, including a quoted channel name | FR3.6 | UT-154..156, IT-216 | T-4.2 | 2 h |
| T-4.8 | Guard: send commands must not open TCP. Implemented as a `#[cfg(test)]` socket-creation interlock + a test that runs `publish --dry-run`/real publish under it | FR3.1 | IT-231 | T-4.3 | 3 h |

## M5 — Identity, presence commands, read-only surface (24 h)

| ID | Task | FR | Done when | Deps | Est |
|---|---|---|---|---|---|
| T-5.1 | Nick resolution (`--as` → env → config for `listen`; `--as` → env → auto-detect for commands), charset/`NICKLEN` validation, **RFC1459 case-folded keys for lookup, instance files and socket names** (`07#R17`) | FR5.1, FR5.2 | UT-170..174, IT-220 | T-4.2 | 5 h |
| T-5.2 | Ambiguity/absence paths with the exact hint strings | FR3.6, FR5.2 | UT-155, IT-216 | T-5.1 | 2 h |
| T-5.3 | `whos-there` (names + bot cache + WHOIS on demand + `--wait`) | FR4.1 | IT-217, E2E-2 | T-3.8 | 4 h |
| T-5.4 | `channels` with activity/unavailable semantics | FR4.2 | IT-218, E2E-9 | T-3.9 | 3 h |
| T-5.5 | `create-channel` with move semantics, `--topic`, `--keep` rejection | FR4.3 | IT-219, E2E-10 | T-4.4 | 3 h |
| T-5.6 | `nick_mismatch` enforcement + same-uid documentation in `--help` and the skill | FR5.3 | UT-178, IT-221 | T-4.1 | 2 h |
| T-5.7 | `status`/`doctor` (all preconditions of `03#3.10`), `--repair` | FR2.6, ops | IT-225, E2E-13 | T-5.1, T-4.2 | 5 h |

## M6 — Configuration and TLS trust (18 h)

| ID | Task | FR | Done when | Deps | Est |
|---|---|---|---|---|---|
| T-6.1 | `config.rs`: schema, precedence, env vars, clamping, unknown-key rejection, `init` template | FR6.1 | UT-180..183, IT-222 | T-1.9 | 5 h |
| T-6.2 | `pin.rs`: store read/write (atomic, 0600), `trust --list/--forget`, mismatch report with both fingerprints | FR6.2 | UT-184..188, IT-223 | T-0.2 | 5 h |
| T-6.3 | TOFU path gated on `--tofu` + TTY; exit 14 with the observed fingerprint otherwise | FR6.2 | UT-189, IT-223b, E2E-11 | T-6.2 | 3 h |
| T-6.4 | `sni` handling for IP-address servers, TLS 1.2 minimum, ALPN off | FR6.2 | UT-186 | T-6.2 | 2 h |
| T-6.5 | Config-driven limits wired into `frame.rs`/queue/timers (no magic numbers left in code paths that `02#2.13` covers) | FR3.4, FR6.1 | IT-226: config with `line_bytes = 300` changes the `--dry-run` chunking | T-6.1, T-4.5 | 3 h |

## M7 — Onboarding block and docs (13 h)

| ID | Task | FR | Done when | Deps | Est |
|---|---|---|---|---|---|
| T-7.1 | `skill/SKILL.md` from `plan/08` (≤ 4 KB, `include_str!`) plus `skill --format human` (≤ 15 lines for the person steering from a normal client, `07#R18`) | FR8.1, FR7.3 | UT-195 (size + required sections), UT-197 (every exit code present), UT-198 (every example parses), IT-227 | T-4.6, T-5.7 | 5 h |
| T-7.2 | `chatterbox skill` + `--format plain`, README with the two-machine quickstart and the human's view | FR7.3, FR8.1 | IT-227, E2E-12 | T-7.1 | 3 h |
| T-7.3 | `docs/protocol-notes.md` final, `docs/design.md` linking to `plan/` (the plan becomes the design doc; the plan stays as the historical record) | – | links resolve | T-0.6 | 2 h |
| T-7.4 | Ergo-agnosticism pass: grep for HofNet/tiny2/IPs in code paths (allowed only in tests/docs defaults), and make sure a stock Ergo/UnrealIRCd server yields `cap_missing` rather than a panic | FR6.1 | UT-196: fake server without `draft/chathistory` → exit 13 with a readable message | T-2.2, T-6.1 | 3 h |

## M8 — Acceptance, CI, packaging, close-out (36 h)

| ID | Task | Done when | Deps | Est |
|---|---|---|---|---|
| T-8.1 | Extend `fake_ircd` (built in `T-1.0`) to full acceptance coverage: multi-channel stores, mid-session `CAP DEL`, `KILL`/`ERROR`, ghost-on-reconnect, scripted partitions (R14) | all IT-* green deterministically 20× in a row | T-2.*, T-3.*, T-4.* | 4 h |
| T-8.2 | `tests/live_ergo.rs` gated on `CHATTERBOX_LIVE=1`: two listeners, cross-send, gap fill, presence; unique `cblive<runid>` nicks and `#cbtest<runid>` channel; teardown always PART+QUIT even on failure | runs green twice in a row against tiny2; `06#6.5` checklist ticked | T-8.1 | 6 h |
| T-8.3 | `tests/e2e/acceptance.sh` + `proxy.py`: E2E-1..7 from issue #1, unattended, with the killable TLS relay for step 6 | script exits 0; every step prints `PASS <id>` | T-8.2 | 8 h |
| T-8.4 | E2E-8..13 (kick, channels, create-channel, pin mismatch, fresh-agent round trip, doctor) | green | T-8.3, T-5.7 | 5 h |
| T-8.5 | CI completion: fmt, clippy `-D warnings`, test, deny/advisories, musl release build, macOS build, `--version` smoke, and a **self-hosted LAN runner** for the live job (the target is RFC1918; hosted runners cannot reach it). Nightly probe green is a **release gate**, not an advisory | CI green on a throwaway PR; the release workflow refuses to tag while the probe is red | T-8.1 | 5 h |
| T-8.6 | Release: tag `v0.1.0`, 4 tarballs + checksums, release notes listing the FRs met and the limitations from `07` | `chatterbox --version` works after a tarball install on a clean container | T-8.5 | 3 h |
| T-8.7 | Close-out: FR traceability table in `01` re-verified row by row, residual unknowns in `07` closed or downgraded, issue #1 commented with the acceptance transcript | table has no empty cells | T-8.3..T-8.6 | 3 h |
| T-8.8 | Post-v1 backlog written to issue(s): registration-backed channels, `no-implicit-names`, cap-notify, tag-aware importance when servers preserve tags, Windows named pipes | ≥ 4 issues opened with bodies | T-8.7 | 2 h |

## Sequencing

```
M0 ─▶ M1 ─▶ M2 ─▶ M4 ─▶ M5 ─▶ M6 ─▶ M7 ─▶ M8
         └──▶ M3 ─┘
```

M3 (inbound) and M4 (outbound) are independent after M2 and are the natural split for two
programmers. M5 needs M3 for presence and M4 for the socket. M8 cannot start early: its value is
that it runs against the finished thing.

Critical path: **T-0.2 → T-2.1 → T-2.8 → T-4.3 → T-8.1 → T-8.3**. If any of those slips, the
milestone slips; everything else can be reordered around it.

## PR slicing (each ≤ ~500 non-test lines, each independently green)

| PR | Contents |
|---|---|
| 1 | M0 scaffold + spikes as `examples/` + findings written back into `plan/` |
| 2 | T-1.1, T-1.2, T-1.4 (codec, tags, ISUPPORT) + UT-1xx |
| 3 | T-1.5, T-1.7, T-1.9 (batch tree, nick matching, errors) |
| 4 | T-1.3, T-1.6, T-1.8 (caps, framing, IPC types) |
| 5 | T-2.1..T-2.5 (connect, negotiate, register, +B, single channel) |
| 6 | T-2.6..T-2.8 (reconnect, timers, diagnostics, poll loop) |
| 7 | T-3.1..T-3.6 (inbound pipeline, formatters) |
| 8 | T-3.7, T-3.8, T-3.9 (gap fill, presence, LIST) |
| 9 | T-4.1..T-4.4 (socket, registry, send machine, importance) |
| 10 | T-4.5..T-4.8, T-5.1..T-5.2 (chunks, CLI fronts, identity) |
| 11 | T-5.3..T-5.7 (whos-there, channels, create-channel, status, doctor) |
| 12 | T-6.1..T-6.5 (config, pins) |
| 13 | T-7.1..T-7.4 (skill, docs, portability pass) |
| 14 | T-8.1..T-8.8 (test rig completion, e2e, CI, release) |

## Stop conditions — escalate instead of improvising

1. T-0.2 (pin spike) fails: re-decide the language, do not hack around `rustls`.
2. T-0.3 shows msgid cursors unsupported: switch `03#3.6` to timestamp cursors, update `plan/02`
   and `plan/03` in the same PR, and add a test for the millisecond-collision case that the msgid
   dedup was protecting against.
3. The required-cap set cannot be granted by some server we must support: that is a
   requirements conflict with issue #1, not an implementation problem — ask before degrading FR1.6
   or FR3.4 silently.
4. Any need appears for a second channel, plaintext IRC, or authentication: those are explicit
   non-goals (`01#1.4`); they need a new issue, not a flag.

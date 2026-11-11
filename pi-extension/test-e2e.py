#!/usr/bin/env python3
"""End-to-end check of the Pi extension against a private Ergo.

    make ergo-up                       # the local server on 127.0.0.1:16697
    pi-extension/test-e2e.py [case...] # needs pi with a working default model
                                       # (or PI_E2E_ARGS="--provider x --model y")
    make ergo-down

Runs a real Pi agent in RPC mode with the extension loaded, and a second
chatterbox client ("peer") that talks to it. Nothing here prompts the agent to
look at the channel: every reply has to come from the extension delivering the
message by itself. A model that declines an instruction makes a case
INCONCLUSIVE, not failed; the delivery checks do not depend on what it answers.
"""

import json
import os
import queue
import shutil
import subprocess
import sys
import tempfile
import threading
import time

CHANNEL = "#ext-e2e"
AGENT = "piext"
PEER = "peer"


def log(*a):
    print(*a, flush=True)


class Lines:
    """Collects the lines of a stream on a thread."""

    def __init__(self, stream, parse=False):
        self.items = []
        self.cond = threading.Condition()
        self.parse = parse
        threading.Thread(target=self._read, args=(stream,), daemon=True).start()

    def _read(self, stream):
        for raw in stream:
            line = raw.rstrip("\n")
            item = line
            if self.parse:
                try:
                    item = json.loads(line)
                except ValueError:
                    continue
            with self.cond:
                self.items.append(item)
                self.cond.notify_all()

    def wait(self, pred, timeout, start=0):
        """Returns (index, item) of the first item from `start` matching pred."""
        deadline = time.time() + timeout
        with self.cond:
            i = start
            while True:
                while i < len(self.items):
                    if pred(self.items[i]):
                        return i, self.items[i]
                    i += 1
                left = deadline - time.time()
                if left <= 0:
                    return None, None
                self.cond.wait(left)

    def mark(self):
        with self.cond:
            return len(self.items)


class Rig:
    def __init__(self):
        self.dir = tempfile.mkdtemp(prefix="cbxe2e")
        os.chmod(self.dir, 0o700)
        cfg = os.path.join(self.dir, "cfg", "chatterbox")
        os.makedirs(cfg)
        with open(os.path.join(cfg, "config.toml"), "w") as f:
            f.write('[server]\nhost = "127.0.0.1"\nport = 16697\n[defaults]\nchannel = "%s"\n' % CHANNEL)
        self.env = dict(os.environ)
        self.env["XDG_CONFIG_HOME"] = os.path.join(self.dir, "cfg")
        self.env["CHATTERBOX_RUNTIME_DIR"] = os.path.join(self.dir, "rt")
        self.env.pop("CHATTERBOX_NICK", None)
        self.peer = None
        self.pi = None

    def cb(self, *args, stdin=None):
        return subprocess.run(["chatterbox", *args], env=self.env, input=stdin, capture_output=True, text=True)

    def start_peer(self):
        self.peer = subprocess.Popen(
            ["chatterbox", "listen", "--as", PEER, "--channel", CHANNEL, "--json"],
            env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.peer_out = Lines(self.peer.stdout, parse=True)
        self.peer_err = Lines(self.peer.stderr)
        i, _ = self.peer_err.wait(lambda l: "joined" in l, 20)
        if i is None:
            raise SystemExit("the peer could not join; is the local Ergo up? (make ergo-up)\n" + "\n".join(self.peer_err.items))

    def start_pi(self):
        self.pi = subprocess.Popen(
            ["pi", "--mode", "rpc", "--no-session", "--thinking", "low"] + os.environ.get("PI_E2E_ARGS", "").split(),
            env=self.env, cwd=self.dir, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=open(os.path.join(self.dir, "pi.err"), "w"), text=True)
        self.events = Lines(self.pi.stdout, parse=True)

    def prompt(self, text):
        mark = self.events.mark()
        self.pi.stdin.write(json.dumps({"type": "prompt", "message": text}) + "\n")
        self.pi.stdin.flush()
        return mark

    def settled(self, start, timeout=240):
        i, _ = self.events.wait(lambda e: e.get("type") == "agent_settled", timeout, start)
        return i

    def peer_gets(self, pred, timeout, start=0):
        return self.peer_out.wait(lambda m: m.get("from") == AGENT and pred(m), timeout, start)

    def prompts_since(self, start):
        """User messages that entered the agent since `start` (there must be none we did not send)."""
        with self.events.cond:
            return [e for e in self.events.items[start:]
                    if e.get("type") == "message_start" and e.get("message", {}).get("role") == "user"]

    def deliveries_since(self, start):
        """Channel batches handed to the model: a new turn for an idle agent
        (message_end), or an entry appended at a turn boundary of a running one."""
        with self.events.cond:
            return [d for d in map(delivery, self.events.items[start:]) if d]

    def stop(self):
        for p in (self.pi, self.peer):
            if p and p.poll() is None:
                p.terminate()
                try:
                    p.wait(10)
                except subprocess.TimeoutExpired:
                    p.kill()
        shutil.rmtree(self.dir, ignore_errors=True)


def delivery(event):
    if event.get("type") == "message_end" and event.get("message", {}).get("customType") == "chatterbox":
        return event["message"]
    entry = event.get("entry", {})
    if event.get("type") == "entry_appended" and entry.get("type") == "custom_message" and entry.get("customType") == "chatterbox":
        return entry
    return None


def text_of(message):
    c = message.get("content")
    if isinstance(c, str):
        return c
    return "".join(p.get("text", "") for p in c or [] if isinstance(p, dict))


RESULTS = []


def result(name, verdict, note=""):
    RESULTS.append((name, verdict, note))
    log("  %-12s %s%s" % (verdict, name, (" — " + note) if note else ""))


def case_join(rig):
    mark = rig.prompt("Call the chatterbox_join tool with nick '%s' and channel '%s'. Then answer with the single word: joined." % (AGENT, CHANNEL))
    if rig.settled(mark) is None:
        return result("join", "FAIL", "the run did not settle")
    who = rig.cb("whos-there", "--as", PEER).stdout
    if AGENT + " bot" in who:
        result("join", "PASS", "present and marked as a bot")
    else:
        result("join", "INCONCLUSIVE" if not rig.deliveries_since(0) else "FAIL", "agent not in the channel: %r" % who)
    return AGENT + " bot" in who


def case_idle_wake(rig):
    """A1/B1/B2: an idle agent is woken, and gets the whole text."""
    pm, em = rig.peer_out.mark(), rig.events.mark()
    filler = "This sentence is only here to push the question far beyond the first hundred and sixty characters of the line. " * 3
    rig.cb("send", "--as", PEER, AGENT, filler + "Now the question: reply to me in the channel with exactly the word KIWI-41.")
    i, e = rig.events.wait(lambda e: delivery(e) is not None, 20, em)
    if i is None:
        return result("idle-wake", "FAIL", "no delivery reached the agent")
    delivered = text_of(delivery(e))
    if "KIWI-41" not in delivered:
        return result("idle-wake", "FAIL", "the delivered text is cut off: %r" % delivered[-120:])
    j, m = rig.peer_gets(lambda m: "KIWI-41" in m["text"], 180, pm)
    rig.settled(em)
    if j is None:
        return result("idle-wake", "INCONCLUSIVE", "delivered in full, but the model did not answer with the word")
    extra = rig.prompts_since(em)
    note = "woken without a prompt; reply %r" % m["text"][:60]
    if m["text"].lower().startswith(PEER + ": " + PEER):
        return result("idle-wake", "FAIL", "doubled address prefix: %r" % m["text"][:40])
    result("idle-wake", "PASS" if not extra else "FAIL", note)


def case_final_message_gap(rig):
    """B4: a message that lands while the final answer is being written."""
    pm = rig.peer_out.mark()
    em = rig.prompt("Without using any tool, write about 250 words on why rivers meander.")
    # Wait until the final assistant message is streaming, then post.
    i, _ = rig.events.wait(lambda e: e.get("type") == "message_update" and e.get("assistantMessageEvent", {}).get("type") == "text_delta", 120, em)
    if i is None:
        return result("final-gap", "INCONCLUSIVE", "no streamed text seen")
    rig.cb("send", "--as", PEER, AGENT, "while you write: reply to me in the channel with exactly the word MANGO-77.")
    sent_at = time.time()
    j, m = rig.peer_gets(lambda m: "MANGO-77" in m["text"], 240, pm)
    waited = time.time() - sent_at
    deliveries = rig.deliveries_since(em)
    got = any("MANGO-77" in text_of(d) for d in deliveries)
    rig.settled(em)
    if not got:
        return result("final-gap", "FAIL", "the message never reached the agent")
    if len(rig.prompts_since(em)) > 1:
        return result("final-gap", "FAIL", "an extra prompt was involved")
    if j is None:
        return result("final-gap", "INCONCLUSIVE", "delivered in the same run, but the model did not answer")
    result("final-gap", "PASS", "delivered and answered %.0f s after posting, no prompt" % waited)


def case_mid_run(rig):
    """B3: a message that lands while a tool is running is seen right after it."""
    pm = rig.peer_out.mark()
    em = rig.prompt("Run the bash command `sleep 6 && echo slept`. After it finishes, answer with the single word: done.")
    i, _ = rig.events.wait(lambda e: e.get("type") == "tool_execution_start", 120, em)
    if i is None:
        return result("mid-run", "INCONCLUSIVE", "the model did not run the command")
    time.sleep(1)
    rig.cb("send", "--as", PEER, AGENT, "reply to me in the channel with exactly the word PLUM-13.")
    j, m = rig.peer_gets(lambda m: "PLUM-13" in m["text"], 240, pm)
    rig.settled(em)
    deliveries = [d for d in rig.deliveries_since(em) if "PLUM-13" in text_of(d)]
    if len(deliveries) != 1:
        return result("mid-run", "FAIL", "delivered %d times" % len(deliveries))
    if j is None:
        return result("mid-run", "INCONCLUSIVE", "delivered once, but the model did not answer")
    result("mid-run", "PASS", "delivered once inside the run, answered")


def case_burst(rig):
    """B6/N2: a burst arrives complete, in order, in few deliveries."""
    em = rig.events.mark()
    n = 12
    for k in range(n):
        rig.cb("publish", "--as", PEER, "burst line %02d (no reply needed)" % k)
    time.sleep(4)
    rig.settled(em, 240)
    time.sleep(1)
    texts = [text_of(d) for d in rig.deliveries_since(em)]
    joined = "\n".join(texts)
    seen = [k for k in range(n) if ("burst line %02d" % k) in joined]
    order = [joined.index("burst line %02d" % k) for k in seen]
    dup = any(joined.count("burst line %02d" % k) != 1 for k in seen)
    if len(seen) != n or order != sorted(order) or dup:
        return result("burst", "FAIL", "got %d of %d, in order: %s, duplicates: %s" % (len(seen), n, order == sorted(order), dup))
    result("burst", "PASS", "%d messages in %d deliveries, in order, once each" % (n, len(texts)))


def case_private(rig):
    """C1/C2: private means private; no doubled prefix."""
    pm = rig.peer_out.mark()
    em = rig.prompt("Using chatterbox, send the participant '%s' a private message with the text: sardines" % PEER)
    j, m = rig.peer_gets(lambda m: "sardines" in m["text"], 240, pm)
    rig.settled(em)
    if j is None:
        return result("private", "INCONCLUSIVE", "the model sent nothing")
    if not m["private"]:
        return result("private", "FAIL", "it arrived publicly: %r" % m)
    result("private", "PASS", "arrived as a private message: %r" % m["text"])


def case_guard(rig):
    """C5: a send is held back when an unseen message is waiting."""
    pm = rig.peer_out.mark()
    long_text = " ".join("word%02d" % k for k in range(120))
    em = rig.prompt("Using chatterbox_send with audience channel, post exactly this text and nothing else: " + long_text)
    # The model is now writing a long tool call. Post while it does.
    i, _ = rig.events.wait(lambda e: e.get("type") == "message_update", 120, em)
    time.sleep(0.5)
    rig.cb("publish", "--as", PEER, "STOP-GUARD: this arrived while you were composing")
    rig.settled(em, 300)
    with rig.events.cond:
        ends = [e for e in rig.events.items[em:] if e.get("type") == "tool_execution_end" and e.get("toolName") == "chatterbox_send"]
    held = [e for e in ends if "NOT SENT" in json.dumps(e.get("result", ""))]
    if not ends:
        return result("guard", "INCONCLUSIVE", "the model did not call chatterbox_send")
    if not held:
        return result("guard", "INCONCLUSIVE", "the message did not land between the boundary and the send (timing)")
    if "STOP-GUARD" not in json.dumps(held[0].get("result", "")):
        return result("guard", "FAIL", "held back, but the unseen message was not returned")
    result("guard", "PASS", "first send held back with the unseen message; %d send call(s) in all" % len(ends))


def case_leave_on_exit(rig):
    """A5: the nickname leaves when the Pi session ends."""
    rig.pi.stdin.close()
    try:
        rig.pi.wait(20)
    except subprocess.TimeoutExpired:
        rig.pi.terminate()
        rig.pi.wait(10)
    deadline = time.time() + 10
    who = ""
    while time.time() < deadline:
        who = rig.cb("whos-there", "--as", PEER).stdout
        if AGENT not in who:
            return result("leave-on-exit", "PASS", "gone from the channel after Pi exited")
        time.sleep(0.5)
    result("leave-on-exit", "FAIL", "still present: %r" % who)


def sends_since(rig, start):
    with rig.events.cond:
        return [e for e in rig.events.items[start:]
                if e.get("type") == "tool_execution_end" and e.get("toolName") == "chatterbox_send"]


def agent_posts(rig, start):
    with rig.peer_out.cond:
        return [m for m in rig.peer_out.items[start:] if m.get("from") == AGENT]


def case_autopost(rig):
    """A final answer the agent did not post itself is posted for it."""
    pm, em = rig.peer_out.mark(), rig.events.mark()
    rig.cb("send", "--as", PEER, AGENT,
           "Do not call any tool. Make your final answer exactly the word PLUM-12 and nothing else.")
    j, m = rig.peer_gets(lambda m: "PLUM-12" in m["text"], 180, pm)
    rig.settled(em)
    if sends_since(rig, em):
        return result("autopost", "INCONCLUSIVE", "the model used chatterbox_send itself")
    if j is None:
        return result("autopost", "FAIL", "the final answer never reached the channel")
    result("autopost", "PASS", "posted %r" % m["text"][:60])


def case_no_double_post(rig):
    """An agent that posted after the latest channel message gets no auto-post on top."""
    pm, em = rig.peer_out.mark(), rig.events.mark()
    rig.cb("send", "--as", PEER, AGENT,
           "Use chatterbox_send to reply to me with exactly the word PEAR-58, then finish with a one-line final answer.")
    j, _ = rig.peer_gets(lambda m: "PEAR-58" in m["text"], 180, pm)
    rig.settled(em)
    time.sleep(5)
    if j is None or not sends_since(rig, em):
        return result("no-double-post", "INCONCLUSIVE", "the model did not send the word itself")
    posts = agent_posts(rig, pm)
    if len(posts) != len(sends_since(rig, em)):
        return result("no-double-post", "FAIL", "%d posts for %d sends: %r" % (
            len(posts), len(sends_since(rig, em)), [p["text"][:40] for p in posts]))
    result("no-double-post", "PASS", "only the agent's own post")


def case_no_reply(rig):
    """A final answer of exactly NO_REPLY posts nothing."""
    pm, em = rig.peer_out.mark(), rig.events.mark()
    rig.cb("send", "--as", PEER, AGENT,
           "Nothing to answer here. Do not call any tool; make your final answer exactly NO_REPLY.")
    rig.settled(em)
    time.sleep(5)
    if sends_since(rig, em):
        return result("no-reply", "INCONCLUSIVE", "the model used chatterbox_send itself")
    posts = agent_posts(rig, pm)
    if posts:
        return result("no-reply", "FAIL", "posted %r" % posts[0]["text"][:60])
    result("no-reply", "PASS", "nothing posted")


CASES = {
    "idle-wake": case_idle_wake,
    "final-gap": case_final_message_gap,
    "mid-run": case_mid_run,
    "burst": case_burst,
    "private": case_private,
    "guard": case_guard,
    "autopost": case_autopost,
    "no-double-post": case_no_double_post,
    "no-reply": case_no_reply,
}


def main():
    wanted = sys.argv[1:] or list(CASES)
    rig = Rig()
    try:
        rig.start_peer()
        rig.start_pi()
        log("pi extension end-to-end (%s on 127.0.0.1:16697)" % CHANNEL)
        if case_join(rig):
            for name in wanted:
                CASES[name](rig)
        case_leave_on_exit(rig)
        errors = [e for e in rig.events.items if e.get("type") == "extension_error"]
        for e in errors:
            log("  extension error:", json.dumps(e)[:300])
        if errors:
            result("no-extension-errors", "FAIL", "%d reported" % len(errors))
    finally:
        rig.stop()
    failed = [r for r in RESULTS if r[1] == "FAIL"]
    log("\n%d passed, %d inconclusive, %d failed" % (
        sum(r[1] == "PASS" for r in RESULTS), sum(r[1] == "INCONCLUSIVE" for r in RESULTS), len(failed)))
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()

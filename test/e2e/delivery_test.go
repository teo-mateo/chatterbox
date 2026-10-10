package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// IT-RECV: one line from a human is exactly one stdout line with sender,
// kind, channel and time.
func TestReceive(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "recvA", channel: ch})
	p := dialPlain(t, shared.Addr(), "recvHuman")
	p.join(ch)
	before := time.Now().UTC().Add(-2 * time.Second)
	p.say(ch, "hello | there \\ friend")
	line, _ := a.expectLine(0, "hello")
	a.expectQuiet(300 * time.Millisecond)
	if a.out.Len() != 1 {
		t.Fatalf("stdout has %d lines, want exactly 1:\n%s", a.out.Len(), a.out.String())
	}
	m := parseText(t, line)
	if m.from != "recvHuman" || m.kind != "human" || m.channel != ch || m.flags != "-" || m.text != `hello | there \\ friend` {
		t.Errorf("%+v", m)
	}
	ts, err := time.Parse("2006-01-02T15:04:05Z", m.time)
	if err != nil || ts.Before(before) || ts.After(time.Now().UTC().Add(2*time.Second)) {
		t.Errorf("time %q (%v)", m.time, err)
	}

	// A private message and a /me.
	p.say("recvA", "just for you")
	line, _ = a.expectLine(1, "just for you")
	if m := parseText(t, line); m.channel != "private" || !m.has("to-you") {
		t.Errorf("private message: %+v", m)
	}
	p.say(ch, "\x01ACTION waves\x01")
	line, _ = a.expectLine(2, "waves")
	if m := parseText(t, line); m.text != "* recvHuman waves" {
		t.Errorf("action: %+v", m)
	}
	// Formatting codes are stripped; other CTCP is dropped and unanswered.
	p.say(ch, "\x01VERSION\x01")
	p.say(ch, "\x02bold\x02 and \x0304red\x03")
	line, _ = a.expectLine(3, "bold")
	if m := parseText(t, line); m.text != "bold and red" {
		t.Errorf("formatting: %+v", m)
	}
	if a.out.Len() != 4 {
		t.Errorf("CTCP VERSION was delivered:\n%s", a.out.String())
	}
}

// IT-RECV-ML: a multiline message from another listener is one stdout line,
// in text and in JSON.
func TestReceiveMultiline(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "mlA", channel: ch})
	j := startListener(t, lopts{nick: "mlJ", channel: ch, json: true})
	b := startListener(t, lopts{nick: "mlB", channel: ch})
	text := "line one\nline two\n\n    indented four\nline five"
	b.mustAs("publish", text)

	line, _ := a.expectLine(0, "line one")
	a.expectQuiet(300 * time.Millisecond)
	if a.out.Len() != 1 {
		t.Fatalf("text listener printed %d lines:\n%s", a.out.Len(), a.out.String())
	}
	m := parseText(t, line)
	if m.text != `line one\nline two\n\n    indented four\nline five` || m.kind != "bot" || m.from != "mlB" {
		t.Errorf("%+v", m)
	}

	jl, _ := j.expectLine(0, "line one")
	if j.out.Len() != 1 {
		t.Fatalf("json listener printed %d lines", j.out.Len())
	}
	var obj struct {
		Time, Channel, From, Kind, MsgID, Text  string
		Addressed, Important, Private, Replayed bool
	}
	if err := json.Unmarshal([]byte(jl), &obj); err != nil {
		t.Fatalf("not JSON: %v: %s", err, jl)
	}
	if obj.Text != text || obj.From != "mlB" || obj.Kind != "bot" || obj.Channel != ch || obj.MsgID == "" || obj.Addressed || obj.Private || obj.Replayed {
		t.Errorf("%+v", obj)
	}
	if !strings.HasPrefix(jl, `{"time":"`) {
		t.Errorf("key order: %s", jl)
	}
}

// IT-FILTER: the wake filter under --wake addressed.
func TestWakeFilter(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "filtA", channel: ch, wake: "addressed"})
	all := startListener(t, lopts{nick: "filtAll", channel: ch})
	p := dialPlain(t, shared.Addr(), "filtHuman")
	p.join(ch)

	silent := []string{
		"nobody in particular",
		"filtA finished the migration",   // a bare mention is not an address
		"filtAll: [IMPORTANT] not for A", // important, but addressed to another agent (D7)
		"@filtA-2 other agent",
	}
	printed := []string{
		"filtA: by prefix",
		"hey @filtA by mention",
		"filtAll, filtA: both of you",
		"[IMPORTANT] everyone stop", // unaddressed important, typed by a human
	}
	for _, s := range silent {
		p.say(ch, s)
	}
	for _, s := range printed {
		p.say(ch, s)
	}
	p.say(ch, "filtA: the end")
	a.expectLine(0, "the end")
	got := a.out.Lines()
	if len(got) != len(printed)+1 {
		t.Fatalf("addressed listener printed %d lines, want %d:\n%s", len(got), len(printed)+1, a.out.String())
	}
	for i, want := range []string{"filtA: by prefix", "hey @filtA by mention", "filtAll, filtA: both of you", "everyone stop"} {
		m := parseText(t, got[i])
		if m.text != want {
			t.Errorf("line %d text %q, want %q", i, m.text, want)
		}
		if i < 3 && (!m.has("to-you") || m.has("important")) {
			t.Errorf("line %d flags %q", i, m.flags)
		}
		if i == 3 && (m.has("to-you") || !m.has("important")) {
			t.Errorf("line %d flags %q", i, m.flags)
		}
	}
	// The unfiltered listener sees all nine.
	all.expectLine(0, "the end")
	if n := all.out.Len(); n != len(silent)+len(printed)+1 {
		t.Errorf("--wake all printed %d lines, want %d", n, len(silent)+len(printed)+1)
	}
}

// IT-STDOUT: connecting, joining, others coming and going, nick and topic
// changes and the listener's own sends put nothing on stdout.
func TestStdoutStaysClean(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "cleanA", channel: ch})
	p := dialPlain(t, shared.Addr(), "cleanHuman")
	p.join(ch)
	p.raw("TOPIC " + ch + " :a new topic") // refused (+t), still noise
	p.raw("NICK cleanHuman2")
	p.expect(" NICK ")
	p.raw("NOTICE " + ch + " :")
	q := dialPlain(t, shared.Addr(), "cleanOper")
	q.oper()
	q.join(ch)
	q.raw("SAMODE " + ch + " +o cleanOper")
	q.raw("TOPIC " + ch + " :set by an operator")
	q.raw("MODE " + ch + " +m")
	q.raw("MODE " + ch + " -m")
	q.raw("PART " + ch + " :bye")
	q.raw("QUIT :gone")
	a.mustAs("publish", "my own message")
	a.mustAs("whos-there")
	a.mustAs("channels")
	p.raw("PART " + ch)
	p.expectFrom(0, func(s string) bool { return strings.HasPrefix(s, ":cleanHuman2!") && strings.Contains(s, " PART ") })
	a.expectQuiet(500 * time.Millisecond)
	if a.out.Len() != 0 {
		t.Errorf("stdout is not empty:\n%s", a.out.String())
	}
	if !strings.Contains(a.err.String(), "joined "+ch+" as cleanA") {
		t.Errorf("stderr lacks the join diagnostic:\n%s", a.err.String())
	}
	for _, l := range a.err.Lines() {
		if !strings.HasPrefix(l, "chatterbox:") {
			t.Errorf("stderr line without the prefix: %q", l)
		}
	}
}

// IT-BACKPRESSURE: a reader that stops reading ends the listener with exit
// 1, and its nickname leaves.
func TestBackpressure(t *testing.T) {
	ch := chanFor(t)
	tuning := fastTuning + "stdout_queue_lines = 5\nstdout_full_limit = \"1500ms\"\n"
	p := dialPlain(t, shared.Addr(), "bpHuman")
	p.oper() // no flood control: the test needs volume
	p.join(ch)

	o := lopts{nick: "bpA", channel: ch, tuning: tuning}
	a := startListener(t, o)
	_ = a
	// The helper drains stdout, so start a second listener whose stdout
	// pipe nobody reads.
	stuck := startStuckListener(t, lopts{nick: "bpStuck", channel: ch, tuning: tuning})
	big := strings.Repeat("x", 380)
	for i := 0; i < 400; i++ { // far more than a pipe buffer holds
		p.say(ch, big)
	}
	select {
	case <-stuck.exited:
	case <-time.After(30 * time.Second):
		t.Fatal("the listener kept running although nobody reads its output")
	}
	if stuck.code != 1 {
		t.Errorf("exit code %d, want 1", stuck.code)
	}
	if !strings.Contains(stuck.err.String(), "has not been read") {
		t.Errorf("stderr: %s", stuck.err.String())
	}
	eventually(t, 10*time.Second, "bpStuck to leave the channel", func() bool {
		return !contains(p.names(ch), "bpStuck")
	})
}

// IT-KICK: a kicked listener exits 6 with the reason; so does one whose
// nickname is changed by an operator.
func TestKickAndForcedNick(t *testing.T) {
	ch := chanFor(t)
	op := dialPlain(t, shared.Addr(), "kickOper")
	op.oper()
	op.join(ch) // first in: channel operator

	a := startListener(t, lopts{nick: "kickA", channel: ch})
	op.raw("KICK " + ch + " kickA :take a break")
	if code := a.waitExit(10 * time.Second); code != 6 {
		t.Errorf("kicked: exit %d, want 6", code)
	}
	if e := a.err.String(); !strings.Contains(e, "kicked from "+ch+" by kickOper: take a break") {
		t.Errorf("stderr: %s", e)
	}
	if r := a.as("publish", "still here?"); r.code != 3 {
		t.Errorf("publish after the listener was kicked: exit %d, want 3", r.code)
	}

	b := startListener(t, lopts{nick: "kickB", channel: ch})
	op.raw("SANICK kickB kickRenamed")
	if code := b.waitExit(10 * time.Second); code != 6 {
		t.Errorf("forced nickname change: exit %d, want 6; stderr:\n%s", code, b.err.String())
	}
	if e := b.err.String(); !strings.Contains(e, "changed the nickname from kickB to kickRenamed") {
		t.Errorf("stderr: %s", e)
	}

	// A join that is refused ends the listener at once (D14).
	op.raw("MODE " + ch + " +i")
	c := startListener(t, lopts{nick: "kickC", channel: ch, noWait: true})
	if code := c.waitExit(10 * time.Second); code != 6 {
		t.Errorf("invite-only channel: exit %d, want 6", code)
	}
	if e := c.err.String(); !strings.Contains(e, "cannot join "+ch) {
		t.Errorf("stderr: %s", e)
	}
}

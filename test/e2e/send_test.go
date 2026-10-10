package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// IT-PUBLISH: publish is seen by another listener and by the plain client.
func TestPublish(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "pubA", channel: ch})
	b := startListener(t, lopts{nick: "pubB", channel: ch})
	p := dialPlain(t, shared.Addr(), "pubHuman")
	p.join(ch)

	r := a.mustAs("publish", "hello", "from", "A")
	if r.out != "" || r.err != "" {
		t.Errorf("publish printed something: out %q err %q", r.out, r.err)
	}
	line, _ := b.expectLine(0, "hello from A")
	if m := parseText(t, line); m.from != "pubA" || m.kind != "bot" || m.flags != "-" {
		t.Errorf("%+v", m)
	}
	p.expect("PRIVMSG " + ch + " :hello from A")

	// Standard input, with the trailing newline of `echo`.
	r = runCmd(t, cmdEnv(a.runtime, a.xdg), "from stdin\n", "publish", "--as", "pubA", "-")
	if r.code != 0 {
		t.Fatalf("publish -: exit %d: %s", r.code, r.err)
	}
	line, _ = b.expectLine(1, "from stdin")
	if m := parseText(t, line); m.text != "from stdin" {
		t.Errorf("stdin message text %q, want a single line without a trailing break", m.text)
	}

	// Flags after the message, "--" before text that starts with a dash.
	a.mustAs("publish", "flag", "last")
	b.expectLine(2, "flag last")
	if r := a.run("publish", "--as", "pubA", "--", "-1 is wrong"); r.code != 0 {
		t.Fatalf("publish -- : %s", r.err)
	}
	b.expectLine(3, "-1 is wrong")
	if r := a.run("publish", "--as", "pubA", "-1 is wrong"); r.code != 2 {
		t.Errorf("a message starting with - without -- must be a usage error, got exit %d", r.code)
	}
	if r := a.run("publish", "--as", "pubA", "--bogus", "text"); r.code != 2 {
		t.Errorf("unknown option: exit %d, want 2", r.code)
	}
	for _, bad := range []string{"", "   "} {
		if r := a.as("publish", bad); r.code != 2 || !strings.Contains(r.err, "not sent") {
			t.Errorf("empty message %q: exit %d: %s", bad, r.code, r.err)
		}
	}
	// The sender never sees its own messages.
	a.expectQuiet(300 * time.Millisecond)
	if a.out.Len() != 0 {
		t.Errorf("the sender printed its own messages:\n%s", a.out.String())
	}
	if b.out.Len() != 4 {
		t.Errorf("receiver printed %d lines, want 4:\n%s", b.out.Len(), b.out.String())
	}
}

// IT-SEND: send posts "nick: text" in the channel, flagged to-you at the
// addressee only; --private reaches only the addressee.
func TestSend(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "sendA", channel: ch})
	b := startListener(t, lopts{nick: "sendB", channel: ch})
	c := startListener(t, lopts{nick: "sendC", channel: ch})
	p := dialPlain(t, shared.Addr(), "sendHuman")
	p.join(ch)

	a.mustAs("send", "sendB", "please review")
	p.expect("PRIVMSG " + ch + " :sendB: please review")
	lb, _ := b.expectLine(0, "please review")
	lc, _ := c.expectLine(0, "please review")
	if m := parseText(t, lb); !m.has("to-you") || m.text != "sendB: please review" || m.channel != ch {
		t.Errorf("addressee: %+v", m)
	}
	if m := parseText(t, lc); m.has("to-you") {
		t.Errorf("bystander flagged to-you: %+v", m)
	}

	// The addressee is matched under the server's case mapping and written
	// as the server spells it.
	a.mustAs("send", "SENDB", "case")
	p.expect("PRIVMSG " + ch + " :sendB: case")

	// A human can be addressed too.
	a.mustAs("send", "sendHuman", "over to you")
	p.expect(":sendHuman: over to you")

	r := a.as("send", "nobodyHere", "hi")
	if r.code != 6 || !strings.Contains(r.err, "nobodyHere is not in "+ch) || !strings.Contains(r.err, "not sent") {
		t.Errorf("unknown nickname: exit %d: %s", r.code, r.err)
	}

	from := p.lines.Len()
	a.mustAs("send", "--private", "sendB", "for your eyes only")
	lb, _ = b.expectLine(0, "for your eyes only")
	if m := parseText(t, lb); m.channel != "private" || !m.has("to-you") || m.text != "for your eyes only" {
		t.Errorf("private: %+v", m)
	}
	a.mustAs("publish", "marker")
	c.expectLine(0, "marker")
	p.expectAfter(from, "marker")
	if strings.Contains(c.out.String(), "eyes only") || strings.Contains(strings.Join(p.lines.Lines()[from:], "\n"), "eyes only") {
		t.Error("a private message was visible to someone else")
	}
	r = a.as("send", "--private", "ghostNick", "hi")
	if r.code != 6 || !strings.Contains(r.err, "not sent") {
		t.Errorf("private to an unknown nickname: exit %d: %s", r.code, r.err)
	}
	if r := a.as("send", "sendB"); r.code != 2 {
		t.Errorf("send without a message: exit %d, want 2", r.code)
	}
}

// IT-IMPORTANT: the marking is visible to a human and flagged for agents,
// whatever the position of the flag.
func TestImportant(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "impA", channel: ch})
	b := startListener(t, lopts{nick: "impB", channel: ch, wake: "addressed"})
	p := dialPlain(t, shared.Addr(), "impHuman")
	p.join(ch)

	a.mustAs("send", "--important", "impB", "stop the deploy")
	p.expect("PRIVMSG " + ch + " :impB: [IMPORTANT] stop the deploy")
	line, _ := b.expectLine(0, "stop the deploy")
	if m := parseText(t, line); m.flags != "to-you,important" || m.text != "impB: stop the deploy" {
		t.Errorf("%+v", m)
	}

	// The flag after the positional arguments must not become text.
	a.mustAs("send", "impB", "--important", "flag in the middle")
	p.expect("PRIVMSG " + ch + " :impB: [IMPORTANT] flag in the middle")

	a.mustAs("publish", "--important", "everyone stop")
	p.expect("PRIVMSG " + ch + " :[IMPORTANT] everyone stop")
	line, _ = b.expectLine(0, "everyone stop")
	if m := parseText(t, line); m.flags != "important" || m.text != "everyone stop" {
		t.Errorf("%+v", m)
	}

	a.mustAs("send", "--private", "--important", "impB", "quietly urgent")
	line, _ = b.expectLine(0, "quietly urgent")
	if m := parseText(t, line); m.flags != "to-you,important" || m.channel != "private" {
		t.Errorf("%+v", m)
	}
}

// IT-ACK: when the server refuses the message the command fails with the
// server's reason and says nothing was sent.
func TestServerRefusal(t *testing.T) {
	ch := chanFor(t)
	op := dialPlain(t, shared.Addr(), "ackOper")
	op.join(ch)
	a := startListener(t, lopts{nick: "ackA", channel: ch})
	op.raw("MODE " + ch + " +m")
	op.expect(" MODE " + ch + " +m")

	r := a.as("publish", "can anyone hear me")
	if r.code != 6 {
		t.Fatalf("exit %d, want 6: %s", r.code, r.err)
	}
	if !strings.Contains(r.err, "the server refused the message") || !strings.Contains(r.err, "message not sent") {
		t.Errorf("stderr: %s", r.err)
	}
	// Same for a multiline message.
	r = a.as("publish", "one\ntwo")
	if r.code != 6 || !strings.Contains(r.err, "message not sent") {
		t.Errorf("multiline: exit %d: %s", r.code, r.err)
	}
	op.raw("MODE " + ch + " -m")
	op.expect(" MODE " + ch + " -m")
	a.mustAs("publish", "now it works")
	op.expect("now it works")
}

// IT-MULTILINE, with the server's flood control on.
func TestLongMessages(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "longA", channel: ch})
	b := startListener(t, lopts{nick: "longB", channel: ch})
	p := dialPlain(t, shared.Addr(), "longHuman")
	p.join(ch)

	// 30 lines, about 3 000 bytes: one message for a listener, separate
	// lines, in order, for a client without multiline.
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("line %02d %s", i, strings.Repeat("a", 90)))
	}
	text := strings.Join(lines, "\n")
	from := p.lines.Len()
	a.mustAs("publish", text)
	got, _ := b.expectLine(0, "line 00")
	if m := parseText(t, got); m.text != strings.Join(lines, `\n`) {
		t.Errorf("30-line message arrived altered (%d bytes, want %d)", len(m.text), len(strings.Join(lines, `\n`)))
	}
	if b.out.Len() != 1 {
		t.Errorf("30-line message printed as %d lines", b.out.Len())
	}
	at := from
	for i := range lines {
		_, at = p.expectAfter(at, "PRIVMSG "+ch+" :"+lines[i])
		at++
	}

	// One very long line: cut for the wire, one line again at the listener.
	long := strings.Repeat("word ", 300) + "end"
	a.mustAs("publish", long)
	got, _ = b.expectLine(1, "word word")
	if m := parseText(t, got); m.text != long {
		t.Errorf("long single line arrived altered (%d bytes, want %d)", len(m.text), len(long))
	}

	// 10 000 bytes: several messages, marked (i/n), in order.
	var big []string
	for i := 0; i < 26; i++ {
		big = append(big, fmt.Sprintf("row %03d %s", i, strings.Repeat("b", 380)))
	}
	start := time.Now()
	r := a.mustAs("send", "longB", strings.Join(big, "\n"))
	if !strings.Contains(r.err, "sent as 3 messages") {
		t.Errorf("stderr %q, expected a note about 3 messages", r.err)
	}
	t.Logf("10 000-byte message took %s under flood control", time.Since(start).Round(time.Millisecond))
	var joined []string
	for i := 1; i <= 3; i++ {
		marker := fmt.Sprintf("longB: (%d/3) ", i)
		got, _ = b.expectLine(2, marker)
		m := parseText(t, got)
		if !m.has("to-you") || !strings.HasPrefix(m.text, marker) {
			t.Errorf("part %d: flags %q, text starts %q", i, m.flags, m.text[:20])
		}
		joined = append(joined, strings.TrimPrefix(m.text, marker))
	}
	if strings.Join(joined, `\n`) != strings.Join(big, `\n`) {
		t.Error("the parts do not add up to the message")
	}

	// 100 lines is the server's limit for one message.
	hundred := strings.TrimSuffix(strings.Repeat("l\n", 100), "\n")
	r = a.mustAs("publish", hundred)
	if strings.Contains(r.err, "sent as") {
		t.Errorf("a 100-line message was split: %s", r.err)
	}
	got, _ = b.expectLine(5, `l\nl\n`)
	if m := parseText(t, got); strings.Count(m.text, `\n`) != 99 {
		t.Errorf("100-line message has %d breaks", strings.Count(m.text, `\n`))
	}
	if b.out.Len() != 6 {
		t.Errorf("listener printed %d lines, want 6", b.out.Len())
	}
}

// IT-MULTILINE: the size cap, and a client that goes away mid-message.
func TestLongMessageLimits(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "limA", channel: ch, config: "[limits]\nmax_message_bytes = 30000\n"})
	b := startListener(t, lopts{nick: "limB", channel: ch})

	r := runCmd(t, cmdEnv(a.runtime, a.xdg), strings.Repeat("x", 30001), "publish", "--as", "limA", "-")
	if r.code != 2 || !strings.Contains(r.err, "over the limit of 30000") || !strings.Contains(r.err, "not sent") {
		t.Errorf("over the cap: exit %d: %s", r.code, r.err)
	}

	// A message of many parts; the command is killed while it is going out.
	var rows []string
	for i := 0; i < 72; i++ {
		rows = append(rows, fmt.Sprintf("row %03d %s", i, strings.Repeat("c", 380)))
	}
	cmd := exec.Command(binary, "publish", "--as", "limA", "-")
	cmd.Env = cmdEnv(a.runtime, a.xdg)
	cmd.Stdin = strings.NewReader(strings.Join(rows, "\n"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	b.expectLine(0, "(1/8) ")
	cmd.Process.Kill()
	cmd.Wait()
	// The part in flight is finished, the rest is dropped. Left alone,
	// flood control would let a further part through about every 5 s.
	time.Sleep(13 * time.Second)
	if n := b.out.Len(); n > 2 {
		t.Errorf("%d parts arrived although the command was killed after the first", n)
	}
	// The listener is still usable.
	a.mustAs("publish", "after the abort")
	b.expectLine(0, "after the abort")
}

// IT-ORDER: concurrent commands do not interleave their parts, and a query
// issued meanwhile is not held up behind them.
func TestOrderAndFairness(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "ordA", channel: ch})
	b := startListener(t, lopts{nick: "ordB", channel: ch})

	const n = 10
	var wg sync.WaitGroup
	results := make([]result, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			text := fmt.Sprintf("msg%02d short", i)
			if i%4 == 0 { // some need two messages
				var rows []string
				for r := 0; r < 11; r++ {
					rows = append(rows, fmt.Sprintf("msg%02d row %02d %s", i, r, strings.Repeat("d", 380)))
				}
				text = strings.Join(rows, "\n")
			}
			results[i] = runCmd(t, cmdEnv(a.runtime, a.xdg), text, "publish", "--as", "ordA", "-")
		}(i)
	}
	// While the queue is busy, a query must come back promptly.
	time.Sleep(1500 * time.Millisecond)
	who := a.mustAs("whos-there")
	if !strings.Contains(who.out, "ordB bot") {
		t.Errorf("whos-there: %s", who.out)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		t.Errorf("all %d publishes finished before the query returned; the test proves nothing about fairness", n)
	default:
	}
	<-done
	for i, r := range results {
		if r.code != 0 {
			t.Fatalf("publish %d: exit %d: %s", i, r.code, r.err)
		}
	}
	want := n + 3 // three of the ten are split in two
	if !b.out.WaitCount(want, 90*time.Second) {
		t.Fatalf("received %d lines, want %d", b.out.Len(), want)
	}
	// Parts of one message are adjacent and in order.
	lines := b.out.Lines()
	for i := 0; i < len(lines); i++ {
		m := parseText(t, lines[i])
		if !strings.HasPrefix(m.text, "(1/2) ") {
			if strings.HasPrefix(m.text, "(2/2) ") {
				t.Errorf("line %d: second part without its first before it", i)
			}
			continue
		}
		id := m.text[len("(1/2) ") : len("(1/2) ")+5]
		if i+1 >= len(lines) {
			t.Fatalf("message %s: first part is the last line", id)
		}
		next := parseText(t, lines[i+1])
		if !strings.HasPrefix(next.text, "(2/2) "+id) {
			t.Errorf("message %s: its parts are not adjacent; next line starts %q", id, next.text[:20])
		}
		i++
	}
}

// IT-DISCOVER: how commands find their listener.
func TestDiscovery(t *testing.T) {
	ch := chanFor(t)
	rt := shortDir(t)
	xdg := t.TempDir()
	env := cmdEnv(rt, xdg)
	os.Chmod(rt, 0o700)

	// No listener at all.
	r := runCmd(t, env, "", "publish", "--as", "discA", "hello")
	if r.code != 3 || !strings.Contains(r.err, "no listener is running for discA") ||
		!strings.Contains(r.err, "chatterbox listen --as discA --channel") || !strings.Contains(r.err, rt) {
		t.Errorf("no listener: exit %d: %s", r.code, r.err)
	}
	if r := runCmd(t, env, "", "whos-there"); r.code != 3 || !strings.Contains(r.err, "no listener is running") {
		t.Errorf("whos-there with no listener: exit %d: %s", r.code, r.err)
	}

	// Exactly one: read-only commands auto-detect with a notice; posting
	// commands never guess.
	a := startListener(t, lopts{nick: "discA", channel: ch, runtime: rt, xdg: xdg})
	p := dialPlain(t, shared.Addr(), "discHuman")
	p.join(ch)
	r = runCmd(t, env, "", "whos-there")
	if r.code != 0 || !strings.Contains(r.err, "using listener discA (auto-detected)") || !strings.Contains(r.out, "discA bot") {
		t.Errorf("auto-detect: exit %d out %q err %q", r.code, r.out, r.err)
	}
	if r := runCmd(t, env, "", "channels"); r.code != 0 || !strings.Contains(r.out, ch) {
		t.Errorf("channels auto-detect: exit %d: %s", r.code, r.err)
	}
	for _, args := range [][]string{{"publish", "guess who"}, {"send", "discHuman", "guess who"}, {"create-channel", "#disc-new"}} {
		r := runCmd(t, env, "", args...)
		if r.code != 3 || !strings.Contains(r.err, "never guesses") || !strings.Contains(r.err, "discA") {
			t.Errorf("%v without --as: exit %d: %s", args, r.code, r.err)
		}
	}

	// Two listeners: both listed; --as and the environment variable select.
	startListener(t, lopts{nick: "discB", channel: ch, runtime: rt, xdg: xdg})
	r = runCmd(t, env, "", "whos-there")
	if r.code != 3 || !strings.Contains(r.err, "discA") || !strings.Contains(r.err, "discB") {
		t.Errorf("two listeners: exit %d: %s", r.code, r.err)
	}
	if r := runCmd(t, env, "", "publish", "--as", "discB", "from B by flag"); r.code != 0 {
		t.Errorf("--as: %s", r.err)
	}
	p.expect(":discB!")
	if r := runCmd(t, append(env, "CHATTERBOX_NICK=discA"), "", "publish", "from A by env"); r.code != 0 {
		t.Errorf("CHATTERBOX_NICK: %s", r.err)
	}
	p.expectFrom(0, func(s string) bool { return strings.HasPrefix(s, ":discA!") && strings.Contains(s, "from A by env") })
	// The flag wins over the environment.
	if r := runCmd(t, append(env, "CHATTERBOX_NICK=discA"), "", "publish", "--as", "discB", "flag beats env"); r.code != 0 {
		t.Errorf("flag over env: %s", r.err)
	}
	p.expectFrom(0, func(s string) bool { return strings.HasPrefix(s, ":discB!") && strings.Contains(s, "flag beats env") })
	for _, line := range p.lines.Lines() {
		if strings.Contains(line, "guess who") {
			t.Errorf("a command without an identity posted: %s", line)
		}
	}
	_ = a
}

// IT-WRONGAGENT: when B's listener is gone, B's commands must not go out
// under A's name.
func TestNoCrossPosting(t *testing.T) {
	ch := chanFor(t)
	rt := shortDir(t)
	xdg := t.TempDir()
	a := startListener(t, lopts{nick: "wrongA", channel: ch, runtime: rt, xdg: xdg})
	b := startListener(t, lopts{nick: "wrongB", channel: ch, runtime: rt, xdg: xdg})
	p := dialPlain(t, shared.Addr(), "wrongHuman")
	p.join(ch)
	b.cmd.Process.Kill()
	b.waitExit(5 * time.Second)

	env := cmdEnv(rt, xdg)
	r := runCmd(t, env, "", "publish", "B thinks it is alone")
	if r.code != 3 {
		t.Errorf("publish without --as: exit %d, want 3: %s", r.code, r.err)
	}
	r = runCmd(t, env, "", "publish", "--as", "wrongB", "B names itself")
	if r.code != 3 || !strings.Contains(r.err, "chatterbox listen --as wrongB") {
		t.Errorf("publish --as wrongB: exit %d: %s", r.code, r.err)
	}
	a.mustAs("publish", "A is fine")
	p.expect("A is fine")
	for _, line := range p.lines.Lines() {
		if strings.Contains(line, "B thinks") || strings.Contains(line, "B names") {
			t.Errorf("B's message was posted: %s", line)
		}
	}
}

// IT-ONECONN: control commands never open a connection of their own, and
// need no configuration.
func TestCommandsUseTheListenersConnection(t *testing.T) {
	ch := chanFor(t)
	px := startProxy(t, shared.Addr())
	a := startListener(t, lopts{nick: "oneA", channel: ch, addr: px.Addr()})
	b := startListener(t, lopts{nick: "oneB", channel: ch, addr: px.Addr(), runtime: a.runtime})
	if n := px.Accepted(); n != 2 {
		t.Fatalf("%d connections for two listeners", n)
	}
	// An empty config directory: the commands must not need the file.
	env := cmdEnv(a.runtime, t.TempDir())
	for _, args := range [][]string{
		{"publish", "--as", "oneA", "one"},
		{"send", "--as", "oneA", "oneB", "two"},
		{"send", "--as", "oneB", "--private", "oneA", "three"},
		{"whos-there", "--as", "oneA"},
		{"channels", "--as", "oneB"},
		{"create-channel", "--as", "oneB", ch + "-next"},
	} {
		if r := runCmd(t, env, "", args...); r.code != 0 {
			t.Errorf("%v: exit %d: %s", args, r.code, r.err)
		}
	}
	if n := px.Accepted(); n != 2 {
		t.Errorf("%d connections after the commands, want still 2", n)
	}
	_ = b
}

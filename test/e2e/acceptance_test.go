package e2e

import (
	"strings"
	"testing"
	"time"
)

// The acceptance scenario of issue #1, as one continuous session. It runs in
// two configurations: X with agentA on --wake all, Y with agentA on --wake
// addressed. agentB is on "all" in both. Y is the reason step 4 means
// anything: only there does printing depend on the importance bypass.
//
// X uses separate runtime directories (agents on two machines), Y a shared
// one (agents on the same machine).
func TestAcceptance(t *testing.T) {
	t.Run("X wake all, two machines", func(t *testing.T) { acceptance(t, "all", false) })
	t.Run("Y wake addressed, same machine", func(t *testing.T) { acceptance(t, "addressed", true) })
}

func acceptance(t *testing.T, wakeA string, sameMachine bool) {
	ch := "#accept-" + wakeA
	px := startProxy(t, shared.Addr())
	addressed := wakeA == "addressed"
	nickA, nickB, nickH := "agentA"+wakeA, "agentB"+wakeA, "human"+wakeA

	// AT-1: two listeners with distinct nicknames on one channel; a human
	// joins with a standard client.
	rtA := shortDir(t)
	rtB := rtA
	if !sameMachine {
		rtB = shortDir(t)
	}
	a := startListener(t, lopts{nick: nickA, channel: ch, wake: wakeA, addr: px.Addr(), runtime: rtA})
	b := startListener(t, lopts{nick: nickB, channel: ch, runtime: rtB})
	h := dialPlain(t, shared.Addr(), nickH)
	h.join(ch)
	names := h.names(ch)
	for _, n := range []string{nickA, nickB, nickH} {
		if !contains(names, n) {
			t.Fatalf("AT-1: %s is not in %s: %v", n, ch, names)
		}
	}

	// AT-2: whos-there from either agent lists all three, agents as bots.
	want := nickA + " bot\n" + nickB + " bot\n" + nickH + " human\n"
	for _, l := range []*listener{a, b} {
		if r := l.mustAs("whos-there"); r.out != want {
			t.Errorf("AT-2: whos-there as %s:\n%s", l.nick, r.out)
		}
	}

	// AT-3: A publishes; B prints exactly one line; the human sees it.
	a.mustAs("publish", "hello")
	line, _ := b.expectLine(0, "hello")
	b.expectQuiet(300 * time.Millisecond)
	if b.out.Len() != 1 {
		t.Fatalf("AT-3: agentB printed %d lines:\n%s", b.out.Len(), b.out.String())
	}
	if m := parseText(t, line); m.from != nickA || m.kind != "bot" || m.text != "hello" {
		t.Errorf("AT-3: %+v", m)
	}
	h.expect("PRIVMSG " + ch + " :hello")
	if a.out.Len() != 0 {
		t.Errorf("AT-3: agentA printed its own message:\n%s", a.out.String())
	}

	// AT-4: B sends an important message to A; A prints it under either
	// filter, and the human sees the marking.
	b.mustAs("send", "--important", nickA, "stop")
	line, _ = a.expectLine(0, "stop")
	if m := parseText(t, line); m.flags != "to-you,important" || m.from != nickB || m.text != nickA+": stop" {
		t.Errorf("AT-4: %+v", m)
	}
	h.expect("PRIVMSG " + ch + " :" + nickA + ": [IMPORTANT] stop")
	wantA := 1
	if addressed {
		// The case only the importance bypass explains: unaddressed,
		// typed by a human, printed under --wake addressed.
		h.say(ch, "[IMPORTANT] everyone stop")
		line, _ = a.expectLine(1, "everyone stop")
		if m := parseText(t, line); m.flags != "important" || m.kind != "human" {
			t.Errorf("AT-4 (Y): %+v", m)
		}
		wantA++
		b.expectLine(0, "everyone stop")
	}

	// AT-5: the human posts; agents receive it flagged as human.
	h.say(ch, "status?")
	line, _ = b.expectLine(0, "status?")
	if m := parseText(t, line); m.kind != "human" || m.from != nickH {
		t.Errorf("AT-5: agentB: %+v", m)
	}
	if addressed {
		a.expectQuiet(500 * time.Millisecond)
		if a.out.Len() != wantA {
			t.Errorf("AT-5 (Y): agentA printed an unaddressed message under --wake addressed:\n%s", a.out.String())
		}
		h.say(ch, nickA+": status?")
	}
	line, _ = a.expectLine(wantA, "status?")
	if m := parseText(t, line); m.kind != "human" || m.from != nickH {
		t.Errorf("AT-5: agentA: %+v", m)
	}
	wantA++

	// AT-6: A's connection is dropped; B publishes three messages and the
	// human posts two; the connection is restored. A prints those five
	// once each, in order, flagged replayed, then one further message.
	px.Down()
	if _, ok := a.err.Wait("connection lost", 10*time.Second); !ok {
		t.Fatal("AT-6: the cut was not noticed")
	}
	to := ""
	if addressed {
		to = nickA + ": "
	}
	var missed []string
	// A is not in the channel, so `send` would be refused; B publishes,
	// in Y with the address in the text so that A's filter lets it through.
	post := func(l *listener, text string) {
		l.mustAs("publish", to+text)
		missed = append(missed, to+text)
	}
	post(b, "missed one")
	h.say(ch, to+"missed two")
	missed = append(missed, to+"missed two")
	b.expectLine(0, "missed two") // keeps the order across the two posters
	post(b, "missed three")
	h.say(ch, to+"missed four")
	missed = append(missed, to+"missed four")
	b.expectLine(0, "missed four")
	post(b, "missed five")
	px.Restore()
	if _, ok := a.err.Wait("reconnected; 5 messages replayed", 30*time.Second); !ok {
		t.Fatalf("AT-6: stderr:\n%s", a.err.String())
	}
	h.say(ch, to+"and one more")
	a.expectLine(wantA, "and one more")
	got := a.out.Lines()[wantA:]
	if len(got) != 6 {
		t.Fatalf("AT-6: agentA printed %d lines after the outage, want 6:\n%s", len(got), strings.Join(got, "\n"))
	}
	for i, wantText := range missed {
		m := parseText(t, got[i])
		if m.text != wantText || !m.has("replayed") {
			t.Errorf("AT-6: line %d: %+v, want replayed %q", i, m, wantText)
		}
	}
	if m := parseText(t, got[5]); m.has("replayed") || !strings.HasSuffix(m.text, "and one more") {
		t.Errorf("AT-6: the message after the outage: %+v", m)
	}

	// AT-7: A's listener is stopped; A disappears from whos-there.
	a.cmd.Process.Signal(sigterm)
	if code := a.waitExit(10 * time.Second); code != 0 {
		t.Errorf("AT-7: exit %d", code)
	}
	eventually(t, 10*time.Second, "agentA to leave", func() bool {
		return b.mustAs("whos-there").out == nickB+" bot\n"+nickH+" human\n"
	})
	if r := a.as("publish", "anyone?"); r.code != 3 {
		t.Errorf("AT-7: publish after the listener stopped: exit %d, want 3", r.code)
	}
}

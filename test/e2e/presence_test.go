package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// IT-WHO: two listeners and a human, with the right kinds.
func TestWhosThere(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "whoB", channel: ch})
	startListener(t, lopts{nick: "whoA", channel: ch})
	p := dialPlain(t, shared.Addr(), "whoHuman")
	p.join(ch)

	r := a.mustAs("whos-there")
	if r.out != "whoA bot\nwhoB bot\nwhoHuman human\n" {
		t.Errorf("whos-there:\n%s", r.out)
	}
	r = a.mustAs("whos-there", "--json")
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 3 {
		t.Fatalf("json: %q", r.out)
	}
	var first struct{ Nick, Kind string }
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first.Nick != "whoA" || first.Kind != "bot" {
		t.Errorf("json line %q (%v)", lines[0], err)
	}
	if lines[2] != `{"nick":"whoHuman","kind":"human"}` {
		t.Errorf("json line %q", lines[2])
	}
}

// IT-CHANNELS: names for all channels; last activity for the listener's own
// channel only, because the server refuses history to non-members (S3).
func TestChannels(t *testing.T) {
	own, other := chanFor(t), chanFor(t)+"-other"
	a := startListener(t, lopts{nick: "chanA", channel: own})
	b := startListener(t, lopts{nick: "chanB", channel: other})
	p := dialPlain(t, shared.Addr(), "chanHuman")
	p.join(own)
	p.join(other)

	// Before anyone has spoken there is no activity to report.
	r := a.mustAs("channels")
	if !strings.Contains(r.out, own+" 2 -\n") {
		t.Errorf("a channel nobody has spoken in must show no activity:\n%s", r.out)
	}

	before := time.Now().UTC().Add(-2 * time.Second)
	p.say(own, "something happened here")
	p.say(other, "and here")
	a.expectLine(0, "something happened")
	b.expectLine(0, "and here")

	for i := 0; i < 3; i++ { // repeated calls stay within the time budget
		start := time.Now()
		r = a.mustAs("channels")
		if d := time.Since(start); d > 11*time.Second {
			t.Errorf("call %d took %s", i+1, d)
		}
	}
	var ownLine, otherLine string
	for _, l := range strings.Split(r.out, "\n") {
		f := strings.Fields(l)
		if len(f) != 3 {
			continue
		}
		switch f[0] {
		case own:
			ownLine = l
		case other:
			otherLine = l
		}
	}
	f := strings.Fields(ownLine)
	if len(f) != 3 || f[1] != "2" {
		t.Fatalf("own channel line %q in:\n%s", ownLine, r.out)
	}
	ts, err := time.Parse("2006-01-02T15:04:05Z", f[2])
	if err != nil || ts.Before(before) {
		t.Errorf("own channel last activity %q (%v)", f[2], err)
	}
	if otherLine != other+" 2 -" {
		t.Errorf("other channel line %q, want unknown last activity", otherLine)
	}
	// Known activity sorts first.
	if strings.Index(r.out, own+" ") > strings.Index(r.out, other+" ") {
		t.Errorf("channels with known activity must come first:\n%s", r.out)
	}

	r = a.mustAs("channels", "--json")
	if !strings.Contains(r.out, `{"name":"`+other+`","users":2,"last_activity":null}`) {
		t.Errorf("json:\n%s", r.out)
	}
	if !strings.Contains(r.out, `{"name":"`+own+`","users":2,"last_activity":"`+f[2]+`"}`) {
		t.Errorf("json:\n%s", r.out)
	}
}

// IT-CREATE: create-channel moves the listener (D2, 03 §8).
func TestCreateChannel(t *testing.T) {
	old := chanFor(t)
	px := startProxy(t, shared.Addr())
	a := startListener(t, lopts{nick: "crA", channel: old, addr: px.Addr()})
	p := dialPlain(t, shared.Addr(), "crHuman")
	p.join(old)

	// Bad names change nothing.
	for _, name := range []string{"nohash", "#with space", "#a,b", old, "#" + strings.Repeat("x", 70)} {
		if r := a.as("create-channel", name); r.code != 2 {
			t.Errorf("create-channel %q: exit %d, want 2: %s", name, r.code, r.err)
		}
	}

	// A listed, existing channel: refused, still in the old one.
	taken := old + "-taken"
	q := dialPlain(t, shared.Addr(), "crOther")
	q.join(taken)
	r := a.as("create-channel", taken)
	if r.code != 6 || !strings.Contains(r.err, "already exists") {
		t.Errorf("existing channel: exit %d: %s", r.code, r.err)
	}
	if !contains(p.names(old), "crA") {
		t.Fatal("the listener left its channel although create-channel failed")
	}

	// A secret channel is not listed: the listener goes there, finds it is
	// not the creator, comes back and catches up on what it missed.
	secret := old + "-secret"
	q.join(secret)
	q.raw("MODE " + secret + " +s")
	q.expect(" MODE " + secret + " +s")
	from := p.lines.Len()
	go func() {
		// Posted while the listener is away.
		p.expectAfter(from, " PART "+old)
		p.say(old, "said while crA was away")
	}()
	r = a.as("create-channel", secret)
	if r.code != 6 || !strings.Contains(r.err, "already exists") {
		t.Errorf("secret channel: exit %d: %s", r.code, r.err)
	}
	line, _ := a.expectLine(0, "said while crA was away")
	if m := parseText(t, line); m.channel != old {
		t.Errorf("%+v", m)
	}
	if !contains(p.names(old), "crA") {
		t.Fatal("the listener did not return to its channel")
	}
	if contains(q.names(secret), "crA") {
		t.Error("the listener stayed in the channel it did not create")
	}
	a.mustAs("publish", "back and working")
	p.expect("back and working")

	// A free name: announced, created, and the listener's channel from now on.
	fresh := old + "-fresh"
	from = p.lines.Len()
	r = a.mustAs("create-channel", fresh)
	if want := "now listening on " + fresh + "; the channel exists only while someone is in it\n"; r.out != want {
		t.Errorf("stdout %q, want %q", r.out, want)
	}
	p.expectAfter(from, "PRIVMSG "+old+" :moving to "+fresh)
	p.expectAfter(from, " PART "+old)
	p.join(fresh)
	p.expectFrom(0, func(s string) bool {
		return strings.Contains(s, " 353 ") && strings.Contains(s, fresh) && strings.Contains(s, "@crA")
	})
	if names := p.names(old); contains(names, "crA") {
		t.Errorf("the listener is still in the old channel: %v", names)
	}
	a.mustAs("publish", "first words here")
	p.expect("PRIVMSG " + fresh + " :first words here")
	p.say(fresh, "welcome")
	line, _ = a.expectLine(0, "welcome")
	if m := parseText(t, line); m.channel != fresh {
		t.Errorf("%+v", m)
	}
	status := a.mustAs("channels")
	if !strings.Contains(status.out, fresh+" 2 ") {
		t.Errorf("channels after the move:\n%s", status.out)
	}

	// After a forced reconnect it is back in the new channel, not the old.
	px.Cut()
	if _, ok := a.err.Wait("reconnected", 20*time.Second); !ok {
		t.Fatalf("no reconnect:\n%s", a.err.String())
	}
	eventually(t, 10*time.Second, "crA to be back in "+fresh, func() bool { return contains(p.names(fresh), "crA") })
	if contains(p.names(old), "crA") {
		t.Error("the listener rejoined the old channel after reconnecting")
	}
}

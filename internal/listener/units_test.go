package listener

import (
	"fmt"
	"testing"
	"time"

	"github.com/teo-mateo/chatterbox/internal/config"
)

func TestSeenSetBound(t *testing.T) {
	s := newSeenSet(3)
	for _, id := range []string{"a", "b", "c"} {
		if !s.Add(id) {
			t.Errorf("%s reported as seen on first sight", id)
		}
	}
	if s.Add("b") {
		t.Error("duplicate not recognized")
	}
	s.Add("d") // evicts a
	if s.Len() != 3 {
		t.Errorf("Len = %d, want the bound 3", s.Len())
	}
	if !s.Add("a") {
		t.Error("an evicted id must count as new again")
	}
	if s.Add("d") || s.Add("c") {
		t.Error("recent ids must still be known")
	}
	// Messages without an id are never deduplicated.
	for i := 0; i < 2; i++ {
		if !s.Add("") {
			t.Error("an empty id must always be new")
		}
	}
	big := newSeenSet(5000)
	for i := 0; i < 20000; i++ {
		big.Add(fmt.Sprint(i))
	}
	if big.Len() != 5000 || len(big.order) != 5000 {
		t.Errorf("set grew to %d entries", big.Len())
	}
	if big.Add("19999") || !big.Add("0") {
		t.Error("wrong entries evicted")
	}
}

func TestReferenceOnlyServerTime(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	var r reference
	r.advance(t0, "a", true)
	if r.id != "a" || !r.time.Equal(t0) {
		t.Fatalf("%+v", r)
	}
	// A message stamped by the local clock never becomes the reference.
	r.advance(t0.Add(time.Hour), "local", false)
	if r.id != "a" {
		t.Error("the reference was taken from the local clock")
	}
	// An older message does not move it back.
	r.advance(t0.Add(-time.Second), "older", true)
	if r.id != "a" {
		t.Error("the reference moved backwards")
	}
	// Same timestamp: the later arrival wins.
	r.advance(t0, "b", true)
	if r.id != "b" {
		t.Error("a message sharing the timestamp must become the reference")
	}
	r.advance(t0.Add(time.Millisecond), "c", true)
	if r.id != "c" {
		t.Error("the reference did not advance")
	}
}

func TestBackoff(t *testing.T) {
	b := backoff{min: time.Second, max: 60 * time.Second, rnd: func() float64 { return 0.5 }} // no jitter
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if got := b.next(); got != w*time.Second {
			t.Errorf("attempt %d: %s, want %s", i, got, w*time.Second)
		}
	}
	b.reset()
	if got := b.next(); got != time.Second {
		t.Errorf("after reset: %s", got)
	}

	lo := backoff{min: time.Second, max: 60 * time.Second, rnd: func() float64 { return 0 }}
	hi := backoff{min: time.Second, max: 60 * time.Second, rnd: func() float64 { return 0.999999 }}
	for i := 0; i < 8; i++ {
		base := time.Second << i
		if base > 60*time.Second {
			base = 60 * time.Second
		}
		l, h := lo.next(), hi.next()
		if l < base*8/10 || l > base || h < base || h > base*12/10 {
			t.Errorf("attempt %d: jitter range %s..%s around %s, want ±20%%", i, l, h, base)
		}
	}
}

func TestTickInterval(t *testing.T) {
	if d := tickInterval(config.Default().Tuning); d != 100*time.Millisecond {
		t.Errorf("default tick %s", d)
	}
	fast := config.Default().Tuning
	fast.KeepaliveInterval = config.Duration(100 * time.Millisecond)
	if d := tickInterval(fast); d != 20*time.Millisecond {
		t.Errorf("fast tick %s", d)
	}
	fast.BackoffMin = config.Duration(time.Millisecond)
	if d := tickInterval(fast); d != 10*time.Millisecond {
		t.Errorf("tick floor %s", d)
	}
}

func TestModeAdds(t *testing.T) {
	for modes, want := range map[string]bool{"+B": true, "+iB": true, "+i-B": false, "-B": false, "-i+B": true, "+i": false, "B": true} {
		if got := modeAdds(modes, "B"); got != want {
			t.Errorf("modeAdds(%q) = %v", modes, got)
		}
	}
}

func TestStateNames(t *testing.T) {
	want := map[phase]string{
		phBackoff: "backoff", phDialing: "dialing", phCapLS: "registering", phRegistering: "registering",
		phBotMode: "registering", phJoining: "joining", phCatchup: "catching-up", phReady: "ready", phMoving: "joining",
	}
	for p, name := range want {
		if p.stateName() != name {
			t.Errorf("phase %d is %q, want %q", p, p.stateName(), name)
		}
	}
}

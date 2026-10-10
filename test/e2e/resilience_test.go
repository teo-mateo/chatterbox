package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teo-mateo/chatterbox/test/ergo"
)

// quickIdle detects a silent connection within about two seconds.
const quickIdle = `
[tuning]
keepalive_interval = "700ms"
idle_timeout = "2s"
backoff_min = "200ms"
backoff_max = "1s"
backoff_reset = "3s"
ready_wait = "2s"
hangup_poll = "200ms"
quit_wait = "1s"
nick_retry_window = "40s"
`

// IT-RECONNECT: after a cut the listener rejoins and presence is restored.
func TestReconnect(t *testing.T) {
	ch := chanFor(t)
	px := startProxy(t, shared.Addr())
	a := startListener(t, lopts{nick: "reconA", channel: ch, addr: px.Addr()})
	p := dialPlain(t, shared.Addr(), "reconHuman")
	p.join(ch)
	for i := 0; i < 3; i++ {
		from := p.lines.Len()
		px.Cut()
		p.expectFrom(from, func(s string) bool { return strings.HasPrefix(s, ":reconA!") && strings.Contains(s, " QUIT ") })
		p.expectFrom(from, func(s string) bool { return strings.HasPrefix(s, ":reconA!") && strings.Contains(s, " JOIN ") })
		eventually(t, 10*time.Second, "the reconnect diagnostic", func() bool {
			return strings.Count(a.err.String(), "reconnected;") == i+1
		})
	}
	if n := strings.Count(a.err.String(), "connection lost:"); n != 3 {
		t.Errorf("%d \"connection lost\" diagnostics, want 3:\n%s", n, a.err.String())
	}
	// Still a bot, still working, and nothing went to stdout.
	if r := a.mustAs("whos-there"); !strings.Contains(r.out, "reconA bot") {
		t.Errorf("whos-there after reconnecting: %s", r.out)
	}
	a.mustAs("publish", "still here")
	p.expect("still here")
	if a.out.Len() != 0 {
		t.Errorf("stdout: %s", a.out.String())
	}
}

// IT-PING: a connection that goes silent is detected by the idle timeout.
func TestBlackholeDetected(t *testing.T) {
	ch := chanFor(t)
	px := startProxy(t, shared.Addr())
	a := startListener(t, lopts{nick: "pingA", channel: ch, addr: px.Addr(), tuning: quickIdle})
	// A healthy connection is left alone.
	time.Sleep(3 * time.Second)
	if strings.Contains(a.err.String(), "connection lost") {
		t.Fatalf("a healthy connection was dropped:\n%s", a.err.String())
	}
	start := time.Now()
	px.Blackhole()
	if _, ok := a.err.Wait("connection lost: no data from the server for 2s", 10*time.Second); !ok {
		t.Fatalf("the silent connection was not detected:\n%s", a.err.String())
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Errorf("detection took %s", d)
	}
}

// IT-GHOST: after a black-hole the server still holds the old session and
// its nickname. The listener gets the name back without ever using another.
func TestNicknameHeldByGhost(t *testing.T) {
	ch := chanFor(t)
	px := startProxy(t, shared.Addr())
	a := startListener(t, lopts{nick: "ghostA", channel: ch, addr: px.Addr(), tuning: quickIdle, args: []string{"--verbose"}})
	p := dialPlain(t, shared.Addr(), "ghostHuman")
	p.join(ch)
	from := p.lines.Len()
	px.Blackhole()
	if _, ok := a.err.Wait("connection lost", 10*time.Second); !ok {
		t.Fatal("the black-hole was not detected")
	}
	px.Restore()
	// The server drops the silent session after its own timeout (8 s in
	// the test configuration); until then the nickname is taken.
	if _, ok := a.err.Wait("reconnected;", 40*time.Second); !ok {
		t.Fatalf("the listener did not get back in:\n%s", a.err.String())
	}
	if !strings.Contains(a.err.String(), "the server still holds the nickname ghostA") {
		t.Errorf("expected the listener to have met its own ghost; stderr:\n%s", a.err.String())
	}
	if !a.running() {
		t.Fatal("the listener exited")
	}
	for _, l := range p.lines.Lines()[from:] {
		if strings.Contains(l, " JOIN ") && !strings.HasPrefix(l, ":ghostA!") {
			t.Errorf("another nickname joined: %s", l)
		}
	}
	if r := a.mustAs("whos-there"); !strings.Contains(r.out, "ghostA bot") {
		t.Errorf("whos-there: %s", r.out)
	}
}

// IT-GHOST: the nickname stays taken past the window: exit 4, no other name.
func TestNicknameStillTakenAfterWindow(t *testing.T) {
	ch := chanFor(t)
	px := startProxy(t, shared.Addr())
	tuning := strings.Replace(quickIdle, `nick_retry_window = "40s"`, `nick_retry_window = "3s"`, 1)
	a := startListener(t, lopts{nick: "takenA", channel: ch, addr: px.Addr(), tuning: tuning})
	px.Down()
	if _, ok := a.err.Wait("connection lost", 10*time.Second); !ok {
		t.Fatal("the cut was not noticed")
	}
	thief := dialPlain(t, shared.Addr(), "takenA")
	thief.join(ch)
	px.Restore()
	if code := a.waitExit(30 * time.Second); code != 4 {
		t.Errorf("exit %d, want 4", code)
	}
	if !strings.Contains(a.err.String(), "nickname takenA is already in use") {
		t.Errorf("stderr:\n%s", a.err.String())
	}
	if names := thief.names(ch); len(names) != 1 {
		t.Errorf("the listener joined under another name: %v", names)
	}
}

// seq extracts the number from a "<tag> N" message text.
func seq(t *testing.T, text, tag string) int {
	t.Helper()
	i := strings.Index(text, tag+" ")
	if i < 0 {
		t.Fatalf("no %q in %q", tag, text)
	}
	rest := text[i+len(tag)+1:]
	if j := strings.IndexAny(rest, `\ `); j >= 0 {
		rest = rest[:j]
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		t.Fatalf("no number after %q in %q", tag, text)
	}
	return n
}

// IT-CATCHUP: everything posted during an outage is printed once, in order,
// flagged replayed; what arrives during the catch-up comes after it, once.
func TestCatchup(t *testing.T) {
	ch := chanFor(t)
	px := startProxy(t, shared.Addr())
	a := startListener(t, lopts{nick: "catchA", channel: ch, addr: px.Addr()})
	b := startListener(t, lopts{nick: "catchB", channel: ch}) // posts the multiline ones
	p := dialPlain(t, shared.Addr(), "catchHuman")
	p.oper() // no flood control: many messages share a timestamp
	p.join(ch)

	p.say(ch, "before 0")
	p.say(ch, "before 1")
	a.expectLine(0, "before 1")
	if a.out.Len() != 2 {
		t.Fatalf("stdout before the cut:\n%s", a.out.String())
	}

	px.Down()
	if _, ok := a.err.Wait("connection lost", 10*time.Second); !ok {
		t.Fatal("the cut was not noticed")
	}
	// 50 messages while A is away: bursts sharing a timestamp, and some
	// multiline. The page size of the test server is 20.
	const missed = 50
	for i := 0; i < missed; i++ {
		if i%10 == 5 {
			b.mustAs("publish", fmt.Sprintf("missed %d\nsecond line of %d", i, i))
			continue
		}
		p.say(ch, fmt.Sprintf("missed %d", i))
	}
	b.expectLine(0, fmt.Sprintf("missed %d", missed-1))

	// Keep posting while A reconnects and catches up.
	stop := make(chan struct{})
	var live int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			p.say(ch, fmt.Sprintf("live %d", live))
			live++
			time.Sleep(40 * time.Millisecond)
		}
	}()
	px.Restore()
	if _, ok := a.err.Wait("reconnected;", 30*time.Second); !ok {
		close(stop)
		t.Fatalf("no reconnect:\n%s", a.err.String())
	}
	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
	p.say(ch, "the end")
	a.expectLine(0, "the end")

	lines := a.out.Lines()[2:]
	nextMissed, nextLive := 0, 0
	replayedLive := 0
	for _, l := range lines[:len(lines)-1] {
		m := parseText(t, l)
		switch {
		case strings.Contains(m.text, "missed "):
			if nextLive > 0 {
				t.Fatalf("a missed message after a live one: %s", l)
			}
			if n := seq(t, m.text, "missed"); n != nextMissed {
				t.Fatalf("got missed %d, want missed %d (duplicate, loss or disorder)", n, nextMissed)
			}
			if !m.has("replayed") {
				t.Errorf("missed %d is not flagged replayed", nextMissed)
			}
			if nextMissed%10 == 5 && m.text != fmt.Sprintf(`missed %d\nsecond line of %d`, nextMissed, nextMissed) {
				t.Errorf("multiline message altered: %q", m.text)
			}
			if nextMissed%10 == 5 && (m.kind != "bot" || m.from != "catchB") {
				t.Errorf("replayed message from a bot: %+v", m)
			}
			nextMissed++
		case strings.Contains(m.text, "live "):
			if n := seq(t, m.text, "live"); n != nextLive {
				t.Fatalf("got live %d, want live %d (duplicate, loss or disorder)", n, nextLive)
			}
			if m.has("replayed") {
				replayedLive++
			}
			nextLive++
		default:
			t.Errorf("unexpected line: %s", l)
		}
	}
	if nextMissed != missed {
		t.Errorf("%d of %d missed messages were delivered", nextMissed, missed)
	}
	if nextLive != live {
		t.Errorf("%d of %d messages posted during the catch-up were delivered", nextLive, live)
	}
	t.Logf("%d messages were posted during the reconnect, %d of them came through history", live, replayedLive)
	if !strings.Contains(a.err.String(), "messages replayed") {
		t.Errorf("stderr:\n%s", a.err.String())
	}
	if strings.Contains(a.err.String(), "may have been missed") {
		t.Errorf("a gap was reported although history was complete:\n%s", a.err.String())
	}
}

// IT-CATCHUP-QUIET: a listener that has seen nothing since joining still has
// a reference to catch up from.
func TestCatchupFromJoin(t *testing.T) {
	ch := chanFor(t)
	px := startProxy(t, shared.Addr())
	a := startListener(t, lopts{nick: "quietA", channel: ch, addr: px.Addr(), wake: "addressed"})
	p := dialPlain(t, shared.Addr(), "quietHuman")
	p.join(ch)
	px.Down()
	if _, ok := a.err.Wait("connection lost", 10*time.Second); !ok {
		t.Fatal("the cut was not noticed")
	}
	p.say(ch, "quietA: one")
	p.say(ch, "not for the agent") // the wake filter applies to replays too
	p.say(ch, "quietA: two")
	p.say(ch, "[IMPORTANT] three")
	time.Sleep(300 * time.Millisecond)
	px.Restore()
	a.expectLine(0, "three")
	got := a.out.Lines()
	if len(got) != 3 {
		t.Fatalf("printed %d lines, want 3:\n%s", len(got), a.out.String())
	}
	for i, want := range []string{"quietA: one", "quietA: two", "three"} {
		if m := parseText(t, got[i]); m.text != want || !m.has("replayed") {
			t.Errorf("line %d: %+v", i, m)
		}
	}
	if _, ok := a.err.Wait("reconnected; 4 messages replayed", 5*time.Second); !ok {
		t.Errorf("stderr:\n%s", a.err.String())
	}
}

// IT-RESTART: the server restarts and its in-memory history is gone. The
// listener reconnects, prints nothing twice, says that messages may have
// been missed, and delivers new messages.
func TestServerRestart(t *testing.T) {
	srv := startErgo(t, ergo.Options{})
	ch := "#restart"
	a := startListener(t, lopts{nick: "restartA", channel: ch, addr: srv.Addr()})
	p := dialPlain(t, srv.Addr(), "restartHuman")
	p.join(ch)
	p.say(ch, "before the restart")
	a.expectLine(0, "before the restart")

	srv.Halt()
	if _, ok := a.err.Wait("connection lost", 10*time.Second); !ok {
		t.Fatal("the listener did not notice the server going away")
	}
	time.Sleep(time.Second)
	if err := srv.Resume(); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.err.Wait("reconnected;", 30*time.Second); !ok {
		t.Fatalf("no reconnect:\n%s", a.err.String())
	}
	if !strings.Contains(a.err.String(), "does not reach back to the disconnect") {
		t.Errorf("the lost history was not reported:\n%s", a.err.String())
	}
	p2 := dialPlain(t, srv.Addr(), "restartHuman")
	p2.join(ch)
	p2.say(ch, "after the restart")
	a.expectLine(1, "after the restart")
	if a.out.Len() != 2 {
		t.Errorf("stdout:\n%s", a.out.String())
	}
}

// IT-NOTCONN: a command while the listener is disconnected.
func TestNotConnected(t *testing.T) {
	ch := chanFor(t)
	px := startProxy(t, shared.Addr())
	a := startListener(t, lopts{nick: "notconnA", channel: ch, addr: px.Addr()})
	p := dialPlain(t, shared.Addr(), "notconnHuman")
	p.join(ch)

	px.Down()
	if _, ok := a.err.Wait("connection lost", 10*time.Second); !ok {
		t.Fatal("the cut was not noticed")
	}
	start := time.Now()
	r := a.as("publish", "into the void")
	waited := time.Since(start)
	if r.code != 7 || !strings.Contains(r.err, "not connected") || !strings.Contains(r.err, "message not sent") {
		t.Errorf("exit %d: %s", r.code, r.err)
	}
	if waited < 1500*time.Millisecond || waited > 5*time.Second {
		t.Errorf("waited %s before giving up, expected about the 2 s wait", waited)
	}
	if r := a.as("whos-there"); r.code != 7 {
		t.Errorf("whos-there while disconnected: exit %d: %s", r.code, r.err)
	}

	// A blip shorter than the wait: the command succeeds.
	done := make(chan result, 1)
	go func() { done <- a.as("publish", "worth the wait") }()
	time.Sleep(300 * time.Millisecond)
	px.Restore()
	if r := <-done; r.code != 0 {
		t.Errorf("publish during a short blip: exit %d: %s", r.code, r.err)
	}
	p.expect("worth the wait")
	for _, l := range p.lines.Lines() {
		if strings.Contains(l, "into the void") {
			t.Error("a message reported as not sent was delivered later")
		}
	}
}

// IT-TLS: certificate trust.
func TestCertificateTrust(t *testing.T) {
	srv := startErgo(t, ergo.Options{})
	ch := "#tls"
	xdg := t.TempDir()
	store := filepath.Join(xdg, "chatterbox", "known_servers")

	// First connection to a self-signed server: pinned, fingerprint shown.
	a := startListener(t, lopts{nick: "tlsA", channel: ch, addr: srv.Addr(), xdg: xdg})
	data, err := os.ReadFile(store)
	if err != nil || !strings.HasPrefix(string(data), srv.Addr()+" SHA256:") {
		t.Fatalf("trust file: %q (%v)", data, err)
	}
	fp := strings.TrimSpace(strings.TrimPrefix(string(data), srv.Addr()+" "))
	if !strings.Contains(a.err.String(), "fingerprint: "+fp) || !strings.Contains(a.err.String(), "first use") {
		t.Errorf("the fingerprint was not printed on first use:\n%s", a.err.String())
	}

	// The server comes back with a different certificate: the running
	// listener refuses to reconnect.
	srv.Halt()
	if err := srv.NewCertificate(); err != nil {
		t.Fatal(err)
	}
	if err := srv.Resume(); err != nil {
		t.Fatal(err)
	}
	if code := a.waitExit(30 * time.Second); code != 5 {
		t.Errorf("reconnect to a changed certificate: exit %d, want 5", code)
	}
	for _, want := range []string{"the certificate of " + srv.Addr() + " has changed", "trusted:   " + fp, "presented: SHA256:", "remove the " + srv.Addr(), store, "intercepting"} {
		if !strings.Contains(a.err.String(), want) {
			t.Errorf("explanation lacks %q:\n%s", want, a.err.String())
		}
	}
	// And so does a fresh listener.
	b := startListener(t, lopts{nick: "tlsB", channel: ch, addr: srv.Addr(), xdg: xdg, noWait: true})
	if code := b.waitExit(20 * time.Second); code != 5 {
		t.Errorf("fresh listener against a changed certificate: exit %d, want 5", code)
	}
	if after, _ := os.ReadFile(store); string(after) != string(data) {
		t.Errorf("the trust file was modified by a refused connection:\n%s", after)
	}

	// trust_on_first_use = false: an unverifiable certificate is refused,
	// with the fingerprint to configure.
	c := startListener(t, lopts{nick: "tlsC", channel: ch, addr: srv.Addr(), config: "[tls]\ntrust_on_first_use = false\n", noWait: true})
	if code := c.waitExit(20 * time.Second); code != 5 {
		t.Errorf("trust_on_first_use = false: exit %d, want 5", code)
	}
	if e := c.err.String(); !strings.Contains(e, "trust_on_first_use is off") || !strings.Contains(e, "fingerprint = \"") {
		t.Errorf("stderr:\n%s", e)
	}
	if _, err := os.Stat(filepath.Join(c.xdg, "chatterbox", "known_servers")); !os.IsNotExist(err) {
		t.Error("a certificate was pinned although trust_on_first_use is off")
	}

	// With the fingerprint configured it connects and writes no trust file.
	configured := strings.TrimPrefix(c.err.String()[strings.Index(c.err.String(), "fingerprint = \"")+len("fingerprint = \""):], "")
	configured = configured[:strings.Index(configured, "\"")]
	d := startListener(t, lopts{nick: "tlsD", channel: ch, addr: srv.Addr(), config: "[tls]\nfingerprint = \"" + strings.ToLower(configured) + "\"\ntrust_on_first_use = false\n"})
	if _, err := os.Stat(filepath.Join(d.xdg, "chatterbox", "known_servers")); !os.IsNotExist(err) {
		t.Error("the trust file was written although tls.fingerprint is set")
	}

	// A server recorded as CA-verified is never downgraded to a pin.
	caXDG := t.TempDir()
	os.MkdirAll(filepath.Join(caXDG, "chatterbox"), 0o700)
	os.WriteFile(filepath.Join(caXDG, "chatterbox", "known_servers"), []byte(srv.Addr()+" ca\n"), 0o600)
	e := startListener(t, lopts{nick: "tlsE", channel: ch, addr: srv.Addr(), xdg: caXDG, noWait: true})
	if code := e.waitExit(20 * time.Second); code != 5 {
		t.Errorf("ca entry, self-signed certificate: exit %d, want 5", code)
	}
	if !strings.Contains(e.err.String(), "no longer verifies") {
		t.Errorf("stderr:\n%s", e.err.String())
	}
}

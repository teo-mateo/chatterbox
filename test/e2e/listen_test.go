package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/teo-mateo/chatterbox/test/ergo"
)

// chanFor gives every test its own channel on the shared server.
func chanFor(t *testing.T) string {
	return "#" + strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
}

// IT-JOIN: the listener is in the channel's names; a second --channel, or a
// list, is rejected.
func TestJoin(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "joinA", channel: ch})
	p := dialPlain(t, shared.Addr(), "joinHuman")
	p.join(ch)
	if names := p.names(ch); !contains(names, "joinA") {
		t.Errorf("joinA not in %s: %v", ch, names)
	}
	if a.out.Len() != 0 {
		t.Errorf("stdout not empty after join: %q", a.out.Lines())
	}

	env := cmdEnv(shortDir(t), a.xdg)
	for _, args := range [][]string{
		{"listen", "--as", "x", "--channel", "#a", "--channel", "#b"},
		{"listen", "--as", "x", "--channel", "#a,#b"},
	} {
		r := runCmd(t, env, "", args...)
		if r.code != 2 || !strings.Contains(r.err, "one channel") {
			t.Errorf("%v: exit %d, stderr %q", args, r.code, r.err)
		}
	}
	// Usage errors of listen itself.
	for _, args := range [][]string{
		{"listen", "--channel", "#a"},
		{"listen", "--as", "9bad", "--channel", "#a"},
		{"listen", "--as", "x", "--channel", "nohash"},
		{"listen", "--as", "x", "--channel", "#a", "--wake", "sometimes"},
		{"listen", "--as", "x"},
	} {
		if r := runCmd(t, env, "", args...); r.code != 2 {
			t.Errorf("%v: exit %d, want 2 (%s)", args, r.code, r.err)
		}
	}
}

// IT-BOT: the plain client sees the listener flagged as a bot in WHO.
func TestBotMode(t *testing.T) {
	ch := chanFor(t)
	startListener(t, lopts{nick: "botA", channel: ch})
	p := dialPlain(t, shared.Addr(), "botHuman")
	p.join(ch)
	p.raw("WHO " + ch)
	// :server 352 me #chan user host server nick flags :hops realname
	who := func(nick string) string {
		line, _ := p.expectFrom(0, func(s string) bool {
			f := strings.Fields(s)
			return len(f) > 8 && f[1] == "352" && f[7] == nick
		})
		return strings.Fields(line)[8]
	}
	if flags := who("botA"); !strings.Contains(flags, "B") {
		t.Errorf("botA's WHO flags are %q, expected the bot flag B", flags)
	}
	if flags := who("botHuman"); strings.Contains(flags, "B") {
		t.Errorf("the plain client is flagged as a bot: %q", flags)
	}
}

// IT-NICK: a second listener with a taken nickname exits 4 naming the
// nickname, and no other nickname appears on the server.
func TestNickInUse(t *testing.T) {
	ch := chanFor(t)
	p := dialPlain(t, shared.Addr(), "takenNick")
	p.join(ch)
	l := startListener(t, lopts{nick: "takenNick", channel: ch, noWait: true})
	if code := l.waitExit(20 * time.Second); code != 4 {
		t.Fatalf("exit code %d, want 4; stderr:\n%s", code, l.err.String())
	}
	if !strings.Contains(l.err.String(), "nickname takenNick is already in use") {
		t.Errorf("stderr: %s", l.err.String())
	}
	if names := p.names(ch); len(names) != 1 {
		t.Errorf("someone else joined %s: %v", ch, names)
	}
	for _, line := range p.lines.Lines() {
		if strings.Contains(line, " JOIN ") && !strings.HasPrefix(line, ":takenNick!") {
			t.Errorf("another nickname joined: %s", line)
		}
	}
	if l.out.Len() != 0 {
		t.Errorf("stdout: %q", l.out.Lines())
	}
}

// IT-CAPS: against a server without multiline, the listener exits with an
// error naming the capability.
func TestMissingCapability(t *testing.T) {
	srv := startErgo(t, ergo.Options{NoMultiline: true})
	l := startListener(t, lopts{nick: "capA", channel: "#caps", addr: srv.Addr(), noWait: true})
	if code := l.waitExit(20 * time.Second); code != 9 {
		t.Errorf("exit code %d, want 9", code)
	}
	if !strings.Contains(l.err.String(), "draft/multiline") {
		t.Errorf("stderr does not name the capability: %s", l.err.String())
	}
}

// IT-FIRSTFAIL: unknown host and closed port end the listener promptly with
// exit 9 and the reason (D16).
func TestFirstConnectionFailureIsFatal(t *testing.T) {
	for name, addr := range map[string]string{
		"unknown host": "no-such-host.invalid:6697",
		"closed port":  "127.0.0.1:1",
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			l := startListener(t, lopts{nick: "failA", channel: "#x", addr: addr, noWait: true})
			if code := l.waitExit(20 * time.Second); code != 9 {
				t.Errorf("exit code %d, want 9", code)
			}
			if time.Since(start) > 10*time.Second {
				t.Errorf("took %s to give up", time.Since(start))
			}
			if !strings.Contains(l.err.String(), "cannot connect to") {
				t.Errorf("stderr: %s", l.err.String())
			}
			if l.out.Len() != 0 {
				t.Errorf("stdout: %q", l.out.Lines())
			}
		})
	}
}

// IT-EXIT: termination by signal, by kill, by a vanished reader and by a
// vanished parent all make the nickname leave.
func TestExit(t *testing.T) {
	watch := func(t *testing.T, ch string) *plain {
		p := dialPlain(t, shared.Addr(), "ex"+strings.ToLower(strings.ReplaceAll(t.Name()[len("TestExit/"):], " ", ""))+"W")
		p.join(ch)
		return p
	}

	t.Run("sigterm", func(t *testing.T) {
		ch := chanFor(t)
		p := watch(t, ch)
		l := startListener(t, lopts{nick: "exitTerm", channel: ch})
		l.cmd.Process.Signal(syscall.SIGTERM)
		if code := l.waitExit(10 * time.Second); code != 0 {
			t.Errorf("exit code %d, want 0", code)
		}
		p.expectFrom(0, func(s string) bool {
			return strings.HasPrefix(s, ":exitTerm!") && strings.Contains(s, " QUIT ") && strings.Contains(s, "chatterbox listener stopped")
		})
		if _, err := os.Stat(filepath.Join(l.runtime, "exitterm.sock")); !os.IsNotExist(err) {
			t.Error("socket left behind after a clean exit")
		}
		if _, err := os.Stat(filepath.Join(l.runtime, "exitterm.lock")); err != nil {
			t.Error("lock file must stay after exit")
		}
	})

	t.Run("sigint", func(t *testing.T) {
		ch := chanFor(t)
		l := startListener(t, lopts{nick: "exitInt", channel: ch})
		l.cmd.Process.Signal(syscall.SIGINT)
		if code := l.waitExit(10 * time.Second); code != 0 {
			t.Errorf("exit code %d, want 0", code)
		}
	})

	t.Run("sigkill", func(t *testing.T) {
		ch := chanFor(t)
		p := watch(t, ch)
		l := startListener(t, lopts{nick: "exitKill", channel: ch})
		l.cmd.Process.Kill()
		l.waitExit(10 * time.Second)
		p.expectFrom(0, func(s string) bool { return strings.HasPrefix(s, ":exitKill!") && strings.Contains(s, " QUIT ") })
	})

	// The reader closes the pipe while the channel is silent: no write
	// ever fails, so only polling for hang-up can notice.
	t.Run("reader gone", func(t *testing.T) {
		ch := chanFor(t)
		p := watch(t, ch)
		l := startListener(t, lopts{nick: "exitPipe", channel: ch})
		l.outPipe.Close()
		if code := l.waitExit(10 * time.Second); code != 0 {
			t.Errorf("exit code %d, want 0 (not death by SIGPIPE)", code)
		}
		p.expectFrom(0, func(s string) bool {
			return strings.HasPrefix(s, ":exitPipe!") && strings.Contains(s, " QUIT ") && strings.Contains(s, "chatterbox listener stopped")
		})
	})

	// A harness that redirects background output to a file: when the
	// process that started the listener dies, the listener leaves.
	t.Run("parent gone", func(t *testing.T) {
		ch := chanFor(t)
		p := watch(t, ch)
		o := lopts{nick: "exitOrphan", channel: ch}
		o.prepare(t)
		outFile := filepath.Join(t.TempDir(), "out.log")
		// The wrapper shell stands in for the harness.
		script := `"$0" listen --as exitOrphan --channel "$1" > "$2" 2>&1 & wait`
		wrapper := exec.Command("sh", "-c", script, binary, ch, outFile)
		wrapper.Env = cmdEnv(o.runtime, o.xdg)
		wrapper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := wrapper.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { syscall.Kill(-wrapper.Process.Pid, syscall.SIGKILL); wrapper.Wait() })
		p.expectFrom(0, func(s string) bool { return strings.HasPrefix(s, ":exitOrphan!") && strings.Contains(s, " JOIN ") })
		wrapper.Process.Kill()
		wrapper.Wait()
		p.expectFrom(0, func(s string) bool {
			return strings.HasPrefix(s, ":exitOrphan!") && strings.Contains(s, " QUIT ") && strings.Contains(s, "chatterbox listener stopped")
		})
	})
}

// IT-LOCK: one listener per nickname per machine.
func TestLock(t *testing.T) {
	ch := chanFor(t)
	a := startListener(t, lopts{nick: "lockA", channel: ch})

	second := startListener(t, lopts{nick: "lockA", channel: ch, runtime: a.runtime, xdg: a.xdg, noWait: true})
	if code := second.waitExit(10 * time.Second); code != 4 {
		t.Errorf("second listener: exit %d, want 4", code)
	}
	if !strings.Contains(second.err.String(), "a listener for lockA is already running (pid ") {
		t.Errorf("stderr: %s", second.err.String())
	}
	if r := a.as("whos-there"); r.code != 0 {
		t.Errorf("the first listener stopped working after the second was refused: %s", r.err)
	}

	// A stale socket (no lock holder) is replaced.
	a.cmd.Process.Kill()
	a.waitExit(5 * time.Second)
	if _, err := os.Stat(filepath.Join(a.runtime, "locka.sock")); err != nil {
		t.Fatalf("expected a stale socket after SIGKILL: %v", err)
	}
	b := startListener(t, lopts{nick: "lockA", channel: ch, runtime: a.runtime, xdg: a.xdg})
	if r := b.as("whos-there"); r.code != 0 {
		t.Errorf("listener started over a stale socket does not answer: %s", r.err)
	}
}

// IT-LOCK: 20 listeners started at once for one nickname leave exactly one.
func TestLockRace(t *testing.T) {
	ch := chanFor(t)
	o := lopts{nick: "raceA", channel: ch}
	o.prepare(t)
	const n = 20
	ls := make([]*listener, n)
	var wg sync.WaitGroup
	for i := range ls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ls[i] = startListener(t, lopts{nick: "raceA", channel: ch, runtime: o.runtime, xdg: o.xdg, noWait: true})
		}(i)
	}
	wg.Wait()
	deadline := time.Now().Add(25 * time.Second)
	running := n
	for time.Now().Before(deadline) {
		running = 0
		joined := 0
		for _, l := range ls {
			if l.running() {
				running++
				if strings.Contains(l.err.String(), "joined ") {
					joined++
				}
			}
		}
		if running == 1 && joined == 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if running != 1 {
		t.Fatalf("%d listeners running for one nickname, want exactly 1", running)
	}
	for _, l := range ls {
		if !l.running() && l.code != 4 {
			t.Errorf("a losing listener exited %d, want 4: %s", l.code, l.err.String())
		}
	}
}

// IT-PERM: the socket and its directory are private.
func TestSocketPermissions(t *testing.T) {
	base := shortDir(t)
	rt := filepath.Join(base, "rt") // created by the listener
	a := startListener(t, lopts{nick: "permA", channel: chanFor(t), runtime: rt})
	di, err := os.Stat(rt)
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Errorf("runtime directory mode %v, want 0700 (%v)", di.Mode().Perm(), err)
	}
	si, err := os.Stat(filepath.Join(rt, "perma.sock"))
	if err != nil || si.Mode()&os.ModeSocket == 0 || si.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v, want a socket with 0600 (%v)", si.Mode(), err)
	}
	_ = a

	// A runtime directory with wider permissions is refused.
	wide := filepath.Join(base, "wide")
	os.Mkdir(wide, 0o755)
	os.Chmod(wide, 0o755)
	l := startListener(t, lopts{nick: "permB", channel: chanFor(t), runtime: wide, noWait: true})
	if code := l.waitExit(10 * time.Second); code != 2 || !strings.Contains(l.err.String(), "mode") {
		t.Errorf("wide runtime dir: exit %d, stderr %s", code, l.err.String())
	}
}

// IT-CONFIG: server settings come from the config file; flags override.
func TestConfigAndFlags(t *testing.T) {
	ch := chanFor(t)
	host, port := splitAddr(t, shared.Addr())

	// Everything from the file, including channel, wake filter and format.
	a := startListener(t, lopts{nick: "cfgA", config: "[defaults]\nchannel = \"" + ch + "\"\nwake = \"addressed\"\nformat = \"json\"\n"})
	if !strings.Contains(a.err.String(), "joined "+ch+" as cfgA") {
		t.Errorf("stderr: %s", a.err.String())
	}
	p := dialPlain(t, shared.Addr(), "cfgHuman")
	p.join(ch)
	p.say(ch, "unaddressed")
	p.say(ch, "cfgA: addressed")
	line, _ := a.expectLine(0, "addressed")
	if !strings.HasPrefix(line, `{"time":"`) || strings.Contains(a.out.String(), "unaddressed") {
		t.Errorf("config defaults not applied: %s", a.out.String())
	}

	// The file points at a dead port; flags override host and port.
	b := startListener(t, lopts{nick: "cfgB", channel: ch, addr: "127.0.0.1:1", args: []string{"--server", host, "--port", port}})
	if !b.running() {
		t.Error("flags did not override the config file")
	}

	// No file at all, flags only.
	env := cmdEnv(shortDir(t), t.TempDir())
	r := runCmd(t, env, "", "listen", "--as", "cfgC", "--channel", ch)
	if r.code != 2 || !strings.Contains(r.err, "no server configured") || !strings.Contains(r.err, "config.toml") {
		t.Errorf("missing server: exit %d: %s", r.code, r.err)
	}
}

// Package e2e holds the integration and acceptance tests. They run the
// compiled chatterbox binary as subprocesses against a private Ergo and
// read its stdout and stderr: they test what an agent sees.
package e2e

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
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

var (
	binary string       // the chatterbox binary under test
	shared *ergo.Server // one Ergo for the tests that need nothing special
)

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	tmp, err := os.MkdirTemp("", "cbx-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)
	binary = filepath.Join(tmp, "chatterbox")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/chatterbox")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot build chatterbox: %v\n%s", err, out)
		return 1
	}
	shared, err = ergo.Start(ergo.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot start Ergo:", err)
		return 1
	}
	defer shared.Stop()
	return m.Run()
}

// startErgo starts a dedicated server for one test.
func startErgo(t *testing.T, o ergo.Options) *ergo.Server {
	t.Helper()
	s, err := ergo.Start(o)
	if err != nil {
		t.Fatalf("cannot start Ergo: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

// lineBuf collects lines as they arrive and lets tests wait for one.
type lineBuf struct {
	mu    sync.Mutex
	cond  *sync.Cond
	lines []string
	eof   bool
}

func newLineBuf() *lineBuf {
	b := &lineBuf{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *lineBuf) add(line string) {
	b.mu.Lock()
	b.lines = append(b.lines, line)
	b.mu.Unlock()
	b.cond.Broadcast()
}

func (b *lineBuf) close() {
	b.mu.Lock()
	b.eof = true
	b.mu.Unlock()
	b.cond.Broadcast()
}

func (b *lineBuf) consume(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		b.add(sc.Text())
	}
	b.close()
}

// Lines returns a copy of everything received so far.
func (b *lineBuf) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.lines...)
}

func (b *lineBuf) String() string { return strings.Join(b.Lines(), "\n") }

// WaitFrom waits for a line at index >= from that satisfies pred and
// returns it with its index.
func (b *lineBuf) WaitFrom(from int, timeout time.Duration, pred func(string) bool) (string, int, bool) {
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, b.cond.Broadcast)
	defer timer.Stop()
	b.mu.Lock()
	defer b.mu.Unlock()
	i := from
	for {
		for ; i < len(b.lines); i++ {
			if pred(b.lines[i]) {
				return b.lines[i], i, true
			}
		}
		if b.eof || !time.Now().Before(deadline) {
			return "", -1, false
		}
		b.cond.Wait()
	}
}

// Wait waits for any line containing sub.
func (b *lineBuf) Wait(sub string, timeout time.Duration) (string, bool) {
	l, _, ok := b.WaitFrom(0, timeout, func(s string) bool { return strings.Contains(s, sub) })
	return l, ok
}

// WaitCount waits until at least n lines have arrived.
func (b *lineBuf) WaitCount(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, b.cond.Broadcast)
	defer timer.Stop()
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.lines) < n {
		if b.eof || !time.Now().Before(deadline) {
			return false
		}
		b.cond.Wait()
	}
	return true
}

func (b *lineBuf) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

// shortDir makes a temporary directory with a short path: socket paths are
// limited to about 100 bytes, which t.TempDir can exceed.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "cbx")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// fastTuning shortens every timing so tests run in seconds. The keepalive
// stays well under the server's flood-control rate of two commands per
// second: a faster one would by itself keep the connection throttled.
const fastTuning = `
[tuning]
keepalive_interval = "2s"
idle_timeout = "5s"
backoff_min = "200ms"
backoff_max = "1s"
backoff_reset = "3s"
ready_wait = "2s"
hangup_poll = "200ms"
quit_wait = "1s"
nick_retry_window = "40s"
`

// lopts describe a listener to start.
type lopts struct {
	nick    string
	channel string
	addr    string // host:port; default: the shared Ergo
	wake    string
	json    bool
	// runtime is the runtime directory; empty makes a private one.
	runtime string
	// xdg is the config home; empty makes a private one.
	xdg string
	// config is put in front of the generated config file.
	config string
	// tuning replaces fastTuning when set.
	tuning string
	args   []string
	// noWait returns without waiting for "joined".
	noWait bool
	// stdout, when set, receives the listener's stdout instead of a pipe.
	stdout *os.File
}

// listener is a running `chatterbox listen`.
type listener struct {
	t       *testing.T
	nick    string
	cmd     *exec.Cmd
	out     *lineBuf
	err     *lineBuf
	runtime string
	xdg     string
	outPipe io.ReadCloser

	exited chan struct{}
	code   int
}

func splitAddr(t *testing.T, addr string) (string, string) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

// writeConfig writes the config file a listener will read.
func (o *lopts) prepare(t *testing.T) {
	t.Helper()
	if o.addr == "" {
		o.addr = shared.Addr()
	}
	if o.runtime == "" {
		o.runtime = shortDir(t)
	}
	if o.xdg == "" {
		o.xdg = t.TempDir()
	}
	host, port := splitAddr(t, o.addr)
	tuning := o.tuning
	if tuning == "" {
		tuning = fastTuning
	}
	cfg := fmt.Sprintf("%s\n[server]\nhost = %q\nport = %s\n%s", o.config, host, port, tuning)
	dir := filepath.Join(o.xdg, "chatterbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Listeners sharing a directory may be starting while this is written:
	// replace the file in one step, so none reads it half-written.
	tmp, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.WriteString(cfg); err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), filepath.Join(dir, "config.toml")); err != nil {
		t.Fatal(err)
	}
}

func cmdEnv(runtime, xdg string, extra ...string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + xdg,
		"XDG_CONFIG_HOME=" + xdg,
		"CHATTERBOX_RUNTIME_DIR=" + runtime,
	}
	return append(env, extra...)
}

// startListener runs `chatterbox listen` and, unless noWait is set, waits
// until it has joined its channel.
func startListener(t *testing.T, o lopts) *listener {
	t.Helper()
	o.prepare(t)
	args := []string{"listen", "--as", o.nick}
	if o.channel != "" {
		args = append(args, "--channel", o.channel)
	}
	if o.wake != "" {
		args = append(args, "--wake", o.wake)
	}
	if o.json {
		args = append(args, "--json")
	}
	args = append(args, o.args...)
	cmd := exec.Command(binary, args...)
	cmd.Env = cmdEnv(o.runtime, o.xdg)
	l := &listener{t: t, nick: o.nick, cmd: cmd, out: newLineBuf(), err: newLineBuf(),
		runtime: o.runtime, xdg: o.xdg, exited: make(chan struct{})}
	if o.stdout != nil {
		cmd.Stdout = o.stdout
		l.out.close()
	} else {
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		l.outPipe = pipe
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var readers sync.WaitGroup
	if l.outPipe != nil {
		readers.Add(1)
		go func() { defer readers.Done(); l.out.consume(l.outPipe) }()
	}
	readers.Add(1)
	go func() { defer readers.Done(); l.err.consume(errPipe) }()
	go func() {
		readers.Wait()
		cmd.Wait()
		l.code = cmd.ProcessState.ExitCode()
		close(l.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-l.exited:
		default:
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-l.exited:
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				<-l.exited
			}
		}
		if t.Failed() {
			t.Logf("listener %s stderr:\n%s", o.nick, l.err.String())
			if o.stdout == nil {
				t.Logf("listener %s stdout:\n%s", o.nick, l.out.String())
			}
		}
	})
	if !o.noWait {
		l.waitJoined()
	}
	return l
}

func (l *listener) waitJoined() {
	l.t.Helper()
	if _, ok := l.err.Wait("joined ", 20*time.Second); !ok {
		l.t.Fatalf("listener %s did not join:\n%s", l.nick, l.err.String())
	}
}

// waitExit waits for the listener to end and returns its exit code.
func (l *listener) waitExit(timeout time.Duration) int {
	l.t.Helper()
	select {
	case <-l.exited:
		return l.code
	case <-time.After(timeout):
		l.t.Fatalf("listener %s still running after %s; stderr:\n%s", l.nick, timeout, l.err.String())
		return -1
	}
}

func (l *listener) running() bool {
	select {
	case <-l.exited:
		return false
	default:
		return true
	}
}

// expectLine waits for a stdout line containing sub, starting at index from.
func (l *listener) expectLine(from int, sub string) (string, int) {
	l.t.Helper()
	line, i, ok := l.out.WaitFrom(from, 15*time.Second, func(s string) bool { return strings.Contains(s, sub) })
	if !ok {
		l.t.Fatalf("listener %s printed no line containing %q; stdout:\n%s\nstderr:\n%s", l.nick, sub, l.out.String(), l.err.String())
	}
	return line, i
}

// expectQuiet fails if stdout gains a line within d.
func (l *listener) expectQuiet(d time.Duration) {
	l.t.Helper()
	n := l.out.Len()
	time.Sleep(d)
	if lines := l.out.Lines(); len(lines) != n {
		l.t.Fatalf("listener %s printed unexpected lines:\n%s", l.nick, strings.Join(lines[n:], "\n"))
	}
}

// result is the outcome of a short-lived command.
type result struct {
	out, err string
	code     int
}

// run executes a chatterbox command with the listener's environment.
func (l *listener) run(args ...string) result {
	l.t.Helper()
	return runCmd(l.t, cmdEnv(l.runtime, l.xdg), "", args...)
}

// as runs a command with --as <own nick> appended.
func (l *listener) as(args ...string) result {
	l.t.Helper()
	return l.run(append(args, "--as", l.nick)...)
}

func (l *listener) mustAs(args ...string) result {
	l.t.Helper()
	r := l.as(args...)
	if r.code != 0 {
		l.t.Fatalf("chatterbox %s (as %s) exited %d: %s", strings.Join(args, " "), l.nick, r.code, r.err)
	}
	return r
}

func runCmd(t *testing.T, env []string, stdin string, args ...string) result {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Minute):
		cmd.Process.Kill()
		t.Fatalf("chatterbox %s did not finish", strings.Join(args, " "))
	}
	return result{out: out.String(), err: errb.String(), code: cmd.ProcessState.ExitCode()}
}

// textLine is a parsed line of the default output format.
type textLine struct {
	time, channel, from, kind, flags, text string
}

func parseText(t *testing.T, line string) textLine {
	t.Helper()
	head, text, ok := strings.Cut(line, " | ")
	f := strings.Fields(head)
	if !ok || len(f) != 5 {
		t.Fatalf("not a message line: %q", line)
	}
	return textLine{time: f[0], channel: f[1], from: f[2], kind: f[3], flags: f[4], text: text}
}

func (tl textLine) has(flag string) bool {
	for _, f := range strings.Split(tl.flags, ",") {
		if f == flag {
			return true
		}
	}
	return false
}

// plain is a minimal IRC client that negotiates no capabilities at all. It
// stands in for "a human using any standard IRC client".
type plain struct {
	t     *testing.T
	nick  string
	conn  net.Conn
	lines *lineBuf
	wmu   sync.Mutex
}

func dialPlain(t *testing.T, addr, nick string) *plain {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("plain client cannot connect: %v", err)
	}
	p := &plain{t: t, nick: nick, conn: conn, lines: newLineBuf()}
	t.Cleanup(func() { conn.Close() })
	go func() {
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "PING ") {
				p.raw("PONG " + line[5:])
				continue
			}
			p.lines.add(line)
		}
		p.lines.close()
	}()
	p.raw("NICK " + nick)
	p.raw("USER " + nick + " 0 * :a human")
	p.expect(" 001 ")
	p.expectFrom(0, func(s string) bool { return strings.Contains(s, " 376 ") || strings.Contains(s, " 422 ") })
	return p
}

func (p *plain) raw(line string) {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(p.conn, line+"\r\n")
}

func (p *plain) expectFrom(from int, pred func(string) bool) (string, int) {
	p.t.Helper()
	line, i, ok := p.lines.WaitFrom(from, 20*time.Second, pred)
	if !ok {
		p.t.Fatalf("plain client %s: expected line did not arrive; received:\n%s", p.nick, p.lines.String())
	}
	return line, i
}

func (p *plain) expect(sub string) string {
	p.t.Helper()
	line, _ := p.expectAfter(0, sub)
	return line
}

// expectAfter waits for a line containing sub at index >= from.
func (p *plain) expectAfter(from int, sub string) (string, int) {
	p.t.Helper()
	line, i, ok := p.lines.WaitFrom(from, 20*time.Second, func(s string) bool { return strings.Contains(s, sub) })
	if !ok {
		p.t.Fatalf("plain client %s: no line containing %q; received:\n%s", p.nick, sub, p.lines.String())
	}
	return line, i
}

func (p *plain) join(channel string) {
	p.t.Helper()
	p.raw("JOIN " + channel)
	p.expectFrom(0, func(s string) bool { return strings.Contains(s, " 366 ") && strings.Contains(s, " "+channel+" ") })
}

func (p *plain) say(target, text string) { p.raw("PRIVMSG " + target + " :" + text) }

// oper makes the client a server operator, which also exempts it from
// flood control.
func (p *plain) oper() {
	p.t.Helper()
	p.raw("OPER " + ergo.OperName + " " + ergo.OperPass)
	p.expect(" 381 ")
}

// names returns the nicknames in a channel, without prefixes.
func (p *plain) names(channel string) []string {
	p.t.Helper()
	from := p.lines.Len()
	p.raw("NAMES " + channel)
	_, end := p.expectFrom(from, func(s string) bool { return strings.Contains(s, " 366 ") })
	var out []string
	for _, l := range p.lines.Lines()[from:end] {
		if !strings.Contains(l, " 353 ") {
			continue
		}
		_, list, _ := strings.Cut(l[1:], " :")
		for _, n := range strings.Fields(list) {
			out = append(out, strings.TrimLeft(n, "~&@%+"))
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// eventually polls cond until it holds or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// proxy is a TCP proxy between a listener and Ergo that counts accepted
// connections and can cut, black-hole and refuse them.
type proxy struct {
	t      *testing.T
	ln     net.Listener
	target string

	mu        sync.Mutex
	accepted  int
	pairs     []*pair
	blackhole bool
	down      bool
}

type pair struct {
	client, server net.Conn
	mu             sync.Mutex
	muted          bool
}

func (p *pair) isMuted() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.muted }
func (p *pair) mute()         { p.mu.Lock(); p.muted = true; p.mu.Unlock() }
func (p *pair) close()        { p.client.Close(); p.server.Close() }

func startProxy(t *testing.T, target string) *proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{t: t, ln: ln, target: target}
	t.Cleanup(func() {
		ln.Close()
		p.Cut()
	})
	go p.accept()
	return p
}

func (p *proxy) Addr() string { return p.ln.Addr().String() }

func (p *proxy) accept() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		p.accepted++
		down, hole := p.down, p.blackhole
		p.mu.Unlock()
		if down {
			c.Close()
			continue
		}
		s, err := net.DialTimeout("tcp", p.target, 5*time.Second)
		if err != nil {
			c.Close()
			continue
		}
		pr := &pair{client: c, server: s, muted: hole}
		p.mu.Lock()
		p.pairs = append(p.pairs, pr)
		p.mu.Unlock()
		go pipe(pr, c, s)
		go pipe(pr, s, c)
	}
}

// pipe copies src to dst. A muted pair keeps both sockets open and silently
// discards what arrives: neither side learns the other is unreachable.
func pipe(pr *pair, src, dst net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 && !pr.isMuted() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				pr.close()
				return
			}
		}
		if err != nil {
			if pr.isMuted() {
				// Keep the other side open: to the server a
				// black-holed client is still there.
				src.Close()
				return
			}
			pr.close()
			return
		}
	}
}

// Accepted is the number of connections accepted so far.
func (p *proxy) Accepted() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accepted
}

// Cut closes every current connection; new ones still work.
func (p *proxy) Cut() {
	p.mu.Lock()
	pairs := p.pairs
	p.pairs = nil
	p.mu.Unlock()
	for _, pr := range pairs {
		pr.close()
	}
}

// Blackhole makes current and new connections go silent without closing.
func (p *proxy) Blackhole() {
	p.mu.Lock()
	p.blackhole = true
	for _, pr := range p.pairs {
		pr.mute()
	}
	p.mu.Unlock()
}

// Down cuts every connection and refuses new ones.
func (p *proxy) Down() {
	p.mu.Lock()
	p.down = true
	p.mu.Unlock()
	p.Cut()
}

// Restore lets new connections through again. Connections black-holed
// before stay silent: to the server they are sessions that stopped
// answering, which it drops on its own schedule.
func (p *proxy) Restore() {
	p.mu.Lock()
	p.blackhole, p.down = false, false
	p.mu.Unlock()
}

// startStuckListener starts a listener whose stdout is a pipe nobody reads.
func startStuckListener(t *testing.T, o lopts) *listener {
	t.Helper()
	o.prepare(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	cmd := exec.Command(binary, "listen", "--as", o.nick, "--channel", o.channel)
	cmd.Env = cmdEnv(o.runtime, o.xdg)
	cmd.Stdout = w
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	l := &listener{t: t, nick: o.nick, cmd: cmd, out: newLineBuf(), err: newLineBuf(),
		runtime: o.runtime, xdg: o.xdg, exited: make(chan struct{})}
	go func() {
		l.err.consume(errPipe)
		cmd.Wait()
		l.code = cmd.ProcessState.ExitCode()
		close(l.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-l.exited:
		default:
			cmd.Process.Kill()
			<-l.exited
		}
		if t.Failed() {
			t.Logf("listener %s stderr:\n%s", o.nick, l.err.String())
		}
	})
	l.waitJoined()
	return l
}

const sigterm = syscall.SIGTERM

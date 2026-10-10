package listener_test

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teo-mateo/chatterbox/internal/config"
	"github.com/teo-mateo/chatterbox/internal/control"
	"github.com/teo-mateo/chatterbox/internal/identity"
	"github.com/teo-mateo/chatterbox/internal/irc"
	"github.com/teo-mateo/chatterbox/internal/listener"
)

// fakeServer is a scripted IRC server for behavior a real server cannot be
// made to show on demand: missing tokens, unlabeled failures, silence.
type fakeServer struct {
	t  *testing.T
	ln net.Listener

	// Hook sees every line first; returning true suppresses the default
	// reaction.
	hook func(c *fakeConn, m irc.Message) bool
	// caps and isupport are what the server advertises.
	caps     string
	isupport string

	mu    sync.Mutex
	conns []*fakeConn
}

type fakeConn struct {
	s      *fakeServer
	c      net.Conn
	n      int // connection number, from 1
	nick   string
	nickMu sync.Mutex
	wmu    sync.Mutex
	batch  map[string]*fakeBatch
	msgSeq int
}

type fakeBatch struct {
	label, target string
	lines         []string
}

const (
	defaultCaps     = "message-tags server-time batch draft/multiline=max-bytes=4096,max-lines=100 draft/chathistory draft/event-playback echo-message labeled-response standard-replies multi-prefix"
	defaultISupport = "BOT=B CASEMAPPING=ascii CHANTYPES=# CHANNELLEN=64 CHATHISTORY=100 NICKLEN=32 PREFIX=(qaohv)~&@%+"
)

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{t: t, ln: ln, caps: defaultCaps, isupport: defaultISupport}
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		for _, c := range s.conns {
			c.c.Close()
		}
		s.mu.Unlock()
	})
	go s.accept()
	return s
}

func (s *fakeServer) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		fc := &fakeConn{s: s, c: c, n: len(s.conns) + 1, batch: map[string]*fakeBatch{}}
		s.conns = append(s.conns, fc)
		s.mu.Unlock()
		go fc.serve()
	}
}

// connections is how many connections have been accepted.
func (s *fakeServer) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// nicks returns the nickname each connection registered with.
func (s *fakeServer) nicks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.conns {
		c.nickMu.Lock()
		out = append(out, c.nick)
		c.nickMu.Unlock()
	}
	return out
}

// last returns the most recent connection.
func (s *fakeServer) last() *fakeConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) == 0 {
		return nil
	}
	return s.conns[len(s.conns)-1]
}

func (c *fakeConn) send(format string, args ...any) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	fmt.Fprintf(c.c, format+"\r\n", args...)
}

func (c *fakeConn) now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

func (c *fakeConn) id() string {
	c.msgSeq++
	return fmt.Sprintf("m%d-%d", c.n, c.msgSeq)
}

func (c *fakeConn) mask() string { return c.nick + "!~u@fake.host" }

func (c *fakeConn) serve() {
	defer c.c.Close()
	sc := bufio.NewScanner(c.c)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		m, err := irc.ParseLine(sc.Text())
		if err != nil {
			continue
		}
		if c.s.hook != nil && c.s.hook(c, m) {
			continue
		}
		c.react(m)
	}
}

// react is the well-behaved server.
func (c *fakeConn) react(m irc.Message) {
	label := irc.Tag(&m, "label")
	lt := ""
	if label != "" {
		lt = "label=" + label + ";"
	}
	switch m.Command {
	case "CAP":
		switch irc.Param(&m, 0) {
		case "LS":
			c.send(":fake CAP * LS :%s", c.s.caps)
		case "REQ":
			c.send(":fake CAP * ACK :%s", irc.Param(&m, 1))
		case "END":
			c.send(":fake 001 %s :Welcome", c.nick)
			c.send(":fake 005 %s %s :are supported by this server", c.nick, c.s.isupport)
			c.send(":fake 376 %s :End of MOTD", c.nick)
		}
	case "NICK":
		c.nickMu.Lock()
		c.nick = irc.Param(&m, 0)
		c.nickMu.Unlock()
	case "MODE":
		c.send("@%stime=%s :%s MODE %s %s", lt, c.now(), c.mask(), c.nick, irc.Param(&m, 1))
	case "JOIN":
		ch := irc.Param(&m, 0)
		c.send("@time=%s;msgid=%s :%s JOIN %s", c.now(), c.id(), c.mask(), ch)
		c.send(":fake 353 %s = %s :@%s peer +voiced", c.nick, ch, c.nick)
		c.send(":fake 366 %s %s :End of NAMES list", c.nick, ch)
	case "PING":
		c.send(":fake PONG fake :%s", irc.Param(&m, -1))
	case "PRIVMSG":
		if ref := irc.Tag(&m, "batch"); ref != "" {
			if b := c.batch[ref]; b != nil {
				b.lines = append(b.lines, irc.Param(&m, -1))
			}
			return
		}
		c.send("@%stime=%s;msgid=%s :%s PRIVMSG %s :%s", lt, c.now(), c.id(), c.mask(), irc.Param(&m, 0), irc.Param(&m, -1))
	case "BATCH":
		ref := irc.Param(&m, 0)
		if strings.HasPrefix(ref, "+") {
			c.batch[ref[1:]] = &fakeBatch{label: label, target: irc.Param(&m, 2)}
			return
		}
		b := c.batch[ref[1:]]
		delete(c.batch, ref[1:])
		if b == nil {
			return
		}
		c.send("@label=%s;time=%s;msgid=%s :%s BATCH +e draft/multiline %s", b.label, c.now(), c.id(), c.mask(), b.target)
		for _, l := range b.lines {
			c.send("@batch=e :%s PRIVMSG %s :%s", c.mask(), b.target, l)
		}
		c.send(":%s BATCH -e", c.mask())
	case "CHATHISTORY":
		c.send("@%stime=%s :fake BATCH +h chathistory %s", lt, c.now(), irc.Param(&m, 1))
		c.send(":fake BATCH -h")
	case "QUIT":
		c.send("ERROR :Quit")
		c.c.Close()
	}
}

// run is a listener running in this process against a fake server.
type run struct {
	t      *testing.T
	sock   string
	stdout *lines
	stderr *safeBuffer
	done   chan *listener.ExitError
	outR   *os.File
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type lines struct {
	mu sync.Mutex
	l  []string
}

func (l *lines) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.l...)
}

// startRun starts a listener named agentA on #c. tune adjusts the timings.
func startRun(t *testing.T, s *fakeServer, tune func(*config.Tuning)) *run {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	rt, err := os.MkdirTemp("", "cbxl")
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(rt, 0o700)
	t.Cleanup(func() { os.RemoveAll(rt) })

	cfg := config.Default()
	addr := s.ln.Addr().(*net.TCPAddr)
	cfg.Server.Host, cfg.Server.Port = "127.0.0.1", addr.Port
	cfg.Defaults.Channel = "#c"
	ms := func(n int) config.Duration { return config.Duration(time.Duration(n) * time.Millisecond) }
	cfg.Tuning.KeepaliveInterval, cfg.Tuning.IdleTimeout = ms(200), ms(1500)
	cfg.Tuning.BackoffMin, cfg.Tuning.BackoffMax = ms(50), ms(200)
	cfg.Tuning.AckBase, cfg.Tuning.AckPerLine, cfg.Tuning.RequestTimeout = ms(400), ms(1), ms(400)
	cfg.Tuning.ReadyWait, cfg.Tuning.QuitWait, cfg.Tuning.RegisterTimeout = ms(500), ms(300), ms(2000)
	cfg.Tuning.NickRetryWindow = ms(1000)
	cfg.Tuning.HangupPoll = ms(50)
	if tune != nil {
		tune(&cfg.Tuning)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ru := &run{t: t, sock: identity.SocketPath(rt, "agentA"), stdout: &lines{}, stderr: &safeBuffer{},
		done: make(chan *listener.ExitError, 1), outR: r}
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			ru.stdout.mu.Lock()
			ru.stdout.l = append(ru.stdout.l, sc.Text())
			ru.stdout.mu.Unlock()
		}
	}()
	go func() {
		ru.done <- listener.Run(listener.Options{Nick: "agentA", Config: cfg, RuntimeDir: rt, Stdout: w, Stderr: ru.stderr, Verbose: true})
	}()
	t.Cleanup(func() {
		ru.stop()
		w.Close()
		if t.Failed() {
			t.Logf("listener stderr:\n%s", ru.stderr.String())
		}
	})
	return ru
}

// stop ends the listener the way a vanished agent does: the reader of its
// stdout goes away. It waits for the listener to finish.
func (r *run) stop() {
	r.outR.Close()
	select {
	case e := <-r.done:
		r.done <- e
	case <-time.After(5 * time.Second):
		r.t.Error("the listener did not stop when the reader of its output went away")
	}
}

// exit waits for the listener to end.
func (r *run) exit(timeout time.Duration) *listener.ExitError {
	r.t.Helper()
	select {
	case e := <-r.done:
		r.done <- e
		return e
	case <-time.After(timeout):
		r.t.Fatalf("the listener is still running; stderr:\n%s", r.stderr.String())
		return nil
	}
}

func (r *run) waitErr(sub string) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.stderr.String(), sub) {
		if time.Now().After(deadline) {
			r.t.Fatalf("stderr never contained %q:\n%s", sub, r.stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *run) waitOut(sub string) string {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, l := range r.stdout.get() {
			if strings.Contains(l, sub) {
				return l
			}
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("stdout never contained %q: %q", sub, r.stdout.get())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *run) do(req control.Request) control.Response {
	r.t.Helper()
	resp, err := control.Do(r.sock, req, 5*time.Second, nil)
	if err != nil {
		r.t.Fatalf("control request %+v: %v", req, err)
	}
	return resp
}

func socketGone(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err) && filepath.Ext(path) == ".sock"
}

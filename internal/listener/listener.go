// Package listener is the long-running process behind `chatterbox listen`.
// It owns the agent's single IRC connection, prints incoming messages on
// stdout, and serves the control socket the other commands talk to.
//
// One goroutine, the core, owns all state and runs the event loop in run.
// Everything else (the connection reader and writer, the control server,
// signal handling, the stdout writer) talks to it through events and never
// touches that state. The core never blocks on the network or on stdout.
package listener

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/teo-mateo/chatterbox/internal/config"
	"github.com/teo-mateo/chatterbox/internal/control"
	"github.com/teo-mateo/chatterbox/internal/format"
	"github.com/teo-mateo/chatterbox/internal/identity"
	"github.com/teo-mateo/chatterbox/internal/inbound"
	"github.com/teo-mateo/chatterbox/internal/irc"
	"github.com/teo-mateo/chatterbox/internal/tlspin"
)

// Exit codes of the listener (04 §2).
const (
	exitOK        = 0
	exitInternal  = 1
	exitUsage     = 2
	exitNickInUse = 4
	exitTrust     = 5
	exitRefused   = 6
	exitConnect   = 9
)

// ExitError carries the listener's exit code and the last line for stderr.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// Options configure one listener.
type Options struct {
	Nick       string
	Config     config.Config
	Verbose    bool
	RuntimeDir string
	Stdout     *os.File
	Stderr     io.Writer
}

// phase is where the listener is in the life of its connection.
type phase int

const (
	phBackoff phase = iota
	phDialing
	phCapLS
	phCapReq
	phRegistering
	phBotMode
	phJoining
	phCatchup
	phMoving // create-channel is switching channels
	phReady
	phQuitting
)

// stateName is the phase as reported by the status operation (02 §3).
func (p phase) stateName() string {
	switch p {
	case phBackoff:
		return "backoff"
	case phDialing:
		return "dialing"
	case phCapLS, phCapReq, phRegistering, phBotMode:
		return "registering"
	case phJoining, phMoving:
		return "joining"
	case phCatchup:
		return "catching-up"
	case phReady:
		return "ready"
	}
	return "stopping"
}

// Events delivered to the core.
type (
	evDialed struct {
		gen  int
		conn *irc.Conn
		err  error
	}
	evLine struct {
		gen int
		msg irc.Message
	}
	evConnErr struct {
		gen int
		err error
	}
	evWritten struct {
		gen   int
		label string
		lines int
	}
	evCall     struct{ call *control.Call }
	evCallGone struct{ call *control.Call }
	evStop     struct {
		code int
		msg  string
	}
)

// Listener is the core's state.
type Listener struct {
	opts Options
	tun  config.Tuning
	enc  format.Encoder
	wake string

	events chan any
	out    *lineWriter
	errMu  sync.Mutex

	// Identity and target, across connections.
	nick    string
	channel string // the channel the listener is in, or will rejoin

	// Catch-up state, across connections.
	ref      reference
	seen     *seenSet
	liveBuf  []bufferedChat
	replayed int

	phase      phase
	everReady  bool
	readySince time.Time
	gen        int
	sess       *session
	bo         backoff
	retryAt    time.Time
	nickBusy   time.Time // first "nickname in use" since the last good connection
	deadline   time.Time // of the current handshake phase
	quitAt     time.Time
	exit       *ExitError
	tlsConfig  func() (*tlsConfigResult, error)
	dialCancel context.CancelFunc

	// Control requests.
	waiting []*waitingCall
	jobs    []*job
	cur     *job
	onJoin  func(ok bool, reason string)
	catchup *catchupState

	chanOp      *chanOp
	chanWaiting []*control.Call
}

type waitingCall struct {
	call     *control.Call
	deadline time.Time
}

// Run runs a listener until it is told to stop or fails for good. A nil
// result means exit code 0.
func Run(opts Options) *ExitError {
	if err := identity.ValidateNick(opts.Nick); err != nil {
		return &ExitError{Code: exitUsage, Msg: err.Error()}
	}
	cfg := opts.Config
	if cfg.Defaults.Channel == "" {
		return &ExitError{Code: exitUsage, Msg: "no channel given: pass --channel <#name>, or set defaults.channel in the config file"}
	}

	// A closed stdout or stderr must end in a clean exit with QUIT, not in
	// death by SIGPIPE.
	signal.Ignore(syscall.SIGPIPE)
	hupIgnored := signal.Ignored(syscall.SIGHUP)

	// The socket must never be visible with wider permissions.
	syscall.Umask(0o077)
	dir := opts.RuntimeDir
	if err := identity.EnsureRuntimeDir(dir); err != nil {
		return &ExitError{Code: exitUsage, Msg: err.Error()}
	}
	sock := identity.SocketPath(dir, opts.Nick)
	if err := identity.CheckSocketPath(sock); err != nil {
		return &ExitError{Code: exitUsage, Msg: err.Error()}
	}
	lock, err := control.AcquireLock(identity.LockPath(dir, opts.Nick), opts.Nick)
	if err != nil {
		var locked *control.ErrLocked
		if errors.As(err, &locked) {
			return &ExitError{Code: exitNickInUse, Msg: err.Error()}
		}
		return &ExitError{Code: exitInternal, Msg: err.Error()}
	}
	defer lock.Release()

	l := &Listener{
		opts:    opts,
		tun:     cfg.Tuning,
		enc:     format.ForName(cfg.Defaults.Format),
		wake:    cfg.Defaults.Wake,
		events:  make(chan any, 256),
		nick:    opts.Nick,
		channel: cfg.Defaults.Channel,
		seen:    newSeenSet(5000),
		bo:      backoff{min: cfg.Tuning.BackoffMin.D(), max: cfg.Tuning.BackoffMax.D(), rnd: rand.Float64},
	}
	l.out = newLineWriter(opts.Stdout, cfg.Tuning.StdoutQueueLines, cfg.Tuning.StdoutFullLimit.D(), l.stop)
	go l.out.run()

	// Trust settings are checked before anything else can fail loudly.
	trustDir, err := config.Dir()
	if err != nil {
		return &ExitError{Code: exitUsage, Msg: err.Error()}
	}
	pin := tlspin.Options{
		Host: cfg.Server.Host, Port: cfg.Server.Port,
		Fingerprint: cfg.TLS.Fingerprint, CAFile: cfg.TLS.CAFile,
		TrustOnFirstUse: cfg.TLS.TrustOnFirstUse,
		StorePath:       trustDir + "/known_servers",
		Notice:          func(s string) { l.diag("%s", s) },
	}
	if _, err := pin.TLSConfig(); err != nil {
		return &ExitError{Code: exitUsage, Msg: err.Error()}
	}
	l.tlsConfig = func() (*tlsConfigResult, error) {
		c, err := pin.TLSConfig()
		if err != nil {
			return nil, err
		}
		return &tlsConfigResult{cfg: c, addr: net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))}, nil
	}

	srv, err := control.Listen(sock)
	if err != nil {
		return &ExitError{Code: exitInternal, Msg: err.Error()}
	}
	srv.Handle = func(c *control.Call) { l.post(evCall{c}) }
	go srv.Serve()
	defer srv.Close()

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		l.stop("", exitOK)
	}()

	// The watcher uses stdout's descriptor; it is gone before Run returns.
	watchDone, watchEnded := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchEnded)
		l.watchAgent(hupIgnored, watchDone)
	}()
	defer func() {
		close(watchDone)
		<-watchEnded
	}()

	exit := l.run()

	// Nothing may be left waiting on a socket that is about to vanish.
	l.failAllCalls("the listener is shutting down")
	srv.Close()
	l.out.Drain(500 * time.Millisecond)
	if exit != nil && exit.Code == exitOK && exit.Msg == "" {
		return nil
	}
	return exit
}

// watchAgent ends the listener when the agent behind it is gone: the reader
// of a stdout pipe closed it, or the chain of parent processes broke. The
// second covers harnesses that redirect background output to a file, where
// no write ever fails.
func (l *Listener) watchAgent(hupIgnored bool, done <-chan struct{}) {
	pipe := isPipe(l.opts.Stdout)
	var chain []int
	// A listener started under nohup is meant to outlive its parents.
	if !hupIgnored {
		chain = ancestors()
	}
	if !pipe && len(chain) == 0 {
		return
	}
	t := time.NewTicker(l.tun.HangupPoll.D())
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		if pipe && readerGone(l.opts.Stdout) {
			l.stop("the reader of the listener's output went away", exitOK)
			return
		}
		if len(chain) > 0 && !ancestorsIntact(chain) {
			l.stop("the process that started the listener is gone", exitOK)
			return
		}
	}
}

func (l *Listener) post(ev any) { l.events <- ev }

// stop asks the core to shut down. Safe from any goroutine.
func (l *Listener) stop(msg string, code int) {
	go l.post(evStop{code: code, msg: msg})
}

// diag writes one diagnostic line to stderr. A failed write is ignored.
func (l *Listener) diag(format string, args ...any) {
	l.errMu.Lock()
	defer l.errMu.Unlock()
	fmt.Fprintf(l.opts.Stderr, "chatterbox: "+format+"\n", args...)
}

func (l *Listener) verbose(format string, args ...any) {
	if l.opts.Verbose {
		l.diag(format, args...)
	}
}

type tlsConfigResult struct {
	cfg  *tls.Config
	addr string
}

// run is the core's event loop.
func (l *Listener) run() *ExitError {
	tick := time.NewTicker(tickInterval(l.tun))
	defer tick.Stop()
	l.dial()
	for {
		select {
		case ev := <-l.events:
			l.handle(ev)
		case now := <-tick.C:
			l.onTick(now)
		}
		if l.exit != nil && l.phase != phQuitting {
			l.teardown()
			return l.exit
		}
	}
}

// tickInterval is how often deadlines are checked: fine enough for the
// shortest configured timing, never busier than every 10 ms.
func tickInterval(t config.Tuning) time.Duration {
	d := 100 * time.Millisecond
	for _, v := range []config.Duration{t.KeepaliveInterval, t.BackoffMin, t.ReadyWait, t.AckBase, t.QuitWait} {
		if v.D()/5 < d {
			d = v.D() / 5
		}
	}
	if d < 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	return d
}

func (l *Listener) handle(ev any) {
	switch ev := ev.(type) {
	case evDialed:
		l.onDialed(ev)
	case evLine:
		if l.sess != nil && ev.gen == l.sess.gen {
			l.sess.lastRecv = time.Now()
			l.onLine(ev.msg)
		}
	case evConnErr:
		if l.sess != nil && ev.gen == l.sess.gen {
			l.connLost(describeNetErr(ev.err))
		}
	case evWritten:
		if l.sess != nil && ev.gen == l.sess.gen {
			l.onWritten(ev)
		}
	case evCall:
		l.onCall(ev.call)
	case evCallGone:
		l.onCallGone(ev.call)
	case evStop:
		l.shutdown(ev.code, ev.msg)
	}
}

func (l *Listener) onTick(now time.Time) {
	if l.out.Overdue(now) {
		l.shutdown(exitInternal, fmt.Sprintf("the listener's output has not been read for %s; exiting so the agent does not appear present",
			l.tun.StdoutFullLimit.D()))
	}
	switch l.phase {
	case phQuitting:
		if !now.Before(l.quitAt) {
			l.phase = phBackoff // lets run return
		}
		return
	case phBackoff:
		if l.exit == nil && !now.Before(l.retryAt) {
			l.dial()
		}
	case phCapLS, phCapReq, phRegistering, phBotMode, phJoining, phMoving:
		if (l.phase != phMoving || l.onJoin != nil) && !now.Before(l.deadline) {
			l.connLost(fmt.Sprintf("the server did not complete %s within %s", l.phase.stateName(), l.tun.RegisterTimeout.D()))
		}
	}
	if s := l.sess; s != nil && l.phase != phDialing {
		// While the server is working off what was sent it is silent,
		// which is not the same as gone.
		quiet := s.lastRecv
		if s.busyUntil.After(quiet) {
			quiet = s.busyUntil
		}
		if now.Sub(quiet) >= l.tun.IdleTimeout.D() {
			l.connLost(fmt.Sprintf("no data from the server for %s", l.tun.IdleTimeout.D()))
		} else if s.registered && now.Sub(s.lastPing) >= l.tun.KeepaliveInterval.D() && !now.Before(s.busyUntil) {
			// No PING while the server is still working off earlier
			// lines: it would only queue up behind them.
			s.lastPing = now
			s.wq.Push(irc.Unit{Lines: []string{irc.Line(nil, "PING", "chatterbox")}})
		}
	}
	if s := l.sess; s != nil {
		for label, p := range s.labels {
			if !p.deadline.IsZero() && !now.Before(p.deadline) {
				delete(s.labels, label)
				p.fn(&response{timeout: true})
			}
		}
	}
	l.expireWaiting(now)
	l.channelsTick(now)
}

// shutdown starts the orderly exit: QUIT if there is a registered
// connection, then leave. The first call decides the exit code.
func (l *Listener) shutdown(code int, msg string) {
	if l.exit != nil {
		return
	}
	l.exit = &ExitError{Code: code, Msg: msg}
	if l.dialCancel != nil {
		l.dialCancel()
	}
	if s := l.sess; s != nil && s.registered {
		s.wq.Push(irc.Unit{Lines: []string{irc.Line(nil, "QUIT", "chatterbox listener stopped")}})
		l.phase = phQuitting
		l.quitAt = time.Now().Add(l.tun.QuitWait.D())
		return
	}
	l.phase = phBackoff
}

// teardown closes the connection, if any, for good.
func (l *Listener) teardown() {
	if s := l.sess; s != nil {
		s.close()
		l.sess = nil
	}
}

// fatal ends the listener with an error.
func (l *Listener) fatal(code int, format string, args ...any) {
	l.shutdown(code, fmt.Sprintf(format, args...))
}

// dial starts a connection attempt.
func (l *Listener) dial() {
	l.gen++
	gen := l.gen
	l.phase = phDialing
	tc, err := l.tlsConfig()
	if err != nil {
		l.fatal(exitUsage, "%v", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.dialCancel = cancel
	l.verbose("connecting to %s", tc.addr)
	go func() {
		conn, err := irc.Dial(ctx, tc.addr, tc.cfg, l.tun.DialTimeout.D())
		l.post(evDialed{gen: gen, conn: conn, err: err})
	}()
}

func (l *Listener) onDialed(ev evDialed) {
	if ev.gen != l.gen || l.exit != nil {
		if ev.conn != nil {
			ev.conn.Close()
		}
		return
	}
	l.dialCancel = nil
	if ev.err != nil {
		var trust *tlspin.TrustError
		if errors.As(ev.err, &trust) {
			l.fatal(exitTrust, "%s", trust.Msg)
			return
		}
		l.connFailed(fmt.Sprintf("cannot connect to %s: %s", net.JoinHostPort(l.opts.Config.Server.Host, strconv.Itoa(l.opts.Config.Server.Port)), describeNetErr(ev.err)))
		return
	}
	l.startSession(ev.conn)
}

// connLost handles the end of an established connection.
func (l *Listener) connLost(reason string) {
	if l.phase == phQuitting {
		l.phase = phBackoff
		return
	}
	l.connFailed(reason)
}

// connFailed handles any failure that is retried once the listener has been
// ready, and is fatal before that (D16).
func (l *Listener) connFailed(reason string) {
	wasReady := l.phase == phReady || l.phase == phCatchup || l.phase == phMoving
	l.dropSession(reason)
	if l.exit != nil {
		return
	}
	if !l.everReady {
		l.fatal(exitConnect, "%s", reason)
		return
	}
	if wasReady && time.Since(l.readySince) >= l.tun.BackoffReset.D() {
		l.bo.reset()
	}
	delay := l.bo.next()
	l.phase = phBackoff
	l.retryAt = time.Now().Add(delay)
	if wasReady {
		l.diag("connection lost: %s; reconnecting", reason)
	} else {
		l.verbose("reconnect failed: %s; next attempt in %s", reason, delay.Round(time.Millisecond))
	}
}

// dropSession discards the connection and fails what depended on it.
func (l *Listener) dropSession(reason string) {
	s := l.sess
	l.sess = nil
	l.onJoin = nil
	l.catchup = nil
	l.liveBuf = nil
	if s == nil {
		return
	}
	if l.phase != phQuitting {
		l.phase = phBackoff
	}
	s.close()
	for label, p := range s.labels {
		delete(s.labels, label)
		p.fn(&response{lost: true})
	}
	l.connectionGone(reason)
}

func describeNetErr(err error) string {
	if err == nil {
		return "connection closed"
	}
	if errors.Is(err, io.EOF) {
		return "the server closed the connection"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "cannot resolve " + dns.Name
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	return err.Error()
}

// self describes the listener for message classification.
func (l *Listener) self() inbound.Self {
	cm := identity.CasemapASCII
	if l.sess != nil {
		cm = l.sess.casemap
	}
	return inbound.Self{Nick: l.nick, Channel: l.channel, Casemap: cm}
}

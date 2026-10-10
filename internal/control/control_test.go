package control

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func tempSock(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cbxc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, name+".sock")
}

// serve starts a server whose handler is h. setup, if given, adjusts the
// server before it starts accepting.
func serve(t *testing.T, path string, h func(*Call), setup ...func(*Server)) *Server {
	t.Helper()
	s, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Handle = h
	s.Heartbeat = 50 * time.Millisecond
	for _, f := range setup {
		f(s)
	}
	go s.Serve()
	t.Cleanup(s.Close)
	return s
}

func statusHandler(nick string) func(*Call) {
	return func(c *Call) {
		r := OKResponse()
		r.Nick, r.Channel, r.State, r.PID = nick, "#c", "ready", 42
		c.Finish(r)
	}
}

func TestRoundTripAndEncoding(t *testing.T) {
	path := tempSock(t, "a")
	received := make(chan Request, 1)
	serve(t, path, func(c *Call) {
		received <- c.Req
		c.Progress(1, 3)
		c.Progress(2, 3)
		r := OKResponse()
		r.Nick, r.Channel, r.Parts = "agentA", "#hof", 3
		c.Finish(r)
		c.Finish(ErrResponse(CodeTimeout, "a second final response must be ignored"))
	})
	var progress []Progress
	text := "line one\nline two   <b>&</b> \"quoted\""
	r, err := Do(path, Request{Op: OpSend, To: "agentB", Text: text, Important: true}, time.Second, func(p Progress) { progress = append(progress, p) })
	if err != nil {
		t.Fatal(err)
	}
	got := <-received
	if got.V != Version || got.Op != OpSend || got.To != "agentB" || got.Text != text || !got.Important || got.Private {
		t.Errorf("request as received: %+v", got)
	}
	if !r.Succeeded() || r.V != Version || r.Nick != "agentA" || r.Channel != "#hof" || r.Parts != 3 {
		t.Errorf("response: %+v", r)
	}
	if len(progress) != 2 || progress[0] != (Progress{1, 3}) || progress[1] != (Progress{2, 3}) {
		t.Errorf("progress: %+v", progress)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode: %v %v", fi.Mode(), err)
	}
}

func TestErrorResponse(t *testing.T) {
	path := tempSock(t, "a")
	serve(t, path, func(c *Call) {
		r := ErrResponse(CodeNotConnected, "listener agentA is reconnecting; message not sent")
		r.Sent = SentNo
		c.Finish(r)
	})
	r, err := Do(path, Request{Op: OpPublish, Text: "x"}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Succeeded() || !r.Final() || r.Code != CodeNotConnected || r.Sent != SentNo || !strings.Contains(r.Error, "reconnecting") {
		t.Errorf("%+v", r)
	}
}

// raw sends bytes to the socket and returns the first response line.
func raw(t *testing.T, path string, payload []byte) Response {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go c.Write(payload)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReaderSize(c, 4096).ReadBytes('\n')
	if err != nil {
		t.Fatalf("no response: %v", err)
	}
	var r Response
	if err := json.Unmarshal(line, &r); err != nil {
		t.Fatalf("response %q: %v", line, err)
	}
	return r
}

func TestBadRequests(t *testing.T) {
	path := tempSock(t, "a")
	var called atomic.Bool
	serve(t, path, func(c *Call) { called.Store(true); c.Finish(OKResponse()) })

	if r := raw(t, path, []byte("this is not json\n")); r.Code != CodeBadRequest {
		t.Errorf("garbage: %+v", r)
	}
	if r := raw(t, path, []byte(`{"v":2,"op":"status"}`+"\n")); r.Code != CodeUnsupportedVersion || !strings.Contains(r.Error, "version") {
		t.Errorf("wrong version: %+v", r)
	}
	if r := raw(t, path, []byte(`{"op":"status"}`+"\n")); r.Code != CodeUnsupportedVersion {
		t.Errorf("missing version: %+v", r)
	}
	huge := append([]byte(`{"v":1,"op":"publish","text":"`), make([]byte, MaxRequestBytes+10)...)
	for i := 30; i < len(huge); i++ {
		huge[i] = 'x'
	}
	if r := raw(t, path, huge); r.Code != CodeTooLarge {
		t.Errorf("over the size limit: %+v", r)
	}
	if called.Load() {
		t.Error("the handler ran for an invalid request")
	}
}

func TestPeerUIDCheck(t *testing.T) {
	path := tempSock(t, "a")
	var called atomic.Bool
	handler := func(c *Call) { called.Store(true); c.Finish(OKResponse()) }
	serve(t, path, handler, func(s *Server) {
		s.PeerUID = func(*net.UnixConn) (int, error) { return os.Getuid() + 1, nil }
	})
	r, err := Do(path, Request{Op: OpPublish, Text: "x"}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Succeeded() || !strings.Contains(r.Error, "another user") || called.Load() {
		t.Errorf("a request from another uid was served: %+v", r)
	}
	// The real credential check accepts the current user.
	own := tempSock(t, "b")
	serve(t, own, handler)
	if r, err := Do(own, Request{Op: OpStatus}, time.Second, nil); err != nil || !r.Succeeded() {
		t.Errorf("own uid refused: %+v %v", r, err)
	}
}

func TestHeartbeatKeepsClientAlive(t *testing.T) {
	path := tempSock(t, "a")
	serve(t, path, func(c *Call) {
		go func() {
			time.Sleep(600 * time.Millisecond) // several idle timeouts long
			c.Finish(OKResponse())
		}()
	})
	if r, err := Do(path, Request{Op: OpPublish, Text: "x"}, 200*time.Millisecond, nil); err != nil || !r.Succeeded() {
		t.Errorf("a slow request with heartbeats: %+v %v", r, err)
	}
}

func TestIdleDeadline(t *testing.T) {
	path := tempSock(t, "a")
	serve(t, path, func(c *Call) {}, func(s *Server) { s.Heartbeat = time.Hour }) // never answers
	start := time.Now()
	_, err := Do(path, Request{Op: OpPublish, Text: "x"}, 200*time.Millisecond, nil)
	if err != ErrIdle {
		t.Errorf("err = %v, want ErrIdle", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("the idle deadline did not bound the wait")
	}
}

func TestClientGone(t *testing.T) {
	path := tempSock(t, "a")
	gone := make(chan struct{})
	serve(t, path, func(c *Call) {
		go func() {
			<-c.Gone
			close(gone)
		}()
	})
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte(`{"v":1,"op":"publish","text":"x"}` + "\n"))
	time.Sleep(100 * time.Millisecond)
	c.Close()
	select {
	case <-gone:
	case <-time.After(3 * time.Second):
		t.Error("the handler was not told that the client went away")
	}
}

func TestNoListener(t *testing.T) {
	if _, err := Do(tempSock(t, "absent"), Request{Op: OpStatus}, time.Second, nil); err != ErrNoListener {
		t.Errorf("missing socket: %v", err)
	}
}

func TestDiscover(t *testing.T) {
	dir := filepath.Dir(tempSock(t, "x"))
	if got := Discover(dir, 200*time.Millisecond); len(got) != 0 {
		t.Errorf("empty directory: %+v", got)
	}

	serve(t, filepath.Join(dir, "agentb.sock"), statusHandler("agentB"))
	got := Discover(dir, 200*time.Millisecond)
	if len(got) != 1 || got[0].Nick != "agentB" || got[0].Channel != "#c" || got[0].State != "ready" || got[0].PID != 42 {
		t.Errorf("one listener: %+v", got)
	}

	serve(t, filepath.Join(dir, "agenta.sock"), statusHandler("agentA"))
	// A stale socket: bound once, nobody listening any more.
	stale := filepath.Join(dir, "stale.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	// A socket whose owner never answers.
	serve(t, filepath.Join(dir, "mute.sock"), func(c *Call) {}, func(s *Server) { s.Heartbeat = time.Hour })
	os.WriteFile(filepath.Join(dir, "agentc.lock"), nil, 0o600)

	start := time.Now()
	got = Discover(dir, 200*time.Millisecond)
	if len(got) != 2 || got[0].Nick != "agentA" || got[1].Nick != "agentB" {
		t.Errorf("several listeners, one stale, one mute: %+v", got)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("probing took %s; probes must run in parallel with a timeout", d)
	}
	if !SocketExists(stale) || SocketExists(filepath.Join(dir, "agentc.lock")) {
		t.Error("SocketExists")
	}
}

func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agenta.lock")
	l, err := AcquireLock(path, "agentA")
	if err != nil {
		t.Fatal(err)
	}
	_, err = AcquireLock(path, "agentA")
	locked, ok := err.(*ErrLocked)
	if !ok || locked.PID == "" || !strings.Contains(err.Error(), "a listener for agentA is already running (pid ") {
		t.Errorf("second lock: %v", err)
	}
	l.Release()
	if _, err := os.Stat(path); err != nil {
		t.Error("the lock file must survive Release")
	}
	l2, err := AcquireLock(path, "agentA")
	if err != nil {
		t.Errorf("lock not free after Release: %v", err)
	}
	l2.Release()
}

func TestListenReplacesStaleSocket(t *testing.T) {
	path := tempSock(t, "a")
	os.WriteFile(path, []byte("left behind"), 0o600)
	s, err := Listen(path)
	if err != nil {
		t.Fatalf("a stale file at the socket path was not replaced: %v", err)
	}
	s.Handle = statusHandler("a")
	go s.Serve()
	if r, err := Do(path, Request{Op: OpStatus}, time.Second, nil); err != nil || r.Nick != "a" {
		t.Errorf("%+v %v", r, err)
	}
	s.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Close left the socket file")
	}
}

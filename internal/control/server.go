package control

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/teo-mateo/chatterbox/internal/format"
)

// Lock is the per-nickname lock file, held for the listener's lifetime. The
// file is never deleted: removing it on exit would let two later processes
// each hold a lock on a different inode.
type Lock struct{ f *os.File }

// ErrLocked means another listener holds the nickname on this machine.
type ErrLocked struct {
	Nick string
	PID  string
}

func (e *ErrLocked) Error() string {
	if e.PID != "" {
		return fmt.Sprintf("a listener for %s is already running (pid %s)", e.Nick, e.PID)
	}
	return fmt.Sprintf("a listener for %s is already running", e.Nick)
}

// AcquireLock takes the lock for nick without waiting.
func AcquireLock(path, nick string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open the lock file %s: %v", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		pid, _ := io.ReadAll(io.LimitReader(f, 32))
		f.Close()
		return nil, &ErrLocked{Nick: nick, PID: strings.TrimSpace(string(pid))}
	}
	f.Truncate(0)
	f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return &Lock{f: f}, nil
}

// Release drops the lock. The file stays.
func (l *Lock) Release() {
	if l != nil && l.f != nil {
		l.f.Close()
	}
}

// Call is one request being served. The handler sends any number of progress
// lines and exactly one final response; neither call blocks.
type Call struct {
	Req Request
	// Gone is closed when the client goes away before the final response.
	Gone <-chan struct{}

	progress chan Response
	final    chan Response
	once     sync.Once
}

// Progress queues a progress line. It is dropped if the client is slow.
func (c *Call) Progress(part, of int) {
	select {
	case c.progress <- ProgressResponse(part, of):
	default:
	}
}

// Finish sends the final response. Only the first call has an effect.
func (c *Call) Finish(r Response) {
	c.once.Do(func() { c.final <- r })
}

// Server accepts control connections on the listener's socket.
type Server struct {
	ln   *net.UnixListener
	path string
	// Handle receives every valid request. It must arrange for
	// Call.Finish to be called eventually.
	Handle func(*Call)
	// PeerUID is replaceable for tests.
	PeerUID func(*net.UnixConn) (int, error)
	// Heartbeat is how often an empty progress line is sent while a
	// request is in progress.
	Heartbeat time.Duration

	uid int
}

// Listen binds the control socket. The caller must hold the nickname's lock:
// a socket file left at path is then known to be stale and is removed. The
// process umask must already be 077 so the socket is never visible with
// wider permissions.
func Listen(path string) (*Server, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cannot remove the stale socket %s: %v", path, err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("cannot create the control socket %s: %v", path, err)
	}
	ln.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return &Server{ln: ln, path: path, PeerUID: PeerUID, Heartbeat: 5 * time.Second, uid: os.Getuid()}, nil
}

// Serve accepts connections until Close.
func (s *Server) Serve() {
	for {
		c, err := s.ln.AcceptUnix()
		if err != nil {
			return
		}
		go s.serve(c)
	}
}

// Close stops accepting and removes the socket.
func (s *Server) Close() {
	s.ln.Close()
	os.Remove(s.path)
}

func writeLine(c net.Conn, r Response) error {
	c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := io.WriteString(c, format.MarshalLine(r)+"\n")
	return err
}

func (s *Server) serve(c *net.UnixConn) {
	defer c.Close()
	if uid, err := s.PeerUID(c); err != nil || uid != s.uid {
		writeLine(c, ErrResponse(CodeBadRequest, "this listener belongs to another user"))
		return
	}

	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReaderSize(io.LimitReader(c, MaxRequestBytes+1), 64*1024)
	line, err := br.ReadBytes('\n')
	if err != nil {
		if len(line) > MaxRequestBytes {
			writeLine(c, ErrResponse(CodeTooLarge, fmt.Sprintf("the request is larger than %d bytes", MaxRequestBytes)))
		}
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeLine(c, ErrResponse(CodeBadRequest, "the request is not valid JSON"))
		return
	}
	if req.V != Version {
		writeLine(c, ErrResponse(CodeUnsupportedVersion,
			fmt.Sprintf("the listener speaks control protocol version %d, the command sent version %d; restart the listener with the same chatterbox version", Version, req.V)))
		return
	}

	gone := make(chan struct{})
	call := &Call{Req: req, Gone: gone, progress: make(chan Response, 16), final: make(chan Response, 1)}
	// The client sends nothing more; a read returning means it went away.
	c.SetReadDeadline(time.Time{})
	go func() {
		io.Copy(io.Discard, c)
		close(gone)
	}()
	s.Handle(call)

	beat := time.NewTicker(s.Heartbeat)
	defer beat.Stop()
	for {
		select {
		case r := <-call.final:
			// Progress queued before the final line still goes first.
			for {
				select {
				case p := <-call.progress:
					writeLine(c, p)
					continue
				default:
				}
				break
			}
			writeLine(c, r)
			return
		case p := <-call.progress:
			if writeLine(c, p) != nil {
				return
			}
		case <-beat.C:
			if writeLine(c, Response{V: Version, Progress: &Progress{}}) != nil {
				return
			}
		case <-gone:
			return
		}
	}
}

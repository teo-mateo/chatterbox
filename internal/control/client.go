package control

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/teo-mateo/chatterbox/internal/format"
)

// ErrNoListener means nothing answers on the socket.
var ErrNoListener = errors.New("no listener")

// ErrIdle means the listener stopped sending lines before the final one.
var ErrIdle = errors.New("the listener stopped responding")

// IdleTimeout is how long a command waits without any line from the
// listener before it gives up. The listener sends a progress line at least
// every five seconds while it works.
const IdleTimeout = 20 * time.Second

// Do sends one request and returns the final response. onProgress, if set,
// is called for progress lines that carry a part number.
func Do(socket string, req Request, idle time.Duration, onProgress func(Progress)) (Response, error) {
	c, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return Response{}, ErrNoListener
	}
	defer c.Close()
	req.V = Version
	c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(c, format.MarshalLine(req)+"\n"); err != nil {
		return Response{}, fmt.Errorf("cannot send the request to the listener: %v", err)
	}
	br := bufio.NewReaderSize(c, 64*1024)
	for {
		c.SetReadDeadline(time.Now().Add(idle))
		line, err := br.ReadBytes('\n')
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return Response{}, ErrIdle
			}
			return Response{}, fmt.Errorf("the listener closed the connection without answering: %w", ErrIdle)
		}
		var r Response
		if err := json.Unmarshal(line, &r); err != nil {
			return Response{}, fmt.Errorf("unreadable answer from the listener: %v", err)
		}
		if r.Final() {
			return r, nil
		}
		if r.Progress != nil && r.Progress.Of > 0 && onProgress != nil {
			onProgress(*r.Progress)
		}
	}
}

// Found is a running listener discovered in the runtime directory.
type Found struct {
	Nick    string
	Channel string
	State   string
	PID     int
	Socket  string
}

// Discover probes every socket in dir with a status request, in parallel,
// and returns the listeners that answer, sorted by nickname. Stale sockets
// and anything that does not answer within timeout are left out.
func Discover(dir string, timeout time.Duration) []Found {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.sock"))
	var (
		mu    sync.Mutex
		found []Found
		wg    sync.WaitGroup
	)
	for _, p := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			r, err := Do(p, Request{Op: OpStatus}, timeout, nil)
			if err != nil || !r.Succeeded() || r.Nick == "" {
				return
			}
			mu.Lock()
			found = append(found, Found{Nick: r.Nick, Channel: r.Channel, State: r.State, PID: r.PID, Socket: p})
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	sort.Slice(found, func(i, j int) bool { return strings.ToLower(found[i].Nick) < strings.ToLower(found[j].Nick) })
	return found
}

// SocketExists reports whether a socket file is present at path.
func SocketExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

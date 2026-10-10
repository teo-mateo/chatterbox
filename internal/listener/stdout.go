package listener

import (
	"io"
	"os"
	"sync"
	"time"
)

// lineWriter is the only writer to stdout. Lines are queued so the core
// never blocks on a slow reader, and each is written with a single write
// call. When the queue has been full for too long, or a write fails, it
// reports through fail and stops.
type lineWriter struct {
	w         io.Writer
	limit     int
	fullLimit time.Duration
	fail      func(reason string, code int)

	mu        sync.Mutex
	cond      *sync.Cond
	lines     []string
	fullSince time.Time
	stopped   bool
	ended     chan struct{} // closed when run returns
}

func newLineWriter(w io.Writer, limit int, fullLimit time.Duration, fail func(string, int)) *lineWriter {
	lw := &lineWriter{w: w, limit: limit, fullLimit: fullLimit, fail: fail, ended: make(chan struct{})}
	lw.cond = sync.NewCond(&lw.mu)
	return lw
}

// Push queues one line. It never blocks. Past the limit lines are still
// accepted; the limit only starts the clock that ends the listener.
func (lw *lineWriter) Push(line string) {
	lw.mu.Lock()
	lw.lines = append(lw.lines, line)
	if len(lw.lines) >= lw.limit && lw.fullSince.IsZero() {
		lw.fullSince = time.Now()
	}
	lw.mu.Unlock()
	lw.cond.Signal()
}

// Overdue reports whether the queue has been full for longer than allowed.
func (lw *lineWriter) Overdue(now time.Time) bool {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return !lw.fullSince.IsZero() && now.Sub(lw.fullSince) >= lw.fullLimit
}

func (lw *lineWriter) run() {
	defer close(lw.ended)
	for {
		lw.mu.Lock()
		for len(lw.lines) == 0 && !lw.stopped {
			lw.cond.Wait()
		}
		if lw.stopped && len(lw.lines) == 0 {
			lw.mu.Unlock()
			return
		}
		line := lw.lines[0]
		lw.mu.Unlock()

		if _, err := io.WriteString(lw.w, line+"\n"); err != nil {
			lw.fail("the reader of the listener's output went away", exitOK)
			return
		}

		lw.mu.Lock()
		lw.lines = lw.lines[1:]
		if len(lw.lines) < lw.limit {
			lw.fullSince = time.Time{}
		}
		lw.mu.Unlock()
	}
}

// Drain lets the writer finish what is queued and stop, waiting at most d.
// A writer blocked on a reader that never reads is left behind.
func (lw *lineWriter) Drain(d time.Duration) {
	lw.mu.Lock()
	lw.stopped = true
	lw.mu.Unlock()
	lw.cond.Broadcast()
	select {
	case <-lw.ended:
	case <-time.After(d):
	}
}

// isPipe reports whether f is a pipe or socket, where a vanished reader can
// be detected.
func isPipe(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0
}

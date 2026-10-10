package irc

import "sync"

// Unit is something written to the connection without anything else in
// between: a single command, or a complete multiline batch from "BATCH +ref"
// to "BATCH -ref".
type Unit struct {
	Lines []string
	// Written, if set, runs on the writer goroutine once the unit's last
	// line has been handed to the connection.
	Written func()
}

// WriteQueue orders everything the listener sends. Units come from two
// queues, one for chat messages and one for everything else (PING, PONG,
// queries); when both have work the writer alternates, so a long split
// message does not starve a query and a burst of lookups does not hold up a
// message. Within each queue order is strict.
type WriteQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	msgs    []Unit
	other   []Unit
	lastMsg bool
	closed  bool
}

// NewWriteQueue returns an empty queue.
func NewWriteQueue() *WriteQueue {
	q := &WriteQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// PushMessage queues a chat message unit. It never blocks.
func (q *WriteQueue) PushMessage(u Unit) { q.push(&q.msgs, u) }

// Push queues any other unit. It never blocks.
func (q *WriteQueue) Push(u Unit) { q.push(&q.other, u) }

func (q *WriteQueue) push(dst *[]Unit, u Unit) {
	q.mu.Lock()
	if !q.closed {
		*dst = append(*dst, u)
	}
	q.mu.Unlock()
	q.cond.Signal()
}

// Close makes Next return false once the queued units are drained.
func (q *WriteQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

// Next blocks until a unit is available. ok is false when the queue was
// closed and is empty.
func (q *WriteQueue) Next() (u Unit, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.msgs) == 0 && len(q.other) == 0 {
		if q.closed {
			return Unit{}, false
		}
		q.cond.Wait()
	}
	takeMsg := len(q.msgs) > 0 && (len(q.other) == 0 || !q.lastMsg)
	if takeMsg {
		u, q.msgs = q.msgs[0], q.msgs[1:]
	} else {
		u, q.other = q.other[0], q.other[1:]
	}
	q.lastMsg = takeMsg
	return u, true
}

// Run writes units until the queue is closed and drained or a write fails.
func (q *WriteQueue) Run(write func(lines []string) error) error {
	for {
		u, ok := q.Next()
		if !ok {
			return nil
		}
		if err := write(u.Lines); err != nil {
			q.Close()
			return err
		}
		if u.Written != nil {
			u.Written()
		}
	}
}

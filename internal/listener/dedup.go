package listener

import "time"

// seenSet remembers the ids of the most recent messages, so that a message
// arriving both live and through history is delivered once.
type seenSet struct {
	limit int
	ids   map[string]struct{}
	order []string
	next  int
}

func newSeenSet(limit int) *seenSet {
	return &seenSet{limit: limit, ids: make(map[string]struct{}, limit)}
}

// Add records id and reports whether it was new. An empty id is always new
// and is not recorded.
func (s *seenSet) Add(id string) bool {
	if id == "" {
		return true
	}
	if _, dup := s.ids[id]; dup {
		return false
	}
	if len(s.order) < s.limit {
		s.order = append(s.order, id)
	} else {
		delete(s.ids, s.order[s.next])
		s.order[s.next] = id
		s.next = (s.next + 1) % s.limit
	}
	s.ids[id] = struct{}{}
	return true
}

func (s *seenSet) Len() int { return len(s.ids) }

// reference is the point catch-up starts from: the server time and id of the
// newest message seen in the channel. Only server-supplied times go in here,
// never the local clock, so clock differences cannot open a gap.
type reference struct {
	time time.Time
	id   string
}

// advance moves the reference forward to a message with a server time.
func (r *reference) advance(t time.Time, id string, hasServerTime bool) {
	if !hasServerTime || t.Before(r.time) {
		return
	}
	r.time, r.id = t, id
}

// backoff computes reconnect delays: exponential from min to max with ±20 %
// jitter.
type backoff struct {
	min, max time.Duration
	attempt  int
	rnd      func() float64 // in [0,1)
}

func (b *backoff) next() time.Duration {
	d := b.min
	for i := 0; i < b.attempt && d < b.max; i++ {
		d *= 2
	}
	if d > b.max {
		d = b.max
	}
	b.attempt++
	jitter := 0.8 + 0.4*b.rnd()
	return time.Duration(float64(d) * jitter)
}

func (b *backoff) reset() { b.attempt = 0 }

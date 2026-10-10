package listener

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/teo-mateo/chatterbox/internal/control"
	"github.com/teo-mateo/chatterbox/internal/inbound"
	"github.com/teo-mateo/chatterbox/internal/irc"
	"github.com/teo-mateo/chatterbox/internal/outbound"
)

// job is one request on the message queue: a publish or send, possibly
// split into several IRC messages, or a create-channel. Jobs run strictly
// one after another, and within a job each part waits for the server's
// acknowledgement before the next is written.
type job struct {
	call *control.Call
	gone bool // the client went away; drop what has not been written

	// publish, send
	target string
	parts  []outbound.Part
	sent   int // parts acknowledged

	// create-channel
	create string
}

// onCall receives a request from the control socket.
func (l *Listener) onCall(c *control.Call) {
	if l.exit != nil {
		c.Finish(control.ErrResponse(control.CodeNotConnected, "the listener is shutting down"))
		return
	}
	if c.Req.Op == control.OpStatus {
		r := control.OKResponse()
		r.Nick, r.Channel, r.State, r.PID = l.nick, l.channel, l.phase.stateName(), os.Getpid()
		c.Finish(r)
		return
	}
	go func() {
		<-c.Gone
		l.post(evCallGone{c})
	}()
	if l.phase != phReady {
		// A short blip should not fail a command: wait a little for the
		// connection to come back, but queue nothing beyond that (D9).
		l.waiting = append(l.waiting, &waitingCall{call: c, deadline: time.Now().Add(l.tun.ReadyWait.D())})
		return
	}
	l.dispatch(c)
}

func (l *Listener) onCallGone(c *control.Call) {
	for i, w := range l.waiting {
		if w.call == c {
			l.waiting = append(l.waiting[:i], l.waiting[i+1:]...)
			return
		}
	}
	for i, j := range l.jobs {
		if j.call == c {
			l.jobs = append(l.jobs[:i], l.jobs[i+1:]...)
			return
		}
	}
	// The message being written is finished; the remaining parts of the
	// request are dropped.
	if l.cur != nil && l.cur.call == c {
		l.cur.gone = true
	}
}

func (l *Listener) notConnected(posting bool) control.Response {
	msg := fmt.Sprintf("listener %s is not connected to the server (%s)", l.nick, l.phase.stateName())
	r := control.ErrResponse(control.CodeNotConnected, msg)
	if posting {
		r.Error += "; message not sent"
		r.Sent = control.SentNo
	}
	return r
}

func isPosting(op string) bool { return op == control.OpPublish || op == control.OpSend }

func (l *Listener) expireWaiting(now time.Time) {
	kept := l.waiting[:0]
	for _, w := range l.waiting {
		if now.Before(w.deadline) {
			kept = append(kept, w)
			continue
		}
		w.call.Finish(l.notConnected(isPosting(w.call.Req.Op)))
	}
	l.waiting = kept
}

func (l *Listener) admitWaiting() {
	waiting := l.waiting
	l.waiting = nil
	for _, w := range waiting {
		l.dispatch(w.call)
	}
}

func (l *Listener) failAllCalls(reason string) {
	fail := func(c *control.Call) {
		r := control.ErrResponse(control.CodeNotConnected, reason)
		if isPosting(c.Req.Op) {
			r.Sent = control.SentNo
			r.Error += "; message not sent"
		}
		c.Finish(r)
	}
	for _, w := range l.waiting {
		fail(w.call)
	}
	for _, j := range l.jobs {
		fail(j.call)
	}
	l.waiting, l.jobs = nil, nil
}

// connectionGone fails the work that was in flight on a lost connection.
// Jobs that had not started wait, like new requests, for a quick return.
func (l *Listener) connectionGone(reason string) {
	if j := l.cur; j != nil {
		l.cur = nil
		if j.create != "" {
			j.call.Finish(control.ErrResponse(control.CodeNotConnected,
				"the connection to the server was lost while creating "+j.create+"; the listener will rejoin "+l.channel))
		} else {
			l.finishSend(j, control.CodeTimeout, "the connection to the server was lost before the message was confirmed", true)
		}
	}
	deadline := time.Now().Add(l.tun.ReadyWait.D())
	for _, j := range l.jobs {
		l.waiting = append(l.waiting, &waitingCall{call: j.call, deadline: deadline})
	}
	l.jobs = nil
	if ch := l.chanOp; ch != nil {
		l.chanOp = nil
		ch.call.Finish(l.notConnected(false))
	}
	for _, c := range l.chanWaiting {
		c.Finish(l.notConnected(false))
	}
	l.chanWaiting = nil
}

func (l *Listener) dispatch(c *control.Call) {
	switch c.Req.Op {
	case control.OpPublish, control.OpSend:
		l.enqueueSend(c)
	case control.OpCreateChannel:
		l.jobs = append(l.jobs, &job{call: c, create: c.Req.Name})
		l.pump()
	case control.OpNames:
		l.names(c)
	case control.OpChannels:
		l.channels(c)
	default:
		c.Finish(control.ErrResponse(control.CodeBadRequest, fmt.Sprintf("unknown operation %q", c.Req.Op)))
	}
}

func badRequest(c *control.Call, posting bool, format string, args ...any) {
	r := control.ErrResponse(control.CodeBadRequest, fmt.Sprintf(format, args...))
	if posting {
		r.Sent = control.SentNo
	}
	c.Finish(r)
}

// validTarget reports whether a nickname can be used as a message target.
func validTarget(nick, chantypes string) bool {
	if nick == "" || strings.ContainsAny(nick[:1], chantypes+":") {
		return false
	}
	for _, c := range nick {
		if c <= ' ' || c == ',' || c == 0x7f {
			return false
		}
	}
	return true
}

// enqueueSend validates a publish or send and puts it on the message queue.
func (l *Listener) enqueueSend(c *control.Call) {
	s := l.sess
	req := c.Req
	text, err := outbound.Normalize(req.Text)
	if err != nil {
		badRequest(c, true, "%v; message not sent", err)
		return
	}
	if max := l.opts.Config.Limits.MaxMessageBytes; max > 0 && len(text) > max {
		r := control.ErrResponse(control.CodeTooLarge, fmt.Sprintf(
			"the message is %d bytes, over the limit of %d (limits.max_message_bytes); message not sent. Send a summary, or put the content in a file and send its path",
			len(text), max))
		r.Sent = control.SentNo
		c.Finish(r)
		return
	}

	target, prefix := l.channel, ""
	if req.Op == control.OpSend {
		chantypes := s.isupport.Get("CHANTYPES", "#")
		if !validTarget(req.To, chantypes) {
			badRequest(c, true, "%q is not a nickname; message not sent", req.To)
			return
		}
		if req.Private {
			target = req.To
		} else {
			actual, present := l.member(req.To)
			if !present {
				r := control.ErrResponse(control.CodeUnknownNick, fmt.Sprintf(
					"%s is not in %s; message not sent. Run `chatterbox whos-there` to see who is", req.To, l.channel))
				r.Sent = control.SentNo
				c.Finish(r)
				return
			}
			prefix = actual + ": "
		}
	}
	if req.Important {
		prefix += inbound.ImportantToken + " "
	}

	parts, err := outbound.Split(prefix, text, outbound.Limits{
		LineBudget: outbound.LineBudget(s.hostmask, target),
		MaxBytes:   s.maxBytes,
		MaxLines:   s.maxLines,
	})
	if err != nil {
		badRequest(c, true, "%v; message not sent", err)
		return
	}
	l.jobs = append(l.jobs, &job{call: c, target: target, parts: parts})
	l.pump()
}

// pump starts the next job when none is running.
func (l *Listener) pump() {
	for l.phase == phReady && l.cur == nil && len(l.jobs) > 0 {
		j := l.jobs[0]
		l.jobs = l.jobs[1:]
		l.cur = j
		if j.create != "" {
			l.createChannel(j)
		} else {
			l.sendPart(j)
		}
	}
}

// sendPart writes the next part of a message as one unit and waits for the
// server to echo it back.
func (l *Listener) sendPart(j *job) {
	s := l.sess
	part := j.parts[j.sent]
	label := l.request(l.tun.AckBase.D(), func(r *response) { l.onAck(j, r) })
	var lines []string
	if !part.Multiline() {
		lines = []string{irc.Line(map[string]string{"label": label}, "PRIVMSG", j.target, part.Lines[0].Text)}
	} else {
		s.batchSeq++
		ref := "cb" + strconv.Itoa(s.batchSeq)
		lines = append(lines, irc.Line(map[string]string{"label": label}, "BATCH", "+"+ref, "draft/multiline", j.target))
		for _, ln := range part.Lines {
			tags := map[string]string{"batch": ref}
			if ln.Concat {
				tags["draft/multiline-concat"] = ""
			}
			lines = append(lines, irc.Line(tags, "PRIVMSG", j.target, ln.Text))
		}
		lines = append(lines, irc.Line(nil, "BATCH", "-"+ref))
	}
	// The acknowledgement timer starts once the last line is out.
	s.wq.PushMessage(irc.Unit{Lines: lines, Written: l.written(label, len(lines))})
	s.curLabel = label
}

// onAck handles the server's answer to one part.
func (l *Listener) onAck(j *job, r *response) {
	if l.cur != j {
		return
	}
	switch {
	case r.lost:
		return // connectionGone reports it
	case r.timeout:
		l.cur = nil
		l.finishSend(j, control.CodeTimeout, "the server did not confirm the message in time", true)
	default:
		if text, refused := errorText(r); refused {
			l.cur = nil
			l.finishSend(j, control.CodeRejected, "the server refused the message: "+text, false)
			break
		}
		j.sent++
		if j.sent < len(j.parts) && !j.gone {
			j.call.Progress(j.sent, len(j.parts))
			l.sendPart(j)
			return
		}
		l.cur = nil
		ok := control.OKResponse()
		ok.Nick, ok.Channel, ok.Parts = l.nick, l.channel, len(j.parts)
		j.call.Finish(ok)
	}
	l.pump()
}

// batchRefused handles an unlabeled FAIL BATCH for the batch in flight.
func (l *Listener) batchRefused(text string) {
	j := l.cur
	if j == nil || j.create != "" || l.sess == nil {
		return
	}
	delete(l.sess.labels, l.sess.curLabel)
	l.cur = nil
	l.finishSend(j, control.CodeRejected, "the server refused the message: "+text, false)
	l.pump()
}

// finishSend fails a publish or send, saying whether anything was sent.
func (l *Listener) finishSend(j *job, code, why string, unknown bool) {
	r := control.ErrResponse(code, why)
	n := len(j.parts)
	switch {
	case j.sent > 0:
		r.Sent, r.PartsSent = control.SentPartial, j.sent
		if unknown {
			r.Error += fmt.Sprintf("; message partly sent: parts 1-%d of %d were delivered, part %d may or may not have been", j.sent, n, j.sent+1)
		} else {
			r.Error += fmt.Sprintf("; message partly sent: parts 1-%d of %d were delivered, the rest was not", j.sent, n)
		}
	case unknown:
		r.Sent = control.SentUnknown
		r.Error += "; the message may or may not have been sent. Check the channel before sending it again"
	default:
		r.Sent = control.SentNo
		r.Error += "; message not sent"
	}
	j.call.Finish(r)
}

// names answers whos-there with a WHO of the listener's channel.
func (l *Listener) names(c *control.Call) {
	s := l.sess
	channel := l.channel
	letter := s.isupport["BOT"]
	l.query(func(r *response) {
		if r.lost {
			c.Finish(l.notConnected(false))
			return
		}
		if r.timeout {
			c.Finish(control.ErrResponse(control.CodeTimeout, "the server did not answer the request in time"))
			return
		}
		var lines []*irc.Message
		if r.msg != nil {
			lines = append(lines, r.msg)
		} else {
			for i := range r.batch.Items {
				if m := r.batch.Items[i].Msg; m != nil {
					lines = append(lines, m)
				}
			}
		}
		out := []control.Participant{}
		for _, m := range lines {
			switch {
			case m.Command == "352" && len(m.Params) >= 7:
				// me channel user host server nick flags :hops realname
				kind := inbound.KindHuman
				if letter != "" && strings.Contains(m.Params[6], letter) {
					kind = inbound.KindBot
				}
				out = append(out, control.Participant{Nick: m.Params[5], Kind: kind})
			case m.Command != "315" && isNumeric(m.Command) && m.Command[0] >= '4':
				c.Finish(control.ErrResponse(control.CodeRejected, "the server refused the request: "+irc.Param(m, -1)))
				return
			}
		}
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Nick) < strings.ToLower(out[j].Nick) })
		ok := control.OKResponse()
		ok.Nick, ok.Channel, ok.Participants = l.nick, channel, out
		c.Finish(ok)
	}, "WHO", channel)
}

// chanOp is a channels request in progress: LIST, then one history lookup
// per channel for its last activity, within a time budget.
type chanOp struct {
	call     *control.Call
	infos    []control.ChannelInfo
	queue    []int // indexes into infos still to look up, in order
	deadline time.Time
	stopped  bool
}

// historyLookupSize is how many recent history entries are fetched to find
// a channel's newest chat message: history also holds joins and quits.
const historyLookupSize = "50"

func (l *Listener) channels(c *control.Call) {
	if l.chanOp != nil {
		// One at a time; a second request waits for the first.
		l.chanWaiting = append(l.chanWaiting, c)
		return
	}
	op := &chanOp{call: c, deadline: time.Now().Add(l.tun.ChannelsBudget.D())}
	l.chanOp = op
	l.query(func(r *response) {
		if l.chanOp != op {
			return
		}
		if r.lost {
			return // connectionGone answers
		}
		if r.timeout {
			l.finishChannels(op, control.ErrResponse(control.CodeTimeout, "the server did not answer the request in time"))
			return
		}
		var lines []*irc.Message
		if r.msg != nil {
			lines = append(lines, r.msg)
		} else {
			for i := range r.batch.Items {
				if m := r.batch.Items[i].Msg; m != nil {
					lines = append(lines, m)
				}
			}
		}
		for _, m := range lines {
			if m.Command == "322" && len(m.Params) >= 3 {
				users, _ := strconv.Atoi(m.Params[2])
				op.infos = append(op.infos, control.ChannelInfo{Name: m.Params[1], Users: users})
			}
		}
		l.planLookups(op)
		l.nextLookup(op)
	}, "LIST")
}

// planLookups orders the history lookups: the listener's own channel first,
// then the others, most populated first, up to the configured number.
func (l *Listener) planLookups(op *chanOp) {
	s := l.sess
	own := s.fold(l.channel)
	idx := make([]int, len(op.infos))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ia, ib := op.infos[idx[a]], op.infos[idx[b]]
		oa, ob := s.fold(ia.Name) == own, s.fold(ib.Name) == own
		if oa != ob {
			return oa
		}
		return ia.Users > ib.Users
	})
	if max := l.opts.Config.Limits.ChannelsHistoryLookups; len(idx) > max {
		idx = idx[:max]
	}
	op.queue = idx
}

func (l *Listener) nextLookup(op *chanOp) {
	s := l.sess
	if l.chanOp != op || s == nil {
		return
	}
	for len(op.queue) > 0 && op.stopped && s.fold(op.infos[op.queue[0]].Name) != s.fold(l.channel) {
		op.queue = op.queue[1:]
	}
	if len(op.queue) == 0 || !time.Now().Before(op.deadline) {
		l.finishChannels(op, control.Response{})
		return
	}
	i := op.queue[0]
	op.queue = op.queue[1:]
	name := op.infos[i].Name
	l.query(func(r *response) {
		if l.chanOp != op {
			return
		}
		if r.lost {
			return
		}
		switch {
		case r.batch != nil && r.batch.Type == "chathistory":
			if t, ok := newestChat(r.batch); ok {
				v := t.UTC().Format("2006-01-02T15:04:05Z")
				op.infos[i].LastActivity = &v
			}
		case s.fold(name) != s.fold(l.channel):
			// Refused for a channel the listener is not in: the
			// server's policy, so the other non-member lookups would
			// fail the same way.
			op.stopped = true
		}
		l.nextLookup(op)
	}, "CHATHISTORY", "LATEST", name, "*", historyLookupSize)
}

// newestChat returns the time of the newest chat message in a history batch.
func newestChat(b *irc.Batch) (time.Time, bool) {
	var newest time.Time
	found := false
	for i := range b.Items {
		it := &b.Items[i]
		var meta *irc.Message
		switch {
		case it.Msg != nil && (it.Msg.Command == "PRIVMSG" || it.Msg.Command == "NOTICE") && strings.Contains(it.Msg.Source, "!"):
			meta = it.Msg
		case it.Batch != nil && it.Batch.Type == "draft/multiline":
			meta = &it.Batch.Open
		default:
			continue
		}
		if t, ok := irc.Time(meta); ok && (!found || t.After(newest)) {
			newest, found = t, true
		}
	}
	return newest, found
}

// channelsTick ends a channels request whose time budget ran out; whatever
// has no answer by then is reported as unknown.
func (l *Listener) channelsTick(now time.Time) {
	if op := l.chanOp; op != nil && len(op.infos) > 0 && !now.Before(op.deadline) {
		l.finishChannels(op, control.Response{})
	}
}

func (l *Listener) finishChannels(op *chanOp, failure control.Response) {
	l.chanOp = nil
	if failure.Final() {
		op.call.Finish(failure)
	} else {
		infos := op.infos
		// Newest activity first, unknown last, then by name.
		sort.SliceStable(infos, func(a, b int) bool {
			la, lb := infos[a].LastActivity, infos[b].LastActivity
			switch {
			case la != nil && lb != nil && *la != *lb:
				return *la > *lb
			case (la == nil) != (lb == nil):
				return la != nil
			}
			return strings.ToLower(infos[a].Name) < strings.ToLower(infos[b].Name)
		})
		if infos == nil {
			infos = []control.ChannelInfo{}
		}
		ok := control.OKResponse()
		ok.Nick, ok.Channels = l.nick, infos
		op.call.Finish(ok)
	}
	if len(l.chanWaiting) > 0 && l.phase == phReady {
		next := l.chanWaiting[0]
		l.chanWaiting = l.chanWaiting[1:]
		l.channels(next)
	}
}

// createChannel moves the listener into a new channel (D2, 03 §8).
func (l *Listener) createChannel(j *job) {
	s := l.sess
	name, old := j.create, l.channel
	fail := func(code, msg string) {
		l.cur = nil
		j.call.Finish(control.ErrResponse(code, msg))
		l.pump()
	}

	// 1. Validate the name.
	chantypes := s.isupport.Get("CHANTYPES", "#")
	switch {
	case name == "" || !strings.ContainsAny(name[:1], chantypes):
		fail(control.CodeBadRequest, fmt.Sprintf("%q is not a channel name: it must start with %s", name, strings.Join(strings.Split(chantypes, ""), " or ")))
		return
	case len(name) > s.isupport.Int("CHANNELLEN", 64):
		fail(control.CodeBadRequest, fmt.Sprintf("the channel name is longer than the server allows (%d bytes)", s.isupport.Int("CHANNELLEN", 64)))
		return
	case len(name) < 2 || strings.IndexFunc(name, func(r rune) bool { return r <= ' ' || r == ',' || r == 0x7f }) >= 0:
		fail(control.CodeBadRequest, fmt.Sprintf("%q is not a channel name: it may not contain spaces, commas or control characters", name))
		return
	case s.fold(name) == s.fold(old):
		fail(control.CodeBadRequest, "the listener is already in "+old)
		return
	}

	exists := func() {
		fail(control.CodeChannelExists, fmt.Sprintf(
			"the channel %s already exists; nothing was created and the listener is still in %s. To listen there instead, restart the listener with --channel %s",
			name, old, name))
	}

	// 2. Fail if the server lists it.
	l.query(func(r *response) {
		if l.cur != j || r.lost {
			return
		}
		if r.timeout {
			fail(control.CodeTimeout, "the server did not answer the request in time; nothing was changed")
			return
		}
		listed := false
		check := func(m *irc.Message) {
			if m != nil && m.Command == "322" && s.fold(irc.Param(m, 1)) == s.fold(name) {
				listed = true
			}
		}
		check(r.msg)
		if r.batch != nil {
			for i := range r.batch.Items {
				check(r.batch.Items[i].Msg)
			}
		}
		if listed {
			exists()
			return
		}
		// 3. Tell the channel where the agent is going.
		label := l.request(l.tun.RequestTimeout.D(), func(r *response) {
			if l.cur != j || r.lost {
				return
			}
			l.moveTo(j, old, name, exists)
		})
		s.wq.PushMessage(irc.Unit{
			Lines:   []string{irc.Line(map[string]string{"label": label}, "PRIVMSG", old, "moving to "+name)},
			Written: l.written(label, 1),
		})
	}, "LIST", name)
}

// moveTo leaves the old channel and joins the new one, then checks that the
// listener is the one who created it.
func (l *Listener) moveTo(j *job, old, name string, exists func()) {
	s := l.sess
	saved := l.ref
	l.phase = phMoving
	s.expectPart[s.fold(old)] = true
	l.send(irc.Line(nil, "PART", old))

	back := func() {
		l.startJoin(old, func(ok bool, reason string) {
			if !ok {
				l.cur = nil
				j.call.Finish(control.ErrResponse(control.CodeChannelExists, "the channel "+name+" already exists, and returning to "+old+" failed: "+reason))
				l.fatal(exitRefused, "cannot rejoin %s after a failed create-channel: %s", old, reason)
				return
			}
			l.ref = saved
			l.startCatchup(func() {
				l.phase = phReady
				l.admitWaiting()
				exists()
			})
		})
	}

	l.startJoin(name, func(ok bool, reason string) {
		if !ok {
			// 5. The server would not let us in or create it.
			back()
			return
		}
		ch := s.fold(name)
		if s.selfOp[ch] {
			// 6. Created: the new channel is the listener's channel
			// from here on, and reconnects rejoin it.
			l.channel = name
			l.ref = s.joinEcho[ch]
			l.phase = phReady
			l.cur = nil
			ok := control.OKResponse()
			ok.Nick, ok.Channel = l.nick, name
			j.call.Finish(ok)
			l.diag("now listening on %s", name)
			l.admitWaiting()
			l.pump()
			return
		}
		// 7. Someone else was there first.
		s.expectPart[ch] = true
		l.send(irc.Line(nil, "PART", name))
		back()
	})
}

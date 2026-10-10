package listener

import (
	"strconv"
	"strings"
	"time"

	"github.com/teo-mateo/chatterbox/internal/identity"
	"github.com/teo-mateo/chatterbox/internal/inbound"
	"github.com/teo-mateo/chatterbox/internal/irc"
)

// Capabilities the listener cannot work without (03 §1).
var requiredCaps = []string{
	"message-tags", "server-time", "batch", "draft/multiline",
	"draft/chathistory", "echo-message", "labeled-response",
}

// Capabilities requested when the server has them. draft/event-playback
// makes history carry joins and quits as themselves, which the listener
// ignores; without it Ergo replays them as chat lines from a pseudo-user.
var optionalCaps = []string{"standard-replies", "multi-prefix", "draft/event-playback"}

// session is everything tied to one connection.
type session struct {
	gen  int
	conn *irc.Conn
	wq   *irc.WriteQueue
	asm  irc.Assembler

	caps       irc.Caps
	isupport   irc.ISupport
	casemap    identity.Casemap
	registered bool
	hostmask   string
	maxBytes   int
	maxLines   int

	labels   map[string]*pending
	labelSeq int
	batchSeq int
	curLabel string // label of the message part in flight

	lastRecv time.Time
	lastPing time.Time
	// busyUntil estimates when the server's flood control will have worked
	// off what the listener has sent: it reads about one line per
	// AckPerLine once the initial burst is used up, and answers nothing in
	// the meantime. Deadlines are counted from there.
	busyUntil time.Time

	// Per channel, keyed by folded name.
	members    map[string]map[string]string // folded nick -> nick
	selfIn     map[string]bool
	selfOp     map[string]bool
	joinEcho   map[string]reference
	expectPart map[string]bool
	joinTarget string
}

type pending struct {
	fn func(*response)
	// base is how long the server may take once it gets to the request;
	// the deadline is set when the request has been written.
	base     time.Duration
	deadline time.Time
}

// response is what a labeled request got back: one message, one batch, or
// nothing because the request timed out or the connection was lost.
type response struct {
	msg     *irc.Message
	batch   *irc.Batch
	timeout bool
	lost    bool
}

func (s *session) close() {
	s.wq.Close()
	s.conn.Close()
}

func (s *session) fold(name string) string { return s.casemap.Fold(name) }

func (l *Listener) startSession(conn *irc.Conn) {
	now := time.Now()
	s := &session{
		gen: l.gen, conn: conn, wq: irc.NewWriteQueue(),
		caps: irc.Caps{}, isupport: irc.ISupport{},
		labels:   map[string]*pending{},
		lastRecv: now, lastPing: now,
		members: map[string]map[string]string{}, selfIn: map[string]bool{}, selfOp: map[string]bool{},
		joinEcho: map[string]reference{}, expectPart: map[string]bool{},
	}
	l.sess = s
	gen := s.gen
	go func() {
		for {
			msg, err := conn.ReadMessage()
			if err != nil {
				l.post(evConnErr{gen: gen, err: err})
				return
			}
			l.post(evLine{gen: gen, msg: msg})
		}
	}()
	go func() {
		if err := s.wq.Run(conn.WriteLines); err != nil {
			l.post(evConnErr{gen: gen, err: err})
		}
	}()
	l.phase = phCapLS
	l.deadline = now.Add(l.tun.RegisterTimeout.D())
	l.send(irc.Line(nil, "CAP", "LS", "302"))
}

// send queues a single non-message command.
func (l *Listener) send(line string) {
	if s := l.sess; s != nil {
		gen := s.gen
		s.wq.Push(irc.Unit{Lines: []string{line}, Written: func() { l.post(evWritten{gen: gen, lines: 1}) }})
	}
}

// written returns the callback that reports a labeled unit as written.
func (l *Listener) written(label string, lines int) func() {
	gen := l.sess.gen
	return func() { l.post(evWritten{gen: gen, label: label, lines: lines}) }
}

// request registers a handler for a labeled request and returns the label.
// The request times out base after the server can be expected to have
// reached it (see session.busyUntil).
func (l *Listener) request(base time.Duration, fn func(*response)) string {
	s := l.sess
	s.labelSeq++
	label := "cbx" + strconv.Itoa(s.labelSeq)
	s.labels[label] = &pending{fn: fn, base: base}
	return label
}

// query sends a labeled non-message command.
func (l *Listener) query(fn func(*response), command string, params ...string) {
	s := l.sess
	if s == nil {
		fn(&response{lost: true})
		return
	}
	label := l.request(l.tun.RequestTimeout.D(), fn)
	s.wq.Push(irc.Unit{
		Lines:   []string{irc.Line(map[string]string{"label": label}, command, params...)},
		Written: l.written(label, 1),
	})
}

func (l *Listener) resolve(label string, r *response) {
	s := l.sess
	if s == nil {
		return
	}
	if p, ok := s.labels[label]; ok {
		delete(s.labels, label)
		p.fn(r)
	}
}

// onWritten accounts for lines handed to the connection and starts the
// timer of the request they carried.
func (l *Listener) onWritten(ev evWritten) {
	s := l.sess
	now := time.Now()
	if s.busyUntil.Before(now) {
		s.busyUntil = now
	}
	s.busyUntil = s.busyUntil.Add(time.Duration(ev.lines) * l.tun.AckPerLine.D())
	if p, ok := s.labels[ev.label]; ok && p.deadline.IsZero() {
		p.deadline = s.busyUntil.Add(p.base)
	}
}

func (l *Listener) onLine(m irc.Message) {
	s := l.sess
	item := s.asm.Feed(m)
	if item != nil {
		l.onItem(s, item)
	}
}

// onItem processes a message or a closed top-level batch.
func (l *Listener) onItem(s *session, item *irc.Item) {
	if l.sess != s {
		return
	}
	if b := item.Batch; b != nil {
		switch b.Type {
		case "draft/multiline":
			l.onMultiline(b, false)
		case "chathistory", "draft/chathistory-targets":
			// Only meaningful as the answer to a request. History the
			// listener did not ask for is discarded.
		default: // labeled-response and anything unknown: plain lines
			for i := range b.Items {
				l.onItem(s, &b.Items[i])
			}
		}
		if label := irc.Tag(&b.Open, "label"); label != "" && l.sess == s {
			l.resolve(label, &response{batch: b})
		}
		return
	}
	m := item.Msg
	l.onMessage(s, m)
	if label := irc.Tag(m, "label"); label != "" && l.sess == s {
		l.resolve(label, &response{msg: m})
	}
}

func isNumeric(cmd string) bool {
	return len(cmd) == 3 && cmd[0] >= '0' && cmd[0] <= '9'
}

// errorText extracts a human-readable refusal from a response, if it is one.
func errorText(r *response) (string, bool) {
	check := func(m *irc.Message) (string, bool) {
		switch {
		case m.Command == "FAIL":
			return irc.Param(m, -1), true
		case isNumeric(m.Command) && m.Command[0] >= '4':
			return irc.Param(m, -1), true
		}
		return "", false
	}
	if r.msg != nil {
		return check(r.msg)
	}
	if r.batch != nil {
		for _, it := range r.batch.Items {
			if it.Msg != nil {
				if text, bad := check(it.Msg); bad {
					return text, true
				}
			}
		}
	}
	return "", false
}

func (l *Listener) onMessage(s *session, m *irc.Message) {
	switch m.Command {
	case "PING":
		l.send(irc.Line(nil, "PONG", irc.Param(m, -1)))
	case "ERROR":
		l.connLost("the server closed the connection: " + irc.Param(m, -1))
	case "CAP":
		l.onCap(s, m)
	case "001":
		s.registered = true
		l.nickBusy = time.Time{}
		if got := irc.Param(m, 0); !strings.EqualFold(got, l.nick) {
			l.fatal(exitRefused, "the server assigned the nickname %s instead of %s", got, l.nick)
		}
	case "005":
		s.isupport.Add(m)
	case "376", "422":
		if l.phase == phRegistering {
			l.registered(s)
		}
	case "433":
		if !s.registered {
			l.nickInUse()
		}
	case "432":
		if !s.registered {
			l.fatal(exitUsage, "the server rejected the nickname %s: %s", l.nick, irc.Param(m, -1))
		}
	case "465":
		l.connLost("banned from the server: " + irc.Param(m, -1))
	case "FAIL":
		l.onFail(m)
	case "353":
		l.onNames(s, m)
	case "366":
		if ch := irc.Param(m, 1); l.onJoin != nil && s.fold(ch) == s.fold(s.joinTarget) && s.selfIn[s.fold(ch)] {
			l.joinDone(true, "")
		}
	case "403", "405", "471", "473", "474", "475", "476", "477", "479":
		if l.onJoin != nil && s.fold(irc.Param(m, 1)) == s.fold(s.joinTarget) {
			l.joinDone(false, irc.Param(m, -1))
		}
	case "JOIN":
		l.onJoinLine(s, m)
	case "PART":
		l.onPart(s, m)
	case "KICK":
		l.onKick(s, m)
	case "QUIT":
		nick := s.fold(irc.Nick(m.Source))
		for _, members := range s.members {
			delete(members, nick)
		}
	case "NICK":
		l.onNick(s, m)
	case "PRIVMSG", "NOTICE":
		l.onChat(m, m.Command, irc.Param(m, 0), irc.Param(m, -1), false)
	}
}

func (l *Listener) onCap(s *session, m *irc.Message) {
	server := l.opts.Config.Server.Host
	switch strings.ToUpper(irc.Param(m, 1)) {
	case "LS":
		if l.phase != phCapLS || !s.caps.AddLS(m) {
			return
		}
		missing := s.caps.Missing(requiredCaps)
		s.maxBytes, s.maxLines = irc.MultilineLimits(s.caps["draft/multiline"])
		if len(missing) == 0 && s.maxBytes == 0 {
			missing = []string{"draft/multiline (advertised without a usable max-bytes)"}
		}
		if len(missing) > 0 {
			l.fatal(exitConnect, "the server %s lacks the IRCv3 capability %s, which chatterbox requires",
				server, strings.Join(missing, ", "))
			return
		}
		want := append([]string(nil), requiredCaps...)
		for _, c := range optionalCaps {
			if s.caps.Has(c) {
				want = append(want, c)
			}
		}
		l.phase = phCapReq
		l.send(irc.Line(nil, "CAP", "REQ", strings.Join(want, " ")))
		l.send(irc.Line(nil, "NICK", l.nick))
		l.send(irc.Line(nil, "USER", l.nick, "0", "*", "chatterbox agent"))
	case "ACK":
		if l.phase == phCapReq {
			l.phase = phRegistering
			l.send(irc.Line(nil, "CAP", "END"))
		}
	case "NAK":
		l.fatal(exitConnect, "the server %s refused the capabilities chatterbox requires (%s)", server, irc.Param(m, -1))
	case "DEL":
		for _, c := range strings.Fields(irc.Param(m, -1)) {
			for _, r := range requiredCaps {
				if c == r {
					l.fatal(exitConnect, "the server %s withdrew the capability %s, which chatterbox requires", server, c)
					return
				}
			}
		}
	}
	// CAP NEW is ignored.
}

func (l *Listener) onFail(m *irc.Message) {
	if irc.Param(m, 1) == "ACCOUNT_REQUIRED" {
		l.fatal(exitRefused, "the server requires an account (%s); registered accounts are out of scope for chatterbox", irc.Param(m, -1))
		return
	}
	// Ergo does not label the failure of a multiline batch. Only one
	// message is in flight at a time, so it belongs to that one.
	if irc.Param(m, 0) == "BATCH" && !m.HasTag("label") {
		l.batchRefused(irc.Param(m, -1))
	}
}

func (l *Listener) nickInUse() {
	server := l.opts.Config.Server.Host
	if !l.everReady {
		l.fatal(exitNickInUse, "nickname %s is already in use on %s", l.nick, server)
		return
	}
	// After a network loss the server may still hold the nickname for the
	// old connection. Keep redialing, never pick another name (D10).
	now := time.Now()
	if l.nickBusy.IsZero() {
		l.nickBusy = now
	}
	if now.Sub(l.nickBusy) >= l.tun.NickRetryWindow.D() {
		l.fatal(exitNickInUse, "nickname %s is already in use on %s (still taken %s after the connection was lost)",
			l.nick, server, l.tun.NickRetryWindow.D())
		return
	}
	l.connFailed("the server still holds the nickname " + l.nick)
}

// registered runs when the welcome burst is complete.
func (l *Listener) registered(s *session) {
	s.casemap = identity.ParseCasemap(s.isupport["CASEMAPPING"])
	letter := s.isupport["BOT"]
	if letter == "" {
		l.fatal(exitConnect, "the server %s has no bot mode (no BOT token), so the listener cannot mark itself as a bot",
			l.opts.Config.Server.Host)
		return
	}
	l.phase = phBotMode
	l.query(func(r *response) {
		switch {
		case r.lost:
		case r.timeout:
			l.connLost("the server did not answer the bot mode request")
		case r.msg != nil && r.msg.Command == "MODE" && modeAdds(irc.Param(r.msg, -1), letter):
			l.startJoin(l.channel, l.firstJoinDone)
		default:
			text, _ := errorText(r)
			l.fatal(exitConnect, "the server %s refused bot mode (+%s) for %s: %s", l.opts.Config.Server.Host, letter, l.nick, text)
		}
	}, "MODE", l.nick, "+"+letter)
}

// modeAdds reports whether a mode string such as "+iB" sets the letter.
func modeAdds(modes, letter string) bool {
	adding := true
	for _, c := range modes {
		switch c {
		case '+':
			adding = true
		case '-':
			adding = false
		default:
			if adding && string(c) == letter {
				return true
			}
		}
	}
	return false
}

// startJoin joins a channel; done runs when the join completed or failed.
func (l *Listener) startJoin(channel string, done func(ok bool, reason string)) {
	s := l.sess
	s.joinTarget = channel
	l.onJoin = done
	if l.phase != phMoving {
		l.phase = phJoining
	}
	l.deadline = time.Now().Add(l.tun.RegisterTimeout.D())
	l.send(irc.Line(nil, "JOIN", channel))
}

func (l *Listener) joinDone(ok bool, reason string) {
	done := l.onJoin
	l.onJoin = nil
	if done != nil {
		done(ok, reason)
	}
}

// firstJoinDone continues after the join that follows registration.
func (l *Listener) firstJoinDone(ok bool, reason string) {
	if !ok {
		l.fatal(exitRefused, "cannot join %s: %s", l.channel, reason)
		return
	}
	if !l.everReady {
		// Nothing is replayed on the first connection: the reference
		// for later catch-up is the moment the listener became present.
		l.ref = l.sess.joinEcho[l.sess.fold(l.channel)]
		l.ready()
		return
	}
	l.startCatchup(l.ready)
}

// ready makes the listener serve requests and deliver live messages.
func (l *Listener) ready() {
	first := !l.everReady
	l.phase = phReady
	l.everReady = true
	l.readySince = time.Now()
	if first {
		l.diag("joined %s as %s", l.channel, l.nick)
	} else {
		l.diag("reconnected; %d messages replayed", l.replayed)
	}
	l.replayed = 0
	l.admitWaiting()
	l.pump()
}

func (l *Listener) onNames(s *session, m *irc.Message) {
	// :server 353 me <symbol> <channel> :[prefixes]nick ...
	ch := s.fold(irc.Param(m, 2))
	all, ops := s.isupport.PrefixChars()
	members := s.members[ch]
	if members == nil {
		members = map[string]string{}
		s.members[ch] = members
	}
	for _, entry := range strings.Fields(irc.Param(m, -1)) {
		nick := strings.TrimLeft(entry, all)
		prefixes := entry[:len(entry)-len(nick)]
		if i := strings.IndexByte(nick, '!'); i >= 0 {
			nick = nick[:i]
		}
		members[s.fold(nick)] = nick
		if s.fold(nick) == s.fold(l.nick) && strings.ContainsAny(prefixes, ops) && ops != "" {
			s.selfOp[ch] = true
		}
	}
}

func (l *Listener) onJoinLine(s *session, m *irc.Message) {
	nick := irc.Nick(m.Source)
	ch := s.fold(irc.Param(m, 0))
	if s.fold(nick) != s.fold(l.nick) {
		if members := s.members[ch]; members != nil {
			members[s.fold(nick)] = nick
		}
		return
	}
	s.selfIn[ch] = true
	s.selfOp[ch] = false
	s.hostmask = m.Source
	s.members[ch] = map[string]string{s.fold(nick): nick}
	if t, ok := irc.Time(m); ok {
		s.joinEcho[ch] = reference{time: t, id: irc.Tag(m, "msgid")}
	}
}

func (l *Listener) onPart(s *session, m *irc.Message) {
	nick := irc.Nick(m.Source)
	ch := s.fold(irc.Param(m, 0))
	if s.fold(nick) != s.fold(l.nick) {
		delete(s.members[ch], s.fold(nick))
		return
	}
	s.selfIn[ch] = false
	delete(s.members, ch)
	if s.expectPart[ch] {
		delete(s.expectPart, ch)
		return
	}
	if ch == s.fold(l.channel) {
		l.fatal(exitRefused, "removed from %s by the server", l.channel)
	}
}

func (l *Listener) onKick(s *session, m *irc.Message) {
	ch := s.fold(irc.Param(m, 0))
	victim := irc.Param(m, 1)
	if s.fold(victim) != s.fold(l.nick) {
		delete(s.members[ch], s.fold(victim))
		return
	}
	s.selfIn[ch] = false
	delete(s.members, ch)
	reason := ""
	if len(m.Params) >= 3 && m.Params[2] != "" {
		reason = ": " + m.Params[2]
	}
	l.fatal(exitRefused, "kicked from %s by %s%s", irc.Param(m, 0), irc.Nick(m.Source), reason)
}

func (l *Listener) onNick(s *session, m *irc.Message) {
	old, renamed := irc.Nick(m.Source), irc.Param(m, 0)
	if s.fold(old) == s.fold(l.nick) {
		l.fatal(exitRefused, "the server changed the nickname from %s to %s; the listener only runs under the name it was given", l.nick, renamed)
		return
	}
	for _, members := range s.members {
		if _, ok := members[s.fold(old)]; ok {
			delete(members, s.fold(old))
			members[s.fold(renamed)] = renamed
		}
	}
}

// member returns the nickname as the server spells it, if it is in the
// listener's channel.
func (l *Listener) member(nick string) (string, bool) {
	s := l.sess
	actual, ok := s.members[s.fold(l.channel)][s.fold(nick)]
	return actual, ok
}

// bufferedChat is a live message held back while history is replayed.
type bufferedChat struct {
	raw       inbound.Raw
	hasTime   bool
	inChannel bool
}

func (l *Listener) onMultiline(b *irc.Batch, replayed bool) {
	if len(b.Params) == 0 {
		return
	}
	l.onChat(&b.Open, irc.MultilineCommand(b), b.Params[0], irc.MultilineText(b), replayed)
}

// onChat handles one chat message; meta carries its source and tags (the
// message itself, or the opening line of its multiline batch).
func (l *Listener) onChat(meta *irc.Message, command, target, text string, replayed bool) {
	s := l.sess
	// Server notices have no user in their source.
	if command == "NOTICE" && !strings.Contains(meta.Source, "!") {
		return
	}
	t, hasTime := irc.Time(meta)
	if !hasTime {
		t = time.Now().UTC()
	}
	c := bufferedChat{
		raw: inbound.Raw{
			Time: t, From: irc.Nick(meta.Source), Target: target, Text: text,
			Bot: meta.HasTag("bot"), MsgID: irc.Tag(meta, "msgid"), Replayed: replayed,
		},
		hasTime:   hasTime,
		inChannel: s.fold(target) == s.fold(l.channel),
	}
	// While history is being replayed, live messages wait their turn.
	if l.catchup != nil && !replayed {
		l.liveBuf = append(l.liveBuf, c)
		return
	}
	l.deliver(c)
}

// deliver runs a message through dedup, classification, the wake filter and
// the encoder.
func (l *Listener) deliver(c bufferedChat) {
	if c.inChannel {
		l.ref.advance(c.raw.Time, c.raw.MsgID, c.hasTime)
	}
	if !l.seen.Add(c.raw.MsgID) {
		return
	}
	m, ok := inbound.Classify(c.raw, l.self())
	if !ok {
		return
	}
	if c.raw.Replayed {
		l.replayed++
	}
	if inbound.Printed(m, l.wake) {
		l.out.Push(l.enc(m))
	}
}

// catchupState is one run of fetching missed history.
type catchupState struct {
	channel string
	refID   string
	first   bool
	pages   int
	lastID  string
	done    func()
}

// startCatchup fetches what was posted in the channel since the reference
// and delivers it before anything live (03 §6).
func (l *Listener) startCatchup(done func()) {
	if l.phase != phMoving {
		l.phase = phCatchup
	}
	l.replayed = 0
	if l.ref.time.IsZero() {
		l.diag("no reference point for the channel history; messages posted while disconnected may have been missed")
		done()
		return
	}
	c := &catchupState{channel: l.channel, refID: l.ref.id, first: true, done: done}
	l.catchup = c
	// Start from a timestamp, slightly early so messages sharing the last
	// timestamp are not skipped; dedup removes the repeats.
	l.historyPage(c, irc.TimeParam(l.ref.time.Add(-l.tun.CatchupOverlap.D())))
}

func (l *Listener) historyPage(c *catchupState, ref string) {
	limit := strconv.Itoa(l.sess.isupport.Int("CHATHISTORY", 100))
	l.query(func(r *response) { l.onHistory(c, r) }, "CHATHISTORY", "AFTER", c.channel, ref, limit)
}

func itemID(it *irc.Item) string {
	if it.Msg != nil {
		return irc.Tag(it.Msg, "msgid")
	}
	return irc.Tag(&it.Batch.Open, "msgid")
}

func (l *Listener) onHistory(c *catchupState, r *response) {
	if l.catchup != c || r.lost {
		return
	}
	if r.batch == nil || r.batch.Type != "chathistory" {
		why := "no answer"
		if text, bad := errorText(r); bad {
			why = text
		}
		l.diag("could not fetch the history of %s (%s); messages posted while disconnected may have been missed", c.channel, why)
		l.finishCatchup(c)
		return
	}
	items := r.batch.Items
	newest := ""
	foundRef := c.refID == ""
	for i := range items {
		it := &items[i]
		if id := itemID(it); id != "" {
			newest = id
			if id == c.refID {
				foundRef = true
			}
		}
		switch {
		case it.Msg != nil && (it.Msg.Command == "PRIVMSG" || it.Msg.Command == "NOTICE"):
			l.onChat(it.Msg, it.Msg.Command, irc.Param(it.Msg, 0), irc.Param(it.Msg, -1), true)
		case it.Batch != nil && it.Batch.Type == "draft/multiline":
			l.onMultiline(it.Batch, true)
		}
	}
	if c.first && !foundRef {
		l.diag("the history of %s does not reach back to the disconnect; messages may have been missed", c.channel)
	}
	c.first = false
	// An empty page is the end. A short page is not: servers may return
	// fewer messages than the limit.
	if len(items) == 0 || newest == "" || newest == c.lastID || c.pages >= 10000 {
		l.finishCatchup(c)
		return
	}
	c.lastID = newest
	c.pages++
	// This id was just issued by the server, so it is a safe reference.
	l.historyPage(c, "msgid="+newest)
}

func (l *Listener) finishCatchup(c *catchupState) {
	l.catchup = nil
	buffered := l.liveBuf
	l.liveBuf = nil
	for _, b := range buffered {
		l.deliver(b)
	}
	c.done()
}

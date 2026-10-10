package listener_test

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/teo-mateo/chatterbox/internal/config"
	"github.com/teo-mateo/chatterbox/internal/control"
	"github.com/teo-mateo/chatterbox/internal/irc"
)

func publish(text string) control.Request {
	return control.Request{Op: control.OpPublish, Text: text}
}

// UT-LABEL: echo, labeled error, unlabeled FAIL, timeout, unrelated traffic.
func TestAcknowledgement(t *testing.T) {
	s := newFakeServer(t)
	var modeV atomic.Value
	modeV.Store("echo")
	s.hook = func(c *fakeConn, m irc.Message) bool {
		label := irc.Tag(&m, "label")
		mode := modeV.Load().(string)
		switch {
		case m.Command == "PRIVMSG" && irc.Tag(&m, "batch") == "" && mode == "numeric":
			c.send("@label=%s :fake 404 %s %s :Cannot send to channel (+m)", label, c.nick, irc.Param(&m, 0))
			return true
		case m.Command == "PRIVMSG" && irc.Tag(&m, "batch") == "" && mode == "silent":
			return true
		case m.Command == "PRIVMSG" && irc.Tag(&m, "batch") == "" && mode == "unrelated":
			// Labeled and unlabeled traffic that is not the answer.
			c.send("@label=someone-elses :fake NOTICE %s :not yours", c.nick)
			c.send(":peer!u@h PRIVMSG #c :chatter in between")
			return false
		case m.Command == "BATCH" && strings.HasPrefix(irc.Param(&m, 0), "-") && mode == "fail":
			// Ergo does not label the failure of a multiline batch.
			c.send(":fake FAIL BATCH MULTILINE_MAX_BYTES 4096 :Multiline batch byte limit 4096 exceeded")
			return true
		}
		return false
	}
	r := startRun(t, s, nil)
	r.waitErr("joined #c as agentA")

	if resp := r.do(publish("hello")); !resp.Succeeded() || resp.Parts != 1 || resp.Nick != "agentA" || resp.Channel != "#c" {
		t.Errorf("echo: %+v", resp)
	}
	if resp := r.do(publish("one\ntwo")); !resp.Succeeded() {
		t.Errorf("multiline echo: %+v", resp)
	}

	modeV.Store("unrelated")
	if resp := r.do(publish("with noise")); !resp.Succeeded() {
		t.Errorf("unrelated labeled traffic confused the acknowledgement: %+v", resp)
	}
	r.waitOut("chatter in between")

	modeV.Store("numeric")
	resp := r.do(publish("refused"))
	if resp.Code != control.CodeRejected || resp.Sent != control.SentNo ||
		!strings.Contains(resp.Error, "Cannot send to channel (+m)") || !strings.Contains(resp.Error, "message not sent") {
		t.Errorf("labeled numeric: %+v", resp)
	}

	modeV.Store("fail")
	resp = r.do(publish("a\nb"))
	if resp.Code != control.CodeRejected || resp.Sent != control.SentNo || !strings.Contains(resp.Error, "byte limit 4096 exceeded") {
		t.Errorf("unlabeled FAIL BATCH: %+v", resp)
	}

	modeV.Store("silent")
	start := time.Now()
	resp = r.do(publish("into the void"))
	if resp.Code != control.CodeTimeout || resp.Sent != control.SentUnknown || !strings.Contains(resp.Error, "may or may not have been sent") {
		t.Errorf("timeout: %+v", resp)
	}
	if d := time.Since(start); d < 300*time.Millisecond || d > 3*time.Second {
		t.Errorf("timed out after %s, expected about the 400 ms acknowledgement time", d)
	}

	// The listener is still usable after each failure.
	modeV.Store("echo")
	if resp := r.do(publish("fine again")); !resp.Succeeded() {
		t.Errorf("after the failures: %+v", resp)
	}
}

// UT-LABEL: the acknowledgement time scales with the lines written, because
// the server's flood control works through them before it answers.
func TestAcknowledgementScalesWithSize(t *testing.T) {
	s := newFakeServer(t)
	s.hook = func(c *fakeConn, m irc.Message) bool {
		if m.Command == "BATCH" && strings.HasPrefix(irc.Param(&m, 0), "-") {
			go func() {
				time.Sleep(900 * time.Millisecond) // over ack_base alone
				c.react(m)
			}()
			return true
		}
		return false
	}
	r := startRun(t, s, func(tn *config.Tuning) {
		tn.AckBase, tn.AckPerLine = config.Duration(300*time.Millisecond), config.Duration(40*time.Millisecond)
	})
	r.waitErr("joined #c")
	// 30 lines: 32 written lines * 40 ms + 300 ms = about 1.6 s allowed.
	text := strings.TrimSuffix(strings.Repeat("line\n", 30), "\n")
	if resp := r.do(publish(text)); !resp.Succeeded() {
		t.Errorf("a slow echo of a long message was treated as lost: %+v", resp)
	}
	// 2 lines: about 0.5 s allowed, less than the echo takes. The lines of
	// the previous message no longer count: their time has passed.
	time.Sleep(1300 * time.Millisecond)
	if resp := r.do(publish("a\nb")); resp.Code != control.CodeTimeout {
		t.Errorf("a short message got the long deadline: %+v", resp)
	}
}

// A split message that fails part-way reports how much was delivered.
func TestPartialDelivery(t *testing.T) {
	s := newFakeServer(t)
	var batches atomic.Int32
	s.hook = func(c *fakeConn, m irc.Message) bool {
		if m.Command == "BATCH" && strings.HasPrefix(irc.Param(&m, 0), "-") {
			if batches.Add(1) == 2 {
				label := c.batch[irc.Param(&m, 0)[1:]].label
				c.send("@label=%s :fake 404 %s #c :Cannot send to channel", label, c.nick)
				return true
			}
		}
		return false
	}
	r := startRun(t, s, nil)
	r.waitErr("joined #c")
	text := strings.TrimSuffix(strings.Repeat(strings.Repeat("x", 300)+"\n", 40), "\n") // 12 000 bytes
	resp := r.do(publish(text))
	if resp.Code != control.CodeRejected || resp.Sent != control.SentPartial || resp.PartsSent != 1 {
		t.Errorf("%+v", resp)
	}
	if !strings.Contains(resp.Error, "parts 1-1 of 4 were delivered, the rest was not") {
		t.Errorf("error text: %s", resp.Error)
	}
	if n := batches.Load(); n != 2 {
		t.Errorf("%d batches were written; nothing may follow a failed part", n)
	}
}

// UT-FSM: failures before the first ready are fatal, with the right code.
func TestFatalBeforeReady(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeServer)
		code  int
		text  string
	}{
		{"missing capability", func(s *fakeServer) {
			s.caps = strings.Replace(defaultCaps, "draft/chathistory ", "", 1)
		}, 9, "lacks the IRCv3 capability draft/chathistory"},
		{"several missing capabilities", func(s *fakeServer) {
			s.caps = "message-tags server-time batch"
		}, 9, "draft/multiline, draft/chathistory, echo-message, labeled-response"},
		{"multiline without limits", func(s *fakeServer) {
			s.caps = strings.Replace(defaultCaps, "draft/multiline=max-bytes=4096,max-lines=100", "draft/multiline", 1)
		}, 9, "draft/multiline"},
		{"capabilities refused", func(s *fakeServer) {
			s.hook = func(c *fakeConn, m irc.Message) bool {
				if m.Command == "CAP" && irc.Param(&m, 0) == "REQ" {
					c.send(":fake CAP * NAK :%s", irc.Param(&m, 1))
					return true
				}
				return false
			}
		}, 9, "refused the capabilities"},
		{"no bot mode", func(s *fakeServer) {
			s.isupport = strings.Replace(defaultISupport, "BOT=B ", "", 1)
		}, 9, "has no bot mode"},
		{"bot mode refused", func(s *fakeServer) {
			s.hook = func(c *fakeConn, m irc.Message) bool {
				if m.Command == "MODE" {
					c.send("@label=%s :fake 501 %s :Unknown MODE flag", irc.Tag(&m, "label"), c.nick)
					return true
				}
				return false
			}
		}, 9, "refused bot mode (+B) for agentA: Unknown MODE flag"},
		{"nickname in use", func(s *fakeServer) {
			s.hook = func(c *fakeConn, m irc.Message) bool {
				if m.Command == "NICK" {
					c.send(":fake 433 * agentA :Nickname is already in use")
					return true
				}
				return false
			}
		}, 4, "nickname agentA is already in use on 127.0.0.1"},
		{"erroneous nickname", func(s *fakeServer) {
			s.hook = func(c *fakeConn, m irc.Message) bool {
				if m.Command == "NICK" {
					c.send(":fake 432 * agentA :Erroneous nickname")
					return true
				}
				return false
			}
		}, 2, "rejected the nickname agentA"},
		{"another nickname assigned", func(s *fakeServer) {
			s.hook = func(c *fakeConn, m irc.Message) bool {
				if m.Command == "CAP" && irc.Param(&m, 0) == "END" {
					c.send(":fake 001 Guest-123 :Welcome")
					return true
				}
				return false
			}
		}, 6, "assigned the nickname Guest-123 instead of agentA"},
		{"account required", func(s *fakeServer) {
			s.hook = func(c *fakeConn, m irc.Message) bool {
				if m.Command == "CAP" && irc.Param(&m, 0) == "END" {
					c.send(":fake FAIL * ACCOUNT_REQUIRED :You must log in with SASL to join this server")
					return true
				}
				return false
			}
		}, 6, "accounts are out of scope"},
		{"banned", func(s *fakeServer) {
			s.hook = func(c *fakeConn, m irc.Message) bool {
				if m.Command == "CAP" && irc.Param(&m, 0) == "END" {
					c.send(":fake 465 agentA :You are banned from this server")
					c.send("ERROR :Closing link")
					c.c.Close()
					return true
				}
				return false
			}
		}, 9, "banned from the server"},
		{"join refused", func(s *fakeServer) {
			s.hook = func(c *fakeConn, m irc.Message) bool {
				if m.Command == "JOIN" {
					c.send(":fake 474 %s %s :Cannot join channel (+b)", c.nick, irc.Param(&m, 0))
					return true
				}
				return false
			}
		}, 6, "cannot join #c: Cannot join channel (+b)"},
		{"server closes during registration", func(s *fakeServer) {
			s.hook = func(c *fakeConn, m irc.Message) bool {
				if m.Command == "USER" {
					c.c.Close()
					return true
				}
				return false
			}
		}, 9, "closed the connection"},
		{"registration never completes", func(s *fakeServer) {
			s.hook = func(c *fakeConn, m irc.Message) bool {
				return m.Command == "CAP" && irc.Param(&m, 0) == "END"
			}
		}, 9, "did not complete registering"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeServer(t)
			tc.setup(s)
			r := startRun(t, s, func(tn *config.Tuning) { tn.RegisterTimeout = config.Duration(500 * time.Millisecond) })
			e := r.exit(10 * time.Second)
			if e == nil {
				t.Fatal("the listener exited 0")
			}
			if e.Code != tc.code || !strings.Contains(e.Msg, tc.text) {
				t.Errorf("exit %d %q, want %d containing %q", e.Code, e.Msg, tc.code, tc.text)
			}
			if n := s.connections(); n != 1 {
				t.Errorf("%d connection attempts; a failure before the first ready must not be retried", n)
			}
			if len(r.stdout.get()) != 0 {
				t.Errorf("stdout: %q", r.stdout.get())
			}
			if !socketGone(r.sock) {
				t.Error("the control socket was left behind")
			}
		})
	}
}

// UT-FSM: after the first ready the same failures divide into retried and
// fatal.
func TestRetriedAndFatalAfterReady(t *testing.T) {
	t.Run("server error is retried", func(t *testing.T) {
		s := newFakeServer(t)
		r := startRun(t, s, nil)
		r.waitErr("joined #c")
		s.last().send("ERROR :Server shutting down")
		s.last().c.Close()
		r.waitErr("connection lost: the server closed the connection: Server shutting down; reconnecting")
		r.waitErr("reconnected;")
		if s.connections() != 2 {
			t.Errorf("%d connections", s.connections())
		}
	})

	t.Run("idle timeout is retried", func(t *testing.T) {
		s := newFakeServer(t)
		var silent atomic.Bool
		s.hook = func(c *fakeConn, m irc.Message) bool { return silent.Load() && c.n == 1 }
		r := startRun(t, s, func(tn *config.Tuning) { tn.IdleTimeout = config.Duration(600 * time.Millisecond) })
		r.waitErr("joined #c")
		time.Sleep(900 * time.Millisecond)
		if strings.Contains(r.stderr.String(), "connection lost") {
			t.Fatal("a connection that answers PING was dropped")
		}
		silent.Store(true)
		r.waitErr("connection lost: no data from the server for 600ms")
		r.waitErr("reconnected;")
	})

	t.Run("nickname in use is retried within the window, then fatal", func(t *testing.T) {
		s := newFakeServer(t)
		s.hook = func(c *fakeConn, m irc.Message) bool {
			if m.Command == "NICK" && c.n > 1 {
				c.send(":fake 433 * agentA :Nickname is already in use")
				return true
			}
			return false
		}
		r := startRun(t, s, nil)
		r.waitErr("joined #c")
		s.last().c.Close()
		e := r.exit(10 * time.Second)
		if e == nil || e.Code != 4 || !strings.Contains(e.Msg, "nickname agentA is already in use") {
			t.Errorf("exit %+v", e)
		}
		if n := s.connections(); n < 4 {
			t.Errorf("only %d connections: the nickname must be retried for the whole window", n)
		}
		for _, nick := range s.nicks() {
			if nick != "" && nick != "agentA" {
				t.Errorf("the listener tried the nickname %q", nick)
			}
		}
	})

	fatal := []struct {
		name string
		do   func(c *fakeConn)
		code int
		text string
	}{
		{"kicked", func(c *fakeConn) { c.send(":op!u@h KICK #c agentA :enough") }, 6, "kicked from #c by op: enough"},
		{"forced nickname change", func(c *fakeConn) { c.send(":agentA!~u@fake.host NICK renamed") }, 6, "changed the nickname from agentA to renamed"},
		{"removed from the channel", func(c *fakeConn) { c.send(":agentA!~u@fake.host PART #c") }, 6, "removed from #c"},
		{"required capability withdrawn", func(c *fakeConn) { c.send(":fake CAP agentA DEL :echo-message") }, 9, "withdrew the capability echo-message"},
	}
	for _, tc := range fatal {
		t.Run(tc.name+" is fatal", func(t *testing.T) {
			s := newFakeServer(t)
			quit := make(chan struct{}, 1)
			s.hook = func(c *fakeConn, m irc.Message) bool {
				if m.Command == "QUIT" {
					quit <- struct{}{}
				}
				return false
			}
			r := startRun(t, s, nil)
			r.waitErr("joined #c")
			tc.do(s.last())
			e := r.exit(10 * time.Second)
			if e == nil || e.Code != tc.code || !strings.Contains(e.Msg, tc.text) {
				t.Errorf("exit %+v, want %d containing %q", e, tc.code, tc.text)
			}
			if s.connections() != 1 {
				t.Errorf("%d connections: a fatal failure must not be retried", s.connections())
			}
			select {
			case <-quit:
			default:
				t.Error("the listener left without QUIT")
			}
		})
	}

	t.Run("optional capability withdrawn is ignored", func(t *testing.T) {
		s := newFakeServer(t)
		r := startRun(t, s, nil)
		r.waitErr("joined #c")
		s.last().send(":fake CAP agentA DEL :multi-prefix")
		s.last().send(":fake CAP agentA NEW :sasl=PLAIN")
		if resp := r.do(publish("still fine")); !resp.Succeeded() {
			t.Errorf("%+v", resp)
		}
	})
}

// UT-MEM: membership through names, join, part, quit, kick and nick change,
// observed through what `send` accepts.
func TestMembership(t *testing.T) {
	s := newFakeServer(t)
	r := startRun(t, s, nil)
	r.waitErr("joined #c")
	c := s.last()
	present := func(nick string) bool {
		resp := r.do(control.Request{Op: control.OpSend, To: nick, Text: "ping"})
		if !resp.Succeeded() && resp.Code != control.CodeUnknownNick {
			t.Fatalf("send to %s: %+v", nick, resp)
		}
		return resp.Succeeded()
	}
	settle := func() { r.do(publish("sync")) } // the server's lines before this are processed

	// From the names reply, prefixes stripped.
	if !present("peer") || !present("voiced") || !present("PEER") {
		t.Error("members from the names reply are not known")
	}
	if present("stranger") {
		t.Error("a nickname that is not in the channel was accepted")
	}

	c.send(":newcomer!u@h JOIN #c")
	c.send(":elsewhere!u@h JOIN #other")
	settle()
	if !present("newcomer") || present("elsewhere") {
		t.Error("join")
	}
	c.send(":newcomer!u@h NICK renamed")
	settle()
	if present("newcomer") || !present("renamed") {
		t.Error("nick change")
	}
	c.send(":renamed!u@h PART #c :bye")
	settle()
	if present("renamed") {
		t.Error("part")
	}
	c.send(":peer!u@h QUIT :gone")
	settle()
	if present("peer") {
		t.Error("quit")
	}
	c.send(":op!u@h KICK #c voiced :out")
	settle()
	if present("voiced") {
		t.Error("kick")
	}
	// The addressee is written the way the server spells it.
	c.send(":CamelCase!u@h JOIN #c")
	settle()
	seen := make(chan string, 1)
	s.hook = func(c *fakeConn, m irc.Message) bool {
		if m.Command == "PRIVMSG" && strings.Contains(irc.Param(&m, -1), "spelling") {
			seen <- irc.Param(&m, -1)
		}
		return false
	}
	r.do(control.Request{Op: control.OpSend, To: "camelcase", Text: "spelling"})
	if got := <-seen; got != "CamelCase: spelling" {
		t.Errorf("sent %q", got)
	}
}

// Messages without a server time are delivered with the local clock;
// history the listener did not ask for is discarded; server notices and
// other noise never reach stdout.
func TestInboundEdgeCases(t *testing.T) {
	s := newFakeServer(t)
	r := startRun(t, s, nil)
	r.waitErr("joined #c")
	c := s.last()

	c.send(":fake BATCH +auto chathistory #c")
	c.send("@batch=auto;time=2020-01-01T00:00:00.000Z;msgid=old1 :peer!u@h PRIVMSG #c :replayed on join, not asked for")
	c.send(":fake BATCH -auto")
	c.send(":fake NOTICE agentA :*** server notice")
	c.send(":fake NOTICE #c :server notice to the channel")
	c.send(":peer!u@h TAGMSG #c")
	c.send(":peer!u@h TOPIC #c :new topic")
	c.send(":peer!u@h PRIVMSG #elsewhere :not our channel")
	c.send(":fake 999 agentA :unknown numeric")
	c.send("this is not an IRC line at all \x00")
	c.send("@msgid=n1 :peer!u@h NOTICE #c :a user notice")
	before := time.Now().UTC().Add(-2 * time.Second)
	c.send("@msgid=nt1 :peer!u@h PRIVMSG #c :no time tag")
	// The same message twice (same id) is printed once.
	c.send("@msgid=dup1;time=2026-10-10T10:00:00.000Z :peer!u@h PRIVMSG #c :twice")
	c.send("@msgid=dup1;time=2026-10-10T10:00:00.000Z :peer!u@h PRIVMSG #c :twice")
	// Interleaved multiline batches from two senders.
	c.send("@msgid=ma;time=2026-10-10T10:00:01.000Z;bot :bot1!u@h BATCH +a draft/multiline #c")
	c.send("@msgid=mb;time=2026-10-10T10:00:02.000Z :peer!u@h BATCH +b draft/multiline #c")
	c.send("@batch=a :bot1!u@h PRIVMSG #c :a1")
	c.send("@batch=b :peer!u@h PRIVMSG #c :b1")
	c.send("@batch=a :bot1!u@h PRIVMSG #c :a2")
	c.send(":peer!u@h BATCH -b")
	c.send(":bot1!u@h BATCH -a")
	c.send("@msgid=end;time=2026-10-10T10:00:03.000Z :peer!u@h PRIVMSG #c :the end")
	r.waitOut("the end")

	got := r.stdout.get()
	want := []string{"a user notice", "no time tag", "twice", "b1", `a1\na2`, "the end"}
	if len(got) != len(want) {
		t.Fatalf("stdout has %d lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i, w := range want {
		if !strings.HasSuffix(got[i], " | "+w) {
			t.Errorf("line %d = %q, want text %q", i, got[i], w)
		}
	}
	ts, err := time.Parse("2006-01-02T15:04:05Z", strings.Fields(got[1])[0])
	if err != nil || ts.Before(before) || ts.After(time.Now().UTC().Add(2*time.Second)) {
		t.Errorf("a message without a time tag must carry the local time, got %q", got[1])
	}
	if !strings.Contains(got[4], " bot1 bot ") || !strings.Contains(got[3], " peer human ") {
		t.Errorf("kinds: %q / %q", got[3], got[4])
	}
}

// 03 §6: catch-up asks by timestamp first, then pages by the newest id it
// was just given, and stops at an empty page, not at a short one.
func TestCatchupPaging(t *testing.T) {
	s := newFakeServer(t)
	// Server times after the listener's join, as on a real server.
	base := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	at := func(sec int) string {
		return base.Add(time.Duration(sec) * time.Second).Format("2006-01-02T15:04:05.000Z")
	}
	var mu sync.Mutex
	var asked []string
	s.hook = func(c *fakeConn, m irc.Message) bool {
		if m.Command != "CHATHISTORY" {
			return false
		}
		ref := irc.Param(&m, 2)
		mu.Lock()
		asked = append(asked, irc.Param(&m, 0)+" "+ref+" "+irc.Param(&m, 3))
		mu.Unlock()
		c.send("@label=%s :fake BATCH +h chathistory #c", irc.Tag(&m, "label"))
		switch {
		case strings.HasPrefix(ref, "timestamp="):
			// The overlap brings back the message already seen.
			c.send("@batch=h;msgid=seen1;time=%s :peer!u@h PRIVMSG #c :already seen", at(0))
			c.send("@batch=h;msgid=h1;time=%s :peer!u@h PRIVMSG #c :missed 1", at(1))
			c.send("@batch=h;msgid=j9;time=%s :other!u@h JOIN #c", at(2))
		case ref == "msgid=j9":
			// A short page: one message although the limit is 100.
			c.send("@batch=h;msgid=h2;time=%s :peer!u@h PRIVMSG #c :missed 2", at(3))
		case ref == "msgid=h2":
			// Empty: the end.
		}
		c.send(":fake BATCH -h")
		return true
	}
	r := startRun(t, s, nil)
	r.waitErr("joined #c")
	s.last().send("@msgid=seen1;time=%s :peer!u@h PRIVMSG #c :already seen", at(0))
	r.waitOut("already seen")
	s.last().c.Close()
	r.waitErr("reconnected; 2 messages replayed")

	want := []string{
		"AFTER timestamp=" + at(-2) + " 100", // two seconds before the last message seen
		"AFTER msgid=j9 100",                 // newest id of the page, whatever its kind
		"AFTER msgid=h2 100",                 // a short page is not the end
	}
	mu.Lock()
	got := strings.Join(asked, "|")
	mu.Unlock()
	if got != strings.Join(want, "|") {
		t.Errorf("history requests:\n got %q\nwant %q", got, strings.Join(want, "|"))
	}
	out := r.stdout.get()
	if len(out) != 3 || !strings.Contains(out[1], "replayed | missed 1") || !strings.Contains(out[2], "replayed | missed 2") {
		t.Errorf("stdout:\n%s", strings.Join(out, "\n"))
	}
	if strings.Contains(r.stderr.String(), "may have been missed") {
		t.Errorf("a gap was reported although the reference message came back:\n%s", r.stderr.String())
	}
}

// 03 §6: a failed history request is reported and the listener carries on;
// so is history that does not reach back to the last message seen.
func TestCatchupFailures(t *testing.T) {
	t.Run("history refused", func(t *testing.T) {
		s := newFakeServer(t)
		s.hook = func(c *fakeConn, m irc.Message) bool {
			if m.Command == "CHATHISTORY" {
				c.send("@label=%s :fake FAIL CHATHISTORY MESSAGE_ERROR AFTER #c :Messages could not be retrieved", irc.Tag(&m, "label"))
				return true
			}
			return false
		}
		r := startRun(t, s, nil)
		r.waitErr("joined #c")
		s.last().c.Close()
		r.waitErr("could not fetch the history of #c (Messages could not be retrieved); messages posted while disconnected may have been missed")
		r.waitErr("reconnected; 0 messages replayed")
		if resp := r.do(publish("carrying on")); !resp.Succeeded() {
			t.Errorf("%+v", resp)
		}
	})
	t.Run("history does not reach back", func(t *testing.T) {
		s := newFakeServer(t)
		s.hook = func(c *fakeConn, m irc.Message) bool {
			if m.Command == "CHATHISTORY" && strings.HasPrefix(irc.Param(&m, 2), "timestamp=") {
				c.send("@label=%s :fake BATCH +h chathistory #c", irc.Tag(&m, "label"))
				c.send("@batch=h;msgid=later;time=2026-10-10T11:00:00.000Z :peer!u@h PRIVMSG #c :much later")
				c.send(":fake BATCH -h")
				return true
			}
			return false
		}
		r := startRun(t, s, nil)
		r.waitErr("joined #c")
		s.last().c.Close()
		r.waitErr("the history of #c does not reach back to the disconnect; messages may have been missed")
		r.waitOut("much later")
	})
}

// D9: a request while the listener is not ready waits a little, then fails
// with "not sent"; status answers at once in every state.
func TestRequestsWhileNotReady(t *testing.T) {
	s := newFakeServer(t)
	var hold atomic.Bool
	s.hook = func(c *fakeConn, m irc.Message) bool { return hold.Load() && m.Command == "CAP" }
	r := startRun(t, s, nil)
	r.waitErr("joined #c")
	if st := r.do(control.Request{Op: control.OpStatus}); st.State != "ready" || st.Nick != "agentA" || st.Channel != "#c" || st.PID == 0 {
		t.Errorf("status: %+v", st)
	}
	hold.Store(true) // the next connection never gets past capability negotiation
	s.last().c.Close()
	r.waitErr("connection lost")

	start := time.Now()
	st := r.do(control.Request{Op: control.OpStatus})
	if time.Since(start) > 300*time.Millisecond || st.State == "ready" || !st.Succeeded() {
		t.Errorf("status while reconnecting: %+v after %s", st, time.Since(start))
	}
	start = time.Now()
	resp := r.do(publish("too early"))
	if resp.Code != control.CodeNotConnected || resp.Sent != control.SentNo || !strings.Contains(resp.Error, "message not sent") {
		t.Errorf("%+v", resp)
	}
	if d := time.Since(start); d < 400*time.Millisecond || d > 2*time.Second {
		t.Errorf("failed after %s, expected the 500 ms wait for ready", d)
	}
	if resp := r.do(control.Request{Op: control.OpNames}); resp.Code != control.CodeNotConnected || resp.Sent != "" {
		t.Errorf("names while reconnecting: %+v", resp)
	}
	if resp := r.do(control.Request{Op: "frobnicate"}); resp.Code != control.CodeNotConnected {
		t.Errorf("%+v", resp)
	}
}

func TestUnknownOperation(t *testing.T) {
	s := newFakeServer(t)
	r := startRun(t, s, nil)
	r.waitErr("joined #c")
	if resp := r.do(control.Request{Op: "frobnicate"}); resp.Code != control.CodeBadRequest {
		t.Errorf("%+v", resp)
	}
	if resp := r.do(control.Request{Op: control.OpSend, To: "#c", Text: "x", Private: true}); resp.Code != control.CodeBadRequest {
		t.Errorf("a channel as a private target: %+v", resp)
	}
	if resp := r.do(control.Request{Op: control.OpSend, To: "a b", Text: "x"}); resp.Code != control.CodeBadRequest {
		t.Errorf("a nickname with a space: %+v", resp)
	}
	if resp := r.do(publish("a NUL \x00 byte")); resp.Code != control.CodeBadRequest || resp.Sent != control.SentNo {
		t.Errorf("a NUL reaching the listener: %+v", resp)
	}
	if resp := r.do(publish(" \n ")); resp.Code != control.CodeBadRequest {
		t.Errorf("a blank message reaching the listener: %+v", resp)
	}
}

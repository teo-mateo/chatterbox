package irc

import (
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func parse(t *testing.T, line string) Message {
	t.Helper()
	m, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine(%q): %v", line, err)
	}
	return m
}

func TestParseLineAndTags(t *testing.T) {
	m := parse(t, `@time=2026-10-10T10:47:32.721Z;label=m0;msgid=abc;bot;+x=a\sb\:c :spkA!~u@host.irc privmsg #chan :hello  world`)
	if m.Command != "PRIVMSG" || Nick(m.Source) != "spkA" || Param(&m, 0) != "#chan" || Param(&m, -1) != "hello  world" {
		t.Errorf("%+v", m)
	}
	if !m.HasTag("bot") || Tag(&m, "label") != "m0" || Tag(&m, "+x") != "a b;c" || Tag(&m, "absent") != "" {
		t.Error("tags")
	}
	ts, ok := Time(&m)
	if !ok || !ts.Equal(time.Date(2026, 10, 10, 10, 47, 32, 721e6, time.UTC)) {
		t.Errorf("time = %v %v", ts, ok)
	}
	if got := TimeParam(ts); got != "timestamp=2026-10-10T10:47:32.721Z" {
		t.Errorf("TimeParam = %s", got)
	}
	if _, ok := Time(&Message{}); ok {
		t.Error("time reported for a message without the tag")
	}
	if Param(&m, 9) != "" || Nick("server.name") != "server.name" {
		t.Error("Param/Nick edge cases")
	}
}

func TestLineBuilder(t *testing.T) {
	cases := map[string]string{
		Line(nil, "PRIVMSG", "#c", "hi there"):                      "PRIVMSG #c :hi there",
		Line(nil, "PRIVMSG", "#c", ""):                              "PRIVMSG #c :",
		Line(nil, "PRIVMSG", "#c", ":)"):                            "PRIVMSG #c ::)",
		Line(map[string]string{"label": "L1"}, "WHO", "#c"):         "@label=L1 WHO #c",
		Line(map[string]string{"batch": "r"}, "PRIVMSG", "#c", "x"): "@batch=r PRIVMSG #c :x",
		Line(nil, "BATCH", "+r", "draft/multiline", "#c"):           "BATCH +r draft/multiline #c",
		Line(nil, "CHATHISTORY", "LATEST", "#c", "*", "1"):          "CHATHISTORY LATEST #c * 1",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	m := parse(t, Line(map[string]string{"batch": "r", "draft/multiline-concat": ""}, "PRIVMSG", "#c", "x"))
	if !m.HasTag("draft/multiline-concat") || Tag(&m, "batch") != "r" {
		t.Error("valueless tag lost")
	}
}

func TestCapLSContinuation(t *testing.T) {
	c := Caps{}
	m1 := parse(t, ":s CAP * LS * :batch draft/multiline=max-bytes=4096,max-lines=100 message-tags")
	m2 := parse(t, ":s CAP * LS :server-time sasl=PLAIN,EXTERNAL echo-message")
	if c.AddLS(&m1) {
		t.Error("continuation line reported as complete")
	}
	if !c.AddLS(&m2) {
		t.Error("last line not reported as complete")
	}
	if !c.Has("batch") || !c.Has("echo-message") || c["sasl"] != "PLAIN,EXTERNAL" {
		t.Errorf("%v", c)
	}
	if got := c.Missing([]string{"batch", "draft/chathistory", "labeled-response"}); !reflect.DeepEqual(got, []string{"draft/chathistory", "labeled-response"}) {
		t.Errorf("Missing = %v", got)
	}
	b, l := MultilineLimits(c["draft/multiline"])
	if b != 4096 || l != 100 {
		t.Errorf("limits %d %d", b, l)
	}
	if b, l := MultilineLimits("max-bytes=1000"); b != 1000 || l != 0 {
		t.Errorf("limits without max-lines: %d %d", b, l)
	}
}

func TestISupport(t *testing.T) {
	s := ISupport{}
	for _, l := range []string{
		":s 005 n AWAYLEN=390 BOT=B CASEMAPPING=ascii CHANNELLEN=64 CHANTYPES=# CHATHISTORY=1000 EXCEPTS :are supported by this server",
		":s 005 n NICKLEN=32 PREFIX=(qaohv)~&@%+ MSGREFTYPES=msgid,timestamp NETWORK=Hof\\x20Net :are supported by this server",
	} {
		m := parse(t, l)
		s.Add(&m)
	}
	if s["BOT"] != "B" || s.Int("CHATHISTORY", 0) != 1000 || s.Int("NICKLEN", 9) != 32 || s.Int("ABSENT", 7) != 7 {
		t.Errorf("%v", s)
	}
	if !s.Has("EXCEPTS") || s.Has("NOPE") || s.Get("CHANTYPES", "x") != "#" || s.Get("NOPE", "def") != "def" {
		t.Error("Has/Get")
	}
	if s["NETWORK"] != "Hof Net" {
		t.Errorf("escape: %q", s["NETWORK"])
	}
	all, op := s.PrefixChars()
	if all != "~&@%+" || op != "~&@" {
		t.Errorf("PrefixChars = %q %q", all, op)
	}
	m := parse(t, ":s 005 n -BOT :are supported")
	s.Add(&m)
	if s.Has("BOT") {
		t.Error("negated token kept")
	}
	if all, op := (ISupport{}).PrefixChars(); all != "@+" || op != "@" {
		t.Errorf("default prefix %q %q", all, op)
	}
}

func feed(t *testing.T, a *Assembler, lines ...string) []*Item {
	t.Helper()
	var out []*Item
	for _, l := range lines {
		if it := a.Feed(parse(t, l)); it != nil {
			out = append(out, it)
		}
	}
	return out
}

func TestBatchMultiline(t *testing.T) {
	var a Assembler
	items := feed(t, &a,
		"@msgid=m1;bot;time=2026-10-10T10:48:05.022Z :a!u@h BATCH +x draft/multiline #c",
		"@batch=x :a!u@h PRIVMSG #c :line one",
		"@batch=x :a!u@h PRIVMSG #c :",
		"@batch=x :a!u@h PRIVMSG #c :line three start ",
		"@batch=x;draft/multiline-concat :a!u@h PRIVMSG #c :and its continuation",
		":a!u@h BATCH -x",
	)
	if len(items) != 1 || items[0].Batch == nil {
		t.Fatalf("%+v", items)
	}
	b := items[0].Batch
	if b.Type != "draft/multiline" || !reflect.DeepEqual(b.Params, []string{"#c"}) || Tag(&b.Open, "msgid") != "m1" || !b.Open.HasTag("bot") {
		t.Errorf("%+v", b)
	}
	if got := MultilineText(b); got != "line one\n\nline three start and its continuation" {
		t.Errorf("text %q", got)
	}
	if MultilineCommand(b) != "PRIVMSG" {
		t.Error("command")
	}
	if a.OpenCount() != 0 {
		t.Error("batch left open")
	}
}

func TestBatchNestedInChathistory(t *testing.T) {
	var a Assembler
	items := feed(t, &a,
		"@label=H :s BATCH +1 chathistory #c",
		"@batch=1;msgid=j1 :b!u@h JOIN #c",
		"@batch=1;msgid=m1 :b!u@h PRIVMSG #c :single",
		"@batch=1;msgid=m2 :a!u@h BATCH +2 draft/multiline #c",
		"@batch=2 :a!u@h PRIVMSG #c :one",
		"@batch=2 :a!u@h PRIVMSG #c :two",
		"@batch=1 :a!u@h BATCH -2",
		":s BATCH -1",
	)
	if len(items) != 1 {
		t.Fatalf("%d items", len(items))
	}
	b := items[0].Batch
	if b.Type != "chathistory" || Tag(&b.Open, "label") != "H" || len(b.Items) != 3 {
		t.Fatalf("%+v", b)
	}
	if b.Items[0].Msg.Command != "JOIN" || Param(b.Items[1].Msg, -1) != "single" {
		t.Error("order of items")
	}
	if nested := b.Items[2].Batch; nested == nil || MultilineText(nested) != "one\ntwo" || Tag(&nested.Open, "msgid") != "m2" {
		t.Errorf("nested batch: %+v", b.Items[2])
	}
}

func TestBatchInterleavedAndPlain(t *testing.T) {
	var a Assembler
	items := feed(t, &a,
		":a!u@h BATCH +x draft/multiline #c",
		":b!u@h BATCH +y draft/multiline #c",
		"@batch=x :a!u@h PRIVMSG #c :a1",
		":s PING :keepalive",
		"@batch=y :b!u@h PRIVMSG #c :b1",
		"@batch=x :a!u@h PRIVMSG #c :a2",
		":b!u@h BATCH -y",
		"@batch=y :b!u@h PRIVMSG #c :late, batch already closed",
		":a!u@h BATCH -x",
		":s BATCH -never-opened",
	)
	if len(items) != 4 {
		t.Fatalf("%d items", len(items))
	}
	if items[0].Msg == nil || items[0].Msg.Command != "PING" {
		t.Error("a line outside any batch must come out at once")
	}
	if MultilineText(items[1].Batch) != "b1" {
		t.Error("batch y")
	}
	if items[2].Msg == nil || Param(items[2].Msg, -1) != "late, batch already closed" {
		t.Error("a line tagged with an unknown batch is a plain line")
	}
	if MultilineText(items[3].Batch) != "a1\na2" {
		t.Error("batch x")
	}
}

func TestBatchUnterminatedAtDisconnect(t *testing.T) {
	var a Assembler
	items := feed(t, &a, ":a!u@h BATCH +x draft/multiline #c", "@batch=x :a!u@h PRIVMSG #c :half")
	if len(items) != 0 || a.OpenCount() != 1 {
		t.Fatal("unterminated batch delivered")
	}
	a.Reset()
	if a.OpenCount() != 0 {
		t.Error("Reset left a batch open")
	}
	if items := feed(t, &a, ":a!u@h PRIVMSG #c :after reconnect"); len(items) != 1 {
		t.Error("assembler unusable after Reset")
	}
}

// recorder collects written units and can block the writer.
type recorder struct {
	mu    sync.Mutex
	units [][]string
	gate  chan struct{}
}

func (r *recorder) write(lines []string) error {
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	r.units = append(r.units, lines)
	r.mu.Unlock()
	return nil
}

func TestWriteQueueBatchIsContiguous(t *testing.T) {
	q := NewWriteQueue()
	batch := []string{"BATCH +r draft/multiline #c", "@batch=r PRIVMSG #c :1", "@batch=r PRIVMSG #c :2", "BATCH -r"}
	q.Push(Unit{Lines: []string{"PING :a"}})
	q.PushMessage(Unit{Lines: batch})
	q.Push(Unit{Lines: []string{"PONG :b"}})
	q.Push(Unit{Lines: []string{"WHO #c"}})
	q.Close()

	server, client := net.Pipe()
	conn := NewConn(client)
	var got []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := server.Read(buf)
			got = append(got, strings.Split(strings.TrimSuffix(string(buf[:n]), "\r\n"), "\r\n")...)
			if err != nil {
				return
			}
		}
	}()
	if err := q.Run(conn.WriteLines); err != nil {
		t.Fatal(err)
	}
	client.Close()
	<-done
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, strings.Join(batch, "\n")) {
		t.Errorf("batch lines were not written contiguously:\n%s", joined)
	}
	for _, want := range []string{"PING :a", "PONG :b", "WHO #c"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q not written", want)
		}
	}
}

func TestWriteQueueAlternatesAndKeepsOrder(t *testing.T) {
	q := NewWriteQueue()
	for _, m := range []string{"m1", "m2", "m3", "m4"} {
		q.PushMessage(Unit{Lines: []string{m}})
	}
	for _, o := range []string{"o1", "o2"} {
		q.Push(Unit{Lines: []string{o}})
	}
	q.Close()
	r := &recorder{}
	q.Run(r.write)
	var order []string
	for _, u := range r.units {
		order = append(order, u[0])
	}
	want := []string{"m1", "o1", "m2", "o2", "m3", "m4"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order %v, want %v", order, want)
	}
}

func TestWriteQueueWrittenCallbackAndError(t *testing.T) {
	q := NewWriteQueue()
	called := make(chan string, 2)
	q.PushMessage(Unit{Lines: []string{"a"}, Written: func() { called <- "a" }})
	q.Close()
	q.Run((&recorder{}).write)
	select {
	case <-called:
	default:
		t.Error("Written callback not run")
	}

	q = NewWriteQueue()
	q.Push(Unit{Lines: []string{"x"}, Written: func() { called <- "x" }})
	q.Push(Unit{Lines: []string{"y"}})
	err := q.Run(func([]string) error { return net.ErrClosed })
	if err == nil {
		t.Error("write error not returned")
	}
	select {
	case v := <-called:
		t.Errorf("Written ran for a failed write: %s", v)
	default:
	}
	q.Push(Unit{Lines: []string{"z"}}) // must not block or panic after close
}

func TestOverLongLine(t *testing.T) {
	server, client := net.Pipe()
	conn := NewConn(client)
	go func() {
		server.Write([]byte(":s PRIVMSG #c :ok\r\n\r\n"))
		server.Write([]byte(strings.Repeat("x", maxLine+100)))
		server.Close()
	}()
	m, err := conn.ReadMessage()
	if err != nil || Param(&m, -1) != "ok" {
		t.Fatalf("first line: %+v %v", m, err)
	}
	if _, err := conn.ReadMessage(); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Errorf("over-long line: %v", err)
	}
}

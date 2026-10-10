package outbound

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

var lim = Limits{LineBudget: 400, MaxBytes: 4096, MaxLines: 100}

func TestNormalize(t *testing.T) {
	ok := map[string]string{
		"hello":      "hello",
		"hello\n":    "hello",
		"a\r\nb\r\n": "a\nb",
		"a\rb":       "a\nb",
		"a\n\nb\n\n": "a\n\nb\n",
		"  padded  ": "  padded  ",
		"héllo ✓":    "héllo ✓",
	}
	for in, want := range ok {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "\n", "   ", " \n\t\n", "a\x00b", "a\xffb"} {
		if _, err := Normalize(in); err == nil {
			t.Errorf("Normalize(%q) accepted", in)
		}
	}
}

func TestLineBudget(t *testing.T) {
	// ":agentA!~u@host.irc PRIVMSG #hof :" + text + CRLF must be 512.
	b := LineBudget("agentA!~u@host.irc", "#hof")
	wire := ":agentA!~u@host.irc PRIVMSG #hof :" + strings.Repeat("x", b) + "\r\n"
	if len(wire) != 512 {
		t.Errorf("relayed line is %d bytes", len(wire))
	}
}

func TestSingleLine(t *testing.T) {
	parts, err := Split("agentB: ", "hello", lim)
	if err != nil || len(parts) != 1 || parts[0].Multiline() || parts[0].Lines[0].Text != "agentB: hello" {
		t.Errorf("%+v %v", parts, err)
	}
	exact := strings.Repeat("x", 400)
	parts, _ = Split("", exact, lim)
	if len(parts) != 1 || parts[0].Multiline() {
		t.Errorf("a line of exactly the budget must be one PRIVMSG: %d lines", len(parts[0].Lines))
	}
}

func TestNewlineForcesBatch(t *testing.T) {
	parts, _ := Split("", "a\n\nb", lim)
	if len(parts) != 1 || !parts[0].Multiline() {
		t.Fatalf("%+v", parts)
	}
	l := parts[0].Lines
	if len(l) != 3 || l[0].Text != "a" || l[1].Text != "" || l[2].Text != "b" || l[1].Concat || l[2].Concat {
		t.Errorf("%+v", l)
	}
}

func TestLongLineCutAfterSpace(t *testing.T) {
	text := strings.Repeat("word ", 150) // 750 bytes
	text = strings.TrimSuffix(text, " ")
	parts, _ := Split("", text, lim)
	if len(parts) != 1 {
		t.Fatalf("%d parts", len(parts))
	}
	l := parts[0].Lines
	if len(l) != 2 || l[0].Concat || !l[1].Concat {
		t.Fatalf("%+v", l)
	}
	if !strings.HasSuffix(l[0].Text, " ") || len(l[0].Text) > 400 {
		t.Errorf("first piece should end with the space it was cut after: %q", l[0].Text[len(l[0].Text)-6:])
	}
	if l[0].Text+l[1].Text != text {
		t.Error("pieces do not join back to the line")
	}
}

func TestCutRespectsUTF8(t *testing.T) {
	text := strings.Repeat("é", 500) // 1000 bytes, no spaces
	parts, _ := Split("", text, lim)
	for _, l := range parts[0].Lines {
		if !utf8.ValidString(l.Text) {
			t.Fatalf("cut inside a character: %q", l.Text)
		}
		if l.Text == "" {
			t.Fatal("blank piece")
		}
		if len(l.Text) > 400 {
			t.Fatalf("piece of %d bytes", len(l.Text))
		}
	}
}

func checkParts(t *testing.T, prefix string, parts []Part, l Limits) {
	t.Helper()
	n := len(parts)
	for i, p := range parts {
		if l.MaxLines > 0 && len(p.Lines) > l.MaxLines {
			t.Fatalf("part %d has %d lines", i+1, len(p.Lines))
		}
		bytes := 0
		for j, line := range p.Lines {
			bytes += len(line.Text) + 1
			if len(line.Text) > l.LineBudget {
				t.Fatalf("part %d line %d is %d bytes", i+1, j, len(line.Text))
			}
			if line.Concat && line.Text == "" {
				t.Fatalf("part %d line %d: blank concat line", i+1, j)
			}
			if j == 0 && line.Concat {
				t.Fatalf("part %d starts with a concat line", i+1)
			}
			if !utf8.ValidString(line.Text) {
				t.Fatalf("part %d line %d: invalid UTF-8", i+1, j)
			}
		}
		if bytes > l.MaxBytes {
			t.Fatalf("part %d is %d bytes", i+1, bytes)
		}
		want := prefix
		if n > 1 {
			want += Marker(i+1, n)
		}
		if !strings.HasPrefix(p.Lines[0].Text, want) {
			t.Fatalf("part %d starts with %q, want prefix %q", i+1, p.Lines[0].Text[:min(len(p.Lines[0].Text), 30)], want)
		}
	}
}

func TestSplitOverMaxBytes(t *testing.T) {
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, strings.Repeat("x", 199))
	}
	text := strings.Join(lines, "\n") // 12 000 bytes
	prefix := "agentB: [IMPORTANT] "
	parts, err := Split(prefix, text, lim)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 3 {
		t.Fatalf("%d parts", len(parts))
	}
	checkParts(t, prefix, parts, lim)
	if got := Join(prefix, parts); got != text {
		t.Error("parts do not join back to the text")
	}
	for _, p := range parts {
		if p.MidLine {
			t.Error("cut inside a logical line although line boundaries were available")
		}
	}
}

func TestSplitOverMaxLines(t *testing.T) {
	text := strings.TrimSuffix(strings.Repeat("l\n", 250), "\n")
	parts, err := Split("", text, lim)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Errorf("%d parts for 250 short lines, want 3", len(parts))
	}
	checkParts(t, "", parts, lim)
	if Join("", parts) != text {
		t.Error("round trip")
	}
}

func TestSplitOneHugeLine(t *testing.T) {
	text := strings.Repeat("abcdefghi ", 1000) + "end" // 10 003 bytes, one line
	parts, err := Split("b: ", text, lim)
	if err != nil {
		t.Fatal(err)
	}
	checkParts(t, "b: ", parts, lim)
	if Join("b: ", parts) != text {
		t.Error("round trip")
	}
	if !parts[0].MidLine {
		t.Error("first part should end mid-line")
	}
}

func TestMarkerWidthGrows(t *testing.T) {
	small := Limits{LineBudget: 100, MaxBytes: 200, MaxLines: 4}
	text := strings.Repeat("y", 150*120)
	parts, err := Split("", text, small)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 100 {
		t.Fatalf("%d parts, expected three-digit count", len(parts))
	}
	checkParts(t, "", parts, small)
	if Join("", parts) != text {
		t.Error("round trip")
	}
}

func TestRejectsUnusableLimits(t *testing.T) {
	if _, err := Split("", "x", Limits{LineBudget: 10, MaxBytes: 4096, MaxLines: 100}); err == nil {
		t.Error("tiny line budget accepted")
	}
	if _, err := Split("", "x", Limits{LineBudget: 400, MaxBytes: 100, MaxLines: 100}); err == nil {
		t.Error("max-bytes below one line accepted")
	}
}

// Stripping prefixes and markers and joining the parts gives the original.
func TestSplitRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	words := []string{"a", "bb", "héllo", "✓", "the", "quick", "brown", "fox", strings.Repeat("z", 90), "", " "}
	limits := []Limits{lim, {LineBudget: 80, MaxBytes: 300, MaxLines: 5}, {LineBudget: 120, MaxBytes: 4096, MaxLines: 0}, {LineBudget: 64, MaxBytes: 130, MaxLines: 2}}
	prefixes := []string{"", "agentB: ", "[IMPORTANT] ", "agentB: [IMPORTANT] "}
	for i := 0; i < 1500; i++ {
		var b strings.Builder
		for n := rng.Intn(400); n > 0; n-- {
			b.WriteString(words[rng.Intn(len(words))])
			switch rng.Intn(12) {
			case 0:
				b.WriteByte('\n')
			case 1:
			default:
				b.WriteByte(' ')
			}
		}
		text, err := Normalize(b.String())
		if err != nil {
			continue
		}
		l := limits[rng.Intn(len(limits))]
		prefix := prefixes[rng.Intn(len(prefixes))]
		parts, err := Split(prefix, text, l)
		if err != nil {
			t.Fatalf("Split: %v (limits %+v, %d bytes)", err, l, len(text))
		}
		checkParts(t, prefix, parts, l)
		if got := Join(prefix, parts); got != text {
			t.Fatalf("round trip failed with %+v prefix %q\n text %q\n  got %q", l, prefix, text, got)
		}
	}
}

package format

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/teo-mateo/chatterbox/internal/inbound"
)

var at = time.Date(2026, 10, 10, 12, 35, 40, 123e6, time.FixedZone("x", 7200))

func msg(text string) inbound.Message {
	return inbound.Message{Time: at, Channel: "#hof", From: "agentB", Kind: inbound.KindBot, MsgID: "abc123", Text: text}
}

func TestTextGolden(t *testing.T) {
	m := msg("tests are green on the plan branch")
	if got, want := Text(m), "2026-10-10T10:35:40Z #hof agentB bot - | tests are green on the plan branch"; got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	m = msg("agentA: staging deploy is failing")
	m.Addressed, m.Important = true, true
	if got, want := Text(m), "2026-10-10T10:35:40Z #hof agentB bot to-you,important | agentA: staging deploy is failing"; got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	m.Replayed = true
	if !strings.Contains(Text(m), " to-you,important,replayed | ") {
		t.Errorf("flag order: %s", Text(m))
	}
	m = msg("line one\nline two")
	m.Replayed = true
	if got, want := Text(m), `2026-10-10T10:35:40Z #hof agentB bot replayed | line one\nline two`; got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	p := inbound.Message{Time: at, From: "teo", Kind: inbound.KindHuman, Private: true, Addressed: true, Text: "only you can see this"}
	if got, want := Text(p), "2026-10-10T10:35:40Z private teo human to-you | only you can see this"; got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestEscapeText(t *testing.T) {
	cases := map[string]string{
		`a\b`:         `a\\b`,
		"a\nb":        `a\nb`,
		"a\rb":        `a\rb`,
		"a\tb":        `a\tb`,
		"a\x00b":      `a\u{0}b`,
		"a\x07b":      `a\u{7}b`,
		"a\x0bb\x0cc": `a\u{b}b\u{c}c`,
		"a\x1bb":      `a\u{1b}b`,
		"a\x7fb":      `a\u{7f}b`,
		"a\u0085b":    `a\u{85}b`,
		"a\u2028b":    `a\u{2028}b`,
		"a\u2029b":    `a\u{2029}b`,
		"a\xffb":      "a\ufffdb",
		"héllo ✓ <&>": "héllo ✓ <&>",
		`already \n`:  `already \\n`,
	}
	for in, want := range cases {
		if got := EscapeText(in); got != want {
			t.Errorf("EscapeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTextSplitsOnFirstBar(t *testing.T) {
	m := msg("a | b | c")
	head, text, _ := strings.Cut(Text(m), " | ")
	if text != "a | b | c" || len(strings.Fields(head)) != 5 {
		t.Errorf("head %q text %q", head, text)
	}
	m.From = "we ird"
	head, _, _ = strings.Cut(Text(m), " | ")
	if len(strings.Fields(head)) != 5 {
		t.Errorf("a space in a header field broke the layout: %q", head)
	}
}

func TestJSONGolden(t *testing.T) {
	m := msg("agentA: staging deploy is failing")
	m.Addressed, m.Important = true, true
	want := `{"time":"2026-10-10T10:35:40Z","channel":"#hof","from":"agentB","kind":"bot","addressed":true,"important":true,"private":false,"replayed":false,"msgid":"abc123","text":"agentA: staging deploy is failing"}`
	if got := JSON(m); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	p := inbound.Message{Time: at, From: "teo", Kind: inbound.KindHuman, Private: true, Addressed: true, Replayed: true, Text: "x"}
	got := JSON(p)
	if !strings.Contains(got, `"channel":null`) || !strings.Contains(got, `"private":true`) || !strings.Contains(got, `"replayed":true`) {
		t.Errorf("private: %s", got)
	}
}

func TestJSONEscaping(t *testing.T) {
	m := msg("if a < b && c > d {\n\tx\u2028y\u2029z\u0085w\x7f\xff}")
	got := JSON(m)
	if !strings.Contains(got, `a < b && c > d`) {
		t.Errorf("HTML escaping is on: %s", got)
	}
	for _, raw := range []string{"\n", "\u2028", "\u2029", "\u0085", "\x7f"} {
		if strings.Contains(got, raw) {
			t.Errorf("raw %q in %s", raw, got)
		}
	}
	var back struct{ Text string }
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatal(err)
	}
	if want := "if a < b && c > d {\n\tx\u2028y\u2029z\u0085w\x7f\ufffd}"; back.Text != want {
		t.Errorf("round trip: %q", back.Text)
	}
}

// No output line may contain anything a reader could take for a line break.
func TestNoLineTerminators(t *testing.T) {
	terminators := []string{"\n", "\r", "\x0b", "\x0c", "\x1c", "\x1d", "\x1e", "\u0085", "\u2028", "\u2029"}
	alphabet := []rune("ab \\|\n\r\t\x00\x0b\x0c\x1c\x1d\x1e\x7f\u0085\u2028\u2029é✓<>&\"")
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		n := rng.Intn(40)
		var b strings.Builder
		for j := 0; j < n; j++ {
			if rng.Intn(20) == 0 {
				b.WriteByte(byte(0x80 + rng.Intn(0x80))) // invalid UTF-8
				continue
			}
			b.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		m := msg(b.String())
		m.From = b.String()
		for name, line := range map[string]string{"text": Text(m), "json": JSON(m)} {
			for _, term := range terminators {
				if strings.Contains(line, term) {
					t.Fatalf("%s line for %q contains %q: %q", name, b.String(), term, line)
				}
			}
		}
	}
}

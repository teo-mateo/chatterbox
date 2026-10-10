// Package inbound turns a received chat message into what the agent sees:
// who sent it, whether it is addressed to the agent, whether it is important,
// and whether the wake filter lets it through.
package inbound

import (
	"strings"
	"time"

	"github.com/teo-mateo/chatterbox/internal/identity"
)

// ImportantToken marks an important message. It is plain text so that a
// human sees it in any IRC client and can type it.
const ImportantToken = "[IMPORTANT]"

const (
	KindBot   = "bot"
	KindHuman = "human"
)

// Message is one delivered chat message.
type Message struct {
	Time      time.Time
	Channel   string // empty for a private message
	From      string
	Kind      string
	Addressed bool
	Important bool
	Private   bool
	Replayed  bool
	MsgID     string
	Text      string

	// HasAddressee is true when the text starts with an address prefix,
	// whoever it names. It decides whether an important message is a
	// broadcast (D7). It is not printed.
	HasAddressee bool
}

// Raw is a chat message as it came off the wire, multiline already joined.
type Raw struct {
	Time     time.Time
	From     string
	Target   string
	Text     string
	Bot      bool
	MsgID    string
	Replayed bool
}

// Self is what classification needs to know about the listener.
type Self struct {
	Nick    string
	Channel string
	Casemap identity.Casemap
}

// Classify builds the delivered message. ok is false for messages that are
// never delivered: the agent's own, CTCP other than ACTION, and messages for
// other targets.
func Classify(r Raw, self Self) (m Message, ok bool) {
	cm := self.Casemap
	if cm.Equal(r.From, self.Nick) {
		return m, false
	}
	private := cm.Equal(r.Target, self.Nick)
	if !private && !cm.Equal(r.Target, self.Channel) {
		return m, false
	}

	text := r.Text
	if strings.HasPrefix(text, "\x01") {
		body := strings.TrimSuffix(text[1:], "\x01")
		verb, rest, _ := strings.Cut(body, " ")
		if !strings.EqualFold(verb, "ACTION") {
			return m, false
		}
		text = "* " + r.From + " " + StripFormatting(rest)
	} else {
		text = StripFormatting(text)
	}

	names, rest := SplitAddressPrefix(text)
	prefix := text[:len(text)-len(rest)]
	important := false
	if after, found := cutImportant(rest); found {
		important = true
		text = prefix + after
		if after == "" {
			text = strings.TrimRight(text, " ")
		}
	}

	addressed := private
	for _, n := range names {
		if cm.Equal(n, self.Nick) {
			addressed = true
		}
	}
	if !addressed && mentionsAt(text, self.Nick, cm) {
		addressed = true
	}

	m = Message{
		Time:         r.Time.UTC(),
		From:         r.From,
		Kind:         KindHuman,
		Addressed:    addressed,
		Important:    important,
		Private:      private,
		Replayed:     r.Replayed,
		MsgID:        r.MsgID,
		Text:         text,
		HasAddressee: len(names) > 0,
	}
	if r.Bot {
		m.Kind = KindBot
	}
	if !private {
		m.Channel = r.Target
	}
	return m, true
}

// cutImportant reports whether s starts with the important token followed by
// a space or the end of the text, and returns what follows it.
func cutImportant(s string) (rest string, found bool) {
	if !strings.HasPrefix(s, ImportantToken) {
		return s, false
	}
	rest = s[len(ImportantToken):]
	if rest == "" {
		return "", true
	}
	if rest[0] != ' ' {
		return s, false
	}
	return rest[1:], true
}

// isAddresseeChar reports whether c can be part of a nickname on IRC. The
// addressee of a message may be a human, so this is wider than the agent
// nickname rule.
func isAddresseeChar(c byte) bool {
	return identity.IsNickChar(c) || strings.IndexByte("[]\\`^{}|", c) >= 0
}

func scanNick(s string) int {
	i := 0
	for i < len(s) && isAddresseeChar(s[i]) {
		i++
	}
	return i
}

// SplitAddressPrefix parses an address prefix (D6): one or more
// nickname-shaped tokens separated by commas, ended by ':' or ',' and a
// space. It returns the names and the text after the prefix; with no prefix
// it returns nil and the whole text. Whether the names are present in the
// channel does not matter.
func SplitAddressPrefix(text string) (names []string, rest string) {
	s := text
	for {
		n := scanNick(s)
		if n == 0 {
			return nil, text
		}
		name := s[:n]
		s = s[n:]
		switch {
		case strings.HasPrefix(s, ":"):
			if len(s) == 1 {
				return append(names, name), ""
			}
			if s[1] != ' ' {
				return nil, text
			}
			return append(names, name), strings.TrimLeft(s[1:], " ")
		case strings.HasPrefix(s, ","):
			after := strings.TrimLeft(s[1:], " ")
			spaced := len(after) < len(s)-1
			// A comma is a separator when another name with its own
			// terminator follows; otherwise it ends the prefix.
			if k := scanNick(after); k > 0 && k < len(after) && (after[k] == ',' || after[k] == ':') {
				names = append(names, name)
				s = after
				continue
			}
			if !spaced {
				return nil, text
			}
			return append(names, name), after
		default:
			return nil, text
		}
	}
}

// mentionsAt reports whether text contains "@nick" as a whole word.
func mentionsAt(text, nick string, cm identity.Casemap) bool {
	ft, fn := cm.Fold(text), "@"+cm.Fold(nick)
	for from := 0; ; {
		i := strings.Index(ft[from:], fn)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(fn)
		before := start == 0 || !isWordChar(ft[start-1])
		after := end == len(ft) || !identity.IsNickChar(ft[end])
		if before && after {
			return true
		}
		from = start + 1
	}
}

func isWordChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Wake filters.
const (
	WakeAll       = "all"
	WakeAddressed = "addressed"
)

// Printed applies the wake filter (FR2.5, D7).
func Printed(m Message, wake string) bool {
	if wake != WakeAddressed {
		return true
	}
	if m.Addressed {
		return true
	}
	return m.Important && !m.HasAddressee
}

// StripFormatting removes IRC formatting codes: bold, italic, underline,
// strikethrough, monospace, reverse, reset, and colors with their arguments.
func StripFormatting(s string) string {
	if strings.IndexFunc(s, func(r rune) bool {
		switch r {
		case 0x02, 0x03, 0x04, 0x0f, 0x11, 0x16, 0x1d, 0x1e, 0x1f:
			return true
		}
		return false
	}) < 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case 0x02, 0x0f, 0x11, 0x16, 0x1d, 0x1e, 0x1f:
		case 0x03:
			i += colorArgs(s[i+1:], 2, isDigit)
		case 0x04:
			i += colorArgs(s[i+1:], 6, isHex)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool {
	return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// colorArgs returns the length of "N[,N]" at the start of s, where N is up
// to max characters (mIRC colors) or exactly max characters (hex colors).
func colorArgs(s string, max int, is func(byte) bool) int {
	run := func(s string) int {
		n := 0
		for n < len(s) && n < max && is(s[n]) {
			n++
		}
		if max == 6 && n != 6 {
			return 0
		}
		return n
	}
	n := run(s)
	if n == 0 {
		return 0
	}
	if n < len(s) && s[n] == ',' {
		if m := run(s[n+1:]); m > 0 {
			return n + 1 + m
		}
	}
	return n
}

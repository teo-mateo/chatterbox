// Package outbound prepares text for sending: it normalizes the input and
// splits it into IRC lines, multiline batches and, over the server's
// multiline limit, several messages.
package outbound

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Normalize validates and cleans message text (03 §5): valid UTF-8 without
// NUL, line endings turned into "\n", one trailing newline removed, not blank.
func Normalize(text string) (string, error) {
	if !utf8.ValidString(text) {
		return "", errors.New("the message is not valid UTF-8")
	}
	if strings.IndexByte(text, 0) >= 0 {
		return "", errors.New("the message contains a NUL character")
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSuffix(text, "\n")
	if strings.TrimSpace(text) == "" {
		return "", errors.New("the message is empty")
	}
	return text, nil
}

// Limits are the server's constraints on one message.
type Limits struct {
	// LineBudget is the number of text bytes that fit one IRC line.
	LineBudget int
	// MaxBytes and MaxLines are the draft/multiline limits. MaxLines 0
	// means unlimited.
	MaxBytes int
	MaxLines int
}

// LineBudget computes the text bytes available on one line: the server
// relays the message as ":nick!user@host PRIVMSG <target> :text\r\n" within
// 512 bytes.
func LineBudget(hostmask, target string) int {
	return 510 - len(":"+hostmask+" ") - len("PRIVMSG "+target+" :")
}

// Line is one PRIVMSG of a message. Concat marks a line that continues the
// previous one without a line break (draft/multiline-concat).
type Line struct {
	Text   string
	Concat bool
}

// Part is one IRC message: a single line, or a multiline batch.
type Part struct {
	Lines []Line
	// MidLine is true when this part ends inside a logical line, so the
	// next part continues it without a line break.
	MidLine bool
}

// Multiline reports whether the part has to be sent as a batch.
func (p Part) Multiline() bool { return len(p.Lines) > 1 }

// Marker is the "(i/n) " text that starts every part of a split message.
func Marker(i, n int) string { return fmt.Sprintf("(%d/%d) ", i, n) }

// wrap cuts one logical line into wire lines of at most budget bytes, at
// UTF-8 boundaries, preferring to cut after a space. Joining the pieces
// reproduces the line exactly.
func wrap(line string, budget int) []Line {
	if len(line) <= budget {
		return []Line{{Text: line}}
	}
	var out []Line
	for len(line) > budget {
		cut := budget
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		if sp := strings.LastIndexByte(line[:cut], ' '); sp > 0 {
			cut = sp + 1
		}
		if cut == 0 { // cannot happen with a sane budget; avoid looping
			cut = budget
		}
		out = append(out, Line{Text: line[:cut], Concat: len(out) > 0})
		line = line[cut:]
	}
	return append(out, Line{Text: line, Concat: len(out) > 0})
}

// wrapWithHead wraps line so that its first piece, with head in front of it,
// still fits the budget. The head itself is never cut.
func wrapWithHead(head, line string, budget int) []Line {
	if head == "" {
		return wrap(line, budget)
	}
	if len(head)+len(line) <= budget {
		return []Line{{Text: head + line}}
	}
	first := wrap(line, budget-len(head))[0]
	out := []Line{{Text: head + first.Text}}
	for _, l := range wrap(line[len(first.Text):], budget) {
		l.Concat = true
		out = append(out, l)
	}
	return out
}

// cost is what a wire line counts against max-bytes: its text plus one byte
// for the line break the server puts between lines.
func cost(l Line) int { return len(l.Text) + 1 }

func fits(lines []Line, lim Limits) bool {
	if lim.MaxLines > 0 && len(lines) > lim.MaxLines {
		return false
	}
	total := 0
	for _, l := range lines {
		total += cost(l)
	}
	return total <= lim.MaxBytes
}

// Split turns prefix+text into the messages to send. prefix is the address
// and importance prefix ("nick: ", "[IMPORTANT] "), repeated on every part
// together with an "(i/n)" marker when the text needs several messages.
func Split(prefix, text string, lim Limits) ([]Part, error) {
	if lim.LineBudget < 64 {
		return nil, fmt.Errorf("the usable line length (%d bytes) is too small to send anything", lim.LineBudget)
	}
	if lim.MaxBytes < lim.LineBudget+1 || (lim.MaxLines > 0 && lim.MaxLines < 2) {
		return nil, fmt.Errorf("the server's multiline limits (max-bytes=%d, max-lines=%d) are too small to use", lim.MaxBytes, lim.MaxLines)
	}
	logical := strings.Split(text, "\n")

	// One message: a single line, or one batch.
	var all []Line
	if len(prefix) > lim.LineBudget/2 {
		return nil, errors.New("the address prefix is too long for the server's line length")
	}
	for i, l := range logical {
		if i == 0 {
			all = append(all, wrapWithHead(prefix, l, lim.LineBudget)...)
			continue
		}
		all = append(all, wrap(l, lim.LineBudget)...)
	}
	if fits(all, lim) {
		return []Part{{Lines: all}}, nil
	}

	// Several messages. The marker's width depends on the count, so pack
	// with a guess and widen it until the result agrees.
	for digits := 1; digits <= 9; digits++ {
		head := prefix + strings.Repeat("0", 2*digits+4) // "(i/n) " placeholder
		if len(head) > lim.LineBudget/2 {
			return nil, errors.New("the address prefix is too long for the server's line length")
		}
		parts := pack(head, logical, lim)
		if len(fmt.Sprint(len(parts))) > digits {
			continue
		}
		n := len(parts)
		for i := range parts {
			real := prefix + Marker(i+1, n)
			first := &parts[i].Lines[0]
			first.Text = real + first.Text[len(head):]
		}
		return parts, nil
	}
	return nil, errors.New("the message needs too many parts")
}

// pack distributes the logical lines over parts, each starting with head.
func pack(head string, logical []string, lim Limits) []Part {
	var parts []Part
	var cur []Line
	used := 0
	room := func(l Line) bool {
		if lim.MaxLines > 0 && len(cur)+1 > lim.MaxLines {
			return false
		}
		return used+cost(l) <= lim.MaxBytes
	}
	flush := func(midLine bool) {
		parts = append(parts, Part{Lines: cur, MidLine: midLine})
		cur, used = nil, 0
	}

	for _, l := range logical {
		rest := l
		first := true // first wire line of this logical line
		for {
			// The first wire line of a part carries the head.
			h := ""
			if len(cur) == 0 {
				h = head
			}
			piece := wrapWithHead(h, rest, lim.LineBudget)[0]
			piece.Concat = !first && len(cur) > 0
			if len(cur) > 0 && !room(piece) {
				flush(!first)
				continue
			}
			consumed := len(piece.Text) - len(h)
			cur = append(cur, piece)
			used += cost(piece)
			rest = rest[consumed:]
			first = false
			if rest == "" {
				break
			}
		}
	}
	flush(false)
	return parts
}

// Join reverses Split for tests and documentation: it strips prefix and
// markers and puts the parts back together.
func Join(prefix string, parts []Part) string {
	var b strings.Builder
	n := len(parts)
	for i, p := range parts {
		for j, l := range p.Lines {
			text := l.Text
			if j == 0 {
				text = strings.TrimPrefix(text, prefix)
				if n > 1 {
					text = strings.TrimPrefix(text, Marker(i+1, n))
				}
			}
			if j > 0 && !l.Concat {
				b.WriteByte('\n')
			}
			b.WriteString(text)
		}
		if i < n-1 && !p.MidLine {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

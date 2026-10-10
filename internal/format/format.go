// Package format encodes a delivered message as exactly one line of text or
// JSON.
package format

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/teo-mateo/chatterbox/internal/inbound"
)

const timeLayout = "2006-01-02T15:04:05Z"

// Encoder turns a message into one output line, without the trailing newline.
type Encoder func(inbound.Message) string

// ForName returns the encoder for a format name ("json" or anything else for
// text).
func ForName(name string) Encoder {
	if name == "json" {
		return JSON
	}
	return Text
}

// Text is the default format:
//
//	<time> <channel> <sender> <kind> <flags> | <text>
func Text(m inbound.Message) string {
	channel := m.Channel
	if m.Private {
		channel = "private"
	}
	var flags []string
	if m.Addressed {
		flags = append(flags, "to-you")
	}
	if m.Important {
		flags = append(flags, "important")
	}
	if m.Replayed {
		flags = append(flags, "replayed")
	}
	fl := "-"
	if len(flags) > 0 {
		fl = strings.Join(flags, ",")
	}
	var b strings.Builder
	b.WriteString(m.Time.UTC().Format(timeLayout))
	b.WriteByte(' ')
	b.WriteString(escapeField(channel))
	b.WriteByte(' ')
	b.WriteString(escapeField(m.From))
	b.WriteByte(' ')
	b.WriteString(m.Kind)
	b.WriteByte(' ')
	b.WriteString(fl)
	b.WriteString(" | ")
	b.WriteString(EscapeText(m.Text))
	return b.String()
}

// escapeField keeps a header field free of spaces and control characters, so
// the line always splits on the first " | ".
func escapeField(s string) string {
	return strings.ReplaceAll(EscapeText(s), " ", `\u{20}`)
}

// EscapeText makes s a single physical line: backslash, newline, carriage
// return and tab get two-character escapes; other C0 controls, DEL, and the
// Unicode line terminators NEL, LINE SEPARATOR and PARAGRAPH SEPARATOR become
// \u{hex}; invalid UTF-8 becomes the replacement character.
func EscapeText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20, r == 0x7f, r == 0x85, r == 0x2028, r == 0x2029:
			b.WriteString(`\u{`)
			b.WriteString(strconv.FormatInt(int64(r), 16))
			b.WriteByte('}')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

type jsonLine struct {
	Time      string  `json:"time"`
	Channel   *string `json:"channel"`
	From      string  `json:"from"`
	Kind      string  `json:"kind"`
	Addressed bool    `json:"addressed"`
	Important bool    `json:"important"`
	Private   bool    `json:"private"`
	Replayed  bool    `json:"replayed"`
	MsgID     string  `json:"msgid"`
	Text      string  `json:"text"`
}

// JSON is the machine-readable format: one object per line, keys in a fixed
// order, HTML escaping off.
func JSON(m inbound.Message) string {
	l := jsonLine{
		Time: m.Time.UTC().Format(timeLayout), From: m.From, Kind: m.Kind,
		Addressed: m.Addressed, Important: m.Important, Private: m.Private,
		Replayed: m.Replayed, MsgID: m.MsgID, Text: m.Text,
	}
	if !m.Private {
		ch := m.Channel
		l.Channel = &ch
	}
	return MarshalLine(l)
}

// MarshalLine encodes v as JSON on one physical line: HTML escaping off, and
// NEL, LINE SEPARATOR and PARAGRAPH SEPARATOR written as unicode escapes.
func MarshalLine(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return `{"error":"cannot encode"}`
	}
	s := strings.TrimRight(buf.String(), "\n")
	if strings.ContainsAny(s, "\u0085\u2028\u2029\u007f") {
		s = strings.NewReplacer("\u0085", `\u0085`, "\u2028", `\u2028`, "\u2029", `\u2029`, "\u007f", `\u007f`).Replace(s)
	}
	return s
}

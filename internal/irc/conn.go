// Package irc holds the wire-level pieces of the listener: the TLS
// connection, line parsing, capability and ISUPPORT tokens, batch assembly
// and the write queue.
package irc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/ergochat/irc-go/ircmsg"
	"github.com/ergochat/irc-go/ircreader"
)

// Message is one parsed IRC line.
type Message = ircmsg.Message

// maxLine bounds one incoming line: full tag data plus the 512-byte body.
const maxLine = ircmsg.MaxlenTags + 512

// Conn is a TLS connection to the server that reads and writes whole lines.
type Conn struct {
	c net.Conn
	r ircreader.Reader
}

// Dial connects and completes the TLS handshake. Plain-text connections are
// not implemented.
func Dial(ctx context.Context, addr string, cfg *tls.Config, timeout time.Duration) (*Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	d := tls.Dialer{NetDialer: &net.Dialer{KeepAlive: 30 * time.Second}, Config: cfg}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return NewConn(c), nil
}

// NewConn wraps an established connection.
func NewConn(c net.Conn) *Conn {
	conn := &Conn{c: c}
	conn.r.Initialize(c, 1024, maxLine)
	return conn
}

// ReadMessage returns the next parseable line. Empty and malformed lines
// are skipped; an over-long line is an error, as the stream cannot be
// trusted after it.
func (c *Conn) ReadMessage() (Message, error) {
	for {
		line, err := c.r.ReadLine()
		if err != nil {
			if errors.Is(err, ircreader.ErrReadQ) {
				return Message{}, fmt.Errorf("the server sent a line longer than %d bytes", maxLine)
			}
			return Message{}, err
		}
		msg, err := ParseLine(string(line))
		if err != nil {
			continue
		}
		return msg, nil
	}
}

// ParseLine parses one line without its terminator.
func ParseLine(line string) (Message, error) {
	msg, err := ircmsg.ParseLine(line)
	if err != nil && !errors.Is(err, ircmsg.ErrorBodyTooLong) {
		return msg, err
	}
	msg.Command = strings.ToUpper(msg.Command)
	return msg, nil
}

// WriteLines writes the lines, each terminated by CRLF, with a single write
// call so nothing else can land between them.
func (c *Conn) WriteLines(lines []string) error {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\r\n")
	}
	_, err := c.c.Write([]byte(b.String()))
	return err
}

// Close closes the connection.
func (c *Conn) Close() error { return c.c.Close() }

// Line builds a line to send. Tag values are escaped; the last parameter may
// contain spaces.
func Line(tags map[string]string, command string, params ...string) string {
	msg := ircmsg.MakeMessage(tags, "", command, params...)
	// A final parameter that is empty or starts with ':' needs the
	// trailing form; forcing it everywhere a text is sent keeps blank
	// multiline lines intact.
	if command == "PRIVMSG" || command == "NOTICE" || command == "QUIT" || command == "PING" || command == "PONG" {
		msg.ForceTrailing()
	}
	line, err := msg.Line()
	if err != nil && !errors.Is(err, ircmsg.ErrorBodyTooLong) {
		return ""
	}
	return strings.TrimRight(line, "\r\n")
}

// Nick returns the nickname part of a message source.
func Nick(source string) string {
	if i := strings.IndexAny(source, "!@"); i >= 0 {
		return source[:i]
	}
	return source
}

// Tag returns a tag's value, or "" when absent.
func Tag(m *Message, name string) string {
	_, v := m.GetTag(name)
	return v
}

// Time returns the message's server time.
func Time(m *Message) (time.Time, bool) {
	v := Tag(m, "time")
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// TimeParam formats a time as a CHATHISTORY timestamp reference.
func TimeParam(t time.Time) string {
	return "timestamp=" + t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// Param returns parameter i, or "" when the message has fewer.
func Param(m *Message, i int) string {
	if i < 0 {
		i += len(m.Params)
	}
	if i < 0 || i >= len(m.Params) {
		return ""
	}
	return m.Params[i]
}

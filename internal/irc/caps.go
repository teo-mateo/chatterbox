package irc

import (
	"strconv"
	"strings"
)

// Caps collects the capabilities a server advertises.
type Caps map[string]string

// AddLS consumes one "CAP * LS" reply and reports whether the list is
// complete (CAP LS 302 marks continuation lines with a "*" parameter).
func (c Caps) AddLS(m *Message) (complete bool) {
	// :server CAP <nick|*> LS [*] :cap cap=value ...
	if len(m.Params) < 3 {
		return true
	}
	more := len(m.Params) >= 4 && m.Params[2] == "*"
	c.add(m.Params[len(m.Params)-1])
	return !more
}

func (c Caps) add(list string) {
	for _, tok := range strings.Fields(list) {
		name, value, _ := strings.Cut(tok, "=")
		c[name] = value
	}
}

// Has reports whether the server advertises the capability.
func (c Caps) Has(name string) bool { _, ok := c[name]; return ok }

// Missing returns the names in want that the server does not advertise.
func (c Caps) Missing(want []string) []string {
	var out []string
	for _, w := range want {
		if !c.Has(w) {
			out = append(out, w)
		}
	}
	return out
}

// MultilineLimits parses the draft/multiline capability value,
// "max-bytes=4096,max-lines=100". A missing max-lines is 0 (unlimited).
func MultilineLimits(value string) (maxBytes, maxLines int) {
	for _, kv := range strings.Split(value, ",") {
		k, v, _ := strings.Cut(kv, "=")
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			continue
		}
		switch k {
		case "max-bytes":
			maxBytes = n
		case "max-lines":
			maxLines = n
		}
	}
	return maxBytes, maxLines
}

// ISupport holds the 005 tokens.
type ISupport map[string]string

// Add consumes one 005 reply: ":server 005 nick TOKEN TOKEN=value ... :are
// supported by this server".
func (s ISupport) Add(m *Message) {
	if len(m.Params) < 3 {
		return
	}
	for _, tok := range m.Params[1 : len(m.Params)-1] {
		if strings.HasPrefix(tok, "-") {
			delete(s, tok[1:])
			continue
		}
		name, value, _ := strings.Cut(tok, "=")
		s[name] = unescapeISupport(value)
	}
}

func unescapeISupport(v string) string {
	if !strings.Contains(v, `\x`) {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+3 < len(v) && v[i+1] == 'x' {
			if n, err := strconv.ParseUint(v[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

// Int returns a numeric token, or def when it is absent or malformed.
func (s ISupport) Int(name string, def int) int {
	n, err := strconv.Atoi(s[name])
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// Get returns a token's value, or def when the token is absent or empty.
func (s ISupport) Get(name, def string) string {
	if v := s[name]; v != "" {
		return v
	}
	return def
}

// Has reports whether the token was sent at all.
func (s ISupport) Has(name string) bool { _, ok := s[name]; return ok }

// PrefixChars returns the membership prefix characters from the PREFIX
// token "(qaohv)~&@%+", highest rank first, and the subset that means
// channel operator or above.
func (s ISupport) PrefixChars() (all, opOrAbove string) {
	v := s.Get("PREFIX", "(ov)@+")
	end := strings.IndexByte(v, ')')
	if !strings.HasPrefix(v, "(") || end < 0 {
		return "@+", "@"
	}
	modes, chars := v[1:end], v[end+1:]
	if len(modes) != len(chars) {
		return "@+", "@"
	}
	if i := strings.IndexByte(modes, 'o'); i >= 0 {
		return chars, chars[:i+1]
	}
	return chars, ""
}

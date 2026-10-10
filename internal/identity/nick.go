// Package identity covers agent nicknames, nickname comparison under the
// server's case mapping, and the per-user runtime directory.
package identity

import (
	"fmt"
	"strings"
)

// MaxNickLen is the upper bound on an agent nickname, before the server's
// own NICKLEN is applied.
const MaxNickLen = 32

// ValidateNick checks an agent nickname: letters, digits, '_' and '-',
// starting with a letter. This is stricter than IRC; it keeps socket file
// names safe and makes "@nick" matching unambiguous.
func ValidateNick(nick string) error {
	if nick == "" {
		return fmt.Errorf("nickname is empty")
	}
	if len(nick) > MaxNickLen {
		return fmt.Errorf("nickname %q is longer than %d characters", nick, MaxNickLen)
	}
	for i := 0; i < len(nick); i++ {
		c := nick[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '_' || c == '-'):
		default:
			if i == 0 {
				return fmt.Errorf("nickname %q must start with a letter", nick)
			}
			return fmt.Errorf("nickname %q may only contain letters, digits, '_' and '-'", nick)
		}
	}
	return nil
}

// IsNickChar reports whether c can be part of an agent nickname.
func IsNickChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// Casemap folds nicknames and channel names the way the server compares them.
type Casemap int

const (
	CasemapASCII Casemap = iota
	CasemapRFC1459
	CasemapRFC1459Strict
)

// ParseCasemap maps a CASEMAPPING token value to a Casemap. Unknown values
// (including Unicode mappings such as "precis") fall back to ASCII folding.
func ParseCasemap(v string) Casemap {
	switch strings.ToLower(v) {
	case "rfc1459":
		return CasemapRFC1459
	case "rfc1459-strict", "strict-rfc1459":
		return CasemapRFC1459Strict
	}
	return CasemapASCII
}

// Fold returns the canonical form of s under the mapping.
func (c Casemap) Fold(s string) string {
	b := []byte(s)
	for i, ch := range b {
		switch {
		case ch >= 'A' && ch <= 'Z':
			b[i] = ch + 32
		case c != CasemapASCII && (ch == '[' || ch == ']' || ch == '\\'):
			b[i] = ch + 32
		case c == CasemapRFC1459 && ch == '~':
			b[i] = '^'
		}
	}
	return string(b)
}

// Equal reports whether a and b name the same nickname or channel.
func (c Casemap) Equal(a, b string) bool { return c.Fold(a) == c.Fold(b) }

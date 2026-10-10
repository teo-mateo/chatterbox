// Package cli parses the command line, runs the subcommands and maps
// outcomes to exit codes.
package cli

import (
	"fmt"
	"strings"
)

// flagKind says whether a flag takes a value.
type flagKind int

const (
	boolFlag flagKind = iota
	valueFlag
)

// parsed is the result of parsing one subcommand's arguments.
type parsed struct {
	values map[string]string
	bools  map[string]bool
	args   []string
}

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

// parseArgs separates flags from positional arguments. Flags may appear
// before, between or after positionals. "--" ends flag parsing; a lone "-"
// is a positional. Anything else starting with "-" must be a known flag: an
// unknown one is an error and is never taken for message text.
func parseArgs(args []string, spec map[string]flagKind) (*parsed, error) {
	p := &parsed{values: map[string]string{}, bools: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			p.args = append(p.args, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			p.args = append(p.args, a)
			continue
		}
		if !strings.HasPrefix(a, "--") {
			return nil, usagef("unknown option %s (options start with --; to send text starting with -, put -- before it)", a)
		}
		name, value, hasValue := strings.Cut(a[2:], "=")
		kind, known := spec[name]
		if !known {
			return nil, usagef("unknown option --%s (to send text starting with -, put -- before it)", name)
		}
		if kind == boolFlag {
			if hasValue {
				return nil, usagef("option --%s takes no value", name)
			}
			p.bools[name] = true
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				hint := ""
				if name == "channel" {
					hint = " (quote channel names, as in --channel '#name': an unquoted # starts a shell comment)"
				}
				return nil, usagef("option --%s needs a value%s", name, hint)
			}
			i++
			value = args[i]
		}
		if _, dup := p.values[name]; dup {
			if name == "channel" {
				return nil, usagef("--channel was given more than once; a listener is in exactly one channel")
			}
			return nil, usagef("option --%s was given more than once", name)
		}
		p.values[name] = value
	}
	return p, nil
}

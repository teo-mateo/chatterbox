package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/teo-mateo/chatterbox/internal/config"
	"github.com/teo-mateo/chatterbox/internal/control"
	"github.com/teo-mateo/chatterbox/internal/format"
	"github.com/teo-mateo/chatterbox/internal/identity"
	"github.com/teo-mateo/chatterbox/internal/listener"
	"github.com/teo-mateo/chatterbox/internal/outbound"
)

// Version is stamped at build time.
var Version = "dev"

// Exit codes (04 §2).
const (
	ExitOK           = 0
	ExitInternal     = 1
	ExitUsage        = 2
	ExitNoListener   = 3
	ExitNickInUse    = 4
	ExitTrust        = 5
	ExitRefused      = 6
	ExitNotConnected = 7
	ExitTimeout      = 8
	ExitConnect      = 9
)

// exitForCode maps a control error code to an exit code.
func exitForCode(code string) int {
	switch code {
	case control.CodeBadRequest, control.CodeTooLarge, control.CodeUnsupportedVersion:
		return ExitUsage
	case control.CodeRejected, control.CodeUnknownNick, control.CodeChannelExists:
		return ExitRefused
	case control.CodeNotConnected:
		return ExitNotConnected
	case control.CodeTimeout:
		return ExitTimeout
	}
	return ExitInternal
}

// Env is what a command sees of the outside world.
type Env struct {
	Stdin  io.Reader
	Stdout *os.File
	Stderr io.Writer
}

type failure struct {
	code int
	msg  string
}

func (f *failure) Error() string { return f.msg }

func failf(code int, format string, a ...any) error {
	return &failure{code: code, msg: fmt.Sprintf(format, a...)}
}

// Run executes one command line and returns the exit code.
func Run(args []string, env Env) int {
	err := run(args, env)
	if err == nil {
		return ExitOK
	}
	var f *failure
	var u *usageError
	switch {
	case errors.As(err, &f):
		if f.msg != "" {
			fmt.Fprintf(env.Stderr, "chatterbox: %s\n", f.msg)
		}
		return f.code
	case errors.As(err, &u):
		fmt.Fprintf(env.Stderr, "chatterbox: %s\nRun `chatterbox help` for usage.\n", u.msg)
		return ExitUsage
	}
	fmt.Fprintf(env.Stderr, "chatterbox: %v\n", err)
	return ExitInternal
}

func run(args []string, env Env) error {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, usageText)
		return &failure{code: ExitUsage}
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "listen":
		return cmdListen(rest, env)
	case "publish":
		return cmdPost(rest, env, false)
	case "send":
		return cmdPost(rest, env, true)
	case "whos-there":
		return cmdWhosThere(rest, env)
	case "channels":
		return cmdChannels(rest, env)
	case "create-channel":
		return cmdCreateChannel(rest, env)
	case "version", "--version":
		fmt.Fprintf(env.Stdout, "chatterbox %s\n", Version)
		return nil
	case "help", "--help", "-h":
		fmt.Fprint(env.Stdout, helpFor(rest))
		return nil
	}
	return usagef("unknown command %q", cmd)
}

func cmdListen(args []string, env Env) error {
	p, err := parseArgs(args, map[string]flagKind{
		"as": valueFlag, "channel": valueFlag, "wake": valueFlag, "json": boolFlag,
		"server": valueFlag, "port": valueFlag, "config": valueFlag, "verbose": boolFlag,
	})
	if err != nil {
		return err
	}
	if len(p.args) > 0 {
		return usagef("listen takes no arguments besides its options, got %q", p.args[0])
	}
	nick := identity.ExplicitNick(p.values["as"])
	if nick == "" {
		return usagef("no nickname: pass --as <nick> or set %s", identity.NickEnv)
	}
	if err := identity.ValidateNick(nick); err != nil {
		return usagef("%v", err)
	}
	if ch, given := p.values["channel"]; given {
		if err := checkChannelArg(ch); err != nil {
			return err
		}
	}
	if w, given := p.values["wake"]; given && w != config.WakeAll && w != config.WakeAddressed {
		return usagef("--wake must be all or addressed, not %q", w)
	}
	flags := config.Flags{
		ConfigPath: p.values["config"], Server: p.values["server"],
		Channel: p.values["channel"], Wake: p.values["wake"], JSON: p.bools["json"],
	}
	if v, given := p.values["port"]; given {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return usagef("--port must be a number between 1 and 65535, not %q", v)
		}
		flags.Port = n
	}
	cfg, err := config.Load(flags)
	if err != nil {
		return usagef("%v", err)
	}
	if err := checkChannelArg(cfg.Defaults.Channel); cfg.Defaults.Channel != "" && err != nil {
		return err
	}
	exit := listener.Run(listener.Options{
		Nick: nick, Config: cfg, Verbose: p.bools["verbose"],
		RuntimeDir: identity.RuntimeDir(), Stdout: env.Stdout, Stderr: env.Stderr,
	})
	if exit == nil {
		return nil
	}
	return &failure{code: exit.Code, msg: exit.Msg}
}

// checkChannelArg rejects anything that is not a single channel name (FR1.3).
func checkChannelArg(ch string) error {
	switch {
	case strings.Contains(ch, ","):
		return usagef("--channel takes exactly one channel, not a list (%q); a listener is in one channel", ch)
	case ch == "" || strings.ContainsAny(ch, " \t\r\n\x07"):
		return usagef("%q is not a channel name", ch)
	case !strings.ContainsAny(ch[:1], "#&"):
		return usagef("%q is not a channel name: it must start with # (quote it, as in --channel '#%s': an unquoted # starts a shell comment)", ch, ch)
	}
	return nil
}

// probeTimeout bounds each status probe during listener discovery.
const probeTimeout = 500 * time.Millisecond

func startHint(nick string) string {
	return fmt.Sprintf("Start one with: chatterbox listen --as %s --channel '<#channel>'", nick)
}

func listRunning(found []control.Found) string {
	var b strings.Builder
	for _, f := range found {
		fmt.Fprintf(&b, "\n  %s (%s, %s)", f.Nick, f.Channel, f.State)
	}
	return b.String()
}

// locate finds the socket of the listener a command should talk to
// (02 §4, D15).
func locate(asFlag string, class identity.CommandClass, env Env) (nick, socket string, err error) {
	dir := identity.RuntimeDir()
	nick = identity.ExplicitNick(asFlag)
	if nick != "" {
		if err := identity.ValidateNick(nick); err != nil {
			return "", "", usagef("%v", err)
		}
		return nick, identity.SocketPath(dir, nick), nil
	}
	found := control.Discover(dir, probeTimeout)
	if !class.MayAutoDetect() {
		msg := fmt.Sprintf("this command acts under an agent's name and never guesses it: pass --as <nick> or set %s.", identity.NickEnv)
		if len(found) == 0 {
			msg += fmt.Sprintf("\nNo listener is running (looked in %s).", dir)
		} else {
			msg += "\nRunning listeners:" + listRunning(found)
		}
		return "", "", failf(ExitNoListener, "%s", msg)
	}
	switch len(found) {
	case 0:
		return "", "", failf(ExitNoListener, "no listener is running (looked in %s).\n%s", dir, startHint("<nick>"))
	case 1:
		fmt.Fprintf(env.Stderr, "chatterbox: using listener %s (auto-detected)\n", found[0].Nick)
		return found[0].Nick, found[0].Socket, nil
	}
	return "", "", failf(ExitNoListener, "several listeners are running; pass --as <nick> or set %s to choose one:%s",
		identity.NickEnv, listRunning(found))
}

// call sends one request to the listener and turns failures into exit codes.
func call(nick, socket string, req control.Request, posting bool, onProgress func(control.Progress)) (control.Response, error) {
	r, err := control.Do(socket, req, control.IdleTimeout, onProgress)
	switch {
	case errors.Is(err, control.ErrNoListener):
		return r, failf(ExitNoListener, "no listener is running for %s (looked in %s).\n%s",
			nick, identity.RuntimeDir(), startHint(nick))
	case errors.Is(err, control.ErrIdle):
		msg := fmt.Sprintf("listener %s stopped responding", nick)
		if posting {
			msg += "; the message may or may not have been sent. Check the channel before sending it again"
		}
		return r, failf(ExitTimeout, "%s", msg)
	case err != nil:
		return r, failf(ExitInternal, "%v", err)
	}
	if !r.Succeeded() {
		return r, failf(exitForCode(r.Code), "%s", r.Error)
	}
	return r, nil
}

// messageText builds the message from the arguments: several are joined
// with single spaces; a lone "-" reads standard input.
func messageText(args []string, env Env) (string, error) {
	if len(args) == 0 {
		return "", usagef("no message given (use - to read it from standard input)")
	}
	text := strings.Join(args, " ")
	if len(args) == 1 && args[0] == "-" {
		data, err := io.ReadAll(io.LimitReader(env.Stdin, control.MaxRequestBytes))
		if err != nil {
			return "", failf(ExitUsage, "cannot read the message from standard input: %v", err)
		}
		text = string(data)
	}
	text, err := outbound.Normalize(text)
	if err != nil {
		return "", failf(ExitUsage, "%v; message not sent", err)
	}
	return text, nil
}

func cmdPost(args []string, env Env, send bool) error {
	spec := map[string]flagKind{"as": valueFlag, "important": boolFlag}
	if send {
		spec["private"] = boolFlag
	}
	p, err := parseArgs(args, spec)
	if err != nil {
		return err
	}
	req := control.Request{Op: control.OpPublish, Important: p.bools["important"]}
	rest := p.args
	if send {
		if len(rest) == 0 {
			return usagef("send needs a nickname and a message: chatterbox send --as <you> <nick> <message>")
		}
		req.Op, req.To, req.Private = control.OpSend, rest[0], p.bools["private"]
		rest = rest[1:]
	}
	// The identity is resolved before the message is read, so a missing
	// --as fails without consuming standard input.
	nick, socket, err := locate(p.values["as"], identity.ClassPosting, env)
	if err != nil {
		return err
	}
	if req.Text, err = messageText(rest, env); err != nil {
		return err
	}
	r, err := call(nick, socket, req, true, nil)
	if err != nil {
		return err
	}
	if r.Parts > 1 {
		fmt.Fprintf(env.Stderr, "chatterbox: sent as %d messages\n", r.Parts)
	}
	return nil
}

func jsonFlagOnly(args []string, name string) (*parsed, error) {
	p, err := parseArgs(args, map[string]flagKind{"as": valueFlag, "json": boolFlag})
	if err != nil {
		return nil, err
	}
	if len(p.args) > 0 {
		return nil, usagef("%s takes no arguments, got %q", name, p.args[0])
	}
	return p, nil
}

func cmdWhosThere(args []string, env Env) error {
	p, err := jsonFlagOnly(args, "whos-there")
	if err != nil {
		return err
	}
	nick, socket, err := locate(p.values["as"], identity.ClassReadOnly, env)
	if err != nil {
		return err
	}
	r, err := call(nick, socket, control.Request{Op: control.OpNames}, false, nil)
	if err != nil {
		return err
	}
	for _, part := range r.Participants {
		if p.bools["json"] {
			fmt.Fprintln(env.Stdout, format.MarshalLine(part))
		} else {
			fmt.Fprintf(env.Stdout, "%s %s\n", part.Nick, part.Kind)
		}
	}
	return nil
}

func cmdChannels(args []string, env Env) error {
	p, err := jsonFlagOnly(args, "channels")
	if err != nil {
		return err
	}
	nick, socket, err := locate(p.values["as"], identity.ClassReadOnly, env)
	if err != nil {
		return err
	}
	r, err := call(nick, socket, control.Request{Op: control.OpChannels}, false, nil)
	if err != nil {
		return err
	}
	for _, ch := range r.Channels {
		if p.bools["json"] {
			fmt.Fprintln(env.Stdout, format.MarshalLine(ch))
			continue
		}
		last := "-"
		if ch.LastActivity != nil {
			last = *ch.LastActivity
		}
		fmt.Fprintf(env.Stdout, "%s %d %s\n", ch.Name, ch.Users, last)
	}
	return nil
}

func cmdCreateChannel(args []string, env Env) error {
	p, err := parseArgs(args, map[string]flagKind{"as": valueFlag})
	if err != nil {
		return err
	}
	if len(p.args) != 1 {
		return usagef("create-channel takes exactly one channel name (quote it, as in '#name': an unquoted # starts a shell comment)")
	}
	nick, socket, err := locate(p.values["as"], identity.ClassPosting, env)
	if err != nil {
		return err
	}
	r, err := call(nick, socket, control.Request{Op: control.OpCreateChannel, Name: p.args[0]}, false, nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "now listening on %s; the channel exists only while someone is in it\n", r.Channel)
	return nil
}

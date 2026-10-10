package cli

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/teo-mateo/chatterbox/internal/control"
)

var sendSpec = map[string]flagKind{"as": valueFlag, "important": boolFlag, "private": boolFlag}

func TestParseArgsInterspersed(t *testing.T) {
	cases := []struct {
		args      []string
		as        string
		important bool
		pos       []string
	}{
		{[]string{"--important", "agentB", "x"}, "", true, []string{"agentB", "x"}},
		{[]string{"agentB", "--important", "x"}, "", true, []string{"agentB", "x"}},
		{[]string{"agentB", "x", "--important"}, "", true, []string{"agentB", "x"}},
		{[]string{"hello", "--as", "a"}, "a", false, []string{"hello"}},
		{[]string{"--as=a", "hello", "world"}, "a", false, []string{"hello", "world"}},
		{[]string{"--as", "a", "--", "--important", "-x"}, "a", false, []string{"--important", "-x"}},
		{[]string{"--as", "a", "-"}, "a", false, []string{"-"}},
		{[]string{"--", "-1 is wrong"}, "", false, []string{"-1 is wrong"}},
		{[]string{"a", "--", "--"}, "", false, []string{"a", "--"}},
	}
	for _, c := range cases {
		p, err := parseArgs(c.args, sendSpec)
		if err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if p.values["as"] != c.as || p.bools["important"] != c.important || !reflect.DeepEqual(p.args, c.pos) {
			t.Errorf("%v: as=%q important=%v args=%q", c.args, p.values["as"], p.bools["important"], p.args)
		}
	}
}

func TestParseArgsRejects(t *testing.T) {
	for _, args := range [][]string{
		{"--bogus", "x"},
		{"-1 is wrong"},
		{"-x"},
		{"--as"},
		{"--as", "a", "--as", "b"},
		{"--important=yes"},
		{"hello", "--verbose"},
	} {
		if _, err := parseArgs(args, sendSpec); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	_, err := parseArgs([]string{"--channel", "#a", "--channel", "#b"}, map[string]flagKind{"channel": valueFlag})
	if err == nil || !strings.Contains(err.Error(), "exactly one channel") {
		t.Errorf("repeated --channel: %v", err)
	}
	_, err = parseArgs([]string{"--channel"}, map[string]flagKind{"channel": valueFlag})
	if err == nil || !strings.Contains(err.Error(), "shell comment") {
		t.Errorf("--channel without a value should hint at quoting: %v", err)
	}
}

func TestExitCodeMapping(t *testing.T) {
	want := map[string]int{
		control.CodeBadRequest:         2,
		control.CodeTooLarge:           2,
		control.CodeUnsupportedVersion: 2,
		control.CodeRejected:           6,
		control.CodeUnknownNick:        6,
		control.CodeChannelExists:      6,
		control.CodeNotConnected:       7,
		control.CodeTimeout:            8,
		"something_new":                1,
	}
	for code, exit := range want {
		if got := exitForCode(code); got != exit {
			t.Errorf("exitForCode(%q) = %d, want %d", code, got, exit)
		}
	}
}

func runCLI(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("CHATTERBOX_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CHATTERBOX_NICK", "")
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var stderr bytes.Buffer
	code := Run(args, Env{Stdin: strings.NewReader(stdin), Stdout: out, Stderr: &stderr})
	data, _ := os.ReadFile(out.Name())
	return code, string(data), stderr.String()
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"frobnicate"},
		{"publish", "--as", "a", "--bogus", "x"},
		{"publish", "--as", "a"},
		{"publish", "--as", "9a", "x"},
		{"send", "--as", "a"},
		{"send", "--as", "a", "b"},
		{"whos-there", "extra"},
		{"channels", "--important"},
		{"create-channel", "--as", "a"},
		{"create-channel", "--as", "a", "#x", "#y"},
		{"listen", "--as", "a", "extra", "--server", "h", "--channel", "#c"},
		{"listen", "--as", "a", "--server", "h", "--channel", "#c", "--port", "0"},
		{"listen", "--as", "a", "--server", "h", "--channel", "#a", "--channel", "#b"},
	} {
		code, out, stderr := runCLI(t, "", args...)
		if code != ExitUsage {
			t.Errorf("%v: exit %d, want 2 (%s)", args, code, stderr)
		}
		if out != "" {
			t.Errorf("%v: wrote to stdout: %q", args, out)
		}
		if !strings.HasPrefix(stderr, "chatterbox:") {
			t.Errorf("%v: stderr %q", args, stderr)
		}
	}
}

func TestNoListenerMessages(t *testing.T) {
	code, _, stderr := runCLI(t, "", "publish", "--as", "agentA", "hi")
	if code != ExitNoListener || !strings.Contains(stderr, "no listener is running for agentA") ||
		!strings.Contains(stderr, "chatterbox listen --as agentA --channel") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	// Posting commands never auto-detect: without a nickname they fail
	// before even looking at the message.
	code, _, stderr = runCLI(t, "", "publish", "hi")
	if code != ExitNoListener || !strings.Contains(stderr, "never guesses") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	code, _, stderr = runCLI(t, "", "whos-there")
	if code != ExitNoListener || !strings.Contains(stderr, "no listener is running") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

func TestVersionAndHelp(t *testing.T) {
	code, out, _ := runCLI(t, "", "version")
	if code != 0 || !strings.HasPrefix(out, "chatterbox ") {
		t.Errorf("version: %d %q", code, out)
	}
	code, out, _ = runCLI(t, "", "help")
	if code != 0 || !strings.Contains(out, "create-channel") {
		t.Errorf("help: %d %q", code, out)
	}
	for cmd := range commandHelp {
		if _, out, _ := runCLI(t, "", "help", cmd); !strings.Contains(out, "chatterbox "+cmd) {
			t.Errorf("help %s: %q", cmd, out)
		}
	}
}

func TestMessageText(t *testing.T) {
	env := Env{Stdin: strings.NewReader("from stdin\r\nsecond\n")}
	got, err := messageText([]string{"-"}, env)
	if err != nil || got != "from stdin\nsecond" {
		t.Errorf("stdin: %q %v", got, err)
	}
	got, err = messageText([]string{"a", "b", "-"}, env)
	if err != nil || got != "a b -" {
		t.Errorf("a dash among other words is text: %q %v", got, err)
	}
	for _, args := range [][]string{{}, {""}, {"  "}, {"a\x00b"}} {
		if _, err := messageText(args, env); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}

package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateNick(t *testing.T) {
	good := []string{"a", "agentA", "agent-2", "a_b", "Z9", strings.Repeat("a", 32)}
	bad := []string{"", "9a", "-a", "_a", "a b", "a.b", "a/b", "../x", "a[b]", "é", strings.Repeat("a", 33), "a\n"}
	for _, n := range good {
		if err := ValidateNick(n); err != nil {
			t.Errorf("ValidateNick(%q) = %v, want nil", n, err)
		}
	}
	for _, n := range bad {
		if err := ValidateNick(n); err == nil {
			t.Errorf("ValidateNick(%q) = nil, want error", n)
		}
	}
}

func TestCasemap(t *testing.T) {
	if !CasemapASCII.Equal("AgentA", "agenta") {
		t.Error("ascii: AgentA != agenta")
	}
	if CasemapASCII.Equal("a[b]", "a{b}") {
		t.Error("ascii must not fold brackets")
	}
	if !CasemapRFC1459.Equal("a[b]\\~", "A{B}|^") {
		t.Error("rfc1459 must fold []\\~ to {}|^")
	}
	if CasemapRFC1459Strict.Equal("a~", "a^") {
		t.Error("rfc1459-strict must not fold ~")
	}
	if ParseCasemap("rfc1459") != CasemapRFC1459 || ParseCasemap("ascii") != CasemapASCII || ParseCasemap("precis") != CasemapASCII {
		t.Error("ParseCasemap")
	}
}

func TestRuntimeDirEnv(t *testing.T) {
	t.Setenv(RuntimeDirEnv, "/somewhere/else")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1")
	if got := RuntimeDir(); got != "/somewhere/else" {
		t.Errorf("RuntimeDir() = %q", got)
	}
	t.Setenv(RuntimeDirEnv, "")
	if got := RuntimeDir(); !strings.HasPrefix(got, "/tmp/chatterbox-") {
		t.Errorf("RuntimeDir() = %q, want it under /tmp regardless of XDG_RUNTIME_DIR", got)
	}
}

func TestEnsureRuntimeDir(t *testing.T) {
	base := t.TempDir()

	fresh := filepath.Join(base, "fresh")
	if err := EnsureRuntimeDir(fresh); err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if fi, _ := os.Stat(fresh); fi.Mode().Perm() != 0o700 {
		t.Errorf("fresh dir mode %v", fi.Mode().Perm())
	}
	if err := EnsureRuntimeDir(fresh); err != nil {
		t.Errorf("existing good dir: %v", err)
	}

	wide := filepath.Join(base, "wide")
	os.Mkdir(wide, 0o755)
	os.Chmod(wide, 0o755)
	if err := EnsureRuntimeDir(wide); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Errorf("wide mode: got %v", err)
	}

	link := filepath.Join(base, "link")
	os.Symlink(fresh, link)
	if err := EnsureRuntimeDir(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("symlink: got %v", err)
	}

	file := filepath.Join(base, "file")
	os.WriteFile(file, nil, 0o600)
	if err := EnsureRuntimeDir(file); err == nil {
		t.Error("regular file accepted as runtime dir")
	}

	if err := ensureRuntimeDir(fresh, os.Getuid()+1); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Errorf("wrong owner: got %v", err)
	}
}

func TestSocketPaths(t *testing.T) {
	if got := SocketPath("/d", "AgentA"); got != "/d/agenta.sock" {
		t.Errorf("SocketPath = %q", got)
	}
	if got := LockPath("/d", "AgentA"); got != "/d/agenta.lock" {
		t.Errorf("LockPath = %q", got)
	}
	if err := CheckSocketPath("/tmp/chatterbox-1000/agenta.sock"); err != nil {
		t.Error(err)
	}
	if err := CheckSocketPath("/" + strings.Repeat("x", 120) + "/a.sock"); err == nil {
		t.Error("over-long socket path accepted")
	}
}

func TestCommandClass(t *testing.T) {
	if ClassPosting.MayAutoDetect() || ClassListen.MayAutoDetect() {
		t.Error("posting and listen commands must never auto-detect")
	}
	if !ClassReadOnly.MayAutoDetect() {
		t.Error("read-only commands may auto-detect")
	}
	t.Setenv(NickEnv, "fromenv")
	if ExplicitNick("flag") != "flag" || ExplicitNick("") != "fromenv" {
		t.Error("ExplicitNick precedence")
	}
}

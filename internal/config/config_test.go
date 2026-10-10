package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "chatterbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const full = `
[server]
host = "tiny2"
port = 7000

[tls]
fingerprint = "AA:BB"
ca_file = "/ca.pem"
trust_on_first_use = false

[defaults]
channel = "#hof"
wake = "addressed"
format = "json"

[limits]
max_message_bytes = 1000
channels_history_lookups = 3

[tuning]
keepalive_interval = "200ms"
`

func TestFileValues(t *testing.T) {
	write(t, full)
	c, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Host != "tiny2" || c.Server.Port != 7000 {
		t.Errorf("server = %+v", c.Server)
	}
	if c.TLS.Fingerprint != "AA:BB" || c.TLS.CAFile != "/ca.pem" || c.TLS.TrustOnFirstUse {
		t.Errorf("tls = %+v", c.TLS)
	}
	if c.Defaults.Channel != "#hof" || c.Defaults.Wake != WakeAddressed || c.Defaults.Format != FormatJSON {
		t.Errorf("defaults = %+v", c.Defaults)
	}
	if c.Limits.MaxMessageBytes != 1000 || c.Limits.ChannelsHistoryLookups != 3 {
		t.Errorf("limits = %+v", c.Limits)
	}
	if c.Tuning.KeepaliveInterval.D() != 200*time.Millisecond {
		t.Errorf("tuning override not applied: %v", c.Tuning.KeepaliveInterval.D())
	}
	if c.Tuning.IdleTimeout.D() != 90*time.Second {
		t.Errorf("tuning default lost: %v", c.Tuning.IdleTimeout.D())
	}
}

func TestFlagsOverrideFile(t *testing.T) {
	write(t, full)
	c, err := Load(Flags{Server: "other", Port: 1234, Channel: "#x", Wake: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Host != "other" || c.Server.Port != 1234 || c.Defaults.Channel != "#x" || c.Defaults.Wake != WakeAll {
		t.Errorf("flags did not win: %+v %+v", c.Server, c.Defaults)
	}
	if c.Defaults.Format != FormatJSON {
		t.Error("file format lost when --json not given")
	}
}

func TestDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Load(Flags{Server: "h"})
	if err != nil {
		t.Fatalf("file must be optional when --server is given: %v", err)
	}
	if c.Server.Port != 6697 || c.Defaults.Wake != WakeAll || c.Defaults.Format != FormatText ||
		!c.TLS.TrustOnFirstUse || c.Limits.MaxMessageBytes != 65536 || c.Limits.ChannelsHistoryLookups != 50 {
		t.Errorf("defaults = %+v", c)
	}
	c, _ = Load(Flags{Server: "h", JSON: true})
	if c.Defaults.Format != FormatJSON {
		t.Error("--json not applied")
	}
}

func TestMissingServer(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	_, err := Load(Flags{})
	if err == nil {
		t.Fatal("expected an error without a server")
	}
	if !strings.Contains(err.Error(), filepath.Join(xdg, "chatterbox", "config.toml")) {
		t.Errorf("error does not name the file to create: %v", err)
	}
}

func TestUnknownKey(t *testing.T) {
	write(t, "[server]\nhost = \"h\"\nnick = \"a\"\n")
	_, err := Load(Flags{})
	if err == nil || !strings.Contains(err.Error(), "server.nick") {
		t.Errorf("unknown key error = %v", err)
	}
	write(t, "nick = \"a\"\n[server]\nhost = \"h\"\n")
	if _, err := Load(Flags{}); err == nil || !strings.Contains(err.Error(), "nick") {
		t.Errorf("top-level nick key must be rejected (D3): %v", err)
	}
}

func TestExplicitConfigPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "alt.toml")
	os.WriteFile(p, []byte("[server]\nhost = \"alt\"\n"), 0o600)
	c, err := Load(Flags{ConfigPath: p})
	if err != nil || c.Server.Host != "alt" {
		t.Errorf("--config: %v %+v", err, c.Server)
	}
	if _, err := Load(Flags{ConfigPath: filepath.Join(t.TempDir(), "missing.toml"), Server: "h"}); err == nil {
		t.Error("a missing file named by --config must be an error")
	}
}

func TestInvalidValues(t *testing.T) {
	for _, body := range []string{
		"[server]\nhost=\"h\"\n[defaults]\nwake=\"some\"\n",
		"[server]\nhost=\"h\"\n[defaults]\nformat=\"xml\"\n",
		"[server]\nhost=\"h\"\nport=70000\n",
		"[server]\nhost=\"h\"\n[tuning]\nidle_timeout=\"fast\"\n",
		"[server]\nhost=\"h\"\n[limits]\nmax_message_bytes=-1\n",
	} {
		write(t, body)
		if _, err := Load(Flags{}); err == nil {
			t.Errorf("accepted invalid config:\n%s", body)
		}
	}
}

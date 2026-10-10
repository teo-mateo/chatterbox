// Package config loads the chatterbox configuration file and overlays flags
// on it. Precedence is flag, then environment, then file, then default.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration is a time.Duration written as a string ("30s") in the file.
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	if v <= 0 {
		return fmt.Errorf("duration %q must be positive", b)
	}
	*d = Duration(v)
	return nil
}

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

type Server struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

type TLS struct {
	Fingerprint     string `toml:"fingerprint"`
	CAFile          string `toml:"ca_file"`
	TrustOnFirstUse bool   `toml:"trust_on_first_use"`
}

type Defaults struct {
	Channel string `toml:"channel"`
	Wake    string `toml:"wake"`
	Format  string `toml:"format"`
}

type Limits struct {
	MaxMessageBytes        int `toml:"max_message_bytes"`
	ChannelsHistoryLookups int `toml:"channels_history_lookups"`
}

// Tuning holds every timing value of the listener. The section is
// undocumented; it exists so tests can run in seconds.
type Tuning struct {
	DialTimeout       Duration `toml:"dial_timeout"`
	RegisterTimeout   Duration `toml:"register_timeout"`
	KeepaliveInterval Duration `toml:"keepalive_interval"`
	IdleTimeout       Duration `toml:"idle_timeout"`
	BackoffMin        Duration `toml:"backoff_min"`
	BackoffMax        Duration `toml:"backoff_max"`
	BackoffReset      Duration `toml:"backoff_reset"`
	AckBase           Duration `toml:"ack_base"`
	AckPerLine        Duration `toml:"ack_per_line"`
	RequestTimeout    Duration `toml:"request_timeout"`
	NickRetryWindow   Duration `toml:"nick_retry_window"`
	ReadyWait         Duration `toml:"ready_wait"`
	ChannelsBudget    Duration `toml:"channels_budget"`
	StdoutFullLimit   Duration `toml:"stdout_full_limit"`
	StdoutQueueLines  int      `toml:"stdout_queue_lines"`
	HangupPoll        Duration `toml:"hangup_poll"`
	QuitWait          Duration `toml:"quit_wait"`
	CatchupOverlap    Duration `toml:"catchup_overlap"`
}

type Config struct {
	Server   Server   `toml:"server"`
	TLS      TLS      `toml:"tls"`
	Defaults Defaults `toml:"defaults"`
	Limits   Limits   `toml:"limits"`
	Tuning   Tuning   `toml:"tuning"`
}

const (
	WakeAll       = "all"
	WakeAddressed = "addressed"
	FormatText    = "text"
	FormatJSON    = "json"
)

// Default returns the built-in defaults.
func Default() Config {
	s := func(d time.Duration) Duration { return Duration(d) }
	return Config{
		Server:   Server{Port: 6697},
		TLS:      TLS{TrustOnFirstUse: true},
		Defaults: Defaults{Wake: WakeAll, Format: FormatText},
		Limits:   Limits{MaxMessageBytes: 65536, ChannelsHistoryLookups: 50},
		Tuning: Tuning{
			DialTimeout:       s(15 * time.Second),
			RegisterTimeout:   s(30 * time.Second),
			KeepaliveInterval: s(30 * time.Second),
			IdleTimeout:       s(90 * time.Second),
			BackoffMin:        s(time.Second),
			BackoffMax:        s(60 * time.Second),
			BackoffReset:      s(60 * time.Second),
			AckBase:           s(10 * time.Second),
			AckPerLine:        s(500 * time.Millisecond),
			RequestTimeout:    s(30 * time.Second),
			NickRetryWindow:   s(5 * time.Minute),
			ReadyWait:         s(5 * time.Second),
			ChannelsBudget:    s(10 * time.Second),
			StdoutFullLimit:   s(60 * time.Second),
			StdoutQueueLines:  10000,
			HangupPoll:        s(2 * time.Second),
			QuitWait:          s(2 * time.Second),
			CatchupOverlap:    s(2 * time.Second),
		},
	}
}

// Dir is the chatterbox configuration directory: $XDG_CONFIG_HOME/chatterbox,
// else ~/.config/chatterbox.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "chatterbox"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find the home directory: %v", err)
	}
	return filepath.Join(home, ".config", "chatterbox"), nil
}

// DefaultPath is the configuration file used when --config is not given.
func DefaultPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.toml"), nil
}

// Flags are the listen flags that override the file. Zero values mean "not
// given".
type Flags struct {
	ConfigPath string
	Server     string
	Port       int
	Channel    string
	Wake       string
	JSON       bool
}

// Load reads the configuration file and applies the flags. The file is
// optional unless --config names it; without a server from either source the
// result is an error naming the file to create.
func Load(f Flags) (Config, error) {
	cfg := Default()
	path := f.ConfigPath
	explicit := path != ""
	if !explicit {
		p, err := DefaultPath()
		if err != nil {
			return cfg, err
		}
		path = p
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := parse(string(data), path, &cfg); err != nil {
			return cfg, err
		}
	case errors.Is(err, os.ErrNotExist) && !explicit:
		// optional
	default:
		return cfg, fmt.Errorf("cannot read the config file: %v", err)
	}

	if f.Server != "" {
		cfg.Server.Host = f.Server
	}
	if f.Port != 0 {
		cfg.Server.Port = f.Port
	}
	if f.Channel != "" {
		cfg.Defaults.Channel = f.Channel
	}
	if f.Wake != "" {
		cfg.Defaults.Wake = f.Wake
	}
	if f.JSON {
		cfg.Defaults.Format = FormatJSON
	}
	return cfg, cfg.validate(path)
}

func parse(data, path string, cfg *Config) error {
	md, err := toml.Decode(data, cfg)
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		sort.Strings(keys)
		return fmt.Errorf("%s: unknown key %s", path, strings.Join(keys, ", "))
	}
	return nil
}

func (c Config) validate(path string) error {
	if c.Server.Host == "" {
		return fmt.Errorf("no server configured: pass --server <host>, or create %s with\n\n[server]\nhost = \"irc.example.org\"\nport = 6697", path)
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server port %d is out of range", c.Server.Port)
	}
	if c.Defaults.Wake != WakeAll && c.Defaults.Wake != WakeAddressed {
		return fmt.Errorf("wake filter %q is not one of: all, addressed", c.Defaults.Wake)
	}
	if c.Defaults.Format != FormatText && c.Defaults.Format != FormatJSON {
		return fmt.Errorf("format %q is not one of: text, json", c.Defaults.Format)
	}
	if c.Limits.MaxMessageBytes < 0 {
		return fmt.Errorf("limits.max_message_bytes must not be negative")
	}
	if c.Limits.ChannelsHistoryLookups < 0 {
		return fmt.Errorf("limits.channels_history_lookups must not be negative")
	}
	if c.Tuning.StdoutQueueLines < 1 {
		return fmt.Errorf("tuning.stdout_queue_lines must be positive")
	}
	return nil
}

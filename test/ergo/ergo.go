// Package ergo starts private Ergo IRC servers for tests and manual work.
// The Ergo release is pinned; it is downloaded once into a cache directory
// and verified against a known checksum.
package ergo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/template"
	"time"
)

// Version is the pinned Ergo release.
const Version = "2.19.1"

// Release archives by GOOS/GOARCH, with their SHA-256.
var releases = map[string]struct{ name, sha256 string }{
	"linux/amd64":  {"linux-x86_64", "c275f2de8e8eb074c38707136392290172400b5b78616cb9f660351527924b47"},
	"linux/arm64":  {"linux-arm64", "3f68fee0ede09eeeb205a553fe9727b409c686752e1cc2fea34ae2f9d64517fb"},
	"darwin/amd64": {"macos-x86_64", "68b388a49034257082ee0292703874630a2c10cfba86c73bb2adc37b515c57a3"},
	"darwin/arm64": {"macos-arm64", "1bd97a0917036061e2dcdfc29149c2e252e3bc856282e0956afa2aaaa54dd787"},
}

// Operator credentials of every server started by this package.
const (
	OperName = "admin"
	OperPass = "operpass"
	// bcrypt of OperPass, as produced by `ergo genpasswd`.
	operHash = "$2a$04$LfybFQd8f75Y3NhznCYJR.yJXSBpCQHCLhbb3RyGq4vkXNKCIT9L6"
)

// CacheDir is where the Ergo release is kept: $CHATTERBOX_ERGO_CACHE, else
// the user cache directory.
func CacheDir() (string, error) {
	if d := os.Getenv("CHATTERBOX_ERGO_CACHE"); d != "" {
		return d, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "chatterbox-test", "ergo"), nil
}

// Binary returns the path of the pinned Ergo binary and its language
// directory, downloading and unpacking the release on first use.
func Binary() (bin, dir string, err error) {
	rel, ok := releases[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", "", fmt.Errorf("no pinned Ergo release for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	cache, err := CacheDir()
	if err != nil {
		return "", "", err
	}
	base := fmt.Sprintf("ergo-%s-%s", Version, rel.name)
	dir = filepath.Join(cache, base)
	bin = filepath.Join(dir, "ergo")
	if _, err := os.Stat(bin); err == nil {
		return bin, dir, nil
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", "", err
	}
	url := fmt.Sprintf("https://github.com/ergochat/ergo/releases/download/v%s/%s.tar.gz", Version, base)
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Get(url)
	if err != nil {
		return "", "", fmt.Errorf("cannot download Ergo: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("cannot download %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != rel.sha256 {
		return "", "", fmt.Errorf("checksum mismatch for %s", url)
	}
	// Unpack next to the final place and rename, so concurrent test
	// packages never see a half-written tree.
	tmp, err := os.MkdirTemp(cache, "unpack-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)
	if err := untar(data, tmp); err != nil {
		return "", "", err
	}
	if err := os.Rename(filepath.Join(tmp, base), dir); err != nil {
		if _, statErr := os.Stat(bin); statErr != nil {
			return "", "", err
		}
	}
	return bin, dir, nil
}

func untar(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, h.Name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("archive entry %q escapes the destination", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
}

// Options vary the server configuration. The zero value is the test
// configuration of the plan: Ergo's defaults, with flood control left on,
// history pages of 20 messages and idle timeouts of a few seconds.
type Options struct {
	// Port to listen on; 0 picks a free one.
	Port int
	// NoMultiline disables draft/multiline.
	NoMultiline bool
	// NoHistory disables message history.
	NoHistory bool
	// PageSize is chathistory-maxmessages; 0 means 20.
	PageSize int
	// PingAfter and DisconnectAfter are the idle timeouts; 0 means 4 s and
	// 8 s.
	PingAfter, DisconnectAfter time.Duration
	// Defaults keeps Ergo's shipped history page size and idle timeouts.
	Defaults bool
}

// Server is a running Ergo.
type Server struct {
	Host string
	Port int
	Dir  string

	opts Options
	bin  string
	cmd  *exec.Cmd
	log  *os.File
}

// Addr is host:port.
func (s *Server) Addr() string { return net.JoinHostPort(s.Host, fmt.Sprint(s.Port)) }

// Start launches a server in a fresh temporary directory with a fresh
// self-signed certificate.
func Start(opts Options) (*Server, error) {
	bin, relDir, err := Binary()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "cbx-ergo-")
	if err != nil {
		return nil, err
	}
	if opts.Port == 0 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		opts.Port = ln.Addr().(*net.TCPAddr).Port
		ln.Close()
	}
	s := &Server{Host: "127.0.0.1", Port: opts.Port, Dir: dir, opts: opts, bin: bin}
	if err := os.Symlink(filepath.Join(relDir, "languages"), filepath.Join(dir, "languages")); err != nil {
		return nil, err
	}
	if err := s.NewCertificate(); err != nil {
		return nil, err
	}
	if err := s.writeConfig(); err != nil {
		return nil, err
	}
	if out, err := s.run("initdb").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ergo initdb: %v: %s", err, out)
	}
	if err := s.start(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) run(sub string) *exec.Cmd {
	cmd := exec.Command(s.bin, sub, "--conf", filepath.Join(s.Dir, "ircd.yaml"), "--quiet")
	cmd.Dir = s.Dir
	return cmd
}

func (s *Server) start() error {
	log, err := os.OpenFile(filepath.Join(s.Dir, "ergo.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := s.run("run")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		return err
	}
	s.cmd, s.log = cmd, log
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", s.Addr(), &tls.Config{InsecureSkipVerify: true})
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.Stop()
	return fmt.Errorf("ergo did not start listening on %s; see %s", s.Addr(), filepath.Join(s.Dir, "ergo.log"))
}

// Halt stops the server process and keeps its directory, so Resume can
// start it again on the same port. In-memory history is lost.
func (s *Server) Halt() {
	if s.cmd == nil {
		return
	}
	s.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.cmd.Process.Kill()
		<-done
	}
	s.cmd = nil
	s.log.Close()
}

// Resume starts a halted server again.
func (s *Server) Resume() error { return s.start() }

// Stop halts the server and removes its directory.
func (s *Server) Stop() {
	s.Halt()
	os.RemoveAll(s.Dir)
}

// NewCertificate writes a fresh self-signed certificate. A running server
// picks it up after Halt and Resume.
func (s *Server) NewCertificate() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"chatterbox test Ergo"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "fullchain.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, "privkey.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600)
}

func (s *Server) writeConfig() error {
	o := s.opts
	v := map[string]any{
		"Port":       s.Port,
		"OperHash":   operHash,
		"MaxBytes":   4096,
		"History":    !o.NoHistory,
		"PageSize":   20,
		"Ping":       "4s",
		"Disconnect": "8s",
	}
	if o.NoMultiline {
		v["MaxBytes"] = 0
	}
	if o.PageSize > 0 {
		v["PageSize"] = o.PageSize
	}
	if o.PingAfter > 0 {
		v["Ping"] = o.PingAfter.String()
	}
	if o.DisconnectAfter > 0 {
		v["Disconnect"] = o.DisconnectAfter.String()
	}
	if o.Defaults {
		v["PageSize"], v["Ping"], v["Disconnect"] = 1000, "1m30s", "2m30s"
	}
	var buf bytes.Buffer
	if err := configTemplate.Execute(&buf, v); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, "ircd.yaml"), buf.Bytes(), 0o644)
}

// configTemplate is Ergo 2.19's default.yaml reduced to what a test server
// needs. Everything not parameterized is Ergo's default, including flood
// control (fakelag), so timing behavior is the real one.
var configTemplate = template.Must(template.New("ircd.yaml").Parse(`network:
    name: ChatterboxTest
server:
    name: ergo.test
    listeners:
        "127.0.0.1:{{.Port}}":
            tls:
                cert: fullchain.pem
                key: privkey.pem
            min-tls-version: 1.2
    casemapping: "ascii"
    enforce-utf8: true
    lookup-hostnames: false
    forward-confirm-hostnames: false
    check-ident: false
    coerce-ident: '~u'
    idle-timeouts:
        registration: 60s
        ping: {{.Ping}}
        disconnect: {{.Disconnect}}
    relaymsg:
        enabled: false
    max-sendq: 96k
    compatibility:
        force-trailing: true
        send-unprefixed-sasl: true
        allow-truncation: false
    ip-limits:
        count: true
        max-concurrent-connections: 16
        throttle: true
        window: 10m
        max-connections-per-window: 32
        cidr-len-ipv4: 32
        cidr-len-ipv6: 64
        exempted:
            - "localhost"
    ip-cloaking:
        enabled: true
        enabled-for-always-on: true
        netname: "irc"
        cidr-len-ipv4: 32
        cidr-len-ipv6: 64
        num-bits: 64
    oper-throttle: 0s
accounts:
    authentication-enabled: true
    registration:
        enabled: true
        allow-before-connect: true
        throttling:
            enabled: true
            duration: 10m
            max-attempts: 30
        bcrypt-cost: 4
        verify-timeout: "32h"
    login-throttling:
        enabled: true
        duration: 1m
        max-attempts: 3
    skip-server-password: false
    login-via-pass-command: true
    advertise-scram: true
    require-sasl:
        enabled: false
    nick-reservation:
        enabled: true
        additional-nick-limit: 0
        method: strict
        allow-custom-enforcement: false
        guest-nickname-format: "Guest-*"
        force-guest-format: false
        force-nick-equals-account: true
        forbid-anonymous-nick-changes: false
    multiclient:
        enabled: true
        allowed-by-default: true
        always-on: "opt-in"
        auto-away: "opt-in"
    default-user-modes: +i
channels:
    default-modes: +ntC
    max-channels-per-client: 100
    operator-only-creation: false
    registration:
        enabled: true
        operator-only: false
        max-channels-per-account: 15
    list-delay: 0s
    invite-expiration: 24h
oper-classes:
    "chat-moderator":
        title: Chat Moderator
        capabilities:
            - "kill"
            - "ban"
            - "nofakelag"
            - "relaymsg"
            - "vhosts"
            - "sajoin"
            - "samode"
            - "snomasks"
            - "roleplay"
    "server-admin":
        title: Server Admin
        extends: "chat-moderator"
        capabilities:
            - "rehash"
            - "accreg"
            - "chanreg"
            - "history"
            - "defcon"
            - "massmessage"
            - "metadata"
opers:
    {{"admin"}}:
        class: "server-admin"
        hidden: true
        whois-line: is the server administrator
        password: "{{.OperHash}}"
logging:
    -
        method: stderr
        type: "* -userinput -useroutput"
        level: info
debug:
    recover-from-errors: true
lock-file: "ircd.lock"
datastore:
    path: ircd.db
    autoupgrade: true
languages:
    enabled: true
    default: en
    path: languages
limits:
    nicklen: 32
    identlen: 20
    realnamelen: 150
    channellen: 64
    awaylen: 390
    kicklen: 390
    topiclen: 390
    monitor-entries: 100
    whowas-entries: 100
    chan-list-modes: 100
    registration-messages: 1024
    multiline:
        max-bytes: {{.MaxBytes}}
        max-lines: 100
fakelag:
    enabled: true
    window: 1s
    burst-limit: 5
    messages-per-window: 2
    cooldown: 2s
    command-budgets:
        "CHATHISTORY": 16
        "MARKREAD":    16
        "MONITOR":     1
        "WHO":         4
        "WEBPUSH":     1
roleplay:
    enabled: false
history:
    enabled: {{.History}}
    channel-length: 2048
    client-length: 256
    autoresize-window: 3d
    autoreplay-on-join: 0
    chathistory-maxmessages: {{.PageSize}}
    znc-maxmessages: 2048
    restrictions:
        expire-time: 1w
        query-cutoff: 'none'
        grace-period: 1h
    persistent:
        enabled: false
        unregistered-channels: false
        registered-channels: "opt-out"
        direct-messages: "opt-out"
    retention:
        allow-individual-delete: false
        enable-account-indexing: false
    tagmsg-storage:
        default: false
        whitelist:
            - "+draft/react"
            - "+react"
allow-environment-overrides: false
metadata:
    enabled: true
    operator-only-modification: false
    max-subs: 100
    max-keys: 100
    client-throttle:
        enabled: true
        duration: 2m
        max-attempts: 10
`))

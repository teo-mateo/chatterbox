package tlspin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testCert struct {
	tls  tls.Certificate
	leaf *x509.Certificate
	pem  []byte
}

// makeCert creates a certificate for 127.0.0.1, self-signed or signed by ca.
func makeCert(t *testing.T, ca *testCert, isCA bool) *testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{"chatterbox test"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  isCA,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	parent, signer := tmpl, any(key)
	if ca != nil {
		parent, signer = ca.leaf, ca.tls.PrivateKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return &testCert{
		tls:  tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		leaf: leaf,
		pem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

// serve starts a TLS server presenting cert and returns its port.
func serve(t *testing.T, cert *testCert) int {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert.tls}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				c.(*tls.Conn).Handshake()
				buf := make([]byte, 1)
				c.Read(buf)
				c.Close()
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func connect(o Options) error {
	cfg, err := o.TLSConfig()
	if err != nil {
		return err
	}
	c, err := tls.Dial("tcp", o.hostport(), cfg)
	if err != nil {
		return err
	}
	return c.Close()
}

func opts(t *testing.T, port int) (Options, *[]string) {
	var notices []string
	return Options{
		Host: "127.0.0.1", Port: port, TrustOnFirstUse: true,
		StorePath: filepath.Join(t.TempDir(), "chatterbox", "known_servers"),
		Notice:    func(s string) { notices = append(notices, s) },
	}, &notices
}

func wantTrustError(t *testing.T, err error, contains ...string) {
	t.Helper()
	var te *TrustError
	if !errors.As(err, &te) {
		t.Fatalf("got %v, want a TrustError", err)
	}
	for _, c := range contains {
		if !strings.Contains(te.Msg, c) {
			t.Errorf("error text lacks %q:\n%s", c, te.Msg)
		}
	}
}

func TestFingerprintMatchesOpenSSL(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not installed")
	}
	cert := makeCert(t, nil, false)
	p := filepath.Join(t.TempDir(), "c.pem")
	os.WriteFile(p, cert.pem, 0o600)
	out, err := exec.Command("openssl", "x509", "-in", p, "-noout", "-fingerprint", "-sha256").Output()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(out)) // sha256 Fingerprint=AA:BB:...
	if want := line[strings.Index(line, "=")+1:]; Fingerprint(cert.leaf) != want {
		t.Errorf("Fingerprint = %s, openssl says %s", Fingerprint(cert.leaf), want)
	}
	if got, err := Normalize(line); err != nil || got != Fingerprint(cert.leaf) {
		t.Errorf("Normalize(openssl output) = %q, %v", got, err)
	}
}

func TestNormalizeSpellings(t *testing.T) {
	cert := makeCert(t, nil, false)
	canon := Fingerprint(cert.leaf)
	plain := strings.ReplaceAll(canon, ":", "")
	for _, s := range []string{canon, strings.ToLower(canon), plain, strings.ToLower(plain),
		"SHA256:" + canon, "sha256:" + strings.ToLower(plain), "  " + canon + "\n"} {
		got, err := Normalize(s)
		if err != nil || got != canon {
			t.Errorf("Normalize(%q) = %q, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "AA:BB", "zz", plain + "00", "SHA1:" + plain} {
		if _, err := Normalize(s); err == nil {
			t.Errorf("Normalize(%q) accepted", s)
		}
	}
}

func TestFirstUsePinsThenRefusesChange(t *testing.T) {
	cert := makeCert(t, nil, false)
	port := serve(t, cert)
	o, notices := opts(t, port)

	if err := connect(o); err != nil {
		t.Fatalf("first connection: %v", err)
	}
	entry, found, _ := Store{Path: o.StorePath}.Lookup(o.hostport())
	if !found || entry != Fingerprint(cert.leaf) {
		t.Fatalf("store entry = %q, %v", entry, found)
	}
	if !strings.Contains(strings.Join(*notices, "\n"), Fingerprint(cert.leaf)) {
		t.Errorf("fingerprint not printed on first use: %v", *notices)
	}
	if fi, _ := os.Stat(o.StorePath); fi.Mode().Perm() != 0o600 {
		t.Errorf("trust file mode %v", fi.Mode().Perm())
	}

	*notices = nil
	if err := connect(o); err != nil {
		t.Fatalf("second connection with the pinned certificate: %v", err)
	}
	if len(*notices) != 0 {
		t.Errorf("notice on a matching pin: %v", *notices)
	}

	// Same host:port, different certificate.
	other := makeCert(t, nil, false)
	o2 := o
	o2.Port = serve(t, other)
	Store{Path: o.StorePath}.Record(o2.hostport(), Fingerprint(cert.leaf))
	err := connect(o2)
	wantTrustError(t, err, "has changed", "trusted:   SHA256:"+Fingerprint(cert.leaf),
		"presented: SHA256:"+Fingerprint(other.leaf), "remove the", o.StorePath, "intercepting")
}

func TestValidChainRecordsCAAndRefusesDowngrade(t *testing.T) {
	ca := makeCert(t, nil, true)
	leaf := makeCert(t, ca, false)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(caFile, ca.pem, 0o600)

	o, notices := opts(t, serve(t, leaf))
	o.CAFile = caFile
	if err := connect(o); err != nil {
		t.Fatalf("valid chain: %v", err)
	}
	if entry, _, _ := (Store{Path: o.StorePath}).Lookup(o.hostport()); entry != EntryCA {
		t.Fatalf("store entry = %q, want ca", entry)
	}
	if len(*notices) != 0 {
		t.Errorf("notice for a valid chain: %v", *notices)
	}

	// The same server later presents a self-signed certificate.
	o2 := o
	o2.Port = serve(t, makeCert(t, nil, false))
	Store{Path: o.StorePath}.Record(o2.hostport(), EntryCA)
	err := connect(o2)
	wantTrustError(t, err, "no longer verifies", "refused")
	if entry, _, _ := (Store{Path: o.StorePath}).Lookup(o2.hostport()); entry != EntryCA {
		t.Errorf("a ca entry was downgraded to %q", entry)
	}
}

func TestConfiguredFingerprint(t *testing.T) {
	cert := makeCert(t, nil, false)
	o, _ := opts(t, serve(t, cert))
	o.Fingerprint = strings.ToLower(strings.ReplaceAll(Fingerprint(cert.leaf), ":", ""))
	if err := connect(o); err != nil {
		t.Fatalf("matching configured fingerprint: %v", err)
	}
	if _, err := os.Stat(o.StorePath); !os.IsNotExist(err) {
		t.Error("trust file written although tls.fingerprint is set")
	}
	o.Fingerprint = Fingerprint(makeCert(t, nil, false).leaf)
	wantTrustError(t, connect(o), "does not match tls.fingerprint")

	o.Fingerprint = "nonsense"
	if _, err := o.TLSConfig(); err == nil {
		t.Error("malformed tls.fingerprint accepted")
	}
}

func TestNoTrustOnFirstUse(t *testing.T) {
	cert := makeCert(t, nil, false)
	o, _ := opts(t, serve(t, cert))
	o.TrustOnFirstUse = false
	wantTrustError(t, connect(o), "trust_on_first_use is off", Fingerprint(cert.leaf), "fingerprint = ")
	if _, found, _ := (Store{Path: o.StorePath}).Lookup(o.hostport()); found {
		t.Error("certificate pinned although trust_on_first_use is off")
	}
}

func TestStoreConcurrentWriters(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "known_servers")}
	fp := Fingerprint(makeCert(t, nil, false).leaf)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Record(net.JoinHostPort("host", string(rune('a'+i%20))), fp); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	entries, _, err := s.read()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 {
		t.Errorf("%d entries after concurrent writes, want 20", len(entries))
	}
}

func TestStoreNeverReplaces(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "known_servers")}
	a := Fingerprint(makeCert(t, nil, false).leaf)
	b := Fingerprint(makeCert(t, nil, false).leaf)
	s.Record("h:1", a)
	s.Record("h:1", b)
	if got, _, _ := s.Lookup("h:1"); got != a {
		t.Errorf("existing entry replaced: %s", got)
	}
	os.WriteFile(s.Path, []byte("garbage line here\n"), 0o600)
	if _, _, err := s.Lookup("h:1"); err == nil {
		t.Error("malformed trust file accepted")
	}
}

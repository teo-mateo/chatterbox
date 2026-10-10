// Package tlspin verifies the server certificate: against the system roots
// when that works, otherwise by pinning the certificate's fingerprint on the
// first connection and refusing any other certificate afterwards.
package tlspin

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// TrustError means the server certificate was refused. It is always fatal.
type TrustError struct{ Msg string }

func (e *TrustError) Error() string { return e.Msg }

// Fingerprint is the SHA-256 of the certificate in DER form, as upper-case
// hex with colons: the value `openssl x509 -fingerprint -sha256` prints.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return formatFingerprint(sum[:])
}

func formatFingerprint(sum []byte) string {
	h := strings.ToUpper(hex.EncodeToString(sum))
	var b strings.Builder
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i : i+2])
	}
	return b.String()
}

// Normalize accepts a fingerprint in any of the usual spellings (with or
// without colons, either case, optional "SHA256:" or "sha256 Fingerprint="
// prefix) and returns the canonical form.
func Normalize(s string) (string, error) {
	orig := s
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "="); i >= 0 {
		s = s[i+1:]
	}
	if len(s) >= 7 && strings.EqualFold(s[:7], "sha256:") {
		s = s[7:]
	}
	s = strings.NewReplacer(":", "", " ", "").Replace(s)
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != sha256.Size {
		return "", fmt.Errorf("%q is not a SHA-256 certificate fingerprint (expected 64 hex digits)", orig)
	}
	return formatFingerprint(b), nil
}

// Options describe one server and how its certificate is to be trusted.
type Options struct {
	Host string
	Port int
	// Fingerprint, when set, is the only certificate accepted; the trust
	// file is neither read nor written.
	Fingerprint string
	// CAFile is an extra CA bundle added to the system roots.
	CAFile string
	// TrustOnFirstUse allows pinning a certificate that does not verify
	// when the server has no entry yet.
	TrustOnFirstUse bool
	// StorePath is the trust file.
	StorePath string
	// Notice receives one-line messages for the user (stderr).
	Notice func(string)
}

func (o Options) hostport() string { return net.JoinHostPort(o.Host, strconv.Itoa(o.Port)) }

// TLSConfig returns a configuration whose VerifyConnection hook applies the
// trust rules. The hook also runs on resumed sessions.
func (o Options) TLSConfig() (*tls.Config, error) {
	want := ""
	if o.Fingerprint != "" {
		fp, err := Normalize(o.Fingerprint)
		if err != nil {
			return nil, fmt.Errorf("tls.fingerprint: %v", err)
		}
		want = fp
	}
	roots, err := o.roots()
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Verification happens in VerifyConnection.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			return o.verify(cs, want, roots)
		},
	}
	if net.ParseIP(o.Host) == nil {
		cfg.ServerName = o.Host
	}
	return cfg, nil
}

func (o Options) roots() (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if o.CAFile != "" {
		pem, err := os.ReadFile(o.CAFile)
		if err != nil {
			return nil, fmt.Errorf("tls.ca_file: %v", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls.ca_file: no certificates found in %s", o.CAFile)
		}
	}
	return pool, nil
}

func (o Options) notice(format string, args ...any) {
	if o.Notice != nil {
		o.Notice(fmt.Sprintf(format, args...))
	}
}

func (o Options) chainValid(cs tls.ConnectionState, roots *x509.CertPool) error {
	inter := x509.NewCertPool()
	for _, c := range cs.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
		DNSName:       o.Host,
		Roots:         roots,
		Intermediates: inter,
	})
	return err
}

func (o Options) verify(cs tls.ConnectionState, configured string, roots *x509.CertPool) error {
	if len(cs.PeerCertificates) == 0 {
		return &TrustError{Msg: fmt.Sprintf("%s presented no certificate", o.hostport())}
	}
	hp := o.hostport()
	presented := Fingerprint(cs.PeerCertificates[0])

	// 1. A configured fingerprint is the only thing accepted.
	if configured != "" {
		if presented != configured {
			return &TrustError{Msg: fmt.Sprintf(
				"the certificate of %s does not match tls.fingerprint.\n  configured: SHA256:%s\n  presented:  SHA256:%s",
				hp, configured, presented)}
		}
		return nil
	}

	store := Store{Path: o.StorePath}
	entry, found, err := store.Lookup(hp)
	if err != nil {
		return &TrustError{Msg: fmt.Sprintf("cannot read the trust file: %v", err)}
	}

	if found {
		// 3. The server once had a valid certificate: never downgrade to a pin.
		if entry == EntryCA {
			if err := o.chainValid(cs, roots); err != nil {
				return &TrustError{Msg: fmt.Sprintf(
					"the certificate of %s no longer verifies: %v.\n"+
						"This server presented a valid certificate before, so an unverifiable one is refused.\n"+
						"If the server's certificate was replaced on purpose, remove the %s\nline from %s and connect again.\n"+
						"Otherwise someone may be intercepting the connection.",
					hp, err, hp, o.StorePath)}
			}
			return nil
		}
		// 2. Pinned.
		if presented != entry {
			return &TrustError{Msg: fmt.Sprintf(
				"the certificate of %s has changed.\n  trusted:   SHA256:%s\n  presented: SHA256:%s\n"+
					"If the server's certificate was replaced on purpose, remove the %s\nline from %s and connect again.\n"+
					"Otherwise someone may be intercepting the connection.",
				hp, entry, presented, hp, o.StorePath)}
		}
		return nil
	}

	// 4. First connection.
	verr := o.chainValid(cs, roots)
	if verr == nil {
		if err := store.Record(hp, EntryCA); err != nil {
			return &TrustError{Msg: fmt.Sprintf("cannot write the trust file: %v", err)}
		}
		return nil
	}
	if !o.TrustOnFirstUse {
		return &TrustError{Msg: fmt.Sprintf(
			"the certificate of %s cannot be verified (%v) and tls.trust_on_first_use is off.\n"+
				"  presented: SHA256:%s\n"+
				"To accept exactly this certificate, set in the config file:\n\n[tls]\nfingerprint = \"%s\"\n",
			hp, verr, presented, presented)}
	}
	if err := store.Record(hp, presented); err != nil {
		return &TrustError{Msg: fmt.Sprintf("cannot write the trust file: %v", err)}
	}
	// Another process may have recorded a different entry first.
	if cur, ok, err := store.Lookup(hp); err != nil || !ok || cur != presented {
		return &TrustError{Msg: fmt.Sprintf(
			"the trust entry for %s was changed by another process while connecting; connect again", hp)}
	}
	o.notice("the certificate of %s cannot be verified (%v); trusting it from now on (first use).", hp, verr)
	o.notice("  fingerprint: SHA256:%s", presented)
	o.notice("  compare it with the server's own; recorded in %s", o.StorePath)
	return nil
}

package e2e

// STARTTLS (RFC 4642) with nnrpd.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"slices"
	"testing"
	"time"

	testkit "github.com/go-while/nntp-testkit"
	"github.com/go-while/nntp-testkit/client"
)

// selfSigned returns a self-signed certificate for 127.0.0.1 and its key, both PEM, and a
// pool that trusts the certificate.
func selfSigned(tb testing.TB) (certPEM, keyPEM []byte, pool *x509.CertPool) {
	tb.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		tb.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		tb.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		tb.Fatal(err)
	}
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), pool
}

// startTLS sends STARTTLS, expects 382 with nothing after it, and returns a strict client
// over the negotiated TLS connection, which verifies the site's certificate.
func startTLS(tb testing.TB, s *Site, c *client.Conn) (*client.Conn, *tls.Conn) {
	tb.Helper()
	expect(tb, c, 382, "STARTTLS")
	if n := c.R.Buffered(); n > 0 {
		tb.Fatalf("%d bytes after the 382 reply: the TLS negotiation must start at once", n)
	}
	tc := tls.Client(c.Conn, &tls.Config{RootCAs: s.CertPool, ServerName: "127.0.0.1",
		MinVersion: tls.VersionTLS12})
	_ = tc.SetDeadline(time.Now().Add(testkit.Deadline()))
	if err := tc.Handshake(); err != nil {
		tb.Fatalf("TLS handshake after STARTTLS: %v", err)
	}
	_ = tc.SetDeadline(time.Time{})
	return client.New(tb, tc), tc
}

// capabilities returns the CAPABILITIES lines.
func capabilities(tb testing.TB, c *client.Conn) []string {
	tb.Helper()
	expect(tb, c, 101, "CAPABILITIES")
	return c.ReadDotLines()
}

// TestStartTLS: nnrpd advertises STARTTLS, answers 382, negotiates TLS with the site's
// certificate, no longer advertises STARTTLS, serves the reader over TLS and refuses a second
// STARTTLS with 502 (RFC 4642).
func TestStartTLS(t *testing.T) {
	s := NewSite(t, SiteOptions{TLS: true})
	c := s.Reader(t)
	if !slices.Contains(capabilities(t, c), "STARTTLS") {
		t.Fatalf("CAPABILITIES does not list STARTTLS")
	}
	tc, conn := startTLS(t, s, c)
	if v := conn.ConnectionState().Version; v < tls.VersionTLS12 {
		t.Errorf("TLS version %x, want 1.2 or later", v)
	}
	if slices.Contains(capabilities(t, tc), "STARTTLS") {
		t.Errorf("CAPABILITIES lists STARTTLS after TLS was negotiated (RFC 4642 section 2.2.2)")
	}
	expect(t, tc, 211, "GROUP e2e.test")
	id := msgID(t)
	expect(t, tc, 340, "POST")
	send(t, tc, wire(newArticle(id, "e2e.test", "over tls", "secret body")))
	status(t, tc, 240, "POST over TLS")
	expect(t, tc, 223, "STAT %s", id)
	expect(t, tc, 502, "STARTTLS")
	expect(t, tc, 205, "QUIT")
}

// TestAuthinfoRequiresTLS: with require_encryption in readers.conf, AUTHINFO gets 483 until
// the client has used STARTTLS, and works afterwards.
func TestAuthinfoRequiresTLS(t *testing.T) {
	s := NewSite(t, SiteOptions{
		TLS: true,
		Readers: `auth "tls-users" {
    hosts: "127.0.0.1"
    require_encryption: true
    auth: "ckpasswd -f @etcdir@/passwd.e2e"
}
access "users" {
    users: "e2e"
    newsgroups: "*"
    access: RPA
}
`,
		Etc: map[string]string{"passwd.e2e": authPasswd},
	})
	c := s.Reader(t)
	expect(t, c, 483, "AUTHINFO USER e2e")
	expect(t, c, 480, "GROUP e2e.test")

	tc, _ := startTLS(t, s, c)
	expect(t, tc, 381, "AUTHINFO USER e2e")
	expect(t, tc, 281, "AUTHINFO PASS e2e-secret")
	expect(t, tc, 211, "GROUP e2e.test")
	expect(t, tc, 205, "QUIT")
}

// TestStartTLSWithoutCertificate: without a certificate, nnrpd still advertises STARTTLS, but
// answers it with 580 (RFC 4642: TLS cannot be initiated) and goes on without TLS.
func TestStartTLSWithoutCertificate(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	c := s.Reader(t)
	if !slices.Contains(capabilities(t, c), "STARTTLS") {
		t.Errorf("CAPABILITIES does not list STARTTLS")
	}
	expect(t, c, 580, "STARTTLS")
	expect(t, c, 211, "GROUP e2e.test")
	if err := c.ExpectNoPending(200 * time.Millisecond); err != nil {
		t.Error(err)
	}
}

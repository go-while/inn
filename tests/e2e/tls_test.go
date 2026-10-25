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
	"regexp"
	"slices"
	"strconv"
	"strings"
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

// tlsSession negotiates TLS with STARTTLS, reads a group and quits, then returns the syslog
// messages nnrpd logged for this session, once it has logged its exit statistics. It skips
// the test when no nnrpd message is readable.
func tlsSession(tb testing.TB, s *Site) []string {
	tb.Helper()
	return tlsSessionPause(tb, s, 0, " times user ")
}

// tlsSessionPause is tlsSession, waiting for pause between the TLS negotiation and the GROUP
// command, and for a session message containing end before it returns.
func tlsSessionPause(tb testing.TB, s *Site, pause time.Duration, end string) []string {
	tb.Helper()
	cursor := s.JournalCursor(tb)
	tc, _ := startTLS(tb, s, s.Reader(tb))
	time.Sleep(pause)
	expect(tb, tc, 211, "GROUP e2e.test")
	expect(tb, tc, 205, "QUIT")

	var session []string
	limit := time.Now().Add(testkit.Deadline())
	for {
		lines := s.ProgramSyslog(tb, "nnrpd", cursor)
		session = nil
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.HasPrefix(lines[i], "starttls: ") {
				session = lines[i:]
				break
			}
		}
		if slices.ContainsFunc(session, func(l string) bool { return strings.Contains(l, end) }) {
			return lines
		}
		if time.Now().After(limit) {
			if len(lines) == 0 {
				tb.Skip("no syslog messages of nnrpd readable: is the user in the systemd-journal group?")
			}
			tb.Fatalf("nnrpd did not log the end of the TLS session:\n%s", strings.Join(lines, "\n"))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestStartTLSLogLevel: the tlsloglevel parameter in inn.conf makes nnrpd log the setup of a
// TLS connection (issue #272; the level was an internal variable set to 0).
func TestStartTLSLogLevel(t *testing.T) {
	s := NewSite(t, SiteOptions{TLS: true, InnConf: map[string]string{"tlsloglevel": "1"}})
	lines := tlsSession(t, s)
	if !slices.Contains(lines, "setting up TLS connection") {
		t.Errorf("tlsloglevel 1: nnrpd did not log \"setting up TLS connection\":\n%s", strings.Join(lines, "\n"))
	}
}

// idleTimer matches the idle timer in the timer statistics nnrpd logs at exit: the total in
// milliseconds and the number of waits.
var idleTimer = regexp.MustCompile(` time \d+ .*\bidle (\d+)\((\d+)\)`)

// TestStartTLSIdleTimer: with the timer parameter in inn.conf, nnrpd counts the time it waits
// for a TLS reader in its idle timer (issue #90; it skipped the timed wait for TLS readers).
func TestStartTLSIdleTimer(t *testing.T) {
	const pause = 500 * time.Millisecond
	s := NewSite(t, SiteOptions{TLS: true, InnConf: map[string]string{"timer": "3600"}})
	// The timer statistics are the last message of a session.
	lines := tlsSessionPause(t, s, pause, " idle ")
	for _, line := range lines {
		m := idleTimer.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		total, _ := strconv.Atoi(m[1])
		t.Logf("timer statistics: %q", line)
		if want := int(pause/time.Millisecond) * 4 / 5; total < want {
			t.Errorf("idle timer %d ms after a pause of %v over TLS, want at least %d ms", total, pause, want)
		}
		return
	}
	t.Fatalf("nnrpd logged no idle timer statistics:\n%s", strings.Join(lines, "\n"))
}

// dumpRow matches the offset that starts a line of nnrpd's hexadecimal dump of TLS data. The
// offset is followed by 16 columns of "xx " (with "|" after the 8th byte, and spaces for
// missing bytes), a space and the bytes as characters ("." for the unprintable ones, a space
// after the 8th one).
var dumpRow = regexp.MustCompile(`^[0-9a-f]{4} `)

// TestStartTLSDump: with tlsloglevel 3, nnrpd logs a hexadecimal dump of the data received
// during the TLS handshake. Every line must end with the dumped bytes as characters, aligned
// after the 16 hexadecimal columns (issue #350).
func TestStartTLSDump(t *testing.T) {
	s := NewSite(t, SiteOptions{TLS: true, InnConf: map[string]string{"tlsloglevel": "3"}})
	var rows int
	for _, line := range tlsSession(t, s) {
		if !dumpRow.MatchString(line) {
			continue
		}
		if rows++; rows == 1 {
			t.Logf("first dump line: %q", line)
		}
		// The journal drops trailing spaces: restore the hexadecimal columns.
		cols := line + strings.Repeat(" ", max(0, 5+16*3-len(line)))
		var b []byte
		for k := range 16 {
			cell := cols[5+3*k : 5+3*k+2]
			if cell == "  " {
				break
			}
			v, err := strconv.ParseUint(cell, 16, 8)
			if err != nil {
				t.Fatalf("dump line %q: column %d is %q", line, k, cell)
			}
			b = append(b, byte(v))
		}
		var want strings.Builder
		want.WriteByte(' ')
		for k, c := range b {
			if c >= ' ' && c <= '~' {
				want.WriteByte(c)
			} else {
				want.WriteByte('.')
			}
			if k == 7 {
				want.WriteByte(' ')
			}
		}
		if got := strings.TrimRight(cols[5+16*3:], " "); got != strings.TrimRight(want.String(), " ") {
			t.Errorf("dump line %q: the character column after the 16 hexadecimal columns is %q, want %q",
				line, got, want.String())
		}
	}
	if rows == 0 {
		t.Errorf("tlsloglevel 3: nnrpd logged no hexadecimal dump of the handshake")
	}
	t.Logf("%d dump lines checked", rows)
}

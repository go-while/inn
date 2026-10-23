package e2e

// AUTHINFO USER/PASS (RFC 4643) with nnrpd and ckpasswd.

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-while/nntp-testkit/client"
)

// authPasswd holds user e2e with the password "e2e-secret" (SHA-256 crypt).
const authPasswd = "e2e:$5$e2esalt12345678$vZ.3HTurAgk67ijZcOE/yNxubGXXFUCQAqRCMRPo2i7\n"

// authReaders lets 127.0.0.1 in only as user e2e, checked by ckpasswd.
const authReaders = `auth "users" {
    hosts: "127.0.0.1"
    auth: "ckpasswd -f @etcdir@/passwd.e2e"
}
access "users" {
    users: "e2e"
    newsgroups: "*"
    access: RPA
}
`

// authinfoAdvertised reports whether CAPABILITIES lists AUTHINFO with the USER argument.
func authinfoAdvertised(tb testing.TB, c *client.Conn) (authinfo, user bool) {
	tb.Helper()
	expect(tb, c, 101, "CAPABILITIES")
	for _, l := range c.ReadDotLines() {
		f := strings.Fields(l)
		if len(f) > 0 && f[0] == "AUTHINFO" {
			authinfo = true
			user = user || slices.Contains(f[1:], "USER")
		}
	}
	return authinfo, user
}

// TestAuthinfo: commands need authentication first (480); a wrong password gets 481, the
// right one 281; then the reader has access, AUTHINFO is no longer advertised, and another
// AUTHINFO gets 502.
func TestAuthinfo(t *testing.T) {
	s := NewSite(t, SiteOptions{
		Readers: authReaders,
		Etc:     map[string]string{"passwd.e2e": authPasswd},
	})
	c := s.Reader(t)
	if code, err := client.ParseCode(c.Greeting); err != nil || (code != 200 && code != 201) {
		t.Fatalf("greeting %q", c.Greeting)
	}
	if _, user := authinfoAdvertised(t, c); !user {
		t.Errorf("CAPABILITIES before authentication lacks AUTHINFO USER")
	}
	expect(t, c, 480, "GROUP e2e.test")
	expect(t, c, 381, "AUTHINFO USER e2e")
	expect(t, c, 481, "AUTHINFO PASS wrong-password")

	expect(t, c, 381, "AUTHINFO USER e2e")
	expect(t, c, 281, "AUTHINFO PASS e2e-secret")
	expect(t, c, 211, "GROUP e2e.test")
	if authinfo, _ := authinfoAdvertised(t, c); authinfo {
		t.Errorf("CAPABILITIES after authentication still lists AUTHINFO (RFC 4643 section 2.2)")
	}
	expect(t, c, 502, "AUTHINFO USER e2e")
	expect(t, c, 205, "QUIT")
}

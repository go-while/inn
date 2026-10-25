package e2e

// AUTHINFO USER/PASS (RFC 4643) with nnrpd and ckpasswd.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

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

// slowAuth is an authenticator that reads its input, sleeps for the number of seconds given
// as its argument and then accepts the user as e2e, whatever the password.
const slowAuth = `cat >/dev/null
sleep "$1"
echo "User:e2e"
`

// slowAuthReaders runs slowAuth sleeping delay seconds for 127.0.0.1, with the given extra
// auth group lines.
func slowAuthReaders(delay int, extra string) string {
	return fmt.Sprintf(`auth "slow" {
    hosts: "127.0.0.1"
    auth: "/bin/sh @etcdir@/slowauth.sh %d"
%s}
access "slow" {
    users: "e2e"
    newsgroups: "*"
    access: RPA
}
`, delay, extra)
}

// TestAuthProgramTimeout: nnrpd kills an authenticator that writes nothing for the
// program_timeout of its auth group, 5 seconds by default, and the authentication fails
// (issue #131; the timeout was hard-coded).
func TestAuthProgramTimeout(t *testing.T) {
	s := NewSite(t, SiteOptions{
		Readers: slowAuthReaders(3, "    program_timeout: 1\n"),
		Etc:     map[string]string{"slowauth.sh": slowAuth},
	})
	for _, tc := range []struct {
		name    string
		readers string
		code    int    // reply to AUTHINFO PASS
		log     string // nnrpd message expected for the authenticator, if any
	}{
		{"timeout 1, sleep 3", slowAuthReaders(3, "    program_timeout: 1\n"), 481,
			"auth: program timeout (no output for 1 s)"},
		{"default timeout, sleep 6", slowAuthReaders(6, ""), 481,
			"auth: program timeout (no output for 5 s)"},
		{"timeout 10, sleep 6", slowAuthReaders(6, "    program_timeout: 10\n"), 281, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.WriteEtc(t, "readers.conf", s.expand(tc.readers))
			cursor := s.JournalCursor(t)
			c := s.Reader(t)
			if code, err := client.ParseCode(c.Greeting); err != nil || (code != 200 && code != 201) {
				t.Fatalf("greeting %q", c.Greeting)
			}
			expect(t, c, 381, "AUTHINFO USER e2e")
			start := time.Now()
			expect(t, c, tc.code, "AUTHINFO PASS anything")
			t.Logf("AUTHINFO PASS answered after %v", time.Since(start).Round(100*time.Millisecond))
			expect(t, c, 205, "QUIT")
			if tc.log == "" {
				return
			}
			limit := time.Now().Add(5 * time.Second)
			for {
				lines := s.ProgramSyslog(t, "nnrpd", cursor)
				if slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, tc.log) }) {
					return
				}
				if time.Now().After(limit) {
					if len(lines) == 0 {
						t.Skip("no syslog messages of nnrpd readable: is the user in the systemd-journal group?")
					}
					t.Fatalf("nnrpd did not log %q:\n%s", tc.log, strings.Join(lines, "\n"))
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}

	// A program_timeout that is not a positive number is a syntax error: nnrpd refuses
	// the connection.
	s.WriteEtc(t, "readers.conf", s.expand(slowAuthReaders(1, "    program_timeout: 0\n")))
	c := s.Reader(t)
	if code, err := client.ParseCode(c.Greeting); err != nil || code != 400 {
		t.Errorf("greeting with program_timeout 0: %q, want 400", c.Greeting)
	}
}

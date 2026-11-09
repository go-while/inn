package e2e

// Perl and Python authentication hooks of nnrpd with very long results (#210).

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// The hooks return an error string of 20000 characters (2.4 times BIG_BUFFER) with code 481,
// or code 281 with a user name of 20000 characters.  Both are longer than the buffers nnrpd
// gives the hooks and are truncated.
const (
	longPerlAuth = `sub authenticate {
    my $user = $attributes{username};
    return (281, "", "u" x 20000) if $user eq "longuser";
    return (481, "E" x 20000);
}
1;
`
	longPythonAuth = `from nnrpd import *


class AUTH:
    def authenticate(self, attributes):
        if attributes["user"].tobytes() == b"longuser":
            return (281, "", "u" * 20000)
        return (481, "E" * 20000)


set_auth_hook(AUTH())
`
)

// hookReaders lets 127.0.0.1 in with any user name when the hook accepts it.
func hookReaders(param, script string) string {
	return `auth "hook" {
    hosts: "127.0.0.1"
    ` + param + `: "` + script + `"
}
access "hook" {
    users: "*"
    newsgroups: "*"
    access: RPA
}
`
}

// binaryContains reports whether the installed program holds the string, which tells whether
// it was built with the language (the message is only compiled in with Perl or Python).
func binaryContains(t *testing.T, program, text string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(prefix(t), "bin", program))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Contains(b, []byte(text))
}

// readUntilQuiet reads what the server sends until it is silent for quiet, at most limit
// bytes.  It fails the test when the server closes the connection: nnrpd died.
func readUntilQuiet(t *testing.T, c *client.Conn, quiet time.Duration, limit int64) []byte {
	t.Helper()
	var got bytes.Buffer
	buf := make([]byte, 4096)
	for int64(got.Len()) < limit {
		_ = c.Conn.SetReadDeadline(time.Now().Add(quiet))
		n, err := c.R.Read(buf)
		got.Write(buf[:n])
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			break
		}
		if err != nil {
			t.Fatalf("connection lost after %d bytes (nnrpd crashed?): %v", got.Len(), err)
		}
	}
	return got.Bytes()
}

// TestAuthHookLongResults: a Perl or Python authentication hook that returns a 20000 character
// error string or user name does not crash nnrpd.  The error string is cut to what nnrpd can
// send in one reply and the session of a long user name goes on (the hooks truncate to the
// buffer sizes the caller passes; issue #210).
func TestAuthHookLongResults(t *testing.T) {
	for _, tc := range []struct {
		name, program, marker, param, file, script string
	}{
		{"perl", "nnrpd", "Perl function authenticate not defined", "perl_auth", "longauth.pl", longPerlAuth},
		{"python", "nnrpd", "python authenticate method succeeded", "python_auth", "longauth.py", longPythonAuth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !binaryContains(t, tc.program, tc.marker) {
				t.Skipf("nnrpd was built without %s", tc.name)
			}
			script := tc.file
			if tc.name == "python" {
				script = strings.TrimSuffix(tc.file, ".py") // a module name in pathfilter
			}
			s := NewSite(t, SiteOptions{
				InnConf: map[string]string{"pathfilter": "@etcdir@"},
				Etc:     map[string]string{tc.file: tc.script},
			})
			readers := hookReaders(tc.param, "@etcdir@/"+tc.file)
			if tc.name == "python" {
				readers = hookReaders(tc.param, script)
			}
			s.WriteEtc(t, "readers.conf", s.expand(readers))

			// A long user name is accepted; the session works.
			c := s.Reader(t)
			expect(t, c, 381, "AUTHINFO USER longuser")
			expect(t, c, 281, "AUTHINFO PASS secret")
			expect(t, c, 211, "GROUP e2e.test")
			expect(t, c, 205, "QUIT")

			// A long error string is truncated; nnrpd stays up and sends the start of it as
			// one line that still ends with CRLF (its reply buffer holds 2047 bytes).
			c = s.Reader(t)
			expect(t, c, 381, "AUTHINFO USER shortuser")
			send(t, c, "AUTHINFO PASS secret\r\n")
			got := readUntilQuiet(t, c, 2*time.Second, 64<<10)
			t.Logf("reply to AUTHINFO PASS: %d bytes", len(got))
			reply := string(got)
			if !strings.HasPrefix(reply, "481 EEEEEEEE") {
				t.Fatalf("reply to AUTHINFO PASS starts with %.40q, want 481 and the error string", reply)
			}
			if n := len(strings.TrimRight(reply, "E\r\n")); n != len("481 ") {
				t.Errorf("reply holds characters other than the error string (%d before the tail)", n)
			}
			if len(reply) > 8192 {
				t.Errorf("reply of %d bytes, longer than the error string buffer", len(reply))
			}
			if !strings.HasSuffix(reply, "\r\n") || strings.Count(reply, "\n") != 1 {
				t.Errorf("reply of %d bytes is not one line ending with CRLF (ends with %q)", len(reply), reply[max(0, len(reply)-8):])
			}
			expect(t, c, 205, "QUIT")
		})
	}
}

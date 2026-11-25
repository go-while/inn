package e2e

// Checks for upstream PRs #412 (readers.conf layouts in nnrpd) and #413 (innd's Python
// filter return values and INN.hashstring).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// tryReader dials the site as a reader without failing the test when nnrpd drops the
// connection: the error says what went wrong.
func tryReader(t *testing.T, s *Site) (*client.Conn, error) {
	t.Helper()
	c, err := client.TryDial(s.Addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	t.Cleanup(c.Close)
	return c, nil
}

// readGroup checks the greeting and that GROUP e2e.test gets 211.
func readGroup(t *testing.T, s *Site) {
	t.Helper()
	c, err := tryReader(t, s)
	if err != nil {
		t.Fatalf("reader connection: %v", err)
	}
	if code, _ := client.ParseCode(c.Greeting); code != 200 && code != 201 {
		t.Fatalf("greeting %q", c.Greeting)
	}
	code, line, err := c.TryCmd("GROUP e2e.test")
	if err != nil || code != 211 {
		t.Fatalf("GROUP e2e.test: %q, %v; want 211", line, err)
	}
	_, _, _ = c.TryCmd("QUIT")
}

// TestKevReadersNestedShorthand (#412): an auth: parameter given directly in a group block
// (an unnamed method) is copied into the auth groups nested in it; nnrpd must still greet.
func TestKevReadersNestedShorthand(t *testing.T) {
	s := NewSite(t, SiteOptions{
		Readers: `group "shared" {
    auth: "ckpasswd -f @etcdir@/passwd.e2e"
    auth "local" {
        hosts: "127.0.0.1, ::1, localhost"
        default: "<local>"
    }
}
access "local" {
    users: "<local>"
    newsgroups: "*"
    access: RPA
}
`,
		Etc: map[string]string{"passwd.e2e": authPasswd},
	})
	readGroup(t, s)
}

// TestKevReadersPerlAccessOnly (#412): an auth group whose access comes from perl_access,
// with no access group in readers.conf.
func TestKevReadersPerlAccessOnly(t *testing.T) {
	s := NewSite(t, SiteOptions{
		Readers: `auth "local" {
    hosts: "127.0.0.1, ::1, localhost"
    default: "<local>"
    perl_access: "@etcdir@/access.pl"
}
`,
		Etc: map[string]string{"access.pl": `sub access {
    my %r = ("read" => "*", "post" => "*");
    return %r;
}
1;
`},
	})
	readGroup(t, s)
}

// TestKevAuthinfoGenericKeepsAccess (#412): a failed AUTHINFO GENERIC must not take away the
// read access the client already has by host.
func TestKevAuthinfoGenericKeepsAccess(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	c, err := tryReader(t, s)
	if err != nil {
		t.Fatalf("reader connection: %v", err)
	}
	if code, line, err := c.TryCmd("GROUP e2e.test"); err != nil || code != 211 {
		t.Fatalf("GROUP before AUTHINFO: %q, %v", line, err)
	}
	code, line, err := c.TryCmd("AUTHINFO GENERIC no-such-authenticator")
	t.Logf("AUTHINFO GENERIC no-such-authenticator: %d %q %v", code, line, err)
	if err != nil {
		t.Fatalf("AUTHINFO GENERIC: %v", err)
	}
	if code/100 == 2 {
		t.Fatalf("AUTHINFO GENERIC succeeded: %q", line)
	}
	code, line, err = c.TryCmd("GROUP e2e.test")
	if err != nil || code != 211 {
		t.Errorf("GROUP after the failed AUTHINFO GENERIC: %q, %v; want 211", line, err)
	}
}

// kevPythonFilter answers by Subject; filter_messageid returns 1 for a Message-ID that
// contains "midtruthy".
const kevPythonFilter = `import INN
from INN import set_filter_hook
class Filter:
    def filter_art(self, art):
        subj = art.get("Subject")
        subj = bytes(subj).decode() if subj is not None else ""
        if subj == "hashtrim":
            a = INN.hashstring("abc  \n", 1)
            b = INN.hashstring("abc", 1)
            return "hash-equal" if a == b else "hash-differ"
        if subj == "hashbound":
            INN.hashstring("a b", 10)
            return "hash-bound-returned"
        if subj == "truthy":
            return True
        if subj == "surrogate":
            return "\udcff"
        return ""
    def filter_messageid(self, msgid):
        if "midtruthy" in msgid:
            return 1
        return ""
set_filter_hook(Filter())
`

func pythonSite(t *testing.T) *Site {
	t.Helper()
	innd, err := os.ReadFile(filepath.Join(prefix(t), "bin", "innd"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(innd, []byte("python interpreter initialized OK")) {
		t.Skip("innd was built without Python")
	}
	return NewSite(t, SiteOptions{
		InnConf: map[string]string{"pathfilter": "@etcdir@"},
		Etc:     map[string]string{"filter_innd.py": kevPythonFilter},
	})
}

// offer sends an article with the given Subject by IHAVE and returns innd's final reply.
func offer(t *testing.T, s *Site, id, subject string) (int, string, error) {
	t.Helper()
	p := s.Peer(t)
	code, line, err := p.TryCmd("IHAVE %s", id)
	if err != nil || code != 335 {
		return code, line, err
	}
	if err := p.Send(wire(newArticle(id, "e2e.test", subject, "body"))); err != nil {
		return 0, "", err
	}
	return p.ReadStatus()
}

// inndAlive checks that innd still answers a new reader connection.
func inndAlive(t *testing.T, s *Site) {
	t.Helper()
	if _, err := tryReader(t, s); err != nil {
		t.Errorf("innd no longer answers: %v", err)
	}
}

// TestKevPythonHashTrim (#413): INN.hashstring with a line count trims trailing white space.
func TestKevPythonHashTrim(t *testing.T) {
	s := pythonSite(t)
	code, line, err := offer(t, s, msgID(t), "hashtrim")
	if err != nil || code != 437 || !strings.Contains(line, "hash-equal") {
		t.Errorf("hashtrim: %d %q %v; want 437 hash-equal", code, line, err)
	}
}

// TestKevPythonHashBound (#413): a line count larger than the string has lines stays inside
// the string.
func TestKevPythonHashBound(t *testing.T) {
	s := pythonSite(t)
	code, line, err := offer(t, s, msgID(t), "hashbound")
	if err != nil || code != 437 || !strings.Contains(line, "hash-bound-returned") {
		t.Errorf("hashbound: %d %q %v; want 437 hash-bound-returned", code, line, err)
	}
	inndAlive(t, s)
}

// TestKevPythonNonString (#413): filter_art returning True or a string that cannot be
// encoded as UTF-8, and filter_messageid returning 1, leave innd running.
func TestKevPythonNonString(t *testing.T) {
	for _, subject := range []string{"truthy", "surrogate"} {
		t.Run(subject, func(t *testing.T) {
			s := pythonSite(t)
			code, line, err := offer(t, s, msgID(t), subject)
			t.Logf("%s: %d %q %v", subject, code, line, err)
			if err != nil || code != 235 {
				t.Errorf("%s: %d %q %v; want 235 (accepted, error logged)", subject, code, line, err)
			}
			inndAlive(t, s)
		})
	}
	t.Run("midtruthy", func(t *testing.T) {
		s := pythonSite(t)
		id := strings.Replace(msgID(t), "<", "<midtruthy.", 1)
		p := s.Peer(t)
		code, line, err := p.TryCmd("IHAVE %s", id)
		t.Logf("IHAVE %s: %d %q %v", id, code, line, err)
		if err != nil || code != 335 {
			t.Errorf("IHAVE: %d %q %v; want 335", code, line, err)
		}
		inndAlive(t, s)
	})
}

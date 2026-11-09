package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// dynamicReaders gives 127.0.0.1 access to every group, with the Python dynamic hook
// nnrpd_dynamic.py (in pathfilter) deciding for each group.
const dynamicReaders = `auth "local" {
    hosts: "127.0.0.1, ::1, localhost"
    default: "<local>"
    python_dynamic: "nnrpd_dynamic"
}
access "local" {
    users: "<local>"
    newsgroups: "*"
    access: RPAN
}
`

// dynamicHook records "<type> <newsgroup>" of every call in etc/dynamic.log and refuses the
// groups whose name begins with e2e.secret.
const dynamicHook = `from nnrpd import set_auth_hook


class Dynamic:
    def dynamic(self, attributes):
        kind = attributes["type"].tobytes().decode()
        group = attributes["newsgroup"].tobytes().decode(errors="backslashreplace")
        with open("@etcdir@/dynamic.log", "a") as f:
            f.write("%s %s\n" % (kind, group))
        if group.startswith("e2e.secret"):
            return "no access to " + group
        return None


set_auth_hook(Dynamic())
`

// TestPythonDynamicNewsgroup: when a reader requests an article by Message-ID, the Python
// dynamic hook of nnrpd gets the name of a newsgroup of the article in attributes['newsgroup']
// (#162). It got an empty string, so a hook that refuses a group let the article through.
// For a crossposted article the hook is called for each group, and the reader gets the
// article when the hook grants access to one of them. Skipped when nnrpd was built without
// Python (configure --with-python).
func TestPythonDynamicNewsgroup(t *testing.T) {
	nnrpd, err := os.ReadFile(filepath.Join(prefix(t), "bin", "nnrpd"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(nnrpd, []byte("authorization skipped due to no Python dynamic")) {
		t.Skip("nnrpd was built without Python")
	}
	s := NewSite(t, SiteOptions{
		InnConf: map[string]string{"pathfilter": "@etcdir@"},
		Groups: []Group{{Name: "e2e.test"}, {Name: "e2e.secret"}, {Name: "e2e.secret2"},
			{Name: "e2e.other"}},
		Readers: dynamicReaders,
		Etc:     map[string]string{"nnrpd_dynamic.py": dynamicHook},
	})
	logFile := filepath.Join(s.Dir, "etc", "dynamic.log")
	// calls returns what the hook was called with since the last call of calls.
	var seen int
	calls := func() []string {
		t.Helper()
		b, err := os.ReadFile(logFile)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		var lines []string
		if len(b) > 0 {
			lines = strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
		}
		out := slices.Clone(lines[min(seen, len(lines)):])
		seen = len(lines)
		return out
	}

	one := s.Inject(t, "e2e.test", 1)[0]
	secret := s.Inject(t, "e2e.secret", 1)[0]
	mixed := s.Inject(t, "e2e.secret,e2e.test", 1)[0]
	both := s.Inject(t, "e2e.secret,e2e.secret2", 1)[0]

	c := s.Reader(t)
	// By group and number, the hook gets the group (this works without the fix).
	expect(t, c, 211, "GROUP e2e.test")
	if got := calls(); !slices.Equal(got, []string{"read e2e.test"}) {
		t.Errorf("GROUP e2e.test: the hook was called with %q", got)
	}
	if code, line := c.Cmd("GROUP e2e.secret"); code != 502 && code != 480 {
		t.Errorf("GROUP e2e.secret: got %q, want a refusal", line)
	}
	if got := calls(); !slices.Equal(got, []string{"read e2e.secret"}) {
		t.Errorf("GROUP e2e.secret: the hook was called with %q", got)
	}

	// An article in one group the hook accepts.
	for _, cmd := range []string{"ARTICLE", "HEAD", "STAT"} {
		if code, line := c.Cmd("%s %s", cmd, one); code/10 != 22 {
			t.Fatalf("%s %s: got %q, want 22x", cmd, one, line)
		}
		if cmd != "STAT" {
			c.ReadDotLines()
		}
		if got := calls(); !slices.Equal(got, []string{"read e2e.test"}) {
			t.Errorf("%s of the article in e2e.test: the hook was called with %q, want [read e2e.test]", cmd, got)
		}
	}

	// An article in one group the hook refuses.
	for _, cmd := range []string{"ARTICLE", "HEAD", "STAT"} {
		if code, line := c.Cmd("%s %s", cmd, secret); code != 502 && code != 480 {
			t.Errorf("%s %s: got %q, want a refusal (hook refuses e2e.secret)", cmd, secret, line)
			if code/10 == 22 && cmd != "STAT" {
				c.ReadDotLines()
			}
		}
		if got := calls(); !slices.Equal(got, []string{"read e2e.secret"}) {
			t.Errorf("%s of the article in e2e.secret: the hook was called with %q, want [read e2e.secret]", cmd, got)
		}
	}

	// A crosspost to a group the hook refuses and one it accepts is readable; the hook is
	// called for the groups, up to the first one it accepts.
	if code, line := c.Cmd("ARTICLE %s", mixed); code != 220 {
		t.Errorf("ARTICLE %s: got %q, want 220", mixed, line)
	} else {
		c.ReadDotLines()
	}
	got := calls()
	if n := len(got); n == 0 || n > 2 || got[n-1] != "read e2e.test" ||
		(n == 2 && got[0] != "read e2e.secret") {
		t.Errorf("ARTICLE of the crosspost to e2e.secret and e2e.test: the hook was called with %q, want [read e2e.test] or [read e2e.secret read e2e.test]", got)
	}

	// A crosspost to two groups the hook refuses is not.
	if code, line := c.Cmd("ARTICLE %s", both); code != 502 && code != 480 {
		t.Errorf("ARTICLE %s: got %q, want a refusal", both, line)
		if code == 220 {
			c.ReadDotLines()
		}
	}
	got = calls()
	slices.Sort(got)
	if want := []string{"read e2e.secret", "read e2e.secret2"}; !slices.Equal(got, want) {
		t.Errorf("ARTICLE of the crosspost to e2e.secret and e2e.secret2: the hook was called with %q, want %q", got, want)
	}

	// XHDR by Message-ID goes through the same check.
	if code, line := c.Cmd("XHDR Subject %s", secret); code != 502 && code != 480 {
		t.Errorf("XHDR Subject %s: got %q, want a refusal", secret, line)
		if code/10 == 22 {
			c.ReadDotLines()
		}
	}
	if got := calls(); !slices.Equal(got, []string{"read e2e.secret"}) {
		t.Errorf("XHDR of the article in e2e.secret: the hook was called with %q", got)
	}
	expect(t, c, 205, "QUIT")
}

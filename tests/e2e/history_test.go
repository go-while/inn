package e2e

// The history file and its dbz index.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestHistoryDbzErrors: when the .dir file of the dbz index of the history file is invalid,
// grephistory says which file is wrong and how, instead of only "Numerical argument out of
// domain" (EDOM, which dbz sets for an invalid .dir file), without a dbzclose error for the
// database it could not open, and a truncated .dir file is an error (issue #151).
func TestHistoryDbzErrors(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	header, _, _ := strings.Cut(s.ReadFile(t, "db/history.dir"), "\n")
	if !strings.HasPrefix(header, "dbz 6 ") {
		t.Fatalf("db/history.dir starts with %q, not a dbz 6 header", header)
	}
	for _, tc := range []struct {
		name, dir, want string
	}{
		{"bad header", "this is not dbz\n", "bad first line"},
		{"truncated", header + "\n", "bad usage value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := filepath.Join(s.Dir, "db", "hist-"+strings.ReplaceAll(tc.name, " ", "-"))
			s.writeFile(t, base[len(s.Dir)+1:], "")
			s.writeFile(t, base[len(s.Dir)+1:]+".dir", tc.dir)
			s.writeFile(t, base[len(s.Dir)+1:]+".index", "")
			s.writeFile(t, base[len(s.Dir)+1:]+".hash", "")
			out, err := s.TryRun("grephistory", "-f", base, "<no-such-article@e2e.invalid>")
			if err == nil {
				t.Errorf("grephistory exited 0:\n%s", out)
			}
			t.Logf("grephistory output:\n%s", out)
			var named bool
			for _, l := range strings.Split(out, "\n") {
				named = named || (strings.Contains(l, tc.want) && strings.Contains(l, base+".dir"))
			}
			if !named {
				t.Errorf("no message with %q naming %s.dir:\n%s", tc.want, base, out)
			}
			if strings.Contains(out, "Numerical argument out of domain") {
				t.Errorf("the EDOM message of dbz reached the user:\n%s", out)
			}
			if strings.Contains(out, "dbzclose") {
				t.Errorf("dbzclose was called for the database that could not be opened:\n%s", out)
			}
		})
	}
}

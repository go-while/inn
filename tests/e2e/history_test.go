package e2e

// The history file and its dbz index.

import (
	"crypto/md5"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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

// TestMakedbzMalformedHistory: makedbz reports each malformed line of a history text file
// once, with its line number, skips it and indexes the other lines, instead of exiting at a
// line without a field separator or a line too long for its buffer (issue #357). A key
// that is not a hexadecimal hash is rejected rather than indexed under a wrong hash.
func TestMakedbzMalformedHistory(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	key := func(id string) string { return fmt.Sprintf("[%X]", md5.Sum([]byte(id))) }
	remembered := "\t1700000000~-~1700000000"
	good := []string{"<good-1@e2e.invalid>", "<good-2@e2e.invalid>", "<good-3@e2e.invalid>"}
	long := "<too-long@e2e.invalid>"
	lines := []string{
		key(good[0]) + remembered,                            // 1
		"no field separator here",                            // 2: used to abort
		key(good[1]) + remembered,                            // 3
		"<not-a-hash@e2e.invalid>" + remembered,              // 4
		"[" + strings.Repeat("Z", 32) + "]" + remembered,     // 5: not hexadecimal
		key(long) + remembered + strings.Repeat("x", 64<<10), // 6: longer than QIO's buffer, used to abort
		key(good[2]) + remembered,                            // 7
		key(good[0]) + remembered,                            // 8: duplicate of line 1
		"[0123456789]" + remembered,                          // 9: too short
	}
	bad := []int{2, 4, 5, 6, 8, 9}
	base := filepath.Join(s.Dir, "db", "hist-malformed")
	s.writeFile(t, "db/hist-malformed", strings.Join(lines, "\n")+"\n")

	out, err := runFor(s, 20*time.Second, "makedbz", "-i", "-f", base)
	t.Logf("makedbz output:\n%s", out)
	if err != nil {
		t.Fatalf("makedbz -i -f %s: %v", base, err)
	}
	for n := 1; n <= len(lines); n++ {
		want := 0
		if slices.Contains(bad, n) {
			want = 1
		}
		if got := strings.Count(out, fmt.Sprintf("line %d:", n)); got != want {
			t.Errorf("line %d is reported %d times, want %d", n, got, want)
		}
	}

	for _, id := range good {
		out, err := runFor(s, 20*time.Second, "grephistory", "-f", base, id)
		if err != nil || strings.TrimSpace(out) != "/dev/null" {
			t.Errorf("grephistory -f %s %s: %v\n%s", base, id, err, out)
		}
	}
	// The too-long line is skipped whole, not indexed.
	if out, err := runFor(s, 20*time.Second, "grephistory", "-f", base, long); err == nil {
		t.Errorf("grephistory finds %s of the skipped long line:\n%s", long, out)
	}
}

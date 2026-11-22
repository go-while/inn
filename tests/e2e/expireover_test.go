package e2e

// expireover, the expiration of overview data (and, with groupbaseexpiry, of articles).

import (
	"path/filepath"
	"strings"
	"testing"
)

// listGroup returns the article numbers LISTGROUP gives for group, or nil with the status
// line when the reply is not 211.
func listGroup(tb testing.TB, s *Site, group string) ([]string, string) {
	tb.Helper()
	c := s.Reader(tb)
	defer func() { _, _ = c.Cmd("QUIT") }()
	code, line := c.Cmd("LISTGROUP %s", group)
	if code != 211 {
		return nil, line
	}
	return c.ReadDotLines(), line
}

// TestExpireoverPartialActiveKeepsGroups: when the read of the active file stops early,
// here at a line longer than the 32 KB buffer of QIO, expireover must not purge the
// newsgroups it has not reached. The purge of removed newsgroups that follows a full pass
// takes every newsgroup not expired during the pass for a removed one: ovsqlite marks them
// deleted and removes their overview data, so a single bad line in the middle of the active
// file used to wipe out the overview of every newsgroup after it. expireover now reports
// the problem, purges nothing and exits with status 1.
func TestExpireoverPartialActiveKeepsGroups(t *testing.T) {
	s := NewSite(t, SiteOptions{
		Groups:  []Group{{Name: "e2e.first"}, {Name: "e2e.last"}},
		InnConf: map[string]string{"ovmethod": "ovsqlite"},
		// nnrpd reads the database directly: commit each row (see TestMethodMatrix).
		Etc: map[string]string{"ovsqlite.conf": "transrowlimit: 1\n"},
		BeforeStart: func(tb testing.TB, s *Site) {
			startDaemon(tb, s, filepath.Join(s.Dir, "run", "ovsqlite.sock"),
				"ovsqlite-server", "-d")
		},
	})
	s.Inject(t, "e2e.first", 2)
	s.Inject(t, "e2e.last", 2)
	for _, g := range []string{"e2e.first", "e2e.last"} {
		if nums, line := listGroup(t, s, g); len(nums) != 2 {
			t.Fatalf("%s before expireover: %q, articles %v; want 2", g, line, nums)
		}
	}

	// expireover reads the active file of innd's pathdb; innd is stopped while the file
	// has the bad line, and gets back the file it wrote.
	s.Stop(t)
	active := s.ReadFile(t, "db/active")
	i := strings.Index(active, "e2e.last ")
	if i < 0 {
		t.Fatalf("no e2e.last in the active file:\n%s", active)
	}
	long := strings.Repeat("x", 40000) + " 0000000000 0000000001 y\n"
	s.writeFile(t, "db/active", active[:i]+long+active[i:])
	out, err := s.TryRun("expireover")
	s.writeFile(t, "db/active", active)
	if err == nil {
		t.Errorf("expireover exited 0 although it could not read the whole active file:\n%s", out)
	}
	if !strings.Contains(out, "too long") {
		t.Errorf("expireover did not report the line too long:\n%s", out)
	}
	s.Start(t)

	for _, g := range []string{"e2e.first", "e2e.last"} {
		if nums, line := listGroup(t, s, g); len(nums) != 2 {
			t.Errorf("%s after expireover: %q, articles %v; want the 2 articles", g, line, nums)
		}
	}
}

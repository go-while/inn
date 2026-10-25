package e2e

// tdx-util, the administration tool of the tradindexed overview.

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// overviewLines returns the overview of group as OVER sends it (no lines for an empty
// group), from a new reader connection.
func overviewLines(tb testing.TB, s *Site, group string) []string {
	tb.Helper()
	c := s.Reader(tb)
	var lines []string
	if field(expect(tb, c, 211, "GROUP %s", group), 1) != "0" {
		expect(tb, c, 224, "OVER 1-")
		lines = c.ReadDotLines()
	}
	expect(tb, c, 205, "QUIT")
	return lines
}

// overviewFields drops the :bytes and :lines fields (the 7th and 8th) of OVER lines: the
// overview that tdx-util -R builds has the size of the stored article, terminating "." line
// included, in :bytes and only a Lines header field in :lines, which differ from what innd
// computes when the article arrives.
func overviewFields(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		f := strings.Split(l, "\t")
		if len(f) > 7 {
			f = slices.Delete(f, 6, 8)
		}
		out[i] = strings.Join(f, "\t")
	}
	return out
}

// newFiles returns the leftovers of an unfinished rebuild (*-NEW files) in the overview.
func newFiles(tb testing.TB, s *Site) []string {
	tb.Helper()
	var found []string
	err := filepath.WalkDir(filepath.Join(s.Dir, "spool", "overview"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(d.Name(), "-NEW") {
			found = append(found, path)
		}
		return err
	})
	if err != nil {
		tb.Fatal(err)
	}
	return found
}

// TestTdxUtilRebuildFromSpool: tdx-util -R - -n group rebuilds the overview of group from
// its directory in the tradspool article spool (patharticles plus the group name with
// periods changed to slashes), so that many groups can be rebuilt with xargs without
// working out each path; -R with a path still works. A directory that cannot be read leaves
// the overview alone: no group is added and no unfinished rebuild is left (issue #46).
func TestTdxUtilRebuildFromSpool(t *testing.T) {
	groups := append([]Group{{Name: "e2e.deep.sub.group"}}, DefaultGroups...)
	s := NewSite(t, SiteOptions{Groups: groups}) // tradspool and tradindexed
	s.Inject(t, "e2e.test", 3)
	s.Inject(t, "e2e.deep.sub.group", 2)

	before := map[string][]string{}
	for _, g := range []string{"e2e.test", "e2e.deep.sub.group"} {
		before[g] = overviewLines(t, s, g)
		if len(before[g]) == 0 {
			t.Fatalf("OVER of %s before the rebuild: no lines", g)
		}
	}

	// Lose the overview of both groups: rebuild them from an empty directory.
	empty := t.TempDir()
	for g := range before {
		s.Run(t, "tdx-util", "-R", empty, "-n", g)
		if got := overviewLines(t, s, g); len(got) != 0 {
			t.Fatalf("OVER of %s after a rebuild from an empty directory: %q", g, got)
		}
	}

	// Rebuild them from the spool without giving a path.
	for g, want := range before {
		out, err := s.TryRun("tdx-util", "-R", "-", "-n", g)
		if err != nil {
			t.Fatalf("tdx-util -R - -n %s: %v\n%s", g, err, out)
		}
		got := overviewLines(t, s, g)
		if !slices.Equal(overviewFields(got), overviewFields(want)) {
			t.Errorf("OVER of %s after tdx-util -R -:\n%s\nwant:\n%s", g,
				strings.Join(got, "\n"), strings.Join(want, "\n"))
		} else if !slices.Equal(got, want) {
			t.Logf("OVER of %s differs only in :bytes or :lines after the rebuild:\n%s\nbefore:\n%s", g,
				strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}

	// A group without a spool directory and a missing path leave the overview alone.
	if out, err := s.TryRun("tdx-util", "-R", "-", "-n", "e2e.no.such.group"); err == nil {
		t.Errorf("tdx-util -R - for a group without a spool directory exited 0:\n%s", out)
	} else if want := filepath.Join(s.Dir, "spool", "articles", "e2e", "no", "such", "group"); !strings.Contains(out, want) {
		t.Errorf("tdx-util -R - for a group without a spool directory does not name %s:\n%s", want, out)
	}
	if out, err := s.TryRun("tdx-util", "-i", "-n", "e2e.no.such.group"); !strings.Contains(out, "cannot find group") {
		t.Errorf("tdx-util -R - added e2e.no.such.group to the overview (%v):\n%s", err, out)
	}
	if out, err := s.TryRun("tdx-util", "-R", filepath.Join(s.Dir, "no-such-dir"), "-n", "e2e.test"); err == nil {
		t.Errorf("tdx-util -R with a missing directory exited 0:\n%s", out)
	}
	if left := newFiles(t, s); len(left) > 0 {
		t.Errorf("unfinished rebuilds left in the overview: %q", left)
	}
	if got := overviewLines(t, s, "e2e.test"); len(got) != len(before["e2e.test"]) {
		t.Errorf("OVER of e2e.test after a failed rebuild: %q", got)
	}
}

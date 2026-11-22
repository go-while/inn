package e2e

// tdx-util, the administration tool of the tradindexed overview.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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

	groupNames := []string{"e2e.test", "e2e.deep.sub.group"}
	wantOverview := make(map[string][]string, len(groupNames))
	empty := t.TempDir()
	for _, g := range groupNames {
		n := len(overviewLines(t, s, g))
		if n == 0 {
			t.Fatalf("OVER of %s before the rebuild: no lines", g)
		}

		// Compare the two path forms of -R. The metadata produced by a
		// rebuild need not match innd's on versions before the :bytes and
		// :lines fixes, which TestTdxUtilRebuildBytesLines checks separately.
		dir := filepath.Join(s.Dir, "spool", "articles",
			strings.ReplaceAll(g, ".", string(filepath.Separator)))
		s.Run(t, "tdx-util", "-R", dir, "-n", g)
		wantOverview[g] = overviewLines(t, s, g)
		if got := len(wantOverview[g]); got != n {
			t.Fatalf("OVER of %s after a rebuild from %s: got %d lines, want %d",
				g, dir, got, n)
		}

		// Lose the overview so the next rebuild must restore it.
		s.Run(t, "tdx-util", "-R", empty, "-n", g)
		if got := overviewLines(t, s, g); len(got) != 0 {
			t.Fatalf("OVER of %s after a rebuild from an empty directory: %q", g, got)
		}
	}

	// Rebuild them from the spool without giving a path.
	for _, g := range groupNames {
		want := wantOverview[g]
		out, err := s.TryRun("tdx-util", "-R", "-", "-n", g)
		if err != nil {
			t.Fatalf("tdx-util -R - -n %s: %v\n%s", g, err, out)
		}
		if got := overviewLines(t, s, g); !slices.Equal(got, want) {
			t.Errorf("OVER of %s after tdx-util -R -:\n%s\nwant:\n%s", g,
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
	if got := overviewLines(t, s, "e2e.test"); !slices.Equal(got, wantOverview["e2e.test"]) {
		t.Errorf("OVER of e2e.test after a failed rebuild: %q", got)
	}
}

// TestTdxUtilRebuildBytesLines: the overview that tdx-util -R builds from the spool has the
// :bytes and :lines that innd wrote when the articles arrived, and so has the overview that
// makehistory -O -S prints: the values RFC 3977 defines. :bytes is the size of the article
// without the dots added by dot-stuffing (in header field names too) and without the
// terminating "." line, :lines the number of lines in the body, whatever a Lines header
// field says. tdx-util used to write the size of the stored article (terminating line and
// stuffing dots included) and the Lines header field (nothing when there was none); innd
// counted a stuffing dot on the first body line or the first header line, and makehistory
// one in a header field name. Checked with the spool in wire format and in native format.
func TestTdxUtilRebuildBytesLines(t *testing.T) {
	cases := []struct {
		name  string
		first string   // a header line put before Path, or ""
		extra []string // header lines added after Date
		body  []string
	}{
		{"plain", "", nil, []string{"one", "two", "three"}},
		{"wrong Lines header", "", []string{"Lines: 99"}, []string{"one", "two"}},
		{"dot-stuffed lines", "", nil, []string{"first", ".hidden", "..two dots", ".", "last"}},
		{"dot-stuffed first line", "", nil, []string{".first", "second"}},
		{"dot-stuffed header", "", []string{".X-Dot: yes"}, []string{"body"}},
		{"dot-stuffed first header", ".X-Dot: first", nil, []string{"body"}},
		{"blank body", "", nil, []string{""}},
	}
	for _, wf := range []string{"true", "false"} {
		t.Run("wireformat "+wf, func(t *testing.T) {
			s := NewSite(t, SiteOptions{InnConf: map[string]string{"wireformat": wf}})
			p := s.Peer(t)
			names := map[string]string{} // message-id: case name
			for _, tc := range cases {
				id := msgID(t)
				names[id] = tc.name
				art := newArticle(id, "e2e.test", tc.name, tc.body...)
				art = slices.Insert(art, 6, tc.extra...)
				if tc.first != "" {
					art = slices.Insert(art, 0, tc.first)
				}
				expect(t, p, 335, "IHAVE %s", id)
				send(t, p, wire(art))
				status(t, p, 235, "IHAVE "+id)
			}
			expect(t, p, 205, "QUIT")

			// :bytes and :lines by the definition, from the articles as the server sends them.
			want := map[string][2]string{}
			c := s.Reader(t)
			for id := range names {
				expect(t, c, 220, "ARTICLE %s", id)
				size, lines, body := 0, 0, false
				for _, l := range c.ReadDotLines() {
					size += len(l) + 2
					if body {
						lines++
					}
					body = body || l == ""
				}
				want[id] = [2]string{fmt.Sprint(size), fmt.Sprint(lines)}
			}
			expect(t, c, 205, "QUIT")

			// counts finds :bytes and :lines after the Message-ID field, in OVER lines or in
			// makehistory -S lines (token, arrival and expiry, then overview without the number).
			counts := func(over []string) map[string][2]string {
				got := map[string][2]string{}
				for _, l := range over {
					f := strings.Split(l, "\t")
					for i := 0; i+3 < len(f); i++ {
						if _, ok := names[f[i]]; ok {
							got[f[i]] = [2]string{f[i+2], f[i+3]}
							break
						}
					}
				}
				return got
			}
			check := func(who string, over []string) {
				t.Helper()
				got := counts(over)
				for id, w := range want {
					if got[id] != w {
						t.Errorf("%s: %s wrote :bytes %q :lines %q, want %q %q",
							names[id], who, got[id][0], got[id][1], w[0], w[1])
					}
				}
			}
			before := overviewLines(t, s, "e2e.test")
			check("innd", before)

			out, err := runFor(s, 30*time.Second, "makehistory", "-O", "-x", "-S")
			if err != nil {
				t.Fatalf("makehistory -O -x -S: %v\n%s", err, out)
			}
			check("makehistory -O -S", strings.Split(out, "\n"))

			dir := filepath.Join(s.Dir, "spool", "articles", "e2e", "test")
			if out, err := runFor(s, 30*time.Second, "tdx-util", "-R", dir, "-n", "e2e.test"); err != nil {
				t.Fatalf("tdx-util -R %s -n e2e.test: %v\n%s", dir, err, out)
			}
			after := overviewLines(t, s, "e2e.test")
			check("tdx-util -R", after)
			if !slices.Equal(after, before) {
				t.Errorf("OVER after tdx-util -R:\n%s\nwant (from innd):\n%s",
					strings.Join(after, "\n"), strings.Join(before, "\n"))
			}
		})
	}
}

// TestTdxUtilRebuildRenameFailure: when tdx-util -R cannot rename the rebuilt index and data
// files of a group into place, it fails and the group keeps its old index entry, so that
// readers still get the right overview from the old files; the rebuild works once the
// rename can be done. A non-empty directory where the backup of the old index goes makes
// the first rename fail. The group starts at article 1001, so that the index base (the
// first article number minus 128) of the rebuilt index differs from the old one.
// tdx-util used to update the group entry (base, low and high water marks, count and
// inode) and unlock the group before renaming the files, so after the failure readers
// looked up the old index with the base of the new one and got the overview of other
// articles, and the articles left out of the rebuild disappeared.
func TestTdxUtilRebuildRenameFailure(t *testing.T) {
	s := NewSite(t, SiteOptions{ // tradspool and tradindexed
		BeforeStart: func(tb testing.TB, s *Site) {
			active := strings.Replace(s.ReadFile(tb, "db/active"),
				"e2e.test 0000000000 0000000001 ", "e2e.test 0000001000 0000001001 ", 1)
			s.writeFile(tb, "db/active", active)
		},
	})
	ids := s.Inject(t, "e2e.test", 6)
	before := overviewLines(t, s, "e2e.test")
	if len(before) != len(ids) {
		t.Fatalf("OVER before the rebuild: %d lines, want %d:\n%s", len(before), len(ids),
			strings.Join(before, "\n"))
	}
	if !strings.HasPrefix(before[0], "1001\t") {
		t.Fatalf("first article of e2e.test is not 1001: %q", before[0])
	}

	// Rebuild from articles 1003 to 1006 only, so that the new index has another base.
	dir := t.TempDir()
	spool := filepath.Join(s.Dir, "spool", "articles", "e2e", "test")
	for n := 1003; n <= 1006; n++ {
		b, err := os.ReadFile(filepath.Join(spool, fmt.Sprint(n)))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(n)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var idx string
	err := filepath.WalkDir(filepath.Join(s.Dir, "spool", "overview"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Name() == "e2e.test.IDX" {
			idx = path
		}
		return err
	})
	if err != nil || idx == "" {
		t.Fatalf("index file of e2e.test not found (%v)", err)
	}
	bak := idx + "-BAK"
	if err := os.MkdirAll(filepath.Join(bak, "block"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := s.TryRun("tdx-util", "-R", dir, "-n", "e2e.test")
	t.Logf("tdx-util -R with %s blocked: %v\n%s", filepath.Base(bak), err, out)
	if err == nil {
		t.Errorf("tdx-util -R exited 0 although the rebuilt files could not be renamed")
	}
	if err := os.RemoveAll(bak); err != nil {
		t.Fatal(err)
	}
	if got := overviewLines(t, s, "e2e.test"); !slices.Equal(got, before) {
		t.Fatalf("OVER after the failed rebuild:\n%s\nwant (unchanged):\n%s",
			strings.Join(got, "\n"), strings.Join(before, "\n"))
	}

	// Without the obstacle, the rebuild gives the overview of articles 1003 to 1006.
	s.Run(t, "tdx-util", "-R", dir, "-n", "e2e.test")
	got := overviewLines(t, s, "e2e.test")
	var want, have []string
	for i, id := range ids[2:] {
		want = append(want, fmt.Sprintf("%d %s", i+1003, id))
	}
	for _, l := range got {
		if f := strings.Split(l, "\t"); len(f) > 4 {
			have = append(have, f[0]+" "+f[4])
		} else {
			have = append(have, l)
		}
	}
	if !slices.Equal(have, want) {
		t.Errorf("OVER after the rebuild (number, Message-ID):\n%s\nwant:\n%s",
			strings.Join(have, "\n"), strings.Join(want, "\n"))
	}
}

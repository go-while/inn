package e2e

// tdx-util, the administration tool of the tradindexed overview.

import (
	"fmt"
	"io/fs"
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
	if got := overviewLines(t, s, "e2e.test"); len(got) != len(before["e2e.test"]) {
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

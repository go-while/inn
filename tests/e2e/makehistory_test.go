package e2e

// makehistory and overchan rebuilding the overview.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// longOverview is more than the 32 KB of QIO_BUFFERSIZE, the longest line QIO reads.
const longOverview = 32 << 10

// injectLongReferences feeds an article to the site whose References header field, folded
// over many lines, makes its overview line longer than longOverview, and returns its
// Message-ID.
func injectLongReferences(tb testing.TB, s *Site, group string) string {
	tb.Helper()
	id := msgID(tb)
	var refs []string
	for i := 0; len(strings.Join(refs, " ")) < longOverview+4096; i++ {
		refs = append(refs, fmt.Sprintf("<ref-%05d.%d@%s>", i, time.Now().UnixNano(), PathHost))
	}
	var folded strings.Builder
	folded.WriteString("References:")
	for i, r := range refs {
		if i > 0 && i%10 == 0 {
			folded.WriteString("\n")
		}
		folded.WriteString(" " + r)
	}
	art := newArticle(id, group, "long References", "body of "+id)
	head, body := split(art)
	head = append(head, strings.Split(folded.String(), "\n")...)
	art = append(append(head, ""), body...)

	p := s.Peer(tb)
	expect(tb, p, 335, "IHAVE %s", id)
	send(tb, p, wire(art))
	status(tb, p, 235, "IHAVE "+id)
	expect(tb, p, 205, "QUIT")
	return id
}

// emptyOverview drops the overview of group with tdx-util -R from an empty directory.
func emptyOverview(tb testing.TB, s *Site, group string) {
	tb.Helper()
	if out, err := runFor(s, time.Minute, "tdx-util", "-R", tb.TempDir(), "-n", group); err != nil {
		tb.Fatalf("tdx-util -R: %v\n%s", err, out)
	}
	if got := overviewLines(tb, s, group); len(got) != 0 {
		tb.Fatalf("OVER of %s after a rebuild from an empty directory: %d lines", group, len(got))
	}
}

// compareOverview fails the test unless the overview of group equals want, apart from the
// :bytes and :lines fields, and says whether the long line is missing.
func compareOverview(tb testing.TB, s *Site, group, longID string, want []string) {
	tb.Helper()
	got := overviewLines(tb, s, group)
	if slices.Equal(overviewFields(got), overviewFields(want)) {
		return
	}
	var short []string
	for _, l := range got {
		short = append(short, fmt.Sprintf("%d bytes: %.200s", len(l), l))
	}
	missing := ""
	if !slices.ContainsFunc(got, func(l string) bool { return strings.Contains(l, longID) }) {
		missing = " (the overview of " + longID + " is missing)"
	}
	tb.Errorf("OVER of %s has %d lines, want %d%s:\n%s", group, len(got), len(want), missing,
		strings.Join(short, "\n"))
}

// TestMakehistoryLongOverview: makehistory -O and overchan rebuild the overview of an
// article whose overview line is longer than 32 KB, which innd accepts and stores. Both read
// overview lines with QIO, which skipped such lines ("overview line 2 is too long",
// "input line too long, skipping") (issue #102).
func TestMakehistoryLongOverview(t *testing.T) {
	const group = "e2e.test"
	s := NewSite(t, SiteOptions{}) // tradspool and tradindexed: makehistory sorts the overview
	s.Inject(t, group, 1)
	longID := injectLongReferences(t, s, group)
	s.Inject(t, group, 1)

	before := overviewLines(t, s, group)
	if len(before) != 3 {
		t.Fatalf("OVER of %s: %d lines, want 3", group, len(before))
	}
	if l := before[1]; !strings.Contains(l, longID) || len(l) <= longOverview {
		t.Fatalf("OVER of %s: the second line has %d bytes, want the overview of %s with more than %d",
			group, len(l), longID, longOverview)
	}

	t.Run("makehistory", func(t *testing.T) {
		emptyOverview(t, s, group)
		out, err := runFor(s, time.Minute, "makehistory", "-O", "-x")
		if err != nil {
			t.Fatalf("makehistory -O -x: %v\n%s", err, out)
		}
		if strings.Contains(out, "too long") {
			t.Errorf("makehistory -O -x:\n%s", out)
		}
		compareOverview(t, s, group, longID, before)
	})

	t.Run("overchan", func(t *testing.T) {
		// makehistory -S writes the overview in the format overchan reads.
		file := filepath.Join(t.TempDir(), "overview")
		f, err := os.Create(file)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(s.Prefix, "bin", "makehistory"), "-O", "-x", "-S")
		cmd.Dir, cmd.Env = s.Dir, append(os.Environ(), s.Env...)
		var stderr capped
		cmd.Stdout, cmd.Stderr = f, &stderr
		err = cmd.Run()
		_ = f.Close()
		if err != nil {
			t.Fatalf("makehistory -O -x -S: %v\n%s", err, stderr.String())
		}
		if info, err := os.Stat(file); err != nil || info.Size() <= longOverview {
			t.Fatalf("makehistory -O -x -S wrote too little overview (%v)", err)
		}

		emptyOverview(t, s, group)
		out, err := runFor(s, time.Minute, "overchan", file)
		if err != nil {
			t.Fatalf("overchan: %v\n%s", err, out)
		}
		if strings.Contains(out, "too long") {
			t.Errorf("overchan:\n%s", out)
		}
		compareOverview(t, s, group, longID, before)
	})
}

// overviewFields drops the :bytes and :lines fields (the 7th and 8th) of OVER lines, so that
// overview rebuilt by a tool can be compared with innd's on the other fields alone, also on a
// build without the fix that makes tdx-util and makehistory compute them like innd.
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

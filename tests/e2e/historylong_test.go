package e2e

// expire and a damaged line in the history file.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExpireHistoryLongLine: a line of the history file too long for expire's read buffer
// (32 KB) is skipped like any other malformed line. expire connected to innd reads the file,
// pauses innd at its end to read what arrived meanwhile, and resumes it; expire -n reads it
// without innd. In both cases the articles that arrived after the damaged line must still
// be in history afterwards, and innd must be running. expire -n used to take that line for
// the end of the file and install a history without them.
func TestExpireHistoryLongLine(t *testing.T) {
	const group = "e2e.hislong"
	s := NewSite(t, SiteOptions{
		Groups: []Group{{Name: group}},
		Etc:    map[string]string{"expire.ctl": "/remember/:11\n*:A:never:never:never\n"},
	})
	history := filepath.Join(s.Dir, "db", "history")
	// innd keeps the history file open: add the line while it is stopped.
	appendLong := func() {
		t.Helper()
		s.Stop(t)
		f, err := os.OpenFile(history, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(strings.Repeat("x", 40000) + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		s.Start(t)
	}
	var ids []string
	inHistory := func(phase string) {
		t.Helper()
		for _, id := range ids {
			out, err := s.TryRun("grephistory", id)
			if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "@") {
				t.Errorf("%s: %s is not in history (%v): %q", phase, id, err, out)
			}
		}
	}

	ids = append(ids, s.Inject(t, group, 2)...)
	appendLong()
	ids = append(ids, s.Inject(t, group, 2)...)
	inHistory("before expire")

	if out, err := s.TryRun("expire", "-v1"); err != nil {
		t.Errorf("expire: %v\n%s", err, out)
	}
	inHistory("after expire connected to innd")
	if out := s.Ctlinnd(t, "mode"); !strings.Contains(out, "Server running") {
		t.Errorf("after expire, ctlinnd mode:\n%s", out)
	}

	appendLong()
	ids = append(ids, s.Inject(t, group, 2)...)
	s.Stop(t)
	if out, err := s.TryRun("expire", "-n", "-v1"); err != nil {
		t.Errorf("expire -n: %v\n%s", err, out)
	}
	inHistory("after expire -n")
	s.Start(t)
}

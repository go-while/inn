package e2e

// inncheck --perm and the history database of the configured history method.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInncheckPermHistoryMethod: inncheck --perm checks the history database of the
// history method inn.conf sets. It used to check the history text file whenever one
// existed, so that after a conversion with hissqlite-convert, which leaves the old hisv6
// files in place, it checked them instead of history.sqlite; and with hisv6, a leftover
// history.sqlite hid a missing history file. A configured database that is not a regular file
// is reported as such.
func TestInncheckPermHistoryMethod(t *testing.T) {
	t.Run("hissqlite with the old history file", func(t *testing.T) {
		s := NewSite(t, SiteOptions{InnConf: map[string]string{"hismethod": "hissqlite"}})
		old := filepath.Join(s.Dir, "db", "history")
		if err := os.WriteFile(old, []byte("old hisv6 history\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{old, old + ".sqlite"} {
			if err := os.Chmod(f, 0o606); err != nil {
				t.Fatal(err)
			}
		}
		out, _ := s.TryRun("inncheck", "--perm")
		if !strings.Contains(out, old+".sqlite:0: mode 606,") {
			t.Errorf("inncheck --perm did not report the mode of %s.sqlite:\n%s", old, out)
		}
		if strings.Contains(out, old+":0:") {
			t.Errorf("inncheck --perm checked %s, the history file of hisv6:\n%s", old, out)
		}
	})

	t.Run("hissqlite with a directory as history.sqlite", func(t *testing.T) {
		s := NewSite(t, SiteOptions{InnConf: map[string]string{"hismethod": "hissqlite"}})
		s.Stop(t)
		db := filepath.Join(s.Dir, "db", "history.sqlite")
		for _, f := range []string{db, db + "-wal", db + "-shm"} {
			if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		if err := os.Mkdir(db, 0o755); err != nil {
			t.Fatal(err)
		}
		out, _ := s.TryRun("inncheck", "--perm")
		if !strings.Contains(out, db+":0: not a regular file") {
			t.Errorf("inncheck --perm did not report that %s is not a regular file:\n%s", db, out)
		}
	})

	t.Run("hisv6 with a leftover history.sqlite", func(t *testing.T) {
		s := NewSite(t, SiteOptions{})
		s.Stop(t)
		hist := filepath.Join(s.Dir, "db", "history")
		if err := os.Remove(hist); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hist+".sqlite", nil, 0o644); err != nil {
			t.Fatal(err)
		}
		out, _ := s.TryRun("inncheck", "--perm")
		if !strings.Contains(out, hist+":0: missing") {
			t.Errorf("inncheck --perm did not report the missing %s:\n%s", hist, out)
		}
	})
}

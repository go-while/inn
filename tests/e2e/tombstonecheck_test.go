package e2e

// The article existence check of nnrpd with expiretombstone (cancels.tombstone).

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestTombstoneCheckUnreadableLog: with expiretombstone and nnrpdcheckart, nnrpd takes a
// token missing from cancels.tombstone as proof that the article is still stored. When it
// could not read the file (here its mode is 0), it used to keep the empty list it had read
// and report every article as present: OVER listed an article removed with "sm -r". It
// must check the storage manager instead.
func TestTombstoneCheckUnreadableLog(t *testing.T) {
	const group = "e2e.tombstone"
	s := NewSite(t, SiteOptions{
		Groups:  []Group{{Name: group}},
		InnConf: map[string]string{"expiretombstone": "true", "nnrpdcheckart": "true"},
	})
	ids := s.Inject(t, group, 2)

	tok := strings.TrimSpace(s.Run(t, "grephistory", ids[0]))
	if !strings.HasPrefix(tok, "@") {
		t.Fatalf("grephistory %s: %q, want a token", ids[0], tok)
	}
	s.Run(t, "sm", "-r", tok)
	log := filepath.Join(s.Dir, "db", "cancels.tombstone")
	if b, err := os.ReadFile(log); err != nil || !strings.Contains(string(b), tok) {
		t.Fatalf("sm -r did not record %s in %s (%v)", tok, log, err)
	}

	numbers := func() []string {
		var nums []string
		for _, l := range overviewLines(t, s, group) {
			nums = append(nums, field(strings.ReplaceAll(l, "\t", " "), 0))
		}
		return nums
	}
	if got := numbers(); !slices.Equal(got, []string{"2"}) {
		t.Fatalf("OVER of %s lists articles %v with a readable %s, want [2]", group, got, log)
	}

	if err := os.Chmod(log, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(log, 0o664) })
	if f, err := os.Open(log); err == nil {
		_ = f.Close()
		t.Skipf("%s is still readable with mode 0 (running with privileges?)", log)
	}
	if got := numbers(); !slices.Equal(got, []string{"2"}) {
		t.Errorf("OVER of %s lists articles %v when nnrpd cannot read %s, want [2]: the removed article 1 is listed as present", group, got, log)
	}
}

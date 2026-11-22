package e2e

// tdx-util -F, the repair of a tradindexed overview.

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestTdxUtilFixReportsRepair: tdx-util -F clears the index entry of an article whose
// overview data is malformed (here the article number in the data does not match the
// entry). It used to report "unable to repair" for every entry it had repaired: the repair
// took any non-zero result of xpwrite for a failure, and a complete write returns the
// number of bytes written.
func TestTdxUtilFixReportsRepair(t *testing.T) {
	const group = "e2e.tdx"
	s := NewSite(t, SiteOptions{Groups: []Group{{Name: group}}})
	s.Inject(t, group, 3)
	s.Stop(t)

	var dat string
	err := filepath.WalkDir(filepath.Join(s.Dir, "spool", "overview"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Name() == group+".DAT" {
			dat = path
		}
		return err
	})
	if err != nil || dat == "" {
		t.Fatalf("no %s.DAT in the overview (%v)", group, err)
	}
	b, err := os.ReadFile(dat)
	if err != nil {
		t.Fatal(err)
	}
	// The data file holds the overview lines in arrival order; the first one is article 1.
	if !bytes.HasPrefix(b, []byte("1\t")) {
		t.Fatalf("%s does not start with the overview of article 1: %.40q", dat, b)
	}
	b[0] = '9'
	if err := os.WriteFile(dat, b, 0o664); err != nil {
		t.Fatal(err)
	}

	out, _ := s.TryRun("tdx-util", "-F")
	if !strings.Contains(out, "malformed overview data for "+group+":1") {
		t.Errorf("tdx-util -F did not find the malformed entry:\n%s", out)
	}
	if strings.Contains(out, "unable to repair") {
		t.Errorf("tdx-util -F reported a failed repair:\n%s", out)
	}
	if out, _ := s.TryRun("tdx-util", "-A"); strings.Contains(out, group+":1") {
		t.Errorf("tdx-util -A still finds the entry of article 1 after tdx-util -F:\n%s", out)
	}

	s.Start(t)
	var nums []string
	for _, l := range overviewLines(t, s, group) {
		nums = append(nums, field(strings.ReplaceAll(l, "\t", " "), 0))
	}
	if !slices.Equal(nums, []string{"2", "3"}) {
		t.Errorf("overview of %s after the repair has articles %v, want [2 3]", group, nums)
	}
}

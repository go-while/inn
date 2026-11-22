package e2e

// innd and the marker file .rebuildoverview that makehistory leaves in pathrun after it has
// rebuilt the overview: rc.news starts innd with -r because of it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRebuildOverviewMarkerRemovedAfterRenumber: innd, started with -r while the marker exists,
// renumbers the active file and then removes the marker, so that rc.news can leave it in place
// until the renumbering is done.
func TestRebuildOverviewMarkerRemovedAfterRenumber(t *testing.T) {
	run := t.TempDir()
	marker := filepath.Join(run, ".rebuildoverview")
	if err := os.WriteFile(marker, nil, 0o664); err != nil {
		t.Fatal(err)
	}
	s := NewSite(t, SiteOptions{
		InnConf: map[string]string{"pathrun": run},
		Args:    []string{"-r"},
	})
	waitFor(t, 10*time.Second, "innd to remove "+marker, func() bool {
		_, err := os.Stat(marker)
		return os.IsNotExist(err)
	})
	// innd runs normally after having renumbered the active file.
	s.Reader(t)
}

// TestRebuildOverviewMarkerKeptWhenInndFails: when innd fails to start (here an unknown
// overview method), the marker stays, and the next start renumbers the active file.
func TestRebuildOverviewMarkerKeptWhenInndFails(t *testing.T) {
	run := t.TempDir()
	marker := filepath.Join(run, ".rebuildoverview")
	if err := os.WriteFile(marker, nil, 0o664); err != nil {
		t.Fatal(err)
	}
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"pathrun": run}})
	s.shutdown(t)

	conf := s.ReadFile(t, "etc/inn.conf")
	if !strings.Contains(conf, "ovmethod: tradindexed") {
		t.Fatalf("no ovmethod in inn.conf:\n%s", conf)
	}
	s.WriteEtc(t, "inn.conf", strings.Replace(conf, "ovmethod: tradindexed", "ovmethod: nonesuch", 1))
	out, err := s.TryRun("innd", "-f", "-r")
	if err == nil {
		t.Fatalf("innd started with an unknown overview method:\n%s", out)
	}
	t.Logf("innd -f -r: %v\n%s", err, out)
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the marker is gone after a failed start of innd: %v", err)
	}
}

// TestRebuildOverviewMarkerKeptWithoutOverview: with enableoverview set to false nothing is
// renumbered, so innd leaves the marker for the next start with overview.
func TestRebuildOverviewMarkerKeptWithoutOverview(t *testing.T) {
	run := t.TempDir()
	marker := filepath.Join(run, ".rebuildoverview")
	if err := os.WriteFile(marker, nil, 0o664); err != nil {
		t.Fatal(err)
	}
	s := NewSite(t, SiteOptions{
		InnConf: map[string]string{"pathrun": run, "enableoverview": "false"},
		Args:    []string{"-r"},
	})
	s.Reader(t) // innd has started, and has gone through the renumbering
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the marker is gone although overview is disabled: %v", err)
	}
}

package e2e

// scanlogs, the daily summary and rotation of the log files.

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var innreportParam = regexp.MustCompile(`(?m)^(\s*)(\w+)(\s+)[^;\n]*;`)

// setInnreportConf rewrites the given parameters of the default section of the site's
// innreport.conf (a copy of the installed sample), and points logpath at the site's log
// directory so that innreport does not touch the installed one.
func setInnreportConf(t *testing.T, s *Site, params map[string]string) string {
	t.Helper()
	params["logpath"] = `"` + filepath.Join(s.Dir, "log") + `"`
	conf := innreportParam.ReplaceAllStringFunc(s.ReadFile(t, "etc/innreport.conf"),
		func(l string) string {
			m := innreportParam.FindStringSubmatch(l)
			if v, ok := params[m[2]]; ok {
				return m[1] + m[2] + m[3] + v + ";"
			}
			return l
		})
	s.WriteEtc(t, "innreport.conf", conf)
	return filepath.Join(s.Dir, "etc", "innreport.conf")
}

// readOld returns the contents of a rotated log file in log/OLD, uncompressed when scanlogs
// compressed it (the extension depends on --with-log-compress).
func readOld(t *testing.T, s *Site, base string) (string, bool) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(s.Dir, "log", "OLD", base+"*"))
	if len(matches) == 0 {
		return "", false
	}
	f, err := os.Open(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var r io.Reader = f
	if strings.HasSuffix(matches[0], ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatalf("%s: %v", matches[0], err)
		}
		r = zr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("%s: %v", matches[0], err)
	}
	return string(b), true
}

// TestScanlogsRotatesPullnewsLog: pullnews.log in pathlog, the standard name of the log file
// of "pullnews -l", is rotated by scanlogs into pathlog/OLD like the logs of nntpsend or
// perl-nocem, and emptied (issue #263).
func TestScanlogsRotatesPullnewsLog(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	setInnreportConf(t, s, map[string]string{})
	log := filepath.Join(s.Dir, "log", "pullnews.log")

	for i, run := range []string{"first", "second"} {
		content := fmt.Sprintf("%s run of pullnews\n", run)
		if err := os.WriteFile(log, []byte(content), 0o664); err != nil {
			t.Fatal(err)
		}
		out := s.Run(t, "scanlogs")
		if left := s.ReadFile(t, "log/pullnews.log"); left != "" {
			t.Fatalf("scanlogs run %d: pullnews.log not rotated, still has %q\nscanlogs: %s",
				i+1, left, out)
		}
		// The newest old log is pullnews.log.1 (scanlogs compresses into .0, then
		// shifts every cycle by one).
		got, ok := readOld(t, s, "pullnews.log.1")
		if !ok {
			t.Fatalf("scanlogs run %d: no pullnews.log.1 in log/OLD\nscanlogs: %s", i+1, out)
		}
		if got != content {
			t.Errorf("scanlogs run %d: log/OLD/pullnews.log.1 has %q, want %q", i+1, got, content)
		}
	}
	if got, _ := readOld(t, s, "pullnews.log.2"); got != "first run of pullnews\n" {
		t.Errorf("log/OLD/pullnews.log.2 has %q, want the log of the first run", got)
	}
	old, _ := filepath.Glob(filepath.Join(s.Dir, "log", "OLD", "pullnews.log*"))
	for i := range old {
		old[i] = filepath.Base(old[i])
	}
	t.Logf("log/OLD after two runs of scanlogs: %s", strings.Join(old, " "))
}

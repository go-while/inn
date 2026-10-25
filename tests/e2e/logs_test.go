package e2e

// scanlogs and innreport, the daily summary and rotation of the log files.

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
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

// TestInnreportNotArchived: without archives, the HTML report is news-notice.html, without a
// date, and so is its entry in innreport.db. innreport used to find no data for the graphs of
// the index page and to die with "Illegal division by zero" (issue #247). Besides, archive
// set to false in innreport.conf was ignored: only the -noarchive flag worked. The graphs
// need the GD Perl module; the test is skipped without it.
func TestInnreportNotArchived(t *testing.T) {
	if err := exec.Command("perl", "-MGD", "-e", "1").Run(); err != nil {
		t.Skip("the GD Perl module is not available: innreport draws no graph")
	}
	for _, tc := range []struct {
		name, archive string
		flags         []string
	}{
		{"innreport.conf", "false", nil},
		{"-noarchive", "true", []string{"-noarchive"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSite(t, SiteOptions{})
			http := filepath.Join(s.Dir, "http")
			conf := setInnreportConf(t, s, map[string]string{
				"html":     "true",
				"graph":    "true",
				"archive":  tc.archive,
				"html_dir": `"` + http + `"`,
				"index":    `"index.html"`,
			})

			// A day of incoming feed, ending an hour ago (innreport assumes the
			// current year).
			end := time.Now().Add(-time.Hour)
			start := end.Add(-24 * time.Hour)
			const stamp = "Jan _2 15:04:05"
			notice := filepath.Join(s.Dir, "tmp", "news.notice")
			lines := fmt.Sprintf("%s %s innd: peer.example:15 checkpoint seconds 43200"+
				" accepted 120 refused 3 rejected 1 duplicate 0 accepted size 345678"+
				" duplicate size 0 rejected size 2000\n", start.Format(stamp), PathHost)
			lines += fmt.Sprintf("%s %s innd: peer.example:15 checkpoint seconds 43200"+
				" accepted 80 refused 2 rejected 0 duplicate 0 accepted size 234567"+
				" duplicate size 0 rejected size 0\n", end.Format(stamp), PathHost)
			if err := os.WriteFile(notice, []byte(lines), 0o644); err != nil {
				t.Fatal(err)
			}

			args := append([]string{"-f", conf}, tc.flags...)
			for run := 1; run <= 2; run++ {
				out, err := s.TryRun("innreport", append(args, notice)...)
				if err != nil || strings.Contains(out, "division by zero") {
					t.Fatalf("innreport run %d: %v\n%s", run, err, out)
				}
			}
			db := strings.Split(strings.TrimSpace(s.ReadFile(t, "http/innreport.db")), "\n")
			if len(db) != 1 || !strings.HasPrefix(db[0], "news-notice.html|") {
				t.Errorf("innreport.db: want a single news-notice.html entry, got %q", db)
			}
			for _, f := range []string{"news-notice.html", "index.html"} {
				if _, err := os.Stat(filepath.Join(http, f)); err != nil {
					t.Errorf("no %s: %v", f, err)
				}
			}
			pics, _ := filepath.Glob(filepath.Join(http, "pics", "index0.*"))
			if len(pics) == 0 {
				t.Errorf("no graph of the index page in %s/pics", http)
			} else if fi, err := os.Stat(pics[0]); err != nil || fi.Size() == 0 {
				t.Errorf("%s: empty graph (%v)", pics[0], err)
			}
		})
	}
}

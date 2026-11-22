package e2e

// The HTML status reports of innd and innfeed, and the site markup of pathetc/inn_status.head
// at the start of their <head> element (#41).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// statusHeadMax is the largest inn_status.head that innd and innfeed copy into their reports.
const statusHeadMax = 64 * 1024

// statusHead returns a header of exactly n bytes: a stylesheet link and an HTML comment.
func statusHead(n int) string {
	head := "<link rel=\"stylesheet\" href=\"/e2e-status.css\">\n<!-- e2e "
	tail := " -->\n"
	return head + strings.Repeat("x", n-len(head)-len(tail)) + tail
}

// refresh is the first line of the built-in header that innd and innfeed write after the site
// markup.
const refresh = "<meta http-equiv=\"refresh\""

// innStatusPage removes innd's HTML status report and waits until innd has written a complete
// new one. innd writes it from its main loop once "status" seconds have passed since the last
// one, so the loop is woken with "ctlinnd mode" until the page is there.
func innStatusPage(t *testing.T, s *Site) string {
	t.Helper()
	path := filepath.Join(s.Dir, "http", "inn_status.html")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var page string
	waitFor(t, 20*time.Second, "innd to write "+path, func() bool {
		if b, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(b), "</html>\n") {
			page = string(b)
			return true
		}
		time.Sleep(250 * time.Millisecond)
		_, _ = s.TryRun("ctlinnd", "-t", "10", "mode")
		return false
	})
	return page
}

// TestStatusHead: innd's HTML status report starts its <head> element with the contents of
// pathetc/inn_status.head when that file exists, is readable and has at most 64 KiB; the
// built-in refresh and title follow it. Otherwise the report has only the built-in header: a
// missing file is skipped silently, an oversized, unreadable or non-regular file (a FIFO, which
// must not make innd block in open) is skipped with one syslog warning, not one at every
// report (#41).
func TestStatusHead(t *testing.T) {
	conf := map[string]string{"htmlstatus": "true", "status": "1"}
	cases := []struct {
		name       string
		head       string // contents of inn_status.head; "" = no file
		unreadable bool   // mode 000
		fifo       bool   // a FIFO with no writer instead of a file
		included   bool
		warning    string // the one warning expected in syslog; "" = none
	}{
		{name: "no file"},
		{name: "short", head: "<link rel=\"stylesheet\" href=\"/e2e-status.css\">\n", included: true},
		{name: "64 KiB", head: statusHead(statusHeadMax), included: true},
		{name: "oversized", head: statusHead(statusHeadMax + 1),
			warning: fmt.Sprintf("larger than %d bytes", statusHeadMax)},
		{name: "unreadable", head: statusHead(100), unreadable: true, warning: "cant open"},
		{name: "fifo", fifo: true, warning: "not a regular file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := SiteOptions{InnConf: conf}
			if c.head != "" {
				o.Etc = map[string]string{"inn_status.head": c.head}
			}
			if c.unreadable {
				o.BeforeStart = func(tb testing.TB, s *Site) {
					if err := os.Chmod(filepath.Join(s.Dir, "etc", "inn_status.head"), 0); err != nil {
						tb.Fatal(err)
					}
				}
			}
			if c.fifo {
				o.BeforeStart = func(tb testing.TB, s *Site) {
					// mkfifo(1) instead of syscall.Mkfifo, which Go does
					// not provide on every platform (illumos).
					fifo := filepath.Join(s.Dir, "etc", "inn_status.head")
					if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
						tb.Fatalf("mkfifo %s: %v: %s", fifo, err, out)
					}
				}
			}
			s := NewSite(t, o)
			page := innStatusPage(t, s)
			if !strings.Contains(page, "<title>"+PathHost+": incoming feeds</title>") {
				t.Errorf("no built-in title in the report:\n%.2000s", page)
			}
			want := "<head>\n" + refresh
			if c.included {
				want = "<head>\n" + c.head + refresh
			}
			if !strings.Contains(page, want) {
				t.Errorf("report does not start its <head> with %.200q:\n%.2000s", want, page)
			}
			if c.head != "" && !c.included && strings.Contains(page, "e2e-status.css") {
				t.Errorf("report contains the %s inn_status.head", c.name)
			}
			if c.warning == "" {
				return
			}
			innStatusPage(t, s) // a second report: the warning must not be repeated
			var warnings []string
			for _, l := range s.Syslog(t) {
				if strings.Contains(l, "inn_status.head") {
					warnings = append(warnings, l)
				}
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], c.warning) {
				t.Errorf("syslog after two reports with the %s inn_status.head: %q, want one %q warning",
					c.name, warnings, c.warning)
			}
		})
	}
}

// TestInnfeedStatusHead: innfeed's HTML status file (gen-html in innfeed.conf) starts its
// <head> element with the same pathetc/inn_status.head as innd's report, and skips a file
// larger than 64 KiB, but not one of exactly 64 KiB (#41).
func TestInnfeedStatusHead(t *testing.T) {
	cases := []struct {
		name     string
		head     string
		included bool
	}{
		{name: "short", head: "<link rel=\"stylesheet\" href=\"/e2e-status.css\">\n", included: true},
		{name: "64 KiB", head: statusHead(statusHeadMax), included: true},
		{name: "oversized", head: statusHead(statusHeadMax + 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			peer, got := newPeer(t, &fakeserver.PeerOptions{})
			s := NewSite(t, SiteOptions{
				Newsfeeds: "ME:*::\ninnfeed!:!*:Tc,Wnm*:@bindir@/innfeed\nfake:*:Tm:innfeed!\n",
				Etc: map[string]string{
					"inn_status.head": c.head,
					"innfeed.conf": fmt.Sprintf("gen-html: true\nstatus-file: innfeed.status\n"+
						"peer fake {\n    ip-name: \"127.0.0.1\"\n    port-number: %d\n}\n", peer.Port()),
				},
			})
			ids := s.Inject(t, "e2e.test", 1)
			waitFor(t, 30*time.Second, "innfeed to deliver the article", func() bool { return got.has(ids...) })
			path := filepath.Join(s.Dir, "http", "innfeed.status")
			var page string
			waitFor(t, 30*time.Second, "innfeed to write "+path, func() bool {
				b, err := os.ReadFile(path)
				page = string(b)
				return err == nil && strings.HasSuffix(page, "</html>\n")
			})
			if !strings.Contains(page, "<title>"+PathHost+": outgoing feeds</title>") {
				t.Errorf("no built-in title in innfeed's status file:\n%.2000s", page)
			}
			want := "<head>\n" + refresh
			if c.included {
				want = "<head>\n" + c.head + refresh
			}
			if !strings.Contains(page, want) {
				t.Errorf("innfeed's status file does not start its <head> with %.200q:\n%.2000s", want, page)
			}
			if !c.included && strings.Contains(page, "e2e-status.css") {
				t.Errorf("innfeed's status file contains the %s inn_status.head", c.name)
			}
		})
	}
}

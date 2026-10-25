package e2e

// news.daily, the daily maintenance script and its mailed report.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runNewsDaily runs news.daily with args for the site, killing its whole process group after
// d (it sleeps 30 seconds before expiring, and runs ctlinnd with long timeouts), and returns
// the beginning of what it wrote to stdout and stderr.
func runNewsDaily(s *Site, d time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(s.Prefix, "bin", "news.daily"), args...)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var out capped
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), fmt.Errorf("news.daily did not finish within %v", d)
	}
	return out.String(), err
}

// TestNewsDailyReportExpireFirst: in the report mailed by news.daily, the messages of an
// expire that failed on a broken expire.ctl come before the output of scanlogs (the error
// log and the innreport statistics), not after it near the end of the report (issue #83).
func TestNewsDailyReportExpireFirst(t *testing.T) {
	// mailcmd gets the report on its standard input; keep the first 64 KiB.
	bin := t.TempDir()
	report := filepath.Join(bin, "report")
	mailer := filepath.Join(bin, "mailer")
	script := fmt.Sprintf("#! /bin/sh\nexec head -c 65536 >'%s'\n", report)
	if err := os.WriteFile(mailer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSite(t, SiteOptions{
		InnConf: map[string]string{"mailcmd": mailer},
		// The regular expiration line has 4 fields instead of 5.
		Etc: map[string]string{"expire.ctl": "/remember/:11\n*:A:1:10\n"},
	})
	setInnreportConf(t, s, map[string]string{})

	// Something for each part of the output of scanlogs: a line in the error log, and a
	// checkpoint of a peer for the innreport statistics.
	errlog, err := os.OpenFile(filepath.Join(s.Dir, "log", "errlog"),
		os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o664)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := errlog.WriteString("e2e: an entry of the error log\n"); err != nil {
		t.Fatal(err)
	}
	if err := errlog.Close(); err != nil {
		t.Fatal(err)
	}
	// innreport needs a time span: two checkpoints, a day apart.
	var notice string
	for _, ago := range []time.Duration{25 * time.Hour, time.Hour} {
		notice += fmt.Sprintf("%s %s innd: peer.example:15 checkpoint seconds 43200"+
			" accepted 120 refused 3 rejected 1 duplicate 0 accepted size 345678"+
			" duplicate size 0 rejected size 2000\n",
			time.Now().Add(-ago).Format("Jan _2 15:04:05"), PathHost)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "log", "news.notice"), []byte(notice),
		0o664); err != nil {
		t.Fatal(err)
	}

	out, err := runNewsDaily(s, 150*time.Second)
	if err != nil {
		t.Fatalf("news.daily: %v\n%s", err, out)
	}
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("no report mailed: %v\nnews.daily: %s", err, out)
	}
	got := string(b)
	t.Logf("report:\n%s", got)

	expire := strings.Index(got, "Expire messages:")
	failure := strings.Index(got, "format error in expire.ctl")
	errors := strings.Index(got, "Error log:")
	post := strings.Index(got, "Post expiration status:")
	switch {
	case expire < 0 || failure < 0:
		t.Fatal("the report has no expire messages with the expire.ctl error")
	case errors < 0 || post < 0:
		t.Fatal("the report lacks the error log of scanlogs or the post expiration status")
	case failure < expire:
		t.Error("the expire.ctl error is not under \"Expire messages:\"")
	case errors < expire:
		t.Error("the expire messages come after the output of scanlogs")
	case post < errors:
		t.Error("the post expiration status comes before the output of scanlogs")
	}
	if stats := strings.Index(got, "peer.example"); stats < 0 {
		t.Log("innreport printed no statistics for peer.example")
	} else if stats < expire {
		t.Error("the expire messages come after the innreport statistics")
	}
}

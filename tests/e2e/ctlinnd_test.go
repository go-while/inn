package e2e

// ctlinnd commands.

import (
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestReloadAllInnConfChanged: inn.conf cannot be reloaded, so "ctlinnd reload all" must say
// so, in its reply and in syslog, when inn.conf has changed since innd started (issue #68),
// also after SIGUSR1. With an unchanged inn.conf, the reply is "Ok".
func TestReloadAllInnConfChanged(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	if got := strings.TrimSpace(s.Ctlinnd(t, "reload", "all", "e2e unchanged")); got != "Ok" {
		t.Errorf("ctlinnd reload all with an unchanged inn.conf: %q, want Ok", got)
	}

	s.WriteEtc(t, "inn.conf", s.ReadFile(t, "etc/inn.conf")+"# changed by the test\n")
	// SIGUSR1 makes innd recreate its control channel; it must not forget the inn.conf it
	// started with.
	if err := syscall.Kill(s.Proc.Pid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	waitFor(t, 5*time.Second, "ctlinnd to work after SIGUSR1", func() bool {
		_, err := s.TryRun("ctlinnd", "-t", "1", "mode")
		return err == nil
	})
	got := strings.TrimSpace(s.Ctlinnd(t, "reload", "all", "e2e changed"))
	t.Logf("ctlinnd reload all after inn.conf changed: %s", got)
	if !strings.Contains(got, "inn.conf") {
		t.Errorf("ctlinnd reload all after inn.conf changed: %q, want a warning that inn.conf was not reloaded", got)
	}
	if got := strings.TrimSpace(s.Ctlinnd(t, "reload", "incoming.conf", "e2e")); got != "Ok" {
		t.Errorf("ctlinnd reload incoming.conf: %q, want Ok (only reload all warns)", got)
	}

	var lines []string
	waitFor(t, 5*time.Second, "innd's syslog message for reload all", func() bool {
		lines = s.Syslog(t)
		return slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "reload all e2e changed") })
	})
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "inn.conf changed") }) {
		t.Errorf("innd did not log that inn.conf changed:\n%s", strings.Join(lines, "\n"))
	}
}

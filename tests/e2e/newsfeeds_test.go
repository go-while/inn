package e2e

// Feeds as newsfeeds entries define them.

import (
	"slices"
	"strings"
	"testing"
)

// TestNewsfeedsVariables: variables in newsfeeds, with a ! or @ before them applied to each
// of their comma-separated patterns. A variable used in the value of another variable is
// expanded too (issue #55): it used to stay as is, so the groups it excludes were fed.
func TestNewsfeedsVariables(t *testing.T) {
	for _, tc := range []struct{ name, newsfeeds string }{
		{"direct", "$LOCAL=e2e.other,e2e.moderated\nfake:*,@$LOCAL:Tf,Wm:\n"},
		{"nested", "$LOCAL=e2e.other,e2e.moderated\n$NOFEED=@$LOCAL\nfake:*,$NOFEED:Tf,Wm:\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSite(t, SiteOptions{Newsfeeds: "ME:*::\n" + tc.newsfeeds})
			fed := s.Inject(t, "e2e.test", 1)
			s.Inject(t, "e2e.other", 1)
			if b := s.flushBatch(t, "fake"); !slices.Equal(b, fed) {
				t.Errorf("batch file: %q, want only %q: e2e.other is excluded", b, fed)
			}
		})
	}
}

// TestNewsfeedsVariableLimit: the value of a variable and an entry can be at most 1 MB
// (1048576 bytes) once their variables are expanded. A variable redefined as twice itself
// doubles on each line; innd used to store it and the entries using it whatever their size,
// and a few more lines overflowed its int length counter (issue #55). A longer one is a
// bad_newsfeeds syntax error: "innd -s" fails, and so does reloading the file.
func TestNewsfeedsVariableLimit(t *testing.T) {
	doubled := func(n int) string { return "$A=x\n" + strings.Repeat("$A=$A$A\n", n) }
	s := NewSite(t, SiteOptions{})
	for _, tc := range []struct {
		name, newsfeeds string
		ok              bool
	}{
		{"variable at the limit", doubled(20), true},
		{"variable over the limit", doubled(21), false},
		{"entry over the limit", doubled(19) + "fake:*,!$A,!$A:Tf,Wm:\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.WriteEtc(t, "newsfeeds", "ME:*::\n"+tc.newsfeeds)
			out, err := s.TryRun("innd", "-s")
			if tc.ok && err != nil {
				t.Errorf("innd -s: %v\n%s", err, out)
			} else if !tc.ok && (err == nil || !strings.Contains(out, "error")) {
				t.Errorf("innd -s accepted the file: %v\n%s", err, out)
			}
		})
	}

	t.Run("reload", func(t *testing.T) {
		s.WriteEtc(t, "newsfeeds", "ME:*::\n"+doubled(21))
		out, err := s.TryRun("ctlinnd", "-t", "10", "reload", "newsfeeds", "e2e")
		if err == nil || !strings.Contains(out, "error") {
			t.Errorf("ctlinnd reload newsfeeds accepted the file: %v\n%s", err, out)
		}
		s.Ctlinnd(t, "mode") // innd still runs, with the previous newsfeeds
		var msgs []string
		for _, l := range s.Syslog(t) {
			if strings.Contains(l, "bad_newsfeeds") && strings.Contains(l, "longer than 1048576 bytes") {
				msgs = append(msgs, l)
			}
		}
		if len(msgs) == 0 {
			t.Error("syslog: no bad_newsfeeds message about the length")
		}
		t.Logf("syslog: %q", msgs)
	})
}

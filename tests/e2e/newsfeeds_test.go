package e2e

// Feeds as newsfeeds entries define them.

import (
	"slices"
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

package e2e

// inncheck, the syntax and permission checker of the configuration files.

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var fixCommand = regexp.MustCompile(`^(chown|chgrp|chmod) \S+ \S+$`)

// TestInncheckFixOutput: with -f and --perm, the output of inncheck is a shell script: the
// commands fixing permissions, and every other line a comment. Syntax problems of the
// checked files and the progress messages of -v used to be printed as is (issue #181).
func TestInncheckFixOutput(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	s.WriteEtc(t, "newsfeeds.bad", "ME:*::\nno colons in this entry\n")
	bad := filepath.Join(s.Dir, "etc", "newsfeeds.bad")
	out, err := s.TryRun("inncheck", "-v", "-f", "--perm", "newsfeeds="+bad)
	if err == nil {
		t.Errorf("inncheck exited 0 with a malformed newsfeeds entry:\n%s", out)
	}
	malformed := false
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "# "):
			malformed = malformed || strings.HasPrefix(l, "# "+bad+":2: malformed line")
		case !fixCommand.MatchString(l):
			t.Errorf("neither a comment nor a fix command: %q", l)
		}
	}
	if !malformed {
		t.Errorf("no comment about line 2 of %s:\n%s", bad, out)
	}
}

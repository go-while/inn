package e2e

import (
	"slices"
	"testing"
	"time"
)

// TestNewnewsKeepsGroupRange: NEWNEWS on another newsgroup leaves the low and high article
// numbers of the selected group alone, so that NEXT and OVER still see all its articles.
func TestNewnewsKeepsGroupRange(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	since := time.Now().UTC().Add(-time.Minute).Format("20060102 150405")
	ids := s.Inject(t, "e2e.test", 3)
	other := s.Inject(t, "e2e.other", 1)
	c := s.Reader(t)

	if got := expect(t, c, 211, "GROUP e2e.test"); got != "211 3 1 3 e2e.test" {
		t.Fatalf("GROUP e2e.test: %q, want \"211 3 1 3 e2e.test\"", got)
	}
	expect(t, c, 230, "NEWNEWS e2e.other %s GMT", since)
	if got := c.ReadDotLines(); !slices.Equal(got, other) {
		t.Fatalf("NEWNEWS e2e.other: %q, want %q", got, other)
	}

	// e2e.other has only article 1: with its range, article 2 of e2e.test would be out of range.
	if got := expect(t, c, 223, "NEXT"); field(got, 1) != "2" || field(got, 2) != ids[1] {
		t.Errorf("NEXT: %q, want 223 2 %s", got, ids[1])
	}
	expect(t, c, 224, "OVER 1-3")
	if over := c.ReadDotLines(); len(over) != 3 {
		t.Errorf("OVER 1-3: %d lines, want 3: %q", len(over), over)
	}
	expect(t, c, 205, "QUIT")
}

// TestNewnewsOneCharacterPathhost: NEWNEWS lists an article whose Xref field has a server
// name of one character, with an exact group and with a wildmat.
func TestNewnewsOneCharacterPathhost(t *testing.T) {
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"pathhost": "x"}})
	since := time.Now().UTC().Add(-time.Minute).Format("20060102 150405")
	ids := s.Inject(t, "e2e.test", 1)
	c := s.Reader(t)

	expect(t, c, 220, "ARTICLE %s", ids[0])
	if head, _ := split(c.ReadDotLines()); header(head, "Xref") != "x e2e.test:1" {
		t.Fatalf("Xref %q, want \"x e2e.test:1\"", header(head, "Xref"))
	}
	for _, groups := range []string{"e2e.test", "e2e.*"} {
		expect(t, c, 230, "NEWNEWS %s %s GMT", groups, since)
		if got := c.ReadDotLines(); !slices.Equal(got, ids) {
			t.Errorf("NEWNEWS %s: %q, want %q", groups, got, ids)
		}
	}
	expect(t, c, 205, "QUIT")
}

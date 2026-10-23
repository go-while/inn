package e2e

// Ways into innd other than NNTP from a test client: rnews and inews.

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
)

// TestRnews: rnews unpacks an rnews batch of two articles and feeds them to innd over its
// local socket.
func TestRnews(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	ids := []string{msgID(t), msgID(t)}
	var batch strings.Builder
	for i, id := range ids {
		text := strings.Join(newArticle(id, "e2e.test", fmt.Sprintf("via rnews %d", i+1),
			"rnews body", ".dot line"), "\n") + "\n"
		fmt.Fprintf(&batch, "#! rnews %d\n%s", len(text), text)
	}
	s.RunInput(t, batch.String(), "rnews", "-N", "-v")

	r := s.Reader(t)
	for _, id := range ids {
		expect(t, r, 222, "BODY %s", id)
		if got := r.ReadDotLines(); !slices.Equal(got, []string{"rnews body", ".dot line"}) {
			t.Errorf("BODY %s: %q", id, got)
		}
	}
	if got := expect(t, r, 211, "GROUP e2e.test"); got != "211 2 1 2 e2e.test" {
		t.Errorf("GROUP: %q", got)
	}
}

// TestInews: inews posts an article through nnrpd.
func TestInews(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	_, port, err := net.SplitHostPort(s.Addr)
	if err != nil {
		t.Fatal(err)
	}
	id := msgID(t)
	art := []string{
		"From: E2E Test <e2e@example.invalid>",
		"Newsgroups: e2e.test",
		"Subject: via inews",
		"Message-ID: " + id,
		"",
		"inews body",
		".dot line",
	}
	s.RunInput(t, strings.Join(art, "\n")+"\n", "inews", "-h", "-p", port)

	r := s.Reader(t)
	expect(t, r, 220, "ARTICLE %s", id)
	head, body := split(r.ReadDotLines())
	if !slices.Equal(body, []string{"inews body", ".dot line"}) {
		t.Errorf("body %q", body)
	}
	if got := header(head, "Subject"); got != "via inews" {
		t.Errorf("Subject %q", got)
	}
	if header(head, "Injection-Info") == "" {
		t.Errorf("no Injection-Info header field: %q", head)
	}
}

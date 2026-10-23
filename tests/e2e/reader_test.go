package e2e

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// TestReaderGreeting: a connection from 127.0.0.1 is handed to nnrpd, which greets 200 and
// lists the reader capabilities.
func TestReaderGreeting(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	c := s.Reader(t)
	if code, err := client.ParseCode(c.Greeting); err != nil || code != 200 {
		t.Fatalf("greeting %q, want 200", c.Greeting)
	}
	expect(t, c, 101, "CAPABILITIES")
	caps := c.ReadDotLines()
	for _, want := range []string{"VERSION 2", "READER", "POST", "OVER", "HDR"} {
		if !slices.ContainsFunc(caps, func(l string) bool { return l == want || strings.HasPrefix(l, want+" ") }) {
			t.Errorf("CAPABILITIES %q lacks %q", caps, want)
		}
	}
	if err := c.ExpectNoPending(200 * time.Millisecond); err != nil {
		t.Error(err)
	}
	expect(t, c, 205, "QUIT")
}

// TestPostAndRead posts through nnrpd and reads the article back with every retrieval
// command; the body has lines that need dot-stuffing.
func TestPostAndRead(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	c := s.Reader(t)
	id := msgID(t)
	body := []string{"first line", ".starts with a dot", "..two dots", ".", "", "last line"}
	art := newArticle(id, "e2e.test", "post and read", body...)
	since := time.Now().UTC().Add(-time.Minute)

	expect(t, c, 340, "POST")
	send(t, c, wire(art))
	status(t, c, 240, "POST article")

	if got := expect(t, c, 211, "GROUP e2e.test"); got != "211 1 1 1 e2e.test" {
		t.Errorf("GROUP: %q, want \"211 1 1 1 e2e.test\"", got)
	}

	if got := expect(t, c, 220, "ARTICLE 1"); field(got, 1) != "1" || field(got, 2) != id {
		t.Errorf("ARTICLE 1: %q", got)
	}
	head, gotBody := split(c.ReadDotLines())
	if !slices.Equal(gotBody, body) {
		t.Errorf("ARTICLE body %q, want %q", gotBody, body)
	}
	if got := header(head, "Message-ID"); got != id {
		t.Errorf("Message-ID %q, want %q", got, id)
	}
	if got := header(head, "Xref"); got != PathHost+" e2e.test:1" {
		t.Errorf("Xref %q", got)
	}

	expect(t, c, 221, "HEAD %s", id)
	if h := c.ReadDotLines(); header(h, "Subject") != "post and read" || slices.Contains(h, "") {
		t.Errorf("HEAD: %q", h)
	}
	expect(t, c, 222, "BODY 1")
	if got := c.ReadDotLines(); !slices.Equal(got, body) {
		t.Errorf("BODY %q, want %q", got, body)
	}
	expect(t, c, 223, "STAT %s", id)
	expect(t, c, 423, "STAT 2")
	expect(t, c, 430, "ARTICLE <no-such-article@%s>", PathHost)

	expect(t, c, 224, "OVER 1")
	over := c.ReadDotLines()
	if len(over) != 1 {
		t.Fatalf("OVER 1: %q", over)
	}
	f := strings.Split(over[0], "\t")
	if len(f) < 8 || f[0] != "1" || f[1] != "post and read" || f[4] != id {
		t.Errorf("OVER 1 fields %q", f)
	}

	expect(t, c, 211, "LISTGROUP e2e.test")
	if got := c.ReadDotLines(); !slices.Equal(got, []string{"1"}) {
		t.Errorf("LISTGROUP %q", got)
	}
	expect(t, c, 225, "HDR Subject 1")
	if got := c.ReadDotLines(); !slices.Equal(got, []string{"1 post and read"}) {
		t.Errorf("HDR Subject 1: %q", got)
	}
	expect(t, c, 230, "NEWNEWS e2e.* %s GMT", since.Format("20060102 150405"))
	if got := c.ReadDotLines(); !slices.Equal(got, []string{id}) {
		t.Errorf("NEWNEWS %q, want [%s]", got, id)
	}
	if err := c.ExpectNoPending(200 * time.Millisecond); err != nil {
		t.Error(err)
	}
	expect(t, c, 205, "QUIT")
}

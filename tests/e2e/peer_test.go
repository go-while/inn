package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// TestPeerIHAVE feeds an article with IHAVE from a peer, offers it again, and reads it as a
// reader.
func TestPeerIHAVE(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	p := s.Peer(t)
	if code, err := client.ParseCode(p.Greeting); err != nil || code != 200 {
		t.Fatalf("peer greeting %q, want 200", p.Greeting)
	}
	id := msgID(t)
	expect(t, p, 335, "IHAVE %s", id)
	send(t, p, wire(newArticle(id, "e2e.test", "via ihave", "hello", ".dot")))
	status(t, p, 235, "IHAVE article")
	expect(t, p, 435, "IHAVE %s", id)
	expect(t, p, 205, "QUIT")

	r := s.Reader(t)
	expect(t, r, 223, "STAT %s", id)
	expect(t, r, 222, "BODY %s", id)
	if got := r.ReadDotLines(); len(got) != 2 || got[0] != "hello" || got[1] != ".dot" {
		t.Errorf("BODY %q", got)
	}
}

// TestPeerStreaming pipelines CHECK and TAKETHIS (RFC 4644) and offers the articles again.
func TestPeerStreaming(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	p := s.Peer(t)
	expect(t, p, 203, "MODE STREAM")

	ids := []string{msgID(t), msgID(t), msgID(t)}
	raw := ""
	for _, id := range ids {
		raw += "CHECK " + id + "\r\n"
	}
	send(t, p, raw)
	for _, id := range ids {
		if got := status(t, p, 238, "CHECK "+id); field(got, 1) != id {
			t.Errorf("CHECK: %q, want the id %s", got, id)
		}
	}

	raw = ""
	for i, id := range ids {
		raw += "TAKETHIS " + id + "\r\n" + wire(newArticle(id, "e2e.test", fmt.Sprintf("streamed %d", i), "body"))
	}
	send(t, p, raw)
	for _, id := range ids {
		if got := status(t, p, 239, "TAKETHIS "+id); field(got, 1) != id {
			t.Errorf("TAKETHIS: %q, want the id %s", got, id)
		}
	}

	expect(t, p, 438, "CHECK %s", ids[0])
	send(t, p, "TAKETHIS "+ids[1]+"\r\n"+wire(newArticle(ids[1], "e2e.test", "again", "body")))
	status(t, p, 439, "TAKETHIS of a held article")
	if err := p.ExpectNoPending(200 * time.Millisecond); err != nil {
		t.Error(err)
	}

	r := s.Reader(t)
	if got := expect(t, r, 211, "GROUP e2e.test"); got != "211 3 1 3 e2e.test" {
		t.Errorf("GROUP: %q", got)
	}
}

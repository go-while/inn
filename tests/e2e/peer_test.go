package e2e

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// TestPeerStoreFailure feeds articles while innd cannot store them: the spool directory is
// read-only, so tradspool cannot create the group's directory. innd does not remember the
// message-IDs of such articles because it wants them again later. IHAVE answers 436. RFC
// 4644 has no code to defer a TAKETHIS, so innd must answer 400 and close the connection
// (section 2.5; 439 would tell the peer never to offer the article again) and must not
// process the TAKETHIS pipelined after it. Once the spool is writable, the articles are
// wanted and stored (issue #262).
func TestPeerStoreFailure(t *testing.T) {
	s := NewSite(t, SiteOptions{Etc: map[string]string{
		"storage.conf": "method tradspool {\n    newsgroups: *\n    class: 0\n}\n",
	}})
	articles := filepath.Join(s.Dir, "spool", "articles")
	if err := os.Chmod(articles, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(articles, 0o755) }) // let TempDir remove it

	p := s.Peer(t)
	ihave := msgID(t)
	expect(t, p, 335, "IHAVE %s", ihave)
	send(t, p, wire(newArticle(ihave, "e2e.test", "ihave, not stored", "body")))
	status(t, p, 436, "IHAVE with a read-only spool")

	expect(t, p, 203, "MODE STREAM")
	ids := []string{msgID(t), msgID(t)}
	send(t, p, "TAKETHIS "+ids[0]+"\r\n"+wire(newArticle(ids[0], "e2e.test", "takethis 1, not stored", "body"))+
		"TAKETHIS "+ids[1]+"\r\n"+wire(newArticle(ids[1], "e2e.test", "takethis 2, not stored", "body")))
	code, line, err := p.ReadStatus()
	if err != nil {
		t.Fatalf("TAKETHIS with a read-only spool: %v", err)
	}
	t.Logf("TAKETHIS with a read-only spool: %s", line)
	if code != 400 {
		t.Errorf("TAKETHIS with a read-only spool: got %q, want 400 and the connection closed", line)
	}
	if line, err := p.ReadLine(); !errors.Is(err, io.EOF) {
		t.Errorf("after the reply to the first TAKETHIS: read %q, %v; want the connection closed", line, err)
	}

	if err := os.Chmod(articles, 0o755); err != nil {
		t.Fatal(err)
	}
	p = s.Peer(t)
	expect(t, p, 203, "MODE STREAM")
	for i, id := range append([]string{ihave}, ids...) {
		if got := expect(t, p, 238, "CHECK %s", id); field(got, 1) != id {
			t.Errorf("CHECK: %q, want the id %s", got, id)
		}
		send(t, p, "TAKETHIS "+id+"\r\n"+wire(newArticle(id, "e2e.test", fmt.Sprintf("stored %d", i), "body")))
		status(t, p, 239, "TAKETHIS "+id+" with a writable spool")
	}

	r := s.Reader(t)
	for _, id := range append([]string{ihave}, ids...) {
		expect(t, r, 223, "STAT %s", id)
	}
}

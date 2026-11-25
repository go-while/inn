package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// TestAnyIncomingUnlistedPeer starts innd -a with an incoming.conf that lists only 127.0.0.3
// and feeds an article from PeerIP (127.0.0.2), which is not listed: innd must serve it as a
// peer, and must not read past the end of its peer list while deciding (an ASan build of
// innd aborts there before the greeting when it does).
func TestAnyIncomingUnlistedPeer(t *testing.T) {
	s := NewSite(t, SiteOptions{
		Args:     []string{"-a"},
		Incoming: "peer other {\n    hostname: \"127.0.0.3\"\n}\n",
	})
	p := s.Peer(t)
	if code, err := client.ParseCode(p.Greeting); err != nil || code != 200 {
		t.Fatalf("unlisted peer greeting %q, want 200", p.Greeting)
	}
	id := msgID(t)
	expect(t, p, 335, "IHAVE %s", id)
	send(t, p, wire(newArticle(id, "e2e.test", "any incoming", "hello")))
	status(t, p, 235, "IHAVE article")
	expect(t, p, 205, "QUIT")

	time.Sleep(200 * time.Millisecond)
	select {
	case <-s.Proc.Done():
		t.Fatalf("innd exited:\n%s", s.Proc.Stderr())
	default:
	}
	if out := s.Proc.Stderr(); strings.Contains(out, "AddressSanitizer") {
		t.Fatalf("innd stderr:\n%s", out)
	}
	r := s.Reader(t)
	expect(t, r, 223, "STAT %s", id)
}

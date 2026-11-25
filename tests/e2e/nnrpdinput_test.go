package e2e

import (
	"strings"
	"testing"
)

// TestReaderLineOverLimit: a command line longer than localmaxartsize, sent after another
// command on the same connection, gets one "Line too long" reply and the connection goes on.
// nnrpd's line buffer grows up to localmaxartsize and then discards the rest of the line
// (InterNetNews/inn#399).
func TestReaderLineOverLimit(t *testing.T) {
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"localmaxartsize": "4096"}})
	c := s.Reader(t)
	expect(t, c, 111, "DATE")
	send(t, c, strings.Repeat("x", 20000)+"\r\n")
	code, line, err := c.ReadStatus()
	if err != nil {
		t.Fatalf("reply to a 20000-byte line: %v (line %q)", err, line)
	}
	t.Logf("reply to a 20000-byte line: %q", line)
	if code != 500 && code != 501 {
		t.Fatalf("reply to a 20000-byte line: %q, want 500 or 501", line)
	}
	expect(t, c, 111, "DATE")
	expect(t, c, 205, "QUIT")
}

// TestPostOnlyTerminator: the first POST of a connection whose article is only the
// terminating "." gets a 441 and the connection goes on (InterNetNews/inn#400).
func TestPostOnlyTerminator(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	c := s.Reader(t)
	expect(t, c, 340, "POST")
	send(t, c, ".\r\n")
	t.Logf("reply to an empty POST: %q", status(t, c, 441, "empty POST"))
	expect(t, c, 111, "DATE")
	expect(t, c, 205, "QUIT")
}

// TestPostOnlyTerminatorAfterArticle: an empty POST after an accepted one on the same
// connection gets a 441 and does not post the earlier article again (InterNetNews/inn#400).
func TestPostOnlyTerminatorAfterArticle(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	c := s.Reader(t)
	expect(t, c, 340, "POST")
	send(t, c, wire(newArticle(msgID(t), "e2e.test", "first", "body")))
	status(t, c, 240, "first POST")
	expect(t, c, 340, "POST")
	send(t, c, ".\r\n")
	t.Logf("reply to an empty POST after an article: %q", status(t, c, 441, "empty POST"))
	if got := expect(t, c, 211, "GROUP e2e.test"); got != "211 1 1 1 e2e.test" {
		t.Errorf("GROUP: %q, want \"211 1 1 1 e2e.test\"", got)
	}
	expect(t, c, 205, "QUIT")
}

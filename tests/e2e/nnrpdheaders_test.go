package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Local review tests for nnrpd changes proposed upstream (InterNetNews/inn #404, #405, #410
// and #411): reader tracking of posts whose Message-ID holds a "/", nnrpdposthost with
// several readers.conf groups, virtual-host rewriting of stored articles with a bare CR or LF
// in their header, long HDR replies by Message-ID, and an empty "Control: cancel".

// nnrpdVhostReaders gives 127.0.0.1 full access through a virtual host whose pathhost differs
// from the site's.
const nnrpdVhostReaders = `auth "local" {
    hosts: "127.0.0.1, ::1, localhost"
    default: "<local>"
}
access "local" {
    users: "<local>"
    newsgroups: "*"
    access: RPAN
    virtualhost: true
    pathhost: "vhost.example"
}
`

const nnrpdLocalAuth = `auth "local" {
    hosts: "127.0.0.1, ::1, localhost"
    default: "<local>"
}
`

func nnrpdDate() string {
	return time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 -0700")
}

// nnrpdIhave feeds lines (wire() adds CRLF to each, so a line may hold a bare CR or LF) by
// IHAVE from PeerIP and fails the test unless innd stores the article.
func nnrpdIhave(tb testing.TB, s *Site, id string, lines []string) {
	tb.Helper()
	p := s.Peer(tb)
	expect(tb, p, 335, "IHAVE %s", id)
	send(tb, p, wire(lines))
	status(tb, p, 235, "IHAVE "+id)
	expect(tb, p, 205, "QUIT")
}

// nnrpdHdr sends cmd on a reader, expects code and returns the dot block; a block that does
// not end cleanly fails the test.
func nnrpdHdr(tb testing.TB, s *Site, code int, cmd string) []string {
	tb.Helper()
	c := s.Reader(tb)
	c.Deadline = 15 * time.Second
	expect(tb, c, code, "%s", cmd)
	lines, err := c.TryReadDotLines()
	if err != nil {
		tb.Fatalf("%s: %v (lines %q)", cmd, err, lines)
	}
	expect(tb, c, 111, "DATE")
	expect(tb, c, 205, "QUIT")
	return lines
}

// TestNnrpdTrackpostSlashMessageID: with readertrack, a post whose Message-ID holds a "/" is
// saved in pathlog/trackposts like any other post (#404); an ordinary Message-ID keeps the
// file name track.<message-id>.
func TestNnrpdTrackpostSlashMessageID(t *testing.T) {
	s := NewSite(t, SiteOptions{
		InnConf: map[string]string{"readertrack": "true"},
		Etc:     map[string]string{"nnrpd.track": "*:e2etrack\n"},
	})
	plain := msgID(t)
	at := strings.LastIndex(plain, "@")
	slash := plain[:at] + "/x" + plain[at:]

	c := s.Reader(t)
	for _, id := range []string{plain, slash} {
		expect(t, c, 340, "POST")
		send(t, c, wire(newArticle(id, "e2e.test", "tracked post", "body of "+id)))
		status(t, c, 240, "POST "+id)
	}
	expect(t, c, 205, "QUIT")

	dir := filepath.Join(s.Dir, "log", "trackposts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no tracking directory: %v", err)
	}
	files := map[string]string{} // Message-ID -> file name
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		if !e.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{plain, slash} {
			if strings.Contains(string(b), "\r\nMessage-ID: "+id+"\r\n") ||
				strings.HasPrefix(string(b), "Message-ID: "+id+"\r\n") {
				files[id] = e.Name()
			}
		}
	}
	t.Logf("trackposts: %q", names)
	if got := files[plain]; got != "track."+plain {
		t.Errorf("tracking copy of %s: %q, want %q", plain, got, "track."+plain)
	}
	if got := files[slash]; got == "" {
		t.Errorf("no tracking copy of %s in %s (files %q)", slash, dir, names)
	}
}

// TestNnrpdPosthostGroups: with nnrpdposthost set in inn.conf, readers.conf layouts that set
// it again in two access groups, or carry access parameters in two group blocks, are read
// without freeing the inn.conf string (#405): nnrpd greets and answers.
func TestNnrpdPosthostGroups(t *testing.T) {
	for _, tc := range []struct{ name, readers string }{
		{"access groups", nnrpdLocalAuth + `access "other" {
    users: "<nobody>"
    newsgroups: "*"
    nnrpdposthost: "other.invalid"
}
access "local" {
    users: "<local>"
    newsgroups: "*"
    access: RPAN
    nnrpdposthost: "local.invalid"
}
`},
		{"group blocks", nnrpdLocalAuth + `group "one" {
    newsgroups: "*"
}
group "two" {
    newsgroups: "*"
}
access "local" {
    users: "<local>"
    newsgroups: "*"
    access: RPAN
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSite(t, SiteOptions{
				InnConf: map[string]string{"nnrpdposthost": "posthost.invalid"},
				Readers: tc.readers,
			})
			c := s.Reader(t)
			t.Logf("greeting: %q", c.Greeting)
			expect(t, c, 211, "GROUP e2e.test")
			expect(t, c, 205, "QUIT")
		})
	}
}

// TestNnrpdVhostHdrPathBareCRLF: for a stored article whose Path field body holds a bare CR or
// LF, HDR Path through a virtual host answers within the field body (#410).
func TestNnrpdVhostHdrPathBareCRLF(t *testing.T) {
	s := NewSite(t, SiteOptions{Readers: nnrpdVhostReaders})
	for _, tc := range []struct{ name, sep string }{{"bare CR", "\r"}, {"bare LF", "\n"}} {
		t.Run(tc.name, func(t *testing.T) {
			id := msgID(t)
			nnrpdIhave(t, s, id, []string{
				"Path: e2e.peer" + tc.sep + "!vhost.example!not-for-mail",
				"From: E2E Test <e2e@example.invalid>",
				"Newsgroups: e2e.test",
				"Subject: Path with a " + tc.name,
				"Message-ID: " + id,
				"Date: " + nnrpdDate(),
				"",
				"body",
			})
			lines := nnrpdHdr(t, s, 225, "HDR Path "+id)
			t.Logf("HDR Path: %q", lines)
			if len(lines) != 1 || !strings.HasPrefix(lines[0], "0 vhost.example!") {
				t.Errorf("HDR Path %s: %q, want one line \"0 vhost.example!...\"", id, lines)
			}
		})
	}
}

// TestNnrpdHdrLongByMessageID: HDR and XHDR by Message-ID send a field body longer than 2 KB
// whole, with its line end (#411).
func TestNnrpdHdrLongByMessageID(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	id := msgID(t)
	var refs []string
	for i := 0; len(strings.Join(refs, " ")) < 3000; i++ {
		refs = append(refs, fmt.Sprintf("<ref-%04d@%s>", i, PathHost))
	}
	art := newArticle(id, "e2e.test", "long References", "body of "+id)
	head, body := split(art)
	for i := 0; i < len(refs); i += 10 {
		j := min(i+10, len(refs))
		if i == 0 {
			head = append(head, "References: "+strings.Join(refs[i:j], " "))
		} else {
			head = append(head, " "+strings.Join(refs[i:j], " "))
		}
	}
	nnrpdIhave(t, s, id, append(append(head, ""), body...))
	want := strings.Join(refs, " ")

	lines := nnrpdHdr(t, s, 225, "HDR References "+id)
	if len(lines) != 1 || lines[0] != "0 "+want {
		t.Errorf("HDR References: %d lines, first %d bytes; want one line of %d bytes",
			len(lines), len(strings.Join(lines, "\n")), len(want)+2)
	}
	lines = nnrpdHdr(t, s, 221, "XHDR References "+id)
	if len(lines) != 1 || lines[0] != id+" "+want {
		t.Errorf("XHDR References: %d lines, first %d bytes; want one line of %d bytes",
			len(lines), len(strings.Join(lines, "\n")), len(id)+1+len(want))
	}
}

// TestNnrpdVhostHeadBareLF: HEAD through a virtual host of a stored article with a bare LF
// right before a CRLF in its header (nnrpd's header end) and Path and Xref after it ends
// with a clean dot block, and the connection goes on (#411).
func TestNnrpdVhostHeadBareLF(t *testing.T) {
	s := NewSite(t, SiteOptions{Readers: nnrpdVhostReaders})
	id := msgID(t)
	nnrpdIhave(t, s, id, []string{
		"From: E2E Test <e2e@example.invalid>",
		"Newsgroups: e2e.test",
		"Subject: bare LF before a CRLF\n",
		"Path: e2e.peer!not-for-mail",
		"Message-ID: " + id,
		"Date: " + nnrpdDate(),
		"",
		"body",
	})
	lines := nnrpdHdr(t, s, 221, "HEAD "+id)
	t.Logf("HEAD: %q", lines)
}

// TestNnrpdVhostShortHeaderNames: through a virtual host, HDR of a header field called "P"
// or "X" returns its body unchanged: only Path and Xref are rewritten (#411).
func TestNnrpdVhostShortHeaderNames(t *testing.T) {
	s := NewSite(t, SiteOptions{Readers: nnrpdVhostReaders})
	id := msgID(t)
	art := newArticle(id, "e2e.test", "short header field names", "body")
	head, body := split(art)
	head = append(head, "X: alpha beta", "P: gamma delta")
	nnrpdIhave(t, s, id, append(append(head, ""), body...))
	for _, tc := range []struct{ name, want string }{{"X", "0 alpha beta"}, {"P", "0 gamma delta"}} {
		lines := nnrpdHdr(t, s, 225, "HDR "+tc.name+" "+id)
		if len(lines) != 1 || lines[0] != tc.want {
			t.Errorf("HDR %s: %q, want %q", tc.name, lines, tc.want)
		}
	}
}

// TestNnrpdPostEmptyCancel: a post with "Control: cancel" and nothing after it gets nnrpd's
// 441 "Message-ID missing in cancel", also when another header field follows it (#411).
func TestNnrpdPostEmptyCancel(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	id := msgID(t)
	c := s.Reader(t)
	expect(t, c, 340, "POST")
	send(t, c, wire([]string{
		"From: E2E Test <e2e@example.invalid>",
		"Newsgroups: e2e.test",
		"Subject: cmsg cancel",
		"Control: cancel",
		"Message-ID: " + id,
		"",
		"body",
	}))
	code, line, err := c.ReadStatus()
	if err != nil {
		t.Fatalf("POST: %v (line %q)", err, line)
	}
	t.Logf("POST: %q", line)
	if code != 441 || !strings.Contains(line, "Message-ID missing in cancel") {
		t.Errorf("POST: %q, want 441 ... Message-ID missing in cancel", line)
	}
	expect(t, c, 205, "QUIT")
}

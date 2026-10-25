package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// headPerlFilter is a Perl filter_art that writes $hdr{__HEAD__} to etc/head.out and rejects
// articles with an X-Spam-Foo header field, which is not one of the header fields innd knows.
const headPerlFilter = `sub filter_art {
    my $head = $hdr{'__HEAD__'};
    return "no __HEAD__" unless defined $head;
    open(my $fh, '>', '@etcdir@/head.out') or return "cannot write head.out: $!";
    binmode $fh;
    print $fh $head;
    close $fh;
    return "X-Spam-Foo seen" if $head =~ /^X-Spam-Foo:/mi;
    return "";
}
1;
`

// headPythonFilter does the same as headPerlFilter with art['__HEAD__'] (Python 3).
const headPythonFilter = `import re
from INN import set_filter_hook


class Filter:
    def filter_art(self, art):
        head = art.get("__HEAD__")
        if head is None:
            return "no __HEAD__"
        head = bytes(head)
        with open("@etcdir@/head.out", "wb") as f:
            f.write(head)
        if re.search(rb"^X-Spam-Foo:", head, re.M | re.I):
            return "X-Spam-Foo seen"
        return ""


set_filter_hook(Filter())
`

// TestFilterHeadPerl: the Perl filter_art gets the whole header of the article in
// $hdr{__HEAD__} (#73): every header field line as received, also the ones innd does not
// know, folded and repeated ones, with CRLF and without the empty line before the body; and
// it can reject an article on a header field innd does not know.
func TestFilterHeadPerl(t *testing.T) {
	testFilterHead(t, "filter_innd.pl", headPerlFilter, "perl")
}

// TestFilterHeadPython: the same with art['__HEAD__'] in the Python filter_art. Skipped when
// innd was built without Python (configure --with-python).
func TestFilterHeadPython(t *testing.T) {
	innd, err := os.ReadFile(filepath.Join(prefix(t), "bin", "innd"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(innd, []byte("python interpreter initialized OK")) {
		t.Skip("innd was built without Python")
	}
	testFilterHead(t, "filter_innd.py", headPythonFilter, "python")
}

func testFilterHead(t *testing.T, file, filter, lang string) {
	s := NewSite(t, SiteOptions{
		InnConf: map[string]string{"pathfilter": "@etcdir@"},
		Etc:     map[string]string{file: filter},
	})
	headOut := filepath.Join(s.Dir, "etc", "head.out")
	date := time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 -0700")

	// Header fields innd knows (Message-ID with trailing white space, which innd cuts off
	// when parsing it, Keywords, a repeated Received) mixed with ones it does not know (a
	// folded one, one in lower case).
	id := msgID(t)
	head := []string{
		"Path: e2e.peer!not-for-mail",
		"From: E2E Test <e2e@example.invalid>",
		"Newsgroups: e2e.test",
		"Subject: whole header",
		"Message-ID: " + id + "  ",
		"Date: " + date,
		"Received: from a.example by b.example",
		"X-Folded: first part",
		"\tsecond part",
		"Received: from c.example by d.example",
		"x-custom-field: lower case",
		"Keywords: one, two",
	}
	p := s.Peer(t)
	expect(t, p, 335, "IHAVE %s", id)
	send(t, p, wire(append(append(head, ""), "body line")))
	status(t, p, 235, "IHAVE "+id)
	got, err := os.ReadFile(headOut)
	if err != nil {
		t.Fatalf("the filter wrote no __HEAD__: %v", err)
	}
	if want := strings.Join(head, "\r\n") + "\r\n"; string(got) != want {
		t.Errorf("__HEAD__ = %q\nwant       %q", got, want)
	}

	// The article was stored as received (innd only prepends to Path and adds Xref).
	c := s.Reader(t)
	expect(t, c, 221, "HEAD %s", id)
	stored := strings.Join(c.ReadDotLines(), "\n")
	for _, h := range head[1:] {
		if !strings.Contains(stored, h) {
			t.Errorf("stored header lacks %q:\n%s", h, stored)
		}
	}
	expect(t, c, 205, "QUIT")

	// An X-Spam-Foo header field gets the article rejected, by IHAVE and by TAKETHIS.
	spam := func(id string) string {
		return wire(append(append(newArticle(id, "e2e.test", "spam")[:6], "X-Spam-Foo: yes", ""),
			"buy now"))
	}
	id2 := msgID(t)
	expect(t, p, 335, "IHAVE %s", id2)
	send(t, p, spam(id2))
	if line := status(t, p, 437, "IHAVE "+id2); !strings.Contains(line, "X-Spam-Foo seen") {
		t.Errorf("IHAVE %s: got %q, want the filter's reason", id2, line)
	}
	expect(t, p, 205, "QUIT")

	p = s.Peer(t)
	expect(t, p, 203, "MODE STREAM")
	id3 := msgID(t)
	send(t, p, "TAKETHIS "+id3+"\r\n"+spam(id3))
	status(t, p, 439, "TAKETHIS "+id3)
	expect(t, p, 205, "QUIT")

	c = s.Reader(t)
	for _, id := range []string{id2, id3} {
		expect(t, c, 430, "STAT %s", id)
	}
	expect(t, c, 205, "QUIT")

	// innd logs the rejections with the filter's reason.
	want := "rejecting[" + lang + "] " + id2 + " 437 X-Spam-Foo seen"
	waitFor(t, 5*time.Second, "the rejection in syslog", func() bool {
		for _, l := range s.Syslog(t) {
			if strings.Contains(l, want) {
				return true
			}
		}
		return false
	})
}

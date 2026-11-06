package e2e

// COMPRESS DEFLATE (RFC 8054) with nnrpd.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// TestCompress: nnrpd advertises COMPRESS DEFLATE, answers 206 and compresses both
// directions. Afterwards CAPABILITIES lists neither COMPRESS nor STARTTLS, a large article with
// dot-stuffed lines posts and reads back intact, pipelined commands work, and COMPRESS,
// STARTTLS and AUTHINFO get 502 (RFC 8054 section 2.2.2).
func TestCompress(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	c := s.Reader(t)
	if !slices.Contains(capabilities(t, c), "COMPRESS DEFLATE") {
		t.Fatalf("CAPABILITIES does not list COMPRESS DEFLATE")
	}
	c.Compress()

	for _, l := range capabilities(t, c) {
		if f := strings.Fields(l); len(f) > 0 && (f[0] == "COMPRESS" || f[0] == "STARTTLS" || f[0] == "MODE-READER") {
			t.Errorf("CAPABILITIES lists %q once compression is active", l)
		}
	}

	body := make([]string, 0, 3000)
	for i := range 3000 {
		switch i % 3 {
		case 0:
			body = append(body, fmt.Sprintf(".line %d starts with a dot", i))
		case 1:
			body = append(body, strings.Repeat(fmt.Sprintf("%d ", i), 20))
		default:
			body = append(body, "")
		}
	}
	id := msgID(t)
	expect(t, c, 340, "POST")
	send(t, c, wire(newArticle(id, "e2e.test", "compressed post", body...)))
	status(t, c, 240, "POST over COMPRESS")

	expect(t, c, 211, "GROUP e2e.test")
	expect(t, c, 222, "BODY %s", id)
	if got := c.ReadDotLines(); !slices.Equal(got, body) {
		t.Errorf("BODY over COMPRESS differs from the posted body (%d lines, want %d)", len(got), len(body))
	}
	expect(t, c, 224, "OVER 1")
	if over := c.ReadDotLines(); len(over) != 1 || field(strings.ReplaceAll(over[0], "\t", " "), 0) != "1" {
		t.Errorf("OVER 1: %q", over)
	}

	send(t, c, "GROUP e2e.test\r\nSTAT 1\r\nDATE\r\nSTAT 99\r\n")
	for _, want := range []int{211, 223, 111, 423} {
		status(t, c, want, "pipelined command")
	}

	expect(t, c, 502, "COMPRESS DEFLATE")
	expect(t, c, 502, "COMPRESS SHRINK") // any valid algorithm name while active, not 503
	expect(t, c, 502, "STARTTLS")
	expect(t, c, 502, "AUTHINFO USER e2e")
	if err := c.ExpectNoPending(200 * time.Millisecond); err != nil {
		t.Error(err)
	}
	expect(t, c, 205, "QUIT")
}

// TestCompressAfterStartTLS: COMPRESS works on a TLS connection without TLS-level compression
// (RFC 8054 allows it; Go's TLS never compresses).
func TestCompressAfterStartTLS(t *testing.T) {
	s := NewSite(t, SiteOptions{TLS: true})
	c := s.Reader(t)
	tc, _ := startTLS(t, s, c)
	if !slices.Contains(capabilities(t, tc), "COMPRESS DEFLATE") {
		t.Fatalf("CAPABILITIES over TLS does not list COMPRESS DEFLATE")
	}
	tc.Compress()
	expect(t, tc, 211, "GROUP e2e.test")
	expect(t, tc, 111, "DATE")
	expect(t, tc, 205, "QUIT")
}

// ihaveReaders is DefaultReaders with IHAVE permission (I).
const ihaveReaders = `auth "local" {
    hosts: "127.0.0.1, ::1, localhost"
    default: "<local>"
}
access "local" {
    users: "<local>"
    newsgroups: "*"
    access: RPAIN
}
`

// uploadSizes are the article sizes of TestCompressUpload: body lines and line width.
var uploadSizes = []struct {
	name         string
	lines, width int
}{{"small", 3, 20}, {"medium", 200, 100}, {"large", 3000, 200}}

// TestCompressUpload: articles of several sizes posted with POST and offered with IHAVE reach
// nnrpd intact over COMPRESS DEFLATE, whether the client sends the article in one write or line
// by line, and nnrpd answers at once. Before INN 2.7.2 (issue #269) nnrpd waited in select()
// for more data on the socket while compressed input it had already read was still waiting to
// be inflated, so once the client had sent everything the reply came only after
// clienttimeout.
func TestCompressUpload(t *testing.T) {
	s := NewSite(t, SiteOptions{Readers: ihaveReaders})
	for _, verb := range []string{"POST", "IHAVE"} {
		for _, size := range uploadSizes {
			for _, mode := range []string{"onewrite", "linebyline"} {
				t.Run(verb+"/"+size.name+"/"+mode, func(t *testing.T) {
					c := s.Reader(t)
					c.Compress()
					compressedUpload(t, c, verb, size.lines, size.width, mode == "linebyline")
				})
			}
		}
	}
}

// TestCompressUploadTLS: the large articles of TestCompressUpload over COMPRESS after
// STARTTLS. nnrpd reads TLS without select(), so INN before 2.7.2 did not hang here.
func TestCompressUploadTLS(t *testing.T) {
	s := NewSite(t, SiteOptions{Readers: ihaveReaders, TLS: true})
	large := uploadSizes[len(uploadSizes)-1]
	for _, verb := range []string{"POST", "IHAVE"} {
		for _, mode := range []string{"onewrite", "linebyline"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
				tc, _ := startTLS(t, s, s.Reader(t))
				tc.Compress()
				compressedUpload(t, tc, verb, large.lines, large.width, mode == "linebyline")
			})
		}
	}
}

// compressedUpload sends an article of lines body lines of width bytes (every 7th starting
// with a dot) with verb (POST or IHAVE) on the compressed c, in one write or line by line,
// expects 240 or 235 within 15 seconds, reads the body back and quits.
func compressedUpload(t *testing.T, c *client.Conn, verb string, lines, width int, lineByLine bool) {
	t.Helper()
	body := make([]string, lines)
	for i := range body {
		l := fmt.Sprintf("%d ", i)
		body[i] = strings.Repeat(l, width/len(l)+1)[:width]
		if i%7 == 0 {
			body[i] = "." + body[i][1:]
		}
	}
	id := msgID(t)
	if verb == "IHAVE" {
		expect(t, c, 335, "IHAVE %s", id)
	} else {
		expect(t, c, 340, "POST")
	}
	raw := wire(newArticle(id, "e2e.test", "compressed "+t.Name(), body...))
	if lineByLine {
		for l := range strings.Lines(raw) {
			send(t, c, l)
		}
	} else {
		send(t, c, raw)
	}
	const wait = 15 * time.Second
	c.Deadline = wait
	code, line, err := c.ReadStatus()
	c.Deadline = 0
	if err != nil {
		t.Fatalf("%s of %d bytes over COMPRESS: no reply within %v (%v): nnrpd waits for "+
			"socket data while compressed input is still pending (issue #269, fixed in 2.7.2)",
			verb, len(raw), wait, err)
	}
	if want := map[string]int{"POST": 240, "IHAVE": 235}[verb]; code != want {
		t.Fatalf("%s over COMPRESS: got %q, want %d", verb, line, want)
	}
	expect(t, c, 222, "BODY %s", id)
	if got := c.ReadDotLines(); !slices.Equal(got, body) {
		t.Errorf("BODY over COMPRESS differs from the sent body (%d lines, want %d)", len(got), len(body))
	}
	expect(t, c, 205, "QUIT")
}

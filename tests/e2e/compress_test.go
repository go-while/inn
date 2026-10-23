package e2e

// COMPRESS DEFLATE (RFC 8054) with nnrpd.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
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

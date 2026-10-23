package e2e

// innd's per-connection statistics (InterNetNews/inn#366).

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAcceptedSizeLongConnection: the "accepted size" innd logs when a feeding connection
// closes is the exact sum of the sizes of the articles it accepted, also beyond 16 MiB. innd
// kept these counters in a float before 2.8.0, which counts bytes exactly only up to 2^24, so
// long-running feeds were reported too small (#366).
func TestAcceptedSizeLongConnection(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	p := s.Peer(t)
	expect(t, p, 203, "MODE STREAM")

	// About 40 MB in odd sizes, so that a float sum has to round.
	const articles = 1300
	const batch = 50
	sent := 0
	for first := 0; first < articles; first += batch {
		var raw strings.Builder
		n := min(batch, articles-first)
		for i := first; i < first+n; i++ {
			id := fmt.Sprintf("<size.%d.%d@%s>", i, time.Now().UnixNano(), PathHost)
			body := make([]string, 400+i%97)
			for j := range body {
				body[j] = strings.Repeat("x", 60+(i*7+j)%23)
			}
			raw.WriteString("TAKETHIS " + id + "\r\n")
			raw.WriteString(wire(newArticle(id, "e2e.test", fmt.Sprintf("size %d", i), body...)))
		}
		sent += raw.Len()
		send(t, p, raw.String())
		for range n {
			status(t, p, 239, "TAKETHIS")
		}
	}
	expect(t, p, 205, "QUIT")
	t.Logf("sent %d articles, %d bytes on the wire", articles, sent)

	// What innd counted per article: the :bytes field of the overview.
	r := s.Reader(t)
	group := expect(t, r, 211, "GROUP e2e.test")
	if field(group, 1) != strconv.Itoa(articles) {
		t.Fatalf("GROUP: %q, want %d articles", group, articles)
	}
	expect(t, r, 224, "OVER 1-%d", articles)
	var want uint64
	for _, l := range r.ReadDotLines() {
		f := strings.Split(l, "\t")
		n, err := strconv.ParseUint(f[6], 10, 64)
		if err != nil {
			t.Fatalf("OVER line %q: :bytes %v", l, err)
		}
		want += n
	}

	closed := regexp.MustCompile(`^` + regexp.QuoteMeta(PeerIP) + `:\d+ closed .* accepted (\d+) .* accepted size (\d+) `)
	var got string
	waitFor(t, 10*time.Second, "innd's \"closed\" log line of the feeding connection", func() bool {
		for _, l := range s.Syslog(t) {
			if m := closed.FindStringSubmatch(l); m != nil && m[1] == strconv.Itoa(articles) {
				got = m[2]
				return true
			}
		}
		return false
	})
	if got != strconv.FormatUint(want, 10) {
		t.Errorf("innd logged accepted size %s for %d articles; the sum of their :bytes is %d", got, articles, want)
	}
}

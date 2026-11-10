package e2e

// The Xref header field innd generates for crossposted articles.

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// xrefInitialBuffer is the size innd first allocates for the Xref header field body it
// generates (MED_BUFFER * 2 + 1 in ARTassignnumbers); it adds MED_BUFFER whenever the next
// group would not fit.
const xrefInitialBuffer = 1024*2 + 1

// xrefNames returns n group names: prefix followed by a number of width digits.
func xrefNames(prefix string, width, n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("%s%0*d", prefix, width, i)
	}
	return names
}

var (
	// xrefLong are 450 groups of 49 characters: 40 of them make an Xref field body of
	// about 2090 octets, 400 about 21 KB.
	xrefLong = xrefNames("org.kernel.vger.linux-xref-reallocation-test.", 4, 450)
	// xrefJesse are 74 groups of 40 characters, an Xref field body of about 3.2 KB, like
	// the crosspost of the report behind #369 (74 groups of org.kernel.vger.linux-*).
	xrefJesse = xrefNames("org.kernel.vger.linux-jesse-crosspost.", 2, 74)
)

// xrefArticle is one crosspost of TestXrefReallocation.
type xrefArticle struct {
	groups  []string
	perLine int // groups per line of the folded Newsgroups header field; 0: up to 900 octets
}

// newsgroups returns the body of the Newsgroups header field of a, folded after a comma.
func (a xrefArticle) newsgroups() string {
	var lines []string
	var line []string
	width := 0
	for _, g := range a.groups {
		if len(line) > 0 && ((a.perLine > 0 && len(line) == a.perLine) ||
			(a.perLine == 0 && width+1+len(g) > 900)) {
			lines = append(lines, strings.Join(line, ","))
			line, width = nil, 0
		}
		line = append(line, g)
		width += 1 + len(g)
	}
	lines = append(lines, strings.Join(line, ","))
	return strings.Join(lines, ",\r\n ")
}

// xrefLen returns the length of the Xref header field body innd generates for groups, not
// counting the folding: pathhost, then " group:number" for each group.
func xrefLen(groups []string, numbers map[string]int) int {
	n := len(PathHost)
	for _, g := range groups {
		n += 1 + len(g) + 1 + len(strconv.Itoa(numbers[g]))
	}
	return n
}

// TestXrefReallocation: innd grows the buffer of the Xref header field body when the
// groups of a crosspost do not fit in it (2049 octets at first), and still stores and feeds
// the article with the right groups (#369). ARTassignnumbers kept a pointer into the buffer
// from before the reallocation and set the replication data (the groups part of Xref) from
// it: SMstore matched storage.conf against freed memory with storeonxref (the default),
// which could fail with "no matching entry in storage.conf" and throttle innd, and the R
// flag of newsfeeds fed that memory. With CNFS and newsgroups: * as in the report, and with
// tradspool, each crosspost must get 235 by IHAVE with innd still running, an Xref with
// every group once and its next number, be readable in every group, and appear with the
// same groups in a WmR file feed. Each case runs on its own innd; the last one sends larger
// crossposts on one connection, so innd reuses and grows the buffer it already grew. A
// heap-use-after-free shows only with AddressSanitizer or when the freed memory has been
// reused; build innd with -fsanitize=address to see it every time.
func TestXrefReallocation(t *testing.T) {
	knownIssue(t, "innd reads the groups of a crosspost from the freed Xref buffer after "+
		"growing it (InterNetNews/inn#369, not merged)")
	cases := []struct {
		name string
		arts []xrefArticle
	}{
		{"just-over-initial-buffer", []xrefArticle{{groups: xrefLong[:40]}}},
		{"74-groups-folded-per-group", []xrefArticle{{groups: xrefJesse, perLine: 1}}},
		{"several-reallocations", []xrefArticle{{groups: xrefLong[:400]}}},
		{"grown-buffer-reused", []xrefArticle{
			{groups: xrefLong[:200]}, // grows the buffer to about 11 KB
			{groups: xrefLong[:200]}, // fits in it
			{groups: xrefLong},       // grows it again
			{groups: xrefLong[:40]},  // a small one in the large buffer
		}},
	}
	for _, storage := range []string{"cnfs", "tradspool"} {
		t.Run(storage, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) { testXrefCase(t, storage, tc.arts) })
			}
		})
	}
}

// xrefCNFSSize is the size of the CNFS buffer of TestXrefReallocation, in KB.
const xrefCNFSSize = 4096

// xrefCNFSBuffer creates a zeroed CNFS buffer file in a short directory of its own
// (cycbuff.conf takes paths of at most 63 characters).
func xrefCNFSBuffer(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "e2e-xref-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "cnfs")
	if len(path) > 63 {
		t.Skipf("buffer path %s is longer than 63 characters: set TMPDIR to a shorter directory", path)
	}
	if err := os.WriteFile(path, make([]byte, xrefCNFSSize<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testXrefCase(t *testing.T, storage string, arts []xrefArticle) {
	etc := map[string]string{}
	if storage == "cnfs" {
		etc["storage.conf"] = "method cnfs {\n    newsgroups: *\n    class: 0\n    options: TEXT\n}\n"
		etc["cycbuff.conf"] = fmt.Sprintf("cycbuff:ONE:%s:%d\nmetacycbuff:TEXT:ONE\n",
			xrefCNFSBuffer(t), xrefCNFSSize)
	} else {
		etc["storage.conf"] = "method tradspool {\n    newsgroups: *\n    class: 0\n}\n"
	}
	groups := []Group{{Name: "e2e.test"}}
	for _, g := range slices.Concat(xrefLong, xrefJesse) {
		groups = append(groups, Group{Name: g})
	}
	s := NewSite(t, SiteOptions{
		Groups:    groups,
		Etc:       etc,
		Newsfeeds: "ME:*::\nreplic:*:Tf,WmR:\n", // message-id and replication data
	})

	type sent struct {
		id      string
		numbers map[string]int // group -> article number
	}
	var all []sent
	next := map[string]int{}
	p := s.Peer(t)
	for i, a := range arts {
		id := msgID(t)
		numbers := map[string]int{}
		for _, g := range a.groups {
			next[g]++
			numbers[g] = next[g]
		}
		if i == 0 && xrefLen(a.groups, numbers) <= xrefInitialBuffer {
			t.Fatalf("test error: the first crosspost does not cross the initial Xref buffer")
		}
		t.Logf("article %d: %d groups, Xref field body of about %d octets, Newsgroups of %d",
			i+1, len(a.groups), xrefLen(a.groups, numbers), len(a.newsgroups()))
		expect(t, p, 335, "IHAVE %s", id)
		send(t, p, wire(newArticle(id, a.newsgroups(), fmt.Sprintf("crosspost %d", i+1),
			"body of "+id)))
		status(t, p, 235, fmt.Sprintf("IHAVE %s (article %d, %d groups)", id, i+1, len(a.groups)))
		if mode := s.Ctlinnd(t, "mode"); !strings.Contains(mode, "Server running") {
			t.Fatalf("ctlinnd mode after article %d:\n%s", i+1, mode)
		}
		all = append(all, sent{id, numbers})
	}
	expect(t, p, 205, "QUIT")

	r := s.Reader(t)
	for i, a := range all {
		expect(t, r, 221, "HEAD %s", a.id)
		var xref string // unfolded
		inXref := false
		for _, l := range r.ReadDotLines() {
			switch {
			case strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t"):
				if inXref {
					xref += " " + strings.TrimSpace(l)
				}
			case strings.HasPrefix(strings.ToLower(l), "xref:"):
				if xref != "" {
					t.Errorf("article %d: two Xref header fields", i+1)
				}
				xref, inXref = strings.TrimSpace(l[len("xref:"):]), true
			default:
				inXref = false
			}
		}
		f := strings.Fields(xref)
		if len(f) == 0 || f[0] != PathHost {
			t.Fatalf("article %d: Xref %.200q does not start with %s", i+1, xref, PathHost)
		}
		got := map[string]int{}
		for _, e := range f[1:] {
			g, n, ok := strings.Cut(e, ":")
			num, err := strconv.Atoi(n)
			if !ok || err != nil {
				t.Errorf("article %d: Xref entry %.100q is not group:number", i+1, e)
				continue
			}
			if _, dup := got[g]; dup {
				t.Errorf("article %d: Xref lists %s twice", i+1, g)
			}
			got[g] = num
		}
		if !maps.Equal(got, a.numbers) {
			t.Errorf("article %d: Xref has %d entries, want %d groups with their numbers; Xref: %.300q",
				i+1, len(got), len(a.numbers), xref)
		}
		for _, g := range slices.Sorted(maps.Keys(a.numbers)) {
			expect(t, r, 211, "GROUP %s", g)
			want := fmt.Sprintf("223 %d %s", a.numbers[g], a.id)
			if line := expect(t, r, 223, "STAT %d", a.numbers[g]); !strings.HasPrefix(line, want) {
				t.Errorf("article %d in %s: %q, want %q", i+1, g, line, want)
			}
		}
		expect(t, r, 222, "BODY %s", a.id)
		if body := r.ReadDotLines(); !slices.Equal(body, []string{"body of " + a.id}) {
			t.Errorf("article %d: body %.200q", i+1, body)
		}
	}
	expect(t, r, 205, "QUIT")

	// The file feed has, for each article, its message-id and the groups part of its Xref
	// (folded like the field, so an entry may span lines).
	s.Ctlinnd(t, "flush", "replic")
	feed := map[string][]string{}
	var order []string
	waitFor(t, 10*time.Second, "the replic file feed", func() bool {
		b, err := os.ReadFile(filepath.Join(s.Dir, "spool", "outgoing", "replic"))
		if err != nil {
			return false
		}
		feed, order = map[string][]string{}, nil
		var cur string
		for _, tok := range strings.Fields(string(b)) {
			if strings.HasPrefix(tok, "<") && strings.HasSuffix(tok, ">") {
				cur = tok
				order = append(order, cur)
				continue
			}
			feed[cur] = append(feed[cur], tok)
		}
		return len(order) >= len(all)
	})
	if len(order) != len(all) || len(feed[""]) > 0 {
		t.Errorf("replic file feed: %d message-ids, want %d; %d entries before the first",
			len(order), len(all), len(feed[""]))
	}
	for i, a := range all {
		got := map[string]int{}
		for _, e := range feed[a.id] {
			g, n, _ := strings.Cut(e, ":")
			num, _ := strconv.Atoi(n)
			got[g] = num
		}
		if !maps.Equal(got, a.numbers) || len(feed[a.id]) != len(a.numbers) {
			t.Errorf("article %d: the R feed data has %d entries, want the %d groups with their numbers: %.300q",
				i+1, len(feed[a.id]), len(a.numbers), strings.Join(feed[a.id], " "))
		}
	}
}

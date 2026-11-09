package e2e

// INN as the client: innxmit, innfeed, getlist and pullnews against fake servers.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
)

// collector records the message-ids a fake peer accepted, in order.
type collector struct {
	mu  sync.Mutex
	ids []string
}

func (c *collector) add(id string, _ []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, id)
}

func (c *collector) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.ids)
}

func (c *collector) has(ids ...string) bool {
	got := c.list()
	for _, id := range ids {
		if !slices.Contains(got, id) {
			return false
		}
	}
	return true
}

// newPeer starts a fake transit peer (nntp-testkit PeerHandler) that records what it accepts.
func newPeer(tb testing.TB, o *fakeserver.PeerOptions) (*fakeserver.Server, *collector) {
	tb.Helper()
	col := &collector{}
	o.OnArticle = col.add
	return fakeserver.New(tb, fakeserver.Options{Handler: fakeserver.PeerHandler(o)}), col
}

// xmitFeeds writes the token and message-id of every article to the batch file "fake".
const xmitFeeds = "ME:*::\nfake:*:Tf,Wnm:\n"

// flushBatch flushes the batch file of site to disk and returns its lines.
func (s *Site) flushBatch(tb testing.TB, site string) []string {
	tb.Helper()
	s.Ctlinnd(tb, "flush", site)
	return s.batch(tb, site)
}

// batch returns the lines of the batch file of site; a missing file has none.
func (s *Site) batch(tb testing.TB, site string) []string {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join(s.Dir, "spool", "outgoing", site))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		tb.Fatal(err)
	}
	return strings.Fields(strings.ReplaceAll(string(b), " ", "_"))
}

// innxmit runs innxmit against the fake peer with the batch file "fake".
func (s *Site) innxmit(tb testing.TB, peer *fakeserver.Server, args ...string) string {
	tb.Helper()
	args = append(append([]string{"-P", strconv.Itoa(peer.Port())}, args...), "127.0.0.1", "fake")
	return s.Run(tb, "innxmit", args...)
}

// TestInnxmitStreaming: innxmit streams a batch to a peer that offers MODE STREAM, and the
// batch file is empty afterwards.
func TestInnxmitStreaming(t *testing.T) {
	peer, got := newPeer(t, &fakeserver.PeerOptions{})
	s := NewSite(t, SiteOptions{Newsfeeds: xmitFeeds})
	ids := s.Inject(t, "e2e.test", 3)
	if b := s.flushBatch(t, "fake"); len(b) != 3 {
		t.Fatalf("batch file: %q, want 3 entries", b)
	}
	s.innxmit(t, peer)
	if !slices.Equal(got.list(), ids) {
		t.Errorf("peer accepted %q, want %q", got.list(), ids)
	}
	if n := peer.Count("TAKETHIS"); n != 3 {
		t.Errorf("TAKETHIS sent %d times, want 3 (counts %v)", n, peer.Counts())
	}
	if n := peer.Count("IHAVE"); n != 0 {
		t.Errorf("IHAVE sent %d times while streaming", n)
	}
	if b := s.batch(t, "fake"); len(b) != 0 {
		t.Errorf("batch file after innxmit: %q", b)
	}
}

// TestInnxmitIHAVEFallback: a peer that refuses MODE STREAM gets the batch with IHAVE.
func TestInnxmitIHAVEFallback(t *testing.T) {
	peer, got := newPeer(t, &fakeserver.PeerOptions{NoStream: true})
	s := NewSite(t, SiteOptions{Newsfeeds: xmitFeeds})
	ids := s.Inject(t, "e2e.test", 3)
	s.flushBatch(t, "fake")
	s.innxmit(t, peer)
	if !slices.Equal(got.list(), ids) {
		t.Errorf("peer accepted %q, want %q", got.list(), ids)
	}
	if n := peer.Count("IHAVE"); n != 3 {
		t.Errorf("IHAVE sent %d times, want 3 (counts %v)", n, peer.Counts())
	}
	if n := peer.Count("TAKETHIS") + peer.Count("CHECK"); n != 0 {
		t.Errorf("CHECK/TAKETHIS sent %d times to a peer without streaming", n)
	}
	if b := s.batch(t, "fake"); len(b) != 0 {
		t.Errorf("batch file after innxmit: %q", b)
	}
}

// TestInnxmitRefused: an article the peer has (438) and one it rejects after the transfer
// (439) are dropped from the batch, not retried.
func TestInnxmitRefused(t *testing.T) {
	o := &fakeserver.PeerOptions{Have: map[string]bool{}, Reject: map[string]bool{}}
	peer, got := newPeer(t, o)
	s := NewSite(t, SiteOptions{Newsfeeds: xmitFeeds})
	ids := s.Inject(t, "e2e.test", 3)
	o.Have[ids[0]] = true
	o.Reject[ids[1]] = true
	s.flushBatch(t, "fake")
	s.innxmit(t, peer)
	if want := ids[2:]; !slices.Equal(got.list(), want) {
		t.Errorf("peer accepted %q, want %q", got.list(), want)
	}
	if b := s.batch(t, "fake"); len(b) != 0 {
		t.Errorf("batch file after innxmit: %q, want the refused articles dropped", b)
	}
}

// TestInnxmitDeferred: when the peer defers an article (IHAVE 436), innxmit takes it as a
// peer out of space: it requeues that article and the rest of the batch and exits 1. The next
// run sends them.
func TestInnxmitDeferred(t *testing.T) {
	o := &fakeserver.PeerOptions{NoStream: true, Defer436Once: map[string]bool{}}
	peer, got := newPeer(t, o)
	s := NewSite(t, SiteOptions{Newsfeeds: xmitFeeds})
	ids := s.Inject(t, "e2e.test", 3)
	o.Defer436Once[ids[1]] = true
	s.flushBatch(t, "fake")
	out, err := s.TryRun("innxmit", "-P", strconv.Itoa(peer.Port()), "127.0.0.1", "fake")
	if err == nil {
		t.Errorf("first run: innxmit exited 0 after a 436, want 1\n%s", out)
	}
	if want := ids[:1]; !slices.Equal(got.list(), want) {
		t.Errorf("first run: peer accepted %q, want %q", got.list(), want)
	}
	left := strings.Join(s.batch(t, "fake"), "\n")
	if strings.Contains(left, ids[0]) || !strings.Contains(left, ids[1]) || !strings.Contains(left, ids[2]) {
		t.Fatalf("batch file after the first run:\n%s\nwant %s and %s requeued", left, ids[1], ids[2])
	}
	s.innxmit(t, peer)
	if want := []string{ids[0], ids[1], ids[2]}; !slices.Equal(got.list(), want) {
		t.Errorf("second run: peer accepted %q, want %q", got.list(), want)
	}
	if b := s.batch(t, "fake"); len(b) != 0 {
		t.Errorf("batch file after the second run: %q", b)
	}
}

// postArticle returns the lines of a native article for innxmit -R, as innd stores it after
// a local posting: with the header fields a reader server refuses or sets itself (some of
// them folded), a folded kept field, and a body with a line that needs dot-stuffing.
func postArticle(id, subject string) []string {
	return []string{
		"Path: e2e.local!e2e.local.POSTED.client.example!not-for-mail",
		"From: E2E Test <e2e@example.invalid>",
		"Newsgroups: e2e.test,e2e.other",
		"Subject: " + subject,
		"Date: Mon, 02 Nov 2026 10:00:00 +0000",
		"Message-ID: " + id,
		"References: <first@example.invalid>",
		" <second@example.invalid>",
		"Injection-Date: Mon, 02 Nov 2026 10:00:01 +0000",
		"Injection-Info: e2e.local; posting-host=\"192.0.2.1\";",
		" logging-data=\"1234\"; mail-complaints-to=\"abuse@example.invalid\"",
		"NNTP-Posting-Host: 192.0.2.1",
		"NNTP-Posting-Date: Mon, 02 Nov 2026 10:00:01 +0000",
		"X-Trace: abcdef",
		"\tghijkl",
		"X-Complaints-To: abuse@example.invalid",
		"Xref: e2e.local e2e.test:12 e2e.other:3",
		"X-Kept: yes",
		"",
		"first line of " + id,
		".a line starting with a dot",
		"last line",
	}
}

// postBatch writes the articles of ids into the site and the batch file "fake" with
// "path id" lines, and returns the lines of each article as innxmit reads them.
func (s *Site) postBatch(tb testing.TB, ids ...string) [][]string {
	tb.Helper()
	var batch strings.Builder
	var arts [][]string
	for i, id := range ids {
		lines := postArticle(id, fmt.Sprintf("post %d", i+1))
		path := filepath.Join(s.Dir, fmt.Sprintf("post-%d.art", i+1))
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
		fmt.Fprintf(&batch, "%s %s\n", path, id)
		arts = append(arts, lines)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "spool", "outgoing", "fake"), []byte(batch.String()), 0o644); err != nil {
		tb.Fatal(err)
	}
	return arts
}

// postedStripped lists the header fields innxmit -R removes from postArticle.
var postedStripped = []string{
	"Path", "Injection-Info", "NNTP-Posting-Host", "NNTP-Posting-Date",
	"X-Trace", "X-Complaints-To", "Xref",
}

// checkPosted checks that wire (the lines of an article the server received, dot-stuffed)
// is the article lines without the stripped header fields and their continuation lines,
// and with an unchanged body.
func checkPosted(t *testing.T, wire, lines []string) {
	t.Helper()
	wire = slices.DeleteFunc(slices.Clone(wire), func(l string) bool { return l == "." })
	head, body := split(lines)
	var want []string
	skip := false
	for _, l := range head {
		if l[0] != ' ' && l[0] != '\t' {
			name, _, _ := strings.Cut(l, ":")
			skip = slices.Contains(postedStripped, name)
		}
		if !skip {
			want = append(want, l)
		}
	}
	want = append(want, "")
	for _, l := range body {
		if strings.HasPrefix(l, ".") {
			l = "." + l
		}
		want = append(want, l)
	}
	if !slices.Equal(wire, want) {
		t.Errorf("server received:\n%s\nwant:\n%s", strings.Join(wire, "\n"), strings.Join(want, "\n"))
	}
}

// postServer starts a fake reader server that serves st.
func postServer(tb testing.TB, st *fakeserver.Store, o fakeserver.StoreOptions, f fakeserver.Faults) *fakeserver.Server {
	tb.Helper()
	return fakeserver.New(tb, fakeserver.Options{Store: st, StoreOpts: o, Faults: f})
}

// TestInnxmitPost: innxmit -R posts a batch to a reader server that needs AUTHINFO (from
// passwd.nntp), with POST and without MODE STREAM and IHAVE. The header fields a reader
// server refuses or sets itself (Path, Xref, Injection-Info, NNTP-Posting-*, X-Trace,
// X-Complaints-To, with their folded lines) are removed, the others and the body are
// unchanged. An article the server has already (441) is dropped from the batch.
func TestInnxmitPost(t *testing.T) {
	st := fakeserver.NewStore()
	srv := postServer(t, st, fakeserver.StoreOptions{User: "feeder", Pass: "secret"}, fakeserver.Faults{})
	s := NewSite(t, SiteOptions{
		Newsfeeds: xmitFeeds,
		Etc:       map[string]string{"passwd.nntp": "127.0.0.1:feeder:secret\n"},
	})
	ids := []string{msgID(t), msgID(t), msgID(t)}
	arts := s.postBatch(t, ids...)
	// The server holds the second article already.
	st.AddArticle("e2e.test", fakeserver.Article{MessageID: ids[1], Headers: []string{"Message-ID: " + ids[1]}, Body: []string{"old"}})

	s.innxmit(t, srv, "-R", "-l")

	posted := srv.Articles()
	if len(posted) != 3 {
		t.Fatalf("server received %d articles, want 3 (counts %v)", len(posted), srv.Counts())
	}
	checkPosted(t, posted[0], arts[0])
	checkPosted(t, posted[2], arts[2])
	if n := srv.Count("POST"); n != 3 {
		t.Errorf("POST sent %d times, want 3 (counts %v)", n, srv.Counts())
	}
	if n := srv.Count("IHAVE") + srv.Count("MODE") + srv.Count("CHECK") + srv.Count("TAKETHIS"); n != 0 {
		t.Errorf("IHAVE, MODE, CHECK or TAKETHIS sent %d times in POST mode (counts %v)", n, srv.Counts())
	}
	if n := srv.Count("AUTHINFO"); n != 2 {
		t.Errorf("AUTHINFO sent %d times, want 2 (counts %v)", n, srv.Counts())
	}
	for _, id := range []string{ids[0], ids[2]} {
		if _, _, ok := st.ByMessageID(id); !ok {
			t.Errorf("%s is not on the server", id)
		}
	}
	if b := s.batch(t, "fake"); len(b) != 0 {
		t.Errorf("batch file after innxmit: %q, want the posted and the refused article dropped", b)
	}
}

// TestInnxmitPostDeferred: a 440 to POST (posting is not possible now) makes innxmit keep that
// article and the rest of the batch and exit 1; the next run posts them.
func TestInnxmitPostDeferred(t *testing.T) {
	st := fakeserver.NewStore()
	down := postServer(t, st, fakeserver.StoreOptions{}, fakeserver.Faults{
		Override:      map[string][]string{"POST": {"440 Posting not allowed"}},
		OverrideFrom:  map[string]int{"POST": 2},
		OverrideTimes: map[string]int{"POST": 1},
	})
	up := postServer(t, st, fakeserver.StoreOptions{}, fakeserver.Faults{})
	s := NewSite(t, SiteOptions{Newsfeeds: xmitFeeds})
	ids := []string{msgID(t), msgID(t), msgID(t)}
	s.postBatch(t, ids...)

	out, err := s.TryRun("innxmit", "-P", strconv.Itoa(down.Port()), "-R", "127.0.0.1", "fake")
	if err == nil {
		t.Errorf("first run: innxmit exited 0 after a 440, want 1\n%s", out)
	}
	if _, _, ok := st.ByMessageID(ids[0]); !ok {
		t.Errorf("first run: %s was not posted", ids[0])
	}
	left := strings.Join(s.batch(t, "fake"), "\n")
	if strings.Contains(left, ids[0]) || !strings.Contains(left, ids[1]) || !strings.Contains(left, ids[2]) {
		t.Fatalf("batch file after the first run:\n%s\nwant %s and %s requeued", left, ids[1], ids[2])
	}

	s.innxmit(t, up, "-R")
	for _, id := range ids {
		if _, _, ok := st.ByMessageID(id); !ok {
			t.Errorf("second run: %s is not on the server", id)
		}
	}
	if n := len(st.Posted()); n != 3 {
		t.Errorf("server holds %d posted articles, want 3", n)
	}
	if b := s.batch(t, "fake"); len(b) != 0 {
		t.Errorf("batch file after the second run: %q", b)
	}
}

// TestInnxmitPostNoAuth: a server that wants AUTHINFO and has no entry in passwd.nntp answers
// 480 to POST: innxmit stops with the batch file untouched.
func TestInnxmitPostNoAuth(t *testing.T) {
	st := fakeserver.NewStore()
	srv := postServer(t, st, fakeserver.StoreOptions{User: "feeder", Pass: "secret"}, fakeserver.Faults{})
	s := NewSite(t, SiteOptions{Newsfeeds: xmitFeeds})
	ids := []string{msgID(t), msgID(t)}
	s.postBatch(t, ids...)
	before := s.ReadFile(t, "spool/outgoing/fake")

	out, err := s.TryRun("innxmit", "-P", strconv.Itoa(srv.Port()), "-R", "127.0.0.1", "fake")
	if err == nil {
		t.Errorf("innxmit exited 0 after a 480\n%s", out)
	}
	if n := len(st.Posted()); n != 0 {
		t.Errorf("server holds %d posted articles, want 0", n)
	}
	if n := srv.Count("POST"); n != 1 {
		t.Errorf("POST sent %d times, want 1 (counts %v)", n, srv.Counts())
	}
	if after := s.ReadFile(t, "spool/outgoing/fake"); after != before {
		t.Errorf("batch file changed:\n%s\nwas:\n%s", after, before)
	}
}

// TestNntpsendPost: nntpsend -R, on its command line or in the fourth field of nntpsend.ctl,
// runs innxmit -R: the articles innd spooled reach a reader server with POST, without the
// Path and Xref header fields innd gave them.
func TestNntpsendPost(t *testing.T) {
	for _, tc := range []struct {
		name string
		args func(port int) []string
		ctl  func(port int) string
	}{
		{"commandline", func(port int) []string {
			return []string{"-R", "-P", strconv.Itoa(port), "fake", "127.0.0.1"}
		}, nil},
		{"ctlfile", func(int) []string { return nil }, func(port int) string {
			return fmt.Sprintf("fake:127.0.0.1:10m:-R -P%d\n", port)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := fakeserver.NewStore()
			srv := postServer(t, st, fakeserver.StoreOptions{}, fakeserver.Faults{})
			s := NewSite(t, SiteOptions{Newsfeeds: xmitFeeds})
			if tc.ctl != nil {
				s.WriteEtc(t, "nntpsend.ctl", tc.ctl(srv.Port()))
			}
			ids := s.Inject(t, "e2e.test", 2)
			s.flushBatch(t, "fake")
			out := s.Run(t, "nntpsend", tc.args(srv.Port())...)
			// nntpsend writes to pathlog/nntpsend.log, innxmit runs in the background of it.
			log, _ := os.ReadFile(filepath.Join(s.Dir, "log", "nntpsend.log"))
			posted := srv.Articles()
			if len(posted) != 2 {
				t.Fatalf("server received %d articles, want 2 (counts %v)\n%s\n%s", len(posted), srv.Counts(), out, log)
			}
			for i, a := range posted {
				for _, l := range a {
					if strings.HasPrefix(l, "Path:") || strings.HasPrefix(l, "Xref:") {
						t.Errorf("article %d has %q", i+1, l)
					}
				}
			}
			if n := srv.Count("POST"); n != 2 || srv.Count("IHAVE")+srv.Count("TAKETHIS") != 0 {
				t.Errorf("commands sent: %v, want 2 POST only", srv.Counts())
			}
			for _, id := range ids {
				if _, _, ok := st.ByMessageID(id); !ok {
					t.Errorf("%s is not on the server\n%s", id, log)
				}
			}
		})
	}
}

// innfeedSite starts a site whose innfeed feeds every article to the fake peer.
func innfeedSite(tb testing.TB, peer *fakeserver.Server) *Site {
	tb.Helper()
	return NewSite(tb, SiteOptions{
		Newsfeeds: "ME:*::\ninnfeed!:!*:Tc,Wnm*:@bindir@/innfeed\nfake:*:Tm:innfeed!\n",
		Etc: map[string]string{"innfeed.conf": fmt.Sprintf(
			"peer fake {\n    ip-name: \"127.0.0.1\"\n    port-number: %d\n}\n", peer.Port())},
	})
}

// TestInnfeedStreaming: innd hands new articles to innfeed, which streams them to the peer.
func TestInnfeedStreaming(t *testing.T) {
	peer, got := newPeer(t, &fakeserver.PeerOptions{})
	s := innfeedSite(t, peer)
	ids := s.Inject(t, "e2e.test", 3)
	waitFor(t, 30*time.Second, "innfeed to deliver 3 articles", func() bool { return got.has(ids...) })
	if n := peer.Count("IHAVE"); n != 0 {
		t.Errorf("IHAVE sent %d times while streaming (counts %v)", n, peer.Counts())
	}
}

// TestInnfeedIHAVEFallback: innfeed falls back to IHAVE when the peer refuses MODE STREAM.
func TestInnfeedIHAVEFallback(t *testing.T) {
	peer, got := newPeer(t, &fakeserver.PeerOptions{NoStream: true})
	s := innfeedSite(t, peer)
	ids := s.Inject(t, "e2e.test", 3)
	waitFor(t, 30*time.Second, "innfeed to deliver 3 articles", func() bool { return got.has(ids...) })
	if n := peer.Count("IHAVE"); n < 3 {
		t.Errorf("IHAVE sent %d times, want 3 (counts %v)", n, peer.Counts())
	}
}

// TestInnfeedDebugSnapshotFiles: the debug-file and snapshot-file keys of innfeed.conf name
// the file whose presence turns on debugging and the file SIGINT writes a snapshot to, so
// that several innfeed instances do not share innfeed.debug and innfeed.snapshot (issue
// #315). An absolute debug-file and a relative snapshot-file (under pathlog) are tested.
func TestInnfeedDebugSnapshotFiles(t *testing.T) {
	peer, got := newPeer(t, &fakeserver.PeerOptions{})
	conf := fmt.Sprintf("debug-file: \"@etcdir@/e2e.debug\"\nsnapshot-file: e2e.snapshot\n"+
		"peer fake {\n    ip-name: \"127.0.0.1\"\n    port-number: %d\n}\n", peer.Port())
	s := NewSite(t, SiteOptions{
		Newsfeeds: "ME:*::\ninnfeed!:!*:Tc,Wnm*:@bindir@/innfeed\nfake:*:Tm:innfeed!\n",
		Etc:       map[string]string{"innfeed.conf": conf, "e2e.debug": ""},
	})
	if out, err := s.TryRun("inncheck", "--noperm", "innfeed.conf="+filepath.Join(s.Dir, "etc", "innfeed.conf")); err != nil {
		t.Errorf("inncheck rejects debug-file or snapshot-file: %v\n%s", err, out)
	}

	ids := s.Inject(t, "e2e.test", 1)
	waitFor(t, 30*time.Second, "innfeed to deliver the article", func() bool { return got.has(ids...) })
	logFile := filepath.Join(s.Dir, "log", "innfeed.log")
	waitFor(t, 5*time.Second, "debug messages in innfeed.log", func() bool {
		b, _ := os.ReadFile(logFile)
		return strings.Contains(string(b), "Giving article to peer: fake")
	})

	pid, err := strconv.Atoi(strings.TrimSpace(s.ReadFile(t, "run/innfeed.pid")))
	if err != nil {
		t.Fatalf("run/innfeed.pid: %v", err)
	}
	if err := syscall.Kill(pid, syscall.SIGINT); err != nil {
		t.Fatalf("SIGINT to innfeed (pid %d): %v", pid, err)
	}
	snapshot := filepath.Join(s.Dir, "log", "e2e.snapshot")
	waitFor(t, 10*time.Second, "the snapshot in log/e2e.snapshot", func() bool {
		b, _ := os.ReadFile(snapshot)
		return strings.Contains(string(b), "System snapshot taken at")
	})
	if _, err := os.Stat(filepath.Join(s.Dir, "log", "innfeed.snapshot")); err == nil {
		t.Errorf("log/innfeed.snapshot written although snapshot-file is e2e.snapshot")
	}
}

// TestGetlist: getlist prints the LIST answers of a remote server unchanged.
func TestGetlist(t *testing.T) {
	st := fakeserver.NewStore()
	st.AddGroup("up.one", "First group.")
	st.AddGroup("up.two", "Second group.")
	st.AddGroup("other.three", "Third group.")
	st.AddArticle("up.one", fakeserver.Article{
		Headers: []string{"From: a@example.invalid", "Subject: s", "Newsgroups: up.one"},
		Body:    []string{"body"},
	})
	up := fakeserver.New(t, fakeserver.Options{Store: st})
	s := NewSite(t, SiteOptions{})
	port := strconv.Itoa(up.Port())

	for _, tc := range []struct{ name, cmd string }{
		{"active", "LIST ACTIVE"},
		{"active up.*", "LIST ACTIVE up.*"},
		{"newsgroups up.*", "LIST NEWSGROUPS up.*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := client.Dial(t, up.Addr())
			expect(t, c, 215, "%s", tc.cmd)
			want := c.ReadDotLines()
			args := append([]string{"-h", "127.0.0.1", "-p", port}, strings.Fields(tc.name)...)
			got := strings.Split(strings.TrimRight(s.Run(t, "getlist", args...), "\n"), "\n")
			if !slices.Equal(got, want) {
				t.Errorf("getlist %s: %q, want %q", tc.name, got, want)
			}
		})
	}
}

// TestPullnews: pullnews reads a group of an upstream server and feeds its articles to the
// site with IHAVE, then records the high-water mark.
func TestPullnews(t *testing.T) {
	st := fakeserver.NewStore()
	var ids []string
	for i := range 3 {
		id := msgID(t)
		ids = append(ids, id)
		st.AddArticle("e2e.test", fakeserver.Article{MessageID: id, Headers: []string{
			"Path: upstream.invalid!not-for-mail",
			"From: Upstream <up@example.invalid>",
			"Newsgroups: e2e.test",
			fmt.Sprintf("Subject: pulled %d", i+1),
			"Message-ID: " + id,
			"Date: " + time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 -0700"),
		}, Body: []string{"pulled body", ".dot line"}})
	}
	up := fakeserver.New(t, fakeserver.Options{Store: st})
	// pullnews connects from 127.0.0.1 and feeds with IHAVE: make it a peer.
	s := NewSite(t, SiteOptions{Incoming: "peer pullnews {\n    hostname: \"127.0.0.1, " + PeerIP + "\"\n}\n"})
	marks := filepath.Join(s.Dir, "pullnews.marks")
	s.writeFile(t, "pullnews.marks", fmt.Sprintf("127.0.0.1:%d\n    e2e.test\n", up.Port()))

	s.Run(t, "pullnews", "-c", marks, "-s", s.Addr, "-w", "0")

	r := s.ReaderMode(t)
	for _, id := range ids {
		expect(t, r, 222, "BODY %s", id)
		if got := r.ReadDotLines(); !slices.Equal(got, []string{"pulled body", ".dot line"}) {
			t.Errorf("BODY %s: %q", id, got)
		}
	}
	m := s.ReadFile(t, "pullnews.marks")
	mark := ""
	for _, l := range strings.Split(m, "\n") {
		if f := strings.Fields(l); len(f) == 3 && f[0] == "e2e.test" {
			mark = f[2]
		}
	}
	if mark != "3" {
		t.Errorf("pullnews.marks after the run:\n%s\nwant e2e.test with high-water mark 3", m)
	}
}

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

// syntaxPeer is a fake transit peer that cannot accept the one article whose Message-ID
// contains "POISON": it answers a CHECK or IHAVE of it with code (500 or 501) and no
// Message-ID, as servers that disagree about the Message-ID syntax do. It counts the offers
// of every article and records the ones it accepts.
//
// With delay, it waits that long before it answers each CHECK, so that innfeed has several
// CHECK and TAKETHIS commands waiting for their replies when the 500 or 501 comes. With
// takethis, it answers the CHECK of the article with 238 and its TAKETHIS with code, without
// reading the article: the article lines then come as commands, each answered with 500.
type syntaxPeer struct {
	code     string
	stream   bool
	delay    time.Duration
	takethis bool
	mu       sync.Mutex
	offers   map[string]int
	got      []string
}

func (p *syntaxPeer) offered(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.offers[id]
}

func (p *syntaxPeer) accepted(ids ...string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range ids {
		if !slices.Contains(p.got, id) {
			return false
		}
	}
	return true
}

func (p *syntaxPeer) handle(c *fakeserver.Conn, line string) ([]string, bool) {
	verb, arg, _ := strings.Cut(line, " ")
	switch strings.ToUpper(verb) {
	case "CAPABILITIES":
		r := []string{"101 capability list follows", "VERSION 2", "IHAVE"}
		if p.stream {
			r = append(r, "STREAMING")
		}
		return append(r, "."), false
	case "MODE":
		if p.stream && strings.EqualFold(arg, "STREAM") {
			return []string{"203 streaming ok"}, false
		}
		return []string{"501 Unknown MODE variant"}, false
	case "CHECK", "IHAVE":
		isCheck := strings.EqualFold(verb, "CHECK")
		if isCheck != p.stream {
			return []string{"500 unknown command"}, false
		}
		p.mu.Lock()
		p.offers[arg]++
		p.mu.Unlock()
		if isCheck && p.delay > 0 {
			time.Sleep(p.delay)
		}
		if strings.Contains(arg, "POISON") && !p.takethis {
			return []string{p.code + " syntax error"}, false
		}
		if isCheck {
			return []string{"238 " + arg}, false
		}
		if err := c.WriteLines("335 send it"); err != nil {
			return nil, true
		}
		if _, err := c.ReadDotBlock(); err != nil {
			return nil, true
		}
		p.mu.Lock()
		p.got = append(p.got, arg)
		p.mu.Unlock()
		return []string{"235 ok"}, false
	case "TAKETHIS":
		if p.takethis && strings.Contains(arg, "POISON") {
			return []string{p.code + " syntax error"}, false
		}
		if _, err := c.ReadDotBlock(); err != nil {
			return nil, true
		}
		p.mu.Lock()
		p.got = append(p.got, arg)
		p.mu.Unlock()
		return []string{"239 " + arg}, false
	case "QUIT":
		return []string{"205 bye"}, true
	}
	return []string{"500 unknown command"}, false
}

// TestInnfeedSyntaxError: a peer answers the CHECK (streaming) or IHAVE of one article with
// 500 or 501, without a Message-ID (#40). innfeed must drop that article instead of
// offering it again after every reconnect, and the other articles must get through.
//
// The pipelined case delays the CHECK replies, so that the 501 comes while TAKETHIS commands
// sent after the CHECK of the article still wait for their replies: the 501 belongs to the
// CHECK, the oldest command. The TAKETHIS case answers a TAKETHIS with 501 and does not read
// the article, so 500 replies to its lines follow; innfeed must not attribute them to the
// next TAKETHIS commands (and drop these articles).
func TestInnfeedSyntaxError(t *testing.T) {
	type syntaxCase struct {
		name     string
		code     string
		stream   bool
		delay    time.Duration
		takethis bool
	}
	var cases []syntaxCase
	for _, stream := range []bool{true, false} {
		for _, code := range []string{"501", "500"} {
			mode := "IHAVE"
			if stream {
				mode = "streaming"
			}
			cases = append(cases, syntaxCase{name: mode + "/" + code, code: code, stream: stream})
		}
	}
	cases = append(cases,
		syntaxCase{name: "streaming-pipelined/501", code: "501", stream: true, delay: 300 * time.Millisecond},
		syntaxCase{name: "streaming-takethis/501", code: "501", stream: true, delay: 300 * time.Millisecond, takethis: true})
	for _, tc := range cases {
		code := tc.code
		t.Run(tc.name, func(t *testing.T) {
			p := &syntaxPeer{code: code, stream: tc.stream, delay: tc.delay, takethis: tc.takethis, offers: map[string]int{}}
			srv := fakeserver.New(t, fakeserver.Options{Handler: fakeserver.LineHandler(p.handle)})
			s := NewSite(t, SiteOptions{
				Newsfeeds: "ME:*::\ninnfeed!:!*:Tc,Wnm*:@bindir@/innfeed\nfake:*:Tm:innfeed!\n",
				Etc: map[string]string{"innfeed.conf": fmt.Sprintf(
					"initial-reconnect-time: 1\nmax-reconnect-time: 1\nbacklog-rotate-period: 1\nbacklog-ckpt-period: 1\n"+
						"peer fake {\n    ip-name: \"127.0.0.1\"\n    port-number: %d\n    max-connections: 1\n}\n",
					srv.Port())},
			})
			ids := make([]string, 5)
			for i := range ids {
				ids[i] = msgID(t)
			}
			poison := strings.Replace(msgID(t), "<", "<POISON.", 1)
			ids[2] = poison
			pc := s.Peer(t)
			for i, id := range ids {
				expect(t, pc, 335, "IHAVE %s", id)
				send(t, pc, wire(newArticle(id, "e2e.test", fmt.Sprintf("syntax %d", i+1), "body of "+id)))
				status(t, pc, 235, "IHAVE "+id)
			}
			expect(t, pc, 205, "QUIT")

			waitFor(t, 30*time.Second, "innfeed to offer the article", func() bool { return p.offered(poison) > 0 })
			// After a 500 all connections sleep and innfeed keeps the articles that were
			// in flight in its backlog, which it only reads when a connection has done a
			// transfer. A later article provides it.
			time.Sleep(3 * time.Second)
			trigger := msgID(t)
			pc = s.Peer(t)
			expect(t, pc, 335, "IHAVE %s", trigger)
			send(t, pc, wire(newArticle(trigger, "e2e.test", "syntax trigger", "body of "+trigger)))
			status(t, pc, 235, "IHAVE "+trigger)
			expect(t, pc, 205, "QUIT")

			others := slices.Concat(ids[:2], ids[3:], []string{trigger})
			waitFor(t, 30*time.Second, "the other articles to reach the peer", func() bool { return p.accepted(others...) })
			// Give innfeed the time for a few reconnects (1 second each) in which it would
			// offer the article again, and stop early if it does.
			for end := time.Now().Add(5 * time.Second); time.Now().Before(end) && p.offered(poison) == 1; {
				time.Sleep(100 * time.Millisecond)
			}
			if n := p.offered(poison); n != 1 {
				t.Errorf("the article that got %s was offered %d times, want once and then dropped", code, n)
			}
			if p.accepted(poison) {
				t.Errorf("the peer accepted the article it answers %s for", code)
			}
		})
	}
}

// capsPeer is a fake transit peer for the CAPABILITIES tests of innfeed (#317). caps is its
// answer to CAPABILITIES (the reply lines, "." included, or a single error line); with split,
// it writes the first half of a multi-line answer, waits, and then writes the rest, so that
// innfeed reads it in two parts. mode is its answer to MODE STREAM. CHECK and TAKETHIS
// work only after a 203 to MODE STREAM; IHAVE always works. With user, AUTHINFO USER/PASS
// are required before CAPABILITIES lists STREAMING, as innd does. It records the commands
// it gets (verb, and the argument of MODE and AUTHINFO) and the articles it accepts.
type capsPeer struct {
	caps  []string
	split bool
	mode  string
	user  string
	mu    sync.Mutex
	cmds  []string
	got   []string
}

func (p *capsPeer) record(cmd string) {
	p.mu.Lock()
	p.cmds = append(p.cmds, cmd)
	p.mu.Unlock()
}

func (p *capsPeer) commands() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.cmds)
}

func (p *capsPeer) accepted(ids ...string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range ids {
		if !slices.Contains(p.got, id) {
			return false
		}
	}
	return true
}

func (p *capsPeer) handle(c *fakeserver.Conn, line string) ([]string, bool) {
	verb, arg, _ := strings.Cut(line, " ")
	verb = strings.ToUpper(verb)
	switch verb {
	case "MODE", "AUTHINFO":
		p.record(verb + " " + strings.ToUpper(strings.Fields(arg + " x")[0]))
	default:
		p.record(verb)
	}
	streaming := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return slices.Contains(p.cmds, "MODE STREAM") && strings.HasPrefix(p.mode, "203")
	}
	authed := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.user == "" || slices.Contains(p.cmds, "AUTHINFO PASS")
	}
	switch verb {
	case "CAPABILITIES":
		r := p.caps
		if !authed() {
			r = []string{"101 capability list follows", "VERSION 2", "AUTHINFO USER", "."}
		}
		if p.split && len(r) > 2 {
			if err := c.WriteLines(r[:len(r)/2]...); err != nil {
				return nil, true
			}
			time.Sleep(300 * time.Millisecond)
			return r[len(r)/2:], false
		}
		return r, false
	case "AUTHINFO":
		if strings.HasPrefix(strings.ToUpper(arg), "USER ") {
			return []string{"381 password please"}, false
		}
		return []string{"281 authenticated"}, false
	case "MODE":
		if strings.EqualFold(arg, "STREAM") {
			return []string{p.mode}, false
		}
		return []string{"501 Unknown MODE variant"}, false
	case "CHECK":
		if !streaming() {
			return []string{"500 unknown command"}, false
		}
		return []string{"238 " + arg}, false
	case "TAKETHIS":
		if !streaming() {
			return []string{"500 unknown command"}, false
		}
		if _, err := c.ReadDotBlock(); err != nil {
			return nil, true
		}
		p.mu.Lock()
		p.got = append(p.got, arg)
		p.mu.Unlock()
		return []string{"239 " + arg}, false
	case "IHAVE":
		if !authed() {
			return []string{"480 authentication required"}, false
		}
		if err := c.WriteLines("335 send it"); err != nil {
			return nil, true
		}
		if _, err := c.ReadDotBlock(); err != nil {
			return nil, true
		}
		p.mu.Lock()
		p.got = append(p.got, arg)
		p.mu.Unlock()
		return []string{"235 ok"}, false
	case "QUIT":
		return []string{"205 bye"}, true
	}
	return []string{"500 unknown command"}, false
}

// TestInnfeedCapabilities: innfeed sends CAPABILITIES once connected (after AUTHINFO when it
// authenticates) and tries MODE STREAM only if the peer lists STREAMING or does not know
// CAPABILITIES (#317). In every case the articles reach the peer, with CHECK/TAKETHIS when
// streaming and with IHAVE otherwise.
func TestInnfeedCapabilities(t *testing.T) {
	// A list of capabilities longer than innfeed's 256-byte response buffer.
	long := []string{"101 capability list follows", "VERSION 2",
		"IMPLEMENTATION fake transit server for the innfeed CAPABILITIES test " + strings.Repeat("x", 120),
		"IHAVE", "LIST ACTIVE NEWSGROUPS", "X-FAKE-ONE some arguments here", "X-FAKE-TWO more arguments here",
		"X-FAKE-THREE and some more arguments"}
	withStreaming := slices.Concat(long, []string{"STREAMING", "."})
	withoutStreaming := slices.Concat(long, []string{"X-STREAMING-NOT", "."})
	cases := []struct {
		name   string
		peer   *capsPeer
		stream bool     // CHECK/TAKETHIS expected, else IHAVE
		start  []string // the first commands innfeed sends
		noMode bool     // MODE STREAM must not be sent
	}{
		{name: "streaming", peer: &capsPeer{caps: withStreaming, split: true, mode: "203 streaming ok"},
			stream: true, start: []string{"CAPABILITIES", "MODE STREAM"}},
		{name: "no-streaming", peer: &capsPeer{caps: withoutStreaming, split: true, mode: "501 Unknown MODE variant"},
			start: []string{"CAPABILITIES", "IHAVE"}, noMode: true},
		{name: "capabilities-500", peer: &capsPeer{caps: []string{"500 What?"}, mode: "203 streaming ok"},
			stream: true, start: []string{"CAPABILITIES", "MODE STREAM"}},
		{name: "capabilities-500-no-stream", peer: &capsPeer{caps: []string{"500 What?"}, mode: "501 Unknown MODE variant"},
			start: []string{"CAPABILITIES", "MODE STREAM", "IHAVE"}},
		{name: "auth", peer: &capsPeer{caps: withStreaming, mode: "203 streaming ok", user: "feeder"},
			stream: true, start: []string{"AUTHINFO USER", "AUTHINFO PASS", "CAPABILITIES", "MODE STREAM"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.peer
			srv := fakeserver.New(t, fakeserver.Options{Handler: fakeserver.LineHandler(p.handle)})
			auth := ""
			if p.user != "" {
				auth = "    username: \"" + p.user + "\"\n    password: \"secret\"\n"
			}
			s := NewSite(t, SiteOptions{
				Newsfeeds: "ME:*::\ninnfeed!:!*:Tc,Wnm*:@bindir@/innfeed\nfake:*:Tm:innfeed!\n",
				Etc: map[string]string{"innfeed.conf": fmt.Sprintf(
					"peer fake {\n    ip-name: \"127.0.0.1\"\n    port-number: %d\n    max-connections: 1\n%s}\n",
					srv.Port(), auth)},
			})
			ids := s.Inject(t, "e2e.test", 3)
			ok := false
			for end := time.Now().Add(30 * time.Second); !ok && time.Now().Before(end); {
				time.Sleep(50 * time.Millisecond)
				ok = p.accepted(ids...)
			}
			cmds := p.commands()
			if len(cmds) > 40 {
				cmds = cmds[:40]
			}
			if !ok {
				t.Fatalf("the articles did not reach the peer within 30s; commands: %q", cmds)
			}
			if len(cmds) < len(tc.start) || !slices.Equal(cmds[:len(tc.start)], tc.start) {
				t.Errorf("innfeed began with %q, want %q", cmds, tc.start)
			}
			if tc.noMode && slices.Contains(cmds, "MODE STREAM") {
				t.Errorf("innfeed sent MODE STREAM to a peer that does not list STREAMING: %q", cmds)
			}
			if st := slices.Contains(cmds, "TAKETHIS"); st != tc.stream {
				t.Errorf("TAKETHIS used: %v, want %v (commands %q)", st, tc.stream, cmds)
			}
			if ih := slices.Contains(cmds, "IHAVE"); ih == tc.stream {
				t.Errorf("IHAVE used: %v, want %v (commands %q)", ih, !tc.stream, cmds)
			}
		})
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

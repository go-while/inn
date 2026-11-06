package e2e

// Every article storage method with every overview method and both history methods.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	testkit "github.com/go-while/nntp-testkit"
	"github.com/go-while/nntp-testkit/client"
)

// The methods TestMethodMatrix combines.
var (
	matrixStorage  = []string{"tradspool", "timehash", "timecaf", "cnfs", "trash"}
	matrixOverview = []string{"tradindexed", "buffindexed", "ovsqlite", "ovdb"}
	matrixHistory  = []string{"hisv6", "hissqlite"}
)

// The groups of a matrix site. Articles in expireGroup expire after expireDays (about 2.6
// seconds); the others never do.
const (
	expireGroup = "e2e.expire"
	expireDays  = "0.00003"
	expireWait  = 4 * time.Second
)

var matrixGroups = []Group{{Name: "e2e.test"}, {Name: "e2e.other"}, {Name: expireGroup}}

// mxSizeKB is the size of each CNFS and buffindexed buffer.
const mxSizeKB = 4096

// TestMethodMatrix runs a news server through its life with each combination of storage
// method (tradspool, timehash, timecaf, cnfs, trash), overview method (tradindexed,
// buffindexed, ovsqlite, ovdb) and history method (hisv6, hissqlite), configured the way an
// installation would be: storage.conf, cycbuff.conf and buffindexed.conf with preallocated
// buffers, ovsqlite-server started before innd, and a history created by "makedbz -i -o"
// or makehistory. Each combination:
//
//   - feeds articles as a peer with IHAVE and with pipelined CHECK and TAKETHIS: a few dozen
//     small ones (dot-stuffed lines, 8-bit text, References), crossposts and one of about
//     330 KB; offers of articles already there are refused (435, 438, 439);
//   - reads them back through nnrpd: GROUP counts and water marks (RFC 3977), LISTGROUP,
//     OVER (every field, :bytes and :lines included) and HDR, ARTICLE, HEAD, BODY and STAT
//     by number and by Message-ID, byte for byte, and the same article from the storage
//     manager with "sm -R" (whose "sm -c" names the configured method);
//   - cancels an article with "ctlinnd cancel" and a crosspost with a cancel control
//     message (docancels: auth, which executes cancels of articles without Cancel-Lock):
//     gone from storage, overview and reading, still in history;
//   - restarts innd (and ovsqlite-server) and checks all of it again, and that the next
//     article gets the next number;
//   - runs expireover, expire and "ctlinnd renumber" (the steps of news.daily): the
//     articles of expireGroup are gone, a crosspost stays in its other group, history
//     remembers them; CNFS expires by itself, so nothing goes there;
//   - stops the server, deletes overview and history and rebuilds both from the spool with
//     "makehistory -O -F -I", the way makehistory(8) describes: the overview is the same as
//     before, and cancelled or expired articles stay gone.
//
// trash keeps nothing: articles are accepted and remembered in history, but none can be read.
// ovdb is skipped when INN was built without Berkeley DB. By default, hissqlite runs with each
// storage method once (see matrixCombinations); INN_E2E_MATRIX=full runs all combinations.
func TestMethodMatrix(t *testing.T) {
	pfx := prefix(t)
	ovdb := ovdbBuilt(pfx)
	for _, c := range matrixCombinations(os.Getenv(EnvMatrix) == "full") {
		st, ov, hi := c[0], c[1], c[2]
		t.Run(st+"/"+ov+"/"+hi, func(t *testing.T) {
			if ov == "ovdb" && !ovdb {
				t.Skip("ovdb: INN was built without Berkeley DB (configure --with-bdb)")
			}
			m := newMatrix(t, st, ov, hi)
			m.run(t)
		})
	}
}

// EnvMatrix set to "full" makes TestMethodMatrix run every combination of storage,
// overview and history method (40) instead of the default 25.
const EnvMatrix = "INN_E2E_MATRIX"

// EnvKnownIssues: set to run the tests of INN issues that are known but not fixed yet;
// without it, knownIssue skips them so that the suite stays green.
const EnvKnownIssues = "INN_E2E_KNOWN_ISSUES"

// knownIssue skips tb unless INN_E2E_KNOWN_ISSUES is set; why says what fails and where it
// is reported.
func knownIssue(tb testing.TB, why string) {
	tb.Helper()
	if os.Getenv(EnvKnownIssues) == "" {
		tb.Skipf("known INN issue, not fixed yet (set %s=1 to run): %s", EnvKnownIssues, why)
	}
}

// matrixCombinations returns the {storage, overview, history} combinations to run: by
// default every storage method with every overview method and hisv6, plus every storage
// method once with hissqlite, each with another overview method; with full, all of them.
func matrixCombinations(full bool) [][3]string {
	var combos [][3]string
	for _, hi := range matrixHistory {
		for i, st := range matrixStorage {
			for j, ov := range matrixOverview {
				if full || hi == "hisv6" || j == (i+2)%len(matrixOverview) {
					combos = append(combos, [3]string{st, ov, hi})
				}
			}
		}
	}
	return combos
}

// ovdbBuilt tells whether the ovdb overview method was compiled in: without Berkeley DB,
// ovdb_init dies with "Berkeley DB support not compiled" before reading inn.conf.
func ovdbBuilt(pfx string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(pfx, "bin", "ovdb_init"))
	cmd.Env = append(os.Environ(), "INNCONF=/nonexistent/inn.conf")
	var out capped
	cmd.Stdout, cmd.Stderr = &out, &out
	var exit *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exit) {
		return false // no ovdb_init
	}
	return !strings.Contains(out.String(), "not compiled")
}

// Article states in the model.
const (
	present   = iota
	cancelled // by ctlinnd cancel or a cancel control message
	expired   // by expireover and expire
)

// mxArticle is an article of the model: what was sent and where innd filed it.
type mxArticle struct {
	id     string
	groups []string       // Newsgroups, in order
	lines  []string       // the article as sent
	xref   map[string]int // the numbers innd assigned (group → number), for Xref
	nums   map[string]int // the numbers still in the overview
	state  int
	token  string // storage API token, from grephistory
}

// matrix is one site of TestMethodMatrix with the model of what it holds.
type matrix struct {
	storage, overview, history string
	s                          *Site
	bufDir                     string  // CNFS and buffindexed buffers
	ovsqlite                   *daemon // ovsqlite-server
	arts                       []*mxArticle
	high                       map[string]int // the highest number assigned in each group
	groups                     []string

	deferredCancel bool // timecaf: cancels take effect when innd flushes them
	forgotten      bool // history was rebuilt from the spool: gone articles are not remembered

	mark  time.Time // start of the current phase
	times []string  // how long each phase took
}

// done records how long the phase that just ended took.
func (m *matrix) done(phase string) {
	m.times = append(m.times, fmt.Sprintf("%s %.1fs", phase, time.Since(m.mark).Seconds()))
	m.mark = time.Now()
}

func newMatrix(t *testing.T, st, ov, hi string) *matrix {
	m := &matrix{storage: st, overview: ov, history: hi, high: map[string]int{}}
	for _, g := range matrixGroups {
		m.groups = append(m.groups, g.Name)
	}
	m.groups = append(m.groups, "control.cancel")

	etc := map[string]string{
		"expire.ctl": "/remember/:11\n*:A:never:never:never\n" +
			expireGroup + ":A:" + expireDays + ":" + expireDays + ":" + expireDays + "\n",
	}
	switch st {
	case "cnfs":
		etc["storage.conf"] = "method cnfs {\n    newsgroups: *\n    class: 0\n    options: E2E\n}\n"
		buf := m.buffer(t, "cnfs")
		etc["cycbuff.conf"] = fmt.Sprintf("cycbuff:ONE:%s:%d\nmetacycbuff:E2E:ONE\n", buf, mxSizeKB)
	case "trash":
		etc["storage.conf"] = "method trash {\n    newsgroups: *\n    class: 0\n}\n"
	default:
		// The articles that expire get a class of their own, so that timecaf keeps
		// them in a CAF file innd is not writing to when expireover removes them: see
		// TestTimecafExpireoverCurrentFile.
		etc["storage.conf"] = "method " + st + " {\n    newsgroups: " + expireGroup + "\n    class: 1\n}\n" +
			"method " + st + " {\n    newsgroups: *\n    class: 0\n}\n"
	}
	if ov == "ovsqlite" {
		// nnrpd reads the database directly (walmode, the default), so it sees new rows
		// only when ovsqlite-server commits them, transtimelimit (10 seconds by default)
		// after the first one: see TestOvsqliteReaderSeesNewArticle. Commit each row.
		etc["ovsqlite.conf"] = "transrowlimit: 1\n"
	}
	if ov == "buffindexed" {
		etc["buffindexed.conf"] = fmt.Sprintf("0:%s:%d\n", m.buffer(t, "ov0"), mxSizeKB)
	}
	m.s = NewSite(t, SiteOptions{
		Groups: matrixGroups,
		// docancels: without Cancel-Lock support (libcanlock), the default is "none".
		InnConf: map[string]string{"ovmethod": ov, "hismethod": hi, "docancels": "auth"},
		Etc:     etc,
		BeforeStart: func(tb testing.TB, s *Site) {
			m.s = s
			m.startOverview(tb)
		},
	})
	m.deferredCancel = st == "timecaf"
	return m
}

// buffer creates a zeroed buffer file of mxSizeKB in bufDir, a short directory of its own:
// cycbuff.conf and buffindexed.conf take paths of at most 63 characters.
func (m *matrix) buffer(t *testing.T, name string) string {
	t.Helper()
	if m.bufDir == "" {
		dir, err := os.MkdirTemp("", "e2e-buf-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		m.bufDir = dir
	}
	path := filepath.Join(m.bufDir, name)
	if len(path) > 63 {
		t.Skipf("buffer path %s is longer than 63 characters: set TMPDIR to a shorter directory", path)
	}
	zeroFile(t, path)
	return path
}

func zeroFile(tb testing.TB, path string) {
	tb.Helper()
	if err := os.WriteFile(path, make([]byte, mxSizeKB<<10), 0o644); err != nil {
		tb.Fatal(err)
	}
}

// startOverview starts the helper daemons of the overview method, as rc.news does before
// innd: ovsqlite-server, or ovdb_init (which starts ovdb_monitor and ovdb_server).
func (m *matrix) startOverview(tb testing.TB) {
	tb.Helper()
	switch m.overview {
	case "ovsqlite":
		m.ovsqlite = startDaemon(tb, m.s, filepath.Join(m.s.Dir, "run", "ovsqlite.sock"),
			"ovsqlite-server", "-d")
	case "ovdb":
		// ovdb_init starts daemons that keep its standard output: give it a file.
		logFile := filepath.Join(m.s.Dir, "log", "ovdb_init.log")
		f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			tb.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(m.s.Prefix, "bin", "ovdb_init"))
		cmd.Dir, cmd.Env = m.s.Dir, append(os.Environ(), m.s.Env...)
		cmd.Stdout, cmd.Stderr = f, f
		err = cmd.Run()
		_ = f.Close()
		if err != nil {
			tb.Fatalf("ovdb_init: %v\n%s", err, m.s.ReadFile(tb, "log/ovdb_init.log"))
		}
		tb.Cleanup(func() { m.stopOvdb(tb) })
	}
}

// stopOverview stops the helper daemons of the overview method, as "rc.news stop" does.
func (m *matrix) stopOverview(tb testing.TB) {
	tb.Helper()
	switch m.overview {
	case "ovsqlite":
		m.ovsqlite.stop(tb)
	case "ovdb":
		m.stopOvdb(tb)
	}
}

// stopOvdb sends SIGTERM to ovdb_server and ovdb_monitor and waits for their PID files to
// go, like "rc.news stop".
func (m *matrix) stopOvdb(tb testing.TB) {
	for _, name := range []string{"ovdb_server.pid", "ovdb_monitor.pid"} {
		file := filepath.Join(m.s.Dir, "run", name)
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid <= 0 {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGTERM)
		limit := time.Now().Add(10 * time.Second)
		for syscall.Kill(pid, 0) == nil {
			if time.Now().After(limit) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				tb.Errorf("%s (%d) still running 10s after SIGTERM", name, pid)
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		_ = os.Remove(file)
	}
}

// restart stops innd and the overview helpers and starts them again.
func (m *matrix) restart(t *testing.T) {
	t.Helper()
	m.s.Stop(t)
	m.stopOverview(t)
	m.startOverview(t)
	m.s.Start(t)
}

func (m *matrix) run(t *testing.T) {
	m.mark = time.Now()
	defer func() { t.Logf("phases: %s", strings.Join(m.times, ", ")) }()
	trash := m.storage == "trash"

	// Articles that expire, fed first so that they are old enough by the end.
	var toExpire []*mxArticle
	for i := range 3 {
		toExpire = append(toExpire, m.article(t, []string{expireGroup}, fmt.Sprintf("expires %d", i), smallBody(i)))
	}
	crossExpire := m.article(t, []string{"e2e.test", expireGroup}, "crosspost that expires in one group", smallBody(3))
	toExpire = append(toExpire, crossExpire)
	m.ihave(t, toExpire)
	expireFed := time.Now()

	var viaIHAVE, viaStream []*mxArticle
	var prev string
	for i := range 12 {
		g := []string{"e2e.test"}
		if i%3 == 2 {
			g = []string{"e2e.other"}
		}
		a := m.article(t, g, fmt.Sprintf("ihave %d", i), smallBody(10+i))
		if prev != "" && i%2 == 1 {
			a.addHeader("References: " + prev)
		}
		prev = a.id
		viaIHAVE = append(viaIHAVE, a)
	}
	cancel1 := m.article(t, []string{"e2e.test"}, "to be cancelled by ctlinnd", smallBody(30))
	cancel2 := m.article(t, []string{"e2e.test", "e2e.other"}, "crosspost to be cancelled by a control message", smallBody(31))
	viaIHAVE = append(viaIHAVE, cancel1, cancel2)
	m.ihave(t, viaIHAVE)

	for i := range 12 {
		g := []string{"e2e.other"}
		if i%2 == 0 {
			g = []string{"e2e.test"}
		}
		viaStream = append(viaStream, m.article(t, g, fmt.Sprintf("takethis %d", i), smallBody(40+i)))
	}
	cross := m.article(t, []string{"e2e.test", "e2e.other"}, "crosspost", smallBody(60))
	cross.addHeader("References: " + viaIHAVE[0].id + " " + viaIHAVE[1].id)
	large := m.article(t, []string{"e2e.other"}, "large article", largeBody())
	viaStream = append(viaStream, cross, large)
	m.stream(t, viaStream)

	// Offers of articles already there.
	p := m.s.Peer(t)
	expect(t, p, 435, "IHAVE %s", viaIHAVE[0].id)
	expect(t, p, 435, "IHAVE %s", viaStream[0].id)
	expect(t, p, 203, "MODE STREAM")
	expect(t, p, 438, "CHECK %s", viaStream[1].id)
	expect(t, p, 438, "CHECK %s", viaIHAVE[1].id)
	send(t, p, "TAKETHIS "+viaIHAVE[2].id+"\r\n"+wire(viaIHAVE[2].lines))
	status(t, p, 439, "TAKETHIS of an article already there")
	expect(t, p, 205, "QUIT")

	if trash {
		m.verifyTrash(t, "after feeding")
	} else {
		m.learn(t, m.arts)
		m.verify(t, "after feeding")
	}
	m.done("feed")
	if t.Failed() {
		return
	}

	// Cancels.
	m.s.Ctlinnd(t, "cancel", cancel1.id)
	ctl := m.article(t, []string{"e2e.test"}, "cmsg cancel "+cancel2.id, []string{"cancel by e2e"})
	ctl.addHeader("Control: cancel " + cancel2.id)
	ctl.groups = []string{"control.cancel"} // innd files cancel control messages there
	m.ihave(t, []*mxArticle{ctl})
	for _, a := range []*mxArticle{cancel1, cancel2} {
		a.state, a.nums = cancelled, map[string]int{}
	}
	if trash {
		m.verifyTrash(t, "after the cancels")
	} else {
		m.learn(t, []*mxArticle{ctl})
		m.verify(t, "after the cancels")
	}
	m.done("cancel")

	// Restart.
	m.restart(t)
	m.deferredCancel = false
	if trash {
		m.verifyTrash(t, "after a restart")
	} else {
		m.verify(t, "after a restart")
	}
	next := m.article(t, []string{"e2e.test"}, "after the restart", smallBody(70))
	m.ihave(t, []*mxArticle{next})
	if !trash {
		want := m.high["e2e.test"] + 1
		m.learn(t, []*mxArticle{next})
		if got := next.xref["e2e.test"]; got != want {
			t.Errorf("the first article after the restart got number %d in e2e.test, want %d", got, want)
		}
	}
	m.done("restart")
	if t.Failed() {
		return
	}

	// Expiration, as news.daily runs it with group-based expiry.
	time.Sleep(time.Until(expireFed.Add(expireWait)))
	for _, args := range [][]string{{"expireover"}, {"expire", "-v1"}} {
		out, err := runFor(m.s, time.Minute, args[0], args[1:]...)
		if err != nil {
			if strings.Contains(err.Error(), "did not finish") {
				t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
			}
			// With hissqlite, expire tries to unpause innd, which it never paused.
			t.Errorf("%s: %v\n%s", strings.Join(args, " "), err, out)
		} else if out != "" {
			t.Logf("%s:\n%s", strings.Join(args, " "), out)
		}
	}
	m.s.Ctlinnd(t, "renumber", "")
	if m.storage != "cnfs" { // CNFS expires by itself: expire.ctl only sets /remember/
		for _, a := range toExpire {
			if a == crossExpire {
				delete(a.nums, expireGroup)
			} else {
				a.state, a.nums = expired, map[string]int{}
			}
		}
	}
	if trash {
		m.verifyTrash(t, "after expiration")
		m.done("expire")
		return
	}
	m.verify(t, "after expiration")
	m.done("expire")
	if t.Failed() {
		return
	}

	m.rebuild(t)
	m.done("rebuild")
}

// rebuild stops the server, deletes overview and history and rebuilds both from the spool
// (makehistory(8), "OVERVIEW REBUILD"), then checks that the overview is the same.
func (m *matrix) rebuild(t *testing.T) {
	t.Helper()
	before := map[string][]string{}
	for _, g := range m.groups {
		before[g] = overviewLines(t, m.s, g)
	}
	m.s.Stop(t)
	m.stopOverview(t)

	ovdir := filepath.Join(m.s.Dir, "spool", "overview")
	removeContents(t, ovdir)
	if m.overview == "buffindexed" {
		zeroFile(t, filepath.Join(m.bufDir, "ov0"))
		removeGlob(t, filepath.Join(m.s.Dir, "db", "group.index"))
	}
	removeGlob(t, filepath.Join(m.s.Dir, "db", "history*"))

	m.startOverview(t)
	out, err := runFor(m.s, 2*time.Minute, "makehistory", "-O", "-F", "-I")
	if err != nil {
		t.Fatalf("makehistory -O -F -I: %v\n%s", err, out)
	}
	if out != "" {
		t.Logf("makehistory -O -F -I:\n%s", out)
	}
	m.s.Start(t)
	m.s.Ctlinnd(t, "renumber", "")
	m.forgotten = true
	m.revived(t, before)
	m.verify(t, "after rebuilding overview and history")
	for _, g := range m.groups {
		after := overviewLines(t, m.s, g)
		if !slices.Equal(after, before[g]) {
			t.Errorf("after rebuilding: OVER of %s differs:\n%s\nbefore:\n%s", g,
				strings.Join(after, "\n"), strings.Join(before[g], "\n"))
		}
	}
}

// revived accepts overview entries that "makehistory -I" brings back for articles still in
// the spool that expired from one of their groups: INN shows a group that expireover
// emptied with its low water mark at the old high water mark (tdx-group.c and the other
// methods), so -I does not skip that number. It adds them to the model and to before.
func (m *matrix) revived(t *testing.T, before map[string][]string) {
	t.Helper()
	for _, g := range m.groups {
		listed := map[int]bool{}
		c := m.s.Reader(t)
		expect(t, c, 211, "LISTGROUP %s", g)
		for _, l := range c.ReadDotLines() {
			n, _ := strconv.Atoi(l)
			listed[n] = true
		}
		expect(t, c, 205, "QUIT")
		for _, a := range m.arts {
			n, ok := a.xref[g]
			if _, still := a.nums[g]; !ok || still || a.state != present || !listed[n] {
				continue
			}
			t.Logf("makehistory -I brought back %s as %d in %s, where it had expired", a.id, n, g)
			a.nums[g] = n
			before[g] = append(before[g], a.overview(n))
			slices.SortFunc(before[g], func(x, y string) int {
				nx, _ := strconv.Atoi(field(strings.ReplaceAll(x, "\t", " "), 0))
				ny, _ := strconv.Atoi(field(strings.ReplaceAll(y, "\t", " "), 0))
				return nx - ny
			})
		}
	}
}

func removeContents(tb testing.TB, dir string) {
	tb.Helper()
	removeGlob(tb, filepath.Join(dir, "*"))
}

func removeGlob(tb testing.TB, pattern string) {
	tb.Helper()
	names, err := filepath.Glob(pattern)
	if err != nil {
		tb.Fatal(err)
	}
	for _, n := range names {
		if err := os.RemoveAll(n); err != nil {
			tb.Fatal(err)
		}
	}
}

// smallBody returns the body of a small article: lines that need dot-stuffing, 8-bit text,
// white space at the end of a line and an empty line.
func smallBody(n int) []string {
	return []string{
		fmt.Sprintf("small article %d", n),
		".a line with a leading dot",
		"..two dots",
		".",
		"trailing white space \t",
		"8-bit: Grüße, ¿qué tal?",
		"",
		"last line",
	}
}

// largeBody returns the body of an article of about 330 KB.
func largeBody() []string {
	body := make([]string, 4500)
	for i := range body {
		body[i] = fmt.Sprintf("%05d %s", i, strings.Repeat(string(rune('a'+i%26)), 66))
		if i%100 == 50 {
			body[i] = "." + body[i]
		}
	}
	return body
}

// article adds a new article to the model.
func (m *matrix) article(t *testing.T, groups []string, subject string, body []string) *mxArticle {
	a := &mxArticle{id: msgID(t), groups: groups, xref: map[string]int{}, nums: map[string]int{}}
	a.lines = newArticle(a.id, strings.Join(groups, ","), subject, body...)
	m.arts = append(m.arts, a)
	return a
}

// addHeader adds a header field line at the end of the article's head.
func (a *mxArticle) addHeader(line string) {
	i := slices.Index(a.lines, "")
	a.lines = slices.Insert(slices.Clone(a.lines), i, line)
}

func (a *mxArticle) head() []string { h, _ := split(a.lines); return h }
func (a *mxArticle) body() []string { _, b := split(a.lines); return b }

// ihave feeds articles with IHAVE from the peer address.
func (m *matrix) ihave(t *testing.T, arts []*mxArticle) {
	t.Helper()
	p := m.s.Peer(t)
	for _, a := range arts {
		expect(t, p, 335, "IHAVE %s", a.id)
		send(t, p, wire(a.lines))
		status(t, p, 235, "IHAVE "+a.id)
	}
	expect(t, p, 205, "QUIT")
}

// stream feeds articles with pipelined CHECK, then pipelined TAKETHIS (RFC 4644).
func (m *matrix) stream(t *testing.T, arts []*mxArticle) {
	t.Helper()
	p := m.s.Peer(t)
	expect(t, p, 203, "MODE STREAM")
	var raw strings.Builder
	for _, a := range arts {
		raw.WriteString("CHECK " + a.id + "\r\n")
	}
	send(t, p, raw.String())
	for _, a := range arts {
		if got := status(t, p, 238, "CHECK "+a.id); field(got, 1) != a.id {
			t.Errorf("CHECK %s: %q", a.id, got)
		}
	}
	raw.Reset()
	for _, a := range arts {
		raw.WriteString("TAKETHIS " + a.id + "\r\n" + wire(a.lines))
	}
	send(t, p, raw.String())
	for _, a := range arts {
		if got := status(t, p, 239, "TAKETHIS "+a.id); field(got, 1) != a.id {
			t.Errorf("TAKETHIS %s: %q", a.id, got)
		}
	}
	expect(t, p, 205, "QUIT")
}

// learn reads the article numbers innd gave new articles from the overview of their
// groups. Each article must be in each of its groups once, numbered after what the group
// had and in the order the articles were fed. It also looks up their storage tokens.
func (m *matrix) learn(t *testing.T, arts []*mxArticle) {
	t.Helper()
	byID := map[string]*mxArticle{}
	var groups []string
	for _, a := range arts {
		byID[a.id] = a
		for _, g := range a.groups {
			if !slices.Contains(groups, g) {
				groups = append(groups, g)
			}
		}
	}
	for _, g := range groups {
		last := m.high[g]
		for _, l := range overviewLines(t, m.s, g) {
			f := strings.Split(l, "\t")
			if len(f) < 5 {
				t.Fatalf("OVER of %s: line %q", g, l)
			}
			a := byID[f[4]]
			if a == nil {
				continue
			}
			n, err := strconv.Atoi(f[0])
			if err != nil {
				t.Fatalf("OVER of %s: line %q", g, l)
			}
			if _, ok := a.xref[g]; ok {
				t.Errorf("OVER of %s lists %s twice", g, a.id)
			}
			if n <= last {
				t.Errorf("OVER of %s: %s has number %d, want more than %d (fed after it)", g, a.id, n, last)
			}
			last = n
			a.xref[g], a.nums[g] = n, n
		}
		m.high[g] = max(m.high[g], last)
	}
	for _, a := range arts {
		for _, g := range a.groups {
			if _, ok := a.xref[g]; !ok {
				t.Errorf("%s is not in the overview of %s", a.id, g)
			}
		}
		a.token = m.token(t, a.id)
	}
}

// token returns the storage token of a Message-ID, from grephistory.
func (m *matrix) token(t *testing.T, id string) string {
	t.Helper()
	tok := strings.TrimSpace(m.s.Run(t, "grephistory", id))
	if tok == "/dev/null" && m.storage == "trash" {
		return "" // expire found it gone and dropped its token
	}
	if !strings.HasPrefix(tok, "@") || !strings.HasSuffix(tok, "@") {
		t.Fatalf("grephistory %s = %q, want a storage token", id, tok)
	}
	return tok
}

// present returns the numbers of the articles the overview of group should have, in order.
func (m *matrix) present(group string) []int {
	var nums []int
	for _, a := range m.arts {
		if n, ok := a.nums[group]; ok && a.state == present {
			nums = append(nums, n)
		}
	}
	slices.Sort(nums)
	return nums
}

// verify checks every group and every article of the model through nnrpd, the storage
// manager and history.
func (m *matrix) verify(t *testing.T, phase string) {
	t.Helper()
	c := m.s.Reader(t)
	for _, g := range m.groups {
		m.verifyGroup(t, c, g, phase)
	}
	var have, gone []*mxArticle
	var raws [][]byte
	for _, a := range m.arts {
		if a.state == present {
			have = append(have, a)
			raws = append(raws, m.verifyArticle(t, c, a, phase))
		} else {
			m.verifyGone(t, c, a, phase)
			if !(m.deferredCancel && a.state == cancelled) {
				gone = append(gone, a)
			}
		}
	}
	expect(t, c, 205, "QUIT")
	m.verifyStorage(t, phase, have, raws, gone)
	m.verifyHistory(t, phase)
}

// verifyStorage checks the articles with the storage manager: "sm -c" names the configured
// method for each article of have, "sm -R" returns each of them as nnrpd sent it (raws),
// and none of the articles of gone.
func (m *matrix) verifyStorage(t *testing.T, phase string, have []*mxArticle, raws [][]byte, gone []*mxArticle) {
	t.Helper()
	if len(have) > 0 {
		var tokens []string
		for _, a := range have {
			tokens = append(tokens, a.token)
		}
		out, err := m.sm(t, append([]string{"-c"}, tokens...)...)
		lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
		if err != nil || len(lines) != len(have) {
			t.Errorf("%s: sm -c of %d articles: %v\n%s", phase, len(have), err, out)
		} else {
			for i, l := range lines {
				if !strings.HasPrefix(l, have[i].token+" ") || !strings.Contains(l, " method="+m.storage+" ") {
					t.Errorf("%s: sm -c %s (%s): %q, want method=%s", phase, have[i].token, have[i].id, l, m.storage)
				}
			}
		}
		out, err = m.sm(t, append([]string{"-R"}, tokens...)...)
		arts := splitRaw(out)
		if err != nil || len(arts) != len(have) {
			t.Errorf("%s: sm -R of %d articles: %v, %d articles", phase, len(have), err, len(arts))
		} else {
			for i, a := range have {
				if !bytes.Equal(arts[i], raws[i]) {
					t.Errorf("%s: sm -R %s differs from ARTICLE %s:\n%s", phase, a.token, a.id, abbrev(rawLines(arts[i])))
				}
			}
		}
	}
	if len(gone) > 0 {
		var tokens []string
		for _, a := range gone {
			tokens = append(tokens, a.token)
		}
		out, err := m.sm(t, append([]string{"-q", "-R"}, tokens...)...)
		if err == nil || len(out) > 0 {
			for _, b := range splitRaw(out) {
				head, _ := split(rawLines(b))
				t.Errorf("%s: sm -R retrieves the removed article %s", phase, header(head, "Message-ID"))
			}
			if err == nil {
				t.Errorf("%s: sm -R of the removed articles succeeds", phase)
			}
		}
	}
}

// splitRaw splits the output of "sm -R" with several tokens into its articles, each in wire
// format ending with ".\r\n".
func splitRaw(out []byte) [][]byte {
	var arts [][]byte
	for len(out) > 0 {
		i := bytes.Index(out, []byte("\r\n.\r\n"))
		if i < 0 {
			return append(arts, out)
		}
		arts = append(arts, out[:i+5])
		out = out[i+5:]
	}
	return arts
}

// pipeline sends commands in one write (RFC 3977 section 3.5); the caller reads the replies
// in order. nnrpd answers each command in its own packets, and one round trip per command
// costs tens of milliseconds on the loopback interface.
func pipeline(t *testing.T, c *client.Conn, cmds []string) {
	t.Helper()
	send(t, c, strings.Join(cmds, "\r\n")+"\r\n")
}

// deferred returns the numbers in group of cancelled articles that may still be listed:
// timecaf removes cancelled articles when innd flushes its cancels, and buffindexed and
// ovdb keep the overview of cancelled articles, which nnrpd hides when the article is gone
// (nnrpdcheckart).
func (m *matrix) deferred(group string) map[int]bool {
	d := map[int]bool{}
	if !m.deferredCancel {
		return d
	}
	for _, a := range m.arts {
		if n, ok := a.xref[group]; ok && a.state == cancelled {
			d[n] = true
		}
	}
	return d
}

// verifyGroup checks GROUP, LISTGROUP, OVER and HDR of a group.
func (m *matrix) verifyGroup(t *testing.T, c *client.Conn, g, phase string) {
	t.Helper()
	want := m.present(g)
	skip := m.deferred(g)
	rng := ""
	cmds := []string{"GROUP " + g, "LISTGROUP " + g}
	if len(want) > 0 {
		rng = fmt.Sprintf("%d-%d", want[0], want[len(want)-1])
		cmds = append(cmds, "OVER "+rng, "HDR Subject "+rng)
	}
	pipeline(t, c, cmds)

	line := status(t, c, 211, "GROUP "+g)
	count, _ := strconv.Atoi(field(line, 1))
	low, _ := strconv.Atoi(field(line, 2))
	high, _ := strconv.Atoi(field(line, 3))
	if n := len(want); n > 0 {
		if high != m.high[g] {
			t.Errorf("%s: GROUP %s: %q, want the high water mark %d", phase, g, line, m.high[g])
		}
		if low > want[0] || high < want[n-1] || count < n || count > high-low+1 {
			t.Errorf("%s: GROUP %s: %q does not fit articles %v (RFC 3977 6.1.1.2)", phase, g, line, want)
		}
	} else if len(skip) == 0 && (count != 0 || high != low-1 || low < m.high[g]) {
		// INN shows an empty group with the low water mark at the old high water mark
		// (or one more) and the high water mark one less.
		t.Errorf("%s: GROUP %s: %q, want an empty group with the low water mark at least %d", phase, g, line, m.high[g])
	}

	status(t, c, 211, "LISTGROUP "+g)
	var got []int
	for _, l := range c.ReadDotLines() {
		n, err := strconv.Atoi(l)
		if err != nil {
			t.Fatalf("%s: LISTGROUP %s: line %q", phase, g, l)
		}
		if !skip[n] {
			got = append(got, n)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: LISTGROUP %s: %v, want %v", phase, g, got, want)
	}
	if len(want) == 0 {
		return
	}

	byNum := map[int]*mxArticle{}
	for _, a := range m.arts {
		if n, ok := a.nums[g]; ok && a.state == present {
			byNum[n] = a
		}
	}
	status(t, c, 224, "OVER "+rng)
	var overNums []int
	for _, l := range c.ReadDotLines() {
		f := strings.Split(l, "\t")
		n, _ := strconv.Atoi(f[0])
		if skip[n] {
			continue
		}
		overNums = append(overNums, n)
		if a := byNum[n]; a != nil {
			if w := a.overview(n); l != w {
				t.Errorf("%s: OVER of %s:\n%q\nwant\n%q", phase, g, l, w)
			}
		}
	}
	if !slices.Equal(overNums, want) {
		t.Errorf("%s: OVER %s in %s: articles %v, want %v", phase, rng, g, overNums, want)
	}

	status(t, c, 225, "HDR Subject "+rng)
	var hdr, gotHdr []string
	for _, n := range want {
		hdr = append(hdr, fmt.Sprintf("%d %s", n, header(byNum[n].head(), "Subject")))
	}
	for _, l := range c.ReadDotLines() {
		if n, _ := strconv.Atoi(field(l, 0)); !skip[n] {
			gotHdr = append(gotHdr, l)
		}
	}
	if !slices.Equal(gotHdr, hdr) {
		t.Errorf("%s: HDR Subject %s in %s:\n%q\nwant\n%q", phase, rng, g, gotHdr, hdr)
	}
}

// removedNumbers returns GROUP and STAT commands for the numbers an article no longer has
// in the overview of a group, and the groups in the same order.
func (m *matrix) removedNumbers(a *mxArticle) (cmds, groups []string) {
	if m.deferredCancel && a.state == cancelled {
		return nil, nil
	}
	for _, g := range a.groups {
		n, ok := a.xref[g]
		if _, still := a.nums[g]; !ok || still {
			continue
		}
		cmds = append(cmds, "GROUP "+g, fmt.Sprintf("STAT %d", n))
		groups = append(groups, g)
	}
	return cmds, groups
}

// readRemovedNumbers reads the replies to the commands of removedNumbers: the numbers
// cannot be read (423).
func (m *matrix) readRemovedNumbers(t *testing.T, c *client.Conn, a *mxArticle, groups []string, phase string) {
	t.Helper()
	for _, g := range groups {
		status(t, c, 211, "GROUP "+g)
		if code, line, err := c.ReadStatus(); err != nil {
			t.Fatalf("%s: STAT %d in %s: %v", phase, a.xref[g], g, err)
		} else if code != 423 {
			t.Errorf("%s: STAT %d in %s of the removed article %s: %q, want 423", phase, a.xref[g], g, a.id, line)
		}
	}
}

// verifyArticle reads an article by Message-ID and by number in each group, and returns
// it as ARTICLE sent it.
func (m *matrix) verifyArticle(t *testing.T, c *client.Conn, a *mxArticle, phase string) []byte {
	t.Helper()
	cmds := []string{"STAT " + a.id, "ARTICLE " + a.id, "HEAD " + a.id, "BODY " + a.id}
	removed, removedGroups := m.removedNumbers(a)
	cmds = append(cmds, removed...)
	var numGroups []string
	for _, g := range a.groups {
		if n, ok := a.nums[g]; ok {
			cmds = append(cmds, "GROUP "+g, fmt.Sprintf("ARTICLE %d", n))
			numGroups = append(numGroups, g)
		}
	}
	pipeline(t, c, cmds)

	if got := status(t, c, 223, "STAT "+a.id); field(got, 2) != a.id {
		t.Errorf("%s: STAT %s: %q", phase, a.id, got)
	}
	status(t, c, 220, "ARTICLE "+a.id)
	raw := c.RawUntilDot()
	got := rawLines(raw)
	want := a.stored()
	if !slices.Equal(got, want) {
		t.Errorf("%s: ARTICLE %s:\n%s\nwant\n%s", phase, a.id, abbrev(got), abbrev(want))
	}
	wantHead, wantBody := split(want)
	status(t, c, 221, "HEAD "+a.id)
	if got := c.ReadDotLines(); !slices.Equal(got, wantHead) {
		t.Errorf("%s: HEAD %s:\n%s\nwant\n%s", phase, a.id, abbrev(got), abbrev(wantHead))
	}
	status(t, c, 222, "BODY "+a.id)
	if got := c.ReadDotLines(); !slices.Equal(got, wantBody) {
		t.Errorf("%s: BODY %s: %d lines, want %d:\n%s", phase, a.id, len(got), len(wantBody), abbrev(got))
	}
	m.readRemovedNumbers(t, c, a, removedGroups, phase)
	for _, g := range numGroups {
		n := a.nums[g]
		status(t, c, 211, "GROUP "+g)
		if got := status(t, c, 220, fmt.Sprintf("ARTICLE %d in %s", n, g)); field(got, 1) != strconv.Itoa(n) || field(got, 2) != a.id {
			t.Errorf("%s: ARTICLE %d in %s: %q, want %s", phase, n, g, got, a.id)
		}
		if b := c.RawUntilDot(); !bytes.Equal(b, raw) {
			t.Errorf("%s: ARTICLE %d in %s differs from ARTICLE %s", phase, n, g, a.id)
		}
	}
	return raw
}

// verifyGone checks that a cancelled or expired article can be read neither by Message-ID
// nor by number.
func (m *matrix) verifyGone(t *testing.T, c *client.Conn, a *mxArticle, phase string) {
	t.Helper()
	var cmds []string
	stat := !(m.deferredCancel && a.state == cancelled)
	if stat {
		cmds = append(cmds, "STAT "+a.id)
	}
	removed, groups := m.removedNumbers(a)
	cmds = append(cmds, removed...)
	if len(cmds) == 0 {
		return
	}
	pipeline(t, c, cmds)
	if stat {
		if code, line, err := c.ReadStatus(); err != nil {
			t.Fatalf("%s: STAT %s: %v", phase, a.id, err)
		} else if code != 430 {
			t.Errorf("%s: STAT %s of a removed article: %q, want 430", phase, a.id, line)
		}
	}
	m.readRemovedNumbers(t, c, a, groups, phase)
}

// xrefLine returns the Xref header field innd adds to the article.
func (a *mxArticle) xrefLine() string {
	x := "Xref: " + PathHost
	for _, g := range a.groups {
		x += fmt.Sprintf(" %s:%d", g, a.xref[g])
	}
	return x
}

// stored returns the lines of the article as innd stores it: its Path field begins with the
// pathhost, and the Xref field comes at the end of the head.
func (a *mxArticle) stored() []string {
	var out []string
	for _, l := range a.head() {
		if v, ok := strings.CutPrefix(l, "Path: "); ok {
			l = "Path: " + PathHost + "!" + v
		}
		out = append(out, l)
	}
	out = append(out, a.xrefLine(), "")
	return append(out, a.body()...)
}

// size is :bytes, the size of the article with CRLF line ends and without dot-stuffing.
func size(lines []string) int {
	n := 0
	for _, l := range lines {
		n += len(l) + 2
	}
	return n
}

// overview returns the OVER line of the article as number n.
func (a *mxArticle) overview(n int) string {
	h := a.head()
	return strings.Join([]string{strconv.Itoa(n), header(h, "Subject"), header(h, "From"),
		header(h, "Date"), a.id, header(h, "References"), strconv.Itoa(size(a.stored())),
		strconv.Itoa(len(a.body())), a.xrefLine()}, "\t")
}

// verifyHistory offers every article again with CHECK: history must know all of them, also
// the cancelled and expired ones (/remember/ in expire.ctl), unless it was rebuilt from the
// spool.
func (m *matrix) verifyHistory(t *testing.T, phase string) {
	t.Helper()
	p := m.s.Peer(t)
	expect(t, p, 203, "MODE STREAM")
	var ids []string
	var raw strings.Builder
	for _, a := range m.arts {
		if a.state != present && m.forgotten {
			continue
		}
		ids = append(ids, a.id)
		raw.WriteString("CHECK " + a.id + "\r\n")
	}
	send(t, p, raw.String())
	for _, id := range ids {
		code, line, err := p.ReadStatus()
		if err != nil {
			t.Fatalf("%s: CHECK %s: %v", phase, id, err)
		}
		if code != 438 {
			t.Errorf("%s: CHECK %s: %q, want 438 (history has it)", phase, id, line)
		}
	}
	expect(t, p, 205, "QUIT")
}

// verifyTrash checks a site storing into trash: every article was accepted and is in
// history, but none can be read.
func (m *matrix) verifyTrash(t *testing.T, phase string) {
	t.Helper()
	c := m.s.Reader(t)
	var tokens []string
	for _, a := range m.arts {
		if code, line := c.Cmd("STAT %s", a.id); code != 430 {
			t.Errorf("%s: STAT %s with trash: %q, want 430", phase, a.id, line)
		}
		if a.token == "" {
			a.token = m.token(t, a.id)
		}
		if a.token != "" {
			tokens = append(tokens, a.token)
		}
	}
	expect(t, c, 205, "QUIT")
	out, err := m.sm(t, append([]string{"-c"}, tokens...)...)
	if err != nil || strings.Count(string(out), " method=trash") != len(tokens) {
		t.Errorf("%s: sm -c of %d articles, want method=trash for each: %v\n%s", phase, len(tokens), err, out)
	}
	if out, err := m.sm(t, append([]string{"-q", "-R"}, tokens...)...); err == nil || len(out) > 0 {
		t.Errorf("%s: sm -R retrieves articles from trash: %v\n%s", phase, err, abbrev(rawLines(out)))
	}
	m.verifyHistory(t, phase)
}

// sm runs sm with args and returns its standard output (up to 1 MiB) and, on failure, the
// error with its standard error.
func (m *matrix) sm(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(m.s.Prefix, "bin", "sm"), args...)
	cmd.Dir = m.s.Dir
	cmd.Env = append(os.Environ(), m.s.Env...)
	out := &limited{max: 1 << 20}
	var stderr capped
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		return out.buf.Bytes(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.buf.Bytes(), nil
}

// limited keeps the first max bytes written to it.
type limited struct {
	buf bytes.Buffer
	max int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// rawLines splits a dot-terminated block as RawUntilDot returns it into un-stuffed lines.
func rawLines(raw []byte) []string {
	s := strings.TrimSuffix(string(raw), ".\r\n")
	if s == "" {
		return nil
	}
	return client.Unstuff(strings.Split(strings.TrimSuffix(s, "\r\n"), "\r\n"))
}

// abbrev shows the head and the beginning of the body of an article for a failure message.
func abbrev(lines []string) string {
	if len(lines) > 30 {
		return strings.Join(lines[:30], "\n") + fmt.Sprintf("\n[... %d lines]", len(lines))
	}
	return strings.Join(lines, "\n")
}

// daemon is a helper program of a site run in the foreground, such as "ovsqlite-server -d".
type daemon struct {
	name string
	cmd  *exec.Cmd
	out  *capped
	done chan struct{}
}

// startDaemon starts an INN program of the site and waits until it accepts connections on
// the Unix socket sock. The cleanup stops it.
func startDaemon(tb testing.TB, s *Site, sock, name string, args ...string) *daemon {
	tb.Helper()
	d := &daemon{name: name, out: &capped{}, done: make(chan struct{})}
	d.cmd = exec.Command(filepath.Join(s.Prefix, "bin", name), args...)
	d.cmd.Dir = s.Dir
	d.cmd.Env = append(os.Environ(), s.Env...)
	d.cmd.Stdout, d.cmd.Stderr = d.out, d.out
	if err := d.cmd.Start(); err != nil {
		tb.Fatalf("%s: %v", name, err)
	}
	go func() { _ = d.cmd.Wait(); close(d.done) }()
	tb.Cleanup(func() {
		d.stop(tb)
		if tb.Failed() && d.out.String() != "" {
			tb.Logf("%s output:\n%s", name, d.out.String())
		}
	})
	limit := time.Now().Add(testkit.Deadline())
	for {
		if c, err := net.Dial("unix", sock); err == nil {
			_ = c.Close()
			return d
		}
		select {
		case <-d.done:
			tb.Fatalf("%s exited before listening on %s:\n%s", name, sock, d.out.String())
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(limit) {
			tb.Fatalf("%s not listening on %s after %v:\n%s", name, sock, testkit.Deadline(), d.out.String())
		}
	}
}

// stop sends SIGTERM to the daemon and waits for it to exit (SIGKILL after 10 seconds).
func (d *daemon) stop(tb testing.TB) {
	select {
	case <-d.done:
		return
	default:
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-d.done:
	case <-time.After(10 * time.Second):
		_ = d.cmd.Process.Kill()
		<-d.done
		tb.Errorf("%s still running 10s after SIGTERM", d.name)
	}
	if ps := d.cmd.ProcessState; !ps.Success() {
		if ws, ok := ps.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
			tb.Errorf("%s exited with %v:\n%s", d.name, ps, d.out.String())
		}
	}
}

// TestBytesBodyStartsWithDot: the :bytes overview field is the size of the article (with
// CRLF line ends, without dot-stuffing), also when the first line of the body begins with
// a dot. innd computes the size by subtracting the dot-stuffed lines it counted while
// parsing, but ARTparseheader hands over to ARTparsebody after the blank line without
// looking at the first character of the body, so such a line is not counted.
func TestBytesBodyStartsWithDot(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	bodies := [][]string{{"no dot first", ".a dot later"}, {".a dot first", "none later"}}
	p := s.Peer(t)
	for i, body := range bodies {
		id := msgID(t)
		expect(t, p, 335, "IHAVE %s", id)
		send(t, p, wire(newArticle(id, "e2e.test", fmt.Sprintf("body %d", i), body...)))
		status(t, p, 235, "IHAVE "+id)
	}
	expect(t, p, 205, "QUIT")

	c := s.Reader(t)
	expect(t, c, 211, "GROUP e2e.test")
	for n := 1; n <= len(bodies); n++ {
		expect(t, c, 220, "ARTICLE %d", n)
		want := size(rawLines(c.RawUntilDot()))
		expect(t, c, 224, "OVER %d", n)
		lines := c.ReadDotLines()
		if len(lines) != 1 {
			t.Fatalf("OVER %d: %q", n, lines)
		}
		if f := strings.Split(lines[0], "\t"); len(f) < 7 || f[6] != strconv.Itoa(want) {
			t.Errorf("article %d (body %q): OVER %q, want :bytes %d", n, bodies[n-1], lines[0], want)
		}
	}
}

// TestOvsqliteReaderSeesNewArticle: with the ovsqlite overview, a reader sees an article as
// soon as innd has accepted it. With walmode (the default since 2.8.0), nnrpd reads the
// database directly instead of asking ovsqlite-server, and sees only committed rows; the
// server commits transtimelimit (10 seconds by default) after the first row of a
// transaction, so a new article stays out of GROUP and OVER for up to 10 seconds, while
// ARTICLE by Message-ID already finds it. Without walmode, readers go through the server,
// which sees its own transaction; with transrowlimit 1, the server commits each row at once.
func TestOvsqliteReaderSeesNewArticle(t *testing.T) {
	for _, tc := range []struct{ name, conf string }{
		{"walmode=false", "walmode: false\n"},
		{"walmode=true", "walmode: true\n"},
		{"walmode=true/transrowlimit=1", "walmode: true\ntransrowlimit: 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "walmode=true" {
				knownIssue(t, "with walmode, nnrpd sees a new article only after "+
					"ovsqlite-server commits (up to transtimelimit, 10 s); waits for the maintainer")
			}
			s := NewSite(t, SiteOptions{
				InnConf: map[string]string{"ovmethod": "ovsqlite"},
				Etc:     map[string]string{"ovsqlite.conf": tc.conf},
				BeforeStart: func(tb testing.TB, s *Site) {
					startDaemon(tb, s, filepath.Join(s.Dir, "run", "ovsqlite.sock"), "ovsqlite-server", "-d")
				},
			})
			id := s.Inject(t, "e2e.test", 1)[0]
			accepted := time.Now()
			c := s.Reader(t)
			expect(t, c, 223, "STAT %s", id)
			got := expect(t, c, 211, "GROUP e2e.test")
			expect(t, c, 205, "QUIT")
			if got == "211 1 1 1 e2e.test" {
				return
			}
			// How long until it shows up.
			var seen time.Duration
			for time.Since(accepted) < 15*time.Second {
				time.Sleep(200 * time.Millisecond)
				r := s.Reader(t)
				line := expect(t, r, 211, "GROUP e2e.test")
				expect(t, r, 205, "QUIT")
				if field(line, 1) == "1" {
					seen = time.Since(accepted)
					break
				}
			}
			t.Errorf("GROUP right after innd accepted %s (STAT finds it): %q, want \"211 1 1 1 e2e.test\"; "+
				"GROUP showed it %v after it was accepted (0s: not within 15s)", id, got, seen.Round(100*time.Millisecond))
		})
	}
}

// TestTimecafExpireoverCurrentFile: expireover removes an expired timecaf article stored in
// the CAF file innd is writing to (articles of the same class received within the same 256
// seconds). innd keeps that file open with a write lock until it stores an article into
// another file, and expireover's cancel (CAFRemoveMultArts) waits for the lock, so it
// blocks for as long as innd keeps writing to that file, indefinitely on a quiet server.
func TestTimecafExpireoverCurrentFile(t *testing.T) {
	knownIssue(t, "expireover waits for innd's lock on the CAF file innd is writing to; "+
		"waits for the maintainer")
	s := NewSite(t, SiteOptions{
		Groups: matrixGroups,
		Etc: map[string]string{
			"storage.conf": "method timecaf {\n    newsgroups: *\n    class: 0\n}\n",
			"expire.ctl": "/remember/:11\n*:A:never:never:never\n" +
				expireGroup + ":A:" + expireDays + ":" + expireDays + ":" + expireDays + "\n",
		},
	})
	old := s.Inject(t, expireGroup, 1)[0]
	time.Sleep(expireWait)
	s.Inject(t, "e2e.test", 1) // innd writes to the same CAF file, unless 256 seconds ended
	out, err := runFor(s, 20*time.Second, "expireover")
	if err != nil {
		t.Fatalf("expireover with innd writing to the CAF file of an expired article: %v\n%s", err, out)
	}
	c := s.Reader(t)
	if code, line := c.Cmd("STAT %s", old); code != 430 {
		t.Errorf("STAT %s after expireover: %q, want 430", old, line)
	}
}

package e2e

import (
	"bytes"
	"context"
	"crypto/x509"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	testkit "github.com/go-while/nntp-testkit"
	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/proc"
)

// EnvPrefix names the environment variable that holds the installed INN tree under test
// (configure --prefix): its bin/ has innd, nnrpd, ctlinnd and makedbz, its etc/ the sample
// configuration. Without it every test is skipped.
const EnvPrefix = "INN_PREFIX"

// PeerIP is the loopback address the default incoming.conf lists as a feeding peer. A
// connection from 127.0.0.1 is a reader (innd hands it to nnrpd); one from PeerIP is a peer
// that innd serves itself (IHAVE, MODE STREAM, CHECK, TAKETHIS).
const PeerIP = "127.0.0.2"

// PathHost is the site's pathhost and domain.
const PathHost = "e2e.test"

// Group is one newsgroup of a site's active file.
type Group struct {
	Name string
	Flag string // active flag: "y" when empty; "n", "m", "x", "=other.group" ...
	Desc string // newsgroups description; "" = "Group <name>."
}

// DefaultGroups are the groups of a site whose SiteOptions.Groups is nil. INN's own control,
// control.cancel and junk are always added.
var DefaultGroups = []Group{
	{Name: "e2e.test"},
	{Name: "e2e.other"},
	{Name: "e2e.moderated", Flag: "m"},
}

// SiteOptions configures a throwaway INN site. Every file not set here comes from the sample
// configuration installed in INN_PREFIX/etc. In InnConf values, Readers, Incoming, Newsfeeds
// and Etc, @bindir@ is replaced with pathbin and @etcdir@ with the site's etc directory.
type SiteOptions struct {
	Groups    []Group           // nil = DefaultGroups
	InnConf   map[string]string // inn.conf parameters added or replacing the generated ones
	Readers   string            // readers.conf; "" = DefaultReaders
	Incoming  string            // incoming.conf; "" = PeerIP as a streaming peer
	Newsfeeds string            // newsfeeds; "" = "ME:*::" (accept everything, feed nobody)
	Etc       map[string]string // further files in etc, by name (innfeed.conf, passwd.nntp, ...)
	Args      []string          // further innd arguments (innd always gets -f)

	// TLS writes a self-signed certificate for 127.0.0.1 to etc/cert.pem and its key to
	// etc/key.pem (mode 0600), the files nnrpd uses for STARTTLS by default; Site.CertPool
	// trusts it.
	TLS bool
}

// Site is one running innd with its own pathnews in a temporary directory.
type Site struct {
	Dir    string   // pathnews: etc, db, spool, log, run, tmp
	Prefix string   // the INN installation (pathbin is Prefix/bin)
	Addr   string   // innd's listening address, 127.0.0.1:port
	Env    []string // INNCONF=Dir/etc/inn.conf, for INN programs run by the test
	Proc   *proc.Process

	CertPool *x509.CertPool // trusts the site's certificate when SiteOptions.TLS is set
	start    time.Time
}

// prefix returns INN_PREFIX or skips the test.
func prefix(tb testing.TB) string {
	tb.Helper()
	p := os.Getenv(EnvPrefix)
	if p == "" {
		tb.Skipf("%s is not set: point it at an installed INN (configure --prefix)", EnvPrefix)
	}
	if os.Geteuid() == 0 {
		tb.Skip("running as root: run the tests as the news user INN was configured for")
	}
	if _, err := os.Stat(filepath.Join(p, "bin", "innd")); err != nil {
		tb.Fatalf("%s=%s: %v", EnvPrefix, p, err)
	}
	return p
}

// NewSite writes a site into a temporary directory, creates its history database, starts
// innd -f on a free port of 127.0.0.1 and waits for its greeting. The cleanup shuts innd down
// with "ctlinnd shutdown" (the way rc.news stops it) and, when the test failed, logs innd's
// output and the syslog messages of INN programs since the site started (via journalctl,
// when present).
func NewSite(tb testing.TB, o SiteOptions) *Site {
	tb.Helper()
	s := &Site{Dir: tb.TempDir(), Prefix: prefix(tb), start: time.Now()}
	port := freePort(tb)
	s.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	s.Env = []string{"INNCONF=" + filepath.Join(s.Dir, "etc", "inn.conf")}

	for _, d := range []string{"etc", "db", "log", "run", "tmp", "http",
		"spool/articles", "spool/overview", "spool/incoming/bad", "spool/outgoing",
		"spool/archive", "spool/innfeed"} {
		if err := os.MkdirAll(filepath.Join(s.Dir, d), 0o755); err != nil {
			tb.Fatal(err)
		}
	}
	copySamples(tb, filepath.Join(s.Prefix, "etc"), filepath.Join(s.Dir, "etc"))

	conf := map[string]string{
		"mta":         `"/bin/true %s"`,
		"ovmethod":    "tradindexed",
		"hismethod":   "hisv6",
		"pathhost":    PathHost,
		"domain":      PathHost,
		"pathnews":    s.Dir,
		"pathbin":     filepath.Join(s.Prefix, "bin"),
		"pathtmp":     filepath.Join(s.Dir, "tmp"),
		"port":        strconv.Itoa(port),
		"bindaddress": "127.0.0.1",
		"server":      "127.0.0.1",
		// The default (16) makes nnrpd refuse readers with a 400 on a busy machine.
		"nnrpdloadlimit": "0",
	}
	for k, v := range o.InnConf {
		conf[k] = s.expand(v)
	}
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(conf)) {
		fmt.Fprintf(&b, "%s: %s\n", k, conf[k])
	}
	s.WriteEtc(tb, "inn.conf", b.String())
	s.WriteEtc(tb, "readers.conf", s.expand(or(o.Readers, DefaultReaders)))
	s.WriteEtc(tb, "incoming.conf", s.expand(or(o.Incoming, DefaultIncoming)))
	s.WriteEtc(tb, "newsfeeds", s.expand(or(o.Newsfeeds, "ME:*::\n")))
	for name, content := range o.Etc {
		s.WriteEtc(tb, name, s.expand(content))
	}
	if o.TLS {
		certPEM, keyPEM, pool := selfSigned(tb)
		s.writeFile(tb, "etc/cert.pem", string(certPEM))
		if err := os.WriteFile(filepath.Join(s.Dir, "etc", "key.pem"), keyPEM, 0o600); err != nil {
			tb.Fatal(err)
		}
		s.CertPool = pool
	}

	groups := o.Groups
	if groups == nil {
		groups = DefaultGroups
	}
	groups = append([]Group{{Name: "control", Flag: "n"}, {Name: "control.cancel", Flag: "n"},
		{Name: "junk", Flag: "n"}}, groups...)
	var active, descs strings.Builder
	for _, g := range groups {
		fmt.Fprintf(&active, "%s 0000000000 0000000001 %s\n", g.Name, or(g.Flag, "y"))
		fmt.Fprintf(&descs, "%s\t%s\n", g.Name, or(g.Desc, "Group "+g.Name+"."))
	}
	s.writeFile(tb, "db/active", active.String())
	s.writeFile(tb, "db/active.times", "")
	s.writeFile(tb, "db/newsgroups", descs.String())
	s.writeFile(tb, "db/history", "")
	if out, err := s.run("makedbz", "-i", "-o"); err != nil {
		tb.Fatalf("makedbz -i -o: %v\n%s", err, out)
	}

	tb.Cleanup(func() { s.dumpSyslog(tb) }) // runs last: after innd has stopped
	s.Proc = proc.StartTCP(tb, proc.Options{
		Path: filepath.Join(s.Prefix, "bin", "innd"),
		Args: append([]string{"-f"}, o.Args...),
		Env:  s.Env,
		Dir:  s.Dir,
	}, s.Addr)
	tb.Cleanup(func() { s.shutdown(tb) }) // runs before proc's cleanup (SIGTERM, then SIGKILL)
	return s
}

// DefaultReaders gives 127.0.0.1 read, post, approve and NEWNEWS access to every group.
const DefaultReaders = `auth "local" {
    hosts: "127.0.0.1, ::1, localhost"
    default: "<local>"
}
access "local" {
    users: "<local>"
    newsgroups: "*"
    access: RPAN
}
`

// DefaultIncoming lists PeerIP as a streaming peer.
const DefaultIncoming = `peer e2e {
    hostname: "` + PeerIP + `"
    streaming: true
}
`

// expand replaces @bindir@ and @etcdir@ in a configuration file.
func (s *Site) expand(content string) string {
	return strings.NewReplacer("@bindir@", filepath.Join(s.Prefix, "bin"),
		"@etcdir@", filepath.Join(s.Dir, "etc")).Replace(content)
}

// shutdown stops a running innd with "ctlinnd shutdown" and waits for it to exit.
func (s *Site) shutdown(tb testing.TB) {
	select {
	case <-s.Proc.Done():
		return
	default:
	}
	if out, err := s.run("ctlinnd", "-t", "10", "shutdown", "e2e test done"); err != nil {
		tb.Logf("ctlinnd shutdown: %v\n%s", err, out)
		return // proc's cleanup sends SIGTERM, then SIGKILL
	}
	select {
	case <-s.Proc.Done():
	case <-time.After(10 * time.Second):
		tb.Errorf("innd still running 10s after ctlinnd shutdown")
	}
}

// dumpSyslog logs, when the test failed, the files in the site's log directory and the
// syslog messages of INN programs since the site started: innd, nnrpd, innfeed and the other
// programs log their notices there, not to a file.
func (s *Site) dumpSyslog(tb testing.TB) {
	if !tb.Failed() {
		return
	}
	logs, _ := filepath.Glob(filepath.Join(s.Dir, "log", "*"))
	for _, name := range logs {
		if b, err := os.ReadFile(name); err == nil && len(b) > 0 {
			tb.Logf("%s:\n%s", name, b)
		}
	}
	jc, err := exec.LookPath("journalctl")
	if err != nil {
		tb.Logf("syslog: journalctl not found; see the system log for innd and nnrpd messages")
		return
	}
	args := []string{"-q", "--no-pager", "-o", "short-precise",
		"--since", "@" + strconv.FormatInt(s.start.Unix()-1, 10)}
	for _, id := range []string{"innd", "nnrpd", "innfeed", "innxmit", "ctlinnd", "nntpget", "pullnews", "controlchan"} {
		args = append(args, "-t", id)
	}
	out, _ := exec.Command(jc, args...).CombinedOutput()
	tb.Logf("syslog (journalctl %s):\n%s", strings.Join(args, " "), out)
}

// Run runs the INN program name from Prefix/bin with the site's INNCONF in Dir and returns
// its combined output; a failure (non-zero exit, or still running after the hang guard)
// fails the test.
func (s *Site) Run(tb testing.TB, name string, args ...string) string {
	tb.Helper()
	out, err := s.run(name, args...)
	if err != nil {
		tb.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// TryRun is Run that returns the error instead of failing the test.
func (s *Site) TryRun(name string, args ...string) (string, error) { return s.run(name, args...) }

// RunInput is Run with stdin as the program's standard input.
func (s *Site) RunInput(tb testing.TB, stdin, name string, args ...string) string {
	tb.Helper()
	out, err := s.runInput(stdin, name, args...)
	if err != nil {
		tb.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func (s *Site) run(name string, args ...string) (string, error) { return s.runInput("", name, args...) }

func (s *Site) runInput(stdin, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), testkit.Deadline())
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(s.Prefix, "bin", name), args...)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// Ctlinnd runs ctlinnd with args and fails the test unless it succeeds.
func (s *Site) Ctlinnd(tb testing.TB, args ...string) string {
	tb.Helper()
	return s.Run(tb, "ctlinnd", append([]string{"-t", "10"}, args...)...)
}

// Reader dials innd from 127.0.0.1 (a reader: innd hands it to nnrpd) and reads the
// greeting.
func (s *Site) Reader(tb testing.TB) *client.Conn {
	tb.Helper()
	return client.Dial(tb, s.Addr)
}

// Peer dials innd from PeerIP (a feeding peer in the default incoming.conf) and reads the
// greeting.
func (s *Site) Peer(tb testing.TB) *client.Conn {
	tb.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(PeerIP)}, Timeout: testkit.Deadline()}
	nc, err := d.Dial("tcp", s.Addr)
	if err != nil {
		tb.Fatalf("dial %s from %s: %v", s.Addr, PeerIP, err)
	}
	c := client.New(tb, nc)
	g, err := c.ReadGreeting()
	if err != nil {
		tb.Fatalf("peer greeting: %v", err)
	}
	c.Greeting = g
	return c
}

// WriteEtc writes a file of the site's etc directory.
func (s *Site) WriteEtc(tb testing.TB, name, content string) {
	tb.Helper()
	s.writeFile(tb, filepath.Join("etc", name), content)
}

func (s *Site) writeFile(tb testing.TB, rel, content string) {
	tb.Helper()
	if err := os.WriteFile(filepath.Join(s.Dir, rel), []byte(content), 0o644); err != nil {
		tb.Fatal(err)
	}
}

// ReadFile returns a file of the site, relative to Dir.
func (s *Site) ReadFile(tb testing.TB, rel string) string {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join(s.Dir, rel))
	if err != nil {
		tb.Fatal(err)
	}
	return string(b)
}

// copySamples copies the regular files of the installed etc directory.
func copySamples(tb testing.TB, from, to string) {
	tb.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		tb.Fatal(err)
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(from, e.Name()))
		if err != nil {
			if os.IsPermission(err) {
				continue // e.g. a secrets file of another user
			}
			tb.Fatal(err)
		}
		info, err := e.Info()
		if err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, e.Name()), b, info.Mode().Perm()|fs.FileMode(0o600)); err != nil {
			tb.Fatal(err)
		}
	}
}

// freePort returns a TCP port of 127.0.0.1 that was free a moment ago.
func freePort(tb testing.TB) int {
	tb.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// waitFor polls cond every 50ms until it holds or d passes; then it fails the test with what.
func waitFor(tb testing.TB, d time.Duration, what string, cond func() bool) {
	tb.Helper()
	limit := time.Now().Add(d)
	for !cond() {
		if time.Now().After(limit) {
			tb.Fatalf("timed out after %v waiting for %s", d, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Inject feeds n new articles for group to the site with IHAVE from PeerIP and returns their
// message-ids in order.
func (s *Site) Inject(tb testing.TB, group string, n int) []string {
	tb.Helper()
	p := s.Peer(tb)
	ids := make([]string, n)
	for i := range ids {
		ids[i] = msgID(tb)
		expect(tb, p, 335, "IHAVE %s", ids[i])
		send(tb, p, wire(newArticle(ids[i], group, fmt.Sprintf("injected %d", i+1), "body of "+ids[i])))
		status(tb, p, 235, "IHAVE "+ids[i])
	}
	expect(tb, p, 205, "QUIT")
	return ids
}

// ReaderMode dials innd from 127.0.0.1 and sends MODE READER: the way to reach nnrpd when
// incoming.conf makes 127.0.0.1 a peer too.
func (s *Site) ReaderMode(tb testing.TB) *client.Conn {
	tb.Helper()
	c := client.Dial(tb, s.Addr)
	expect(tb, c, 200, "MODE READER")
	return c
}

// Syslog returns the syslog messages of this site's innd since it started (read with
// journalctl by its PID), or skips the test when the journal is not readable for the user
// running it.
func (s *Site) Syslog(tb testing.TB) []string {
	tb.Helper()
	jc, err := exec.LookPath("journalctl")
	if err != nil {
		tb.Skip("journalctl not found: innd logs to syslog")
	}
	out, err := exec.Command(jc, "-q", "--no-pager", "-o", "cat",
		"--since", "@"+strconv.FormatInt(s.start.Unix()-1, 10),
		"_PID="+strconv.Itoa(s.Proc.Pid())).Output()
	if err != nil {
		tb.Skipf("journalctl: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		tb.Skip("no syslog messages of innd readable: is the user in the systemd-journal group?")
	}
	return lines
}

// JournalCursor returns the cursor of the last journal entry, for ProgramSyslog, or skips the
// test when journalctl is missing.
func (s *Site) JournalCursor(tb testing.TB) string {
	tb.Helper()
	jc, err := exec.LookPath("journalctl")
	if err != nil {
		tb.Skip("journalctl not found: INN programs log to syslog")
	}
	out, err := exec.Command(jc, "-q", "-n", "0", "--show-cursor").Output()
	if err != nil {
		tb.Skipf("journalctl: %v", err)
	}
	cursor, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "-- cursor: ")
	if !ok {
		tb.Skipf("journalctl --show-cursor: %q", out)
	}
	return cursor
}

// ProgramSyslog returns the syslog messages that INN programs logged under ident (for
// instance "nnrpd") after the journal entry at cursor (from JournalCursor).
func (s *Site) ProgramSyslog(tb testing.TB, ident, cursor string) []string {
	tb.Helper()
	out, err := exec.Command("journalctl", "-q", "--no-pager", "-o", "cat",
		"--after-cursor", cursor, "-t", ident).Output()
	if err != nil {
		tb.Skipf("journalctl: %v", err)
	}
	if len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

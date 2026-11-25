package e2e

// A small POP3 client (RFC 1939) for the test mail hub. The mail INN programs send on a test
// host (moderated postings, innmail, news2mail) goes through the host's MTA to a hub that
// delivers every recipient to one mailbox; the tests read it back here and delete what they
// found.

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// EnvMailHub names the environment variable that holds the POP3 address (host:port) of the
// test mail hub. When it is empty, DefaultMailHub is used; "none" skips the mail delivery
// tests. EnvMailUser and EnvMailPass hold the mailbox login (default DefaultMailUser and
// DefaultMailPass).
const (
	EnvMailHub  = "INN_E2E_MAILHUB"
	EnvMailUser = "INN_E2E_MAILUSER"
	EnvMailPass = "INN_E2E_MAILPASS"
)

// The test mail network of the e2e test VMs: a throwaway mailbox on a private test net, with
// a static test password that exists nowhere else.
const (
	DefaultMailHub  = "10.10.30.109:110"
	DefaultMailUser = "e2e"
	DefaultMailPass = "e2e-mail"
)

const (
	pop3DialTimeout = 3 * time.Second  // a hub that does not answer by then is unreachable
	pop3CmdTimeout  = 15 * time.Second // each command and its reply
	pop3MaxLine     = 64 << 10         // longest line accepted from the server
	pop3MaxHeader   = 64 << 10         // most bytes read of a message's header (TOP n 0)
	pop3MaxMessage  = 1 << 20          // most bytes read of a whole message (RETR)
	pop3ScanPerPoll = 300              // most new messages whose header one poll reads
)

// errPOP3TooLarge is returned when a multi-line reply exceeds its cap; the reply has then
// been read up to its terminating "." and the session can go on.
var errPOP3TooLarge = errors.New("pop3: reply exceeds the size cap")

// pop3Conn is one POP3 session.
type pop3Conn struct {
	nc net.Conn
	r  *bufio.Reader
}

// dialPOP3 connects to addr and reads the server's +OK greeting.
func dialPOP3(addr string, timeout time.Duration) (*pop3Conn, error) {
	nc, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := &pop3Conn{nc: nc, r: bufio.NewReader(nc)}
	if err := c.nc.SetDeadline(time.Now().Add(pop3CmdTimeout)); err != nil {
		c.Close()
		return nil, err
	}
	line, err := c.readLine()
	if err == nil && !strings.HasPrefix(line, "+OK") {
		err = fmt.Errorf("pop3 greeting %q", line)
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Close closes the connection without QUIT: messages marked with DELE stay in the mailbox.
func (c *pop3Conn) Close() { _ = c.nc.Close() }

// readLine reads one line of at most pop3MaxLine bytes and returns it without its CRLF.
func (c *pop3Conn) readLine() (string, error) {
	var b []byte
	for {
		chunk, err := c.r.ReadSlice('\n')
		if len(b)+len(chunk) > pop3MaxLine {
			return "", fmt.Errorf("pop3: line longer than %d bytes", pop3MaxLine)
		}
		b = append(b, chunk...)
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return "", err
		}
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// cmd sends one command and reads its status line; it returns the text after "+OK", or an
// error for "-ERR" and anything else.
func (c *pop3Conn) cmd(format string, args ...any) (string, error) {
	if err := c.nc.SetDeadline(time.Now().Add(pop3CmdTimeout)); err != nil {
		return "", err
	}
	command := fmt.Sprintf(format, args...)
	if _, err := c.nc.Write([]byte(command + "\r\n")); err != nil {
		return "", err
	}
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if rest, ok := strings.CutPrefix(line, "+OK"); ok {
		return strings.TrimSpace(rest), nil
	}
	verb, _, _ := strings.Cut(command, " ") // not the argument: PASS has the password
	return "", fmt.Errorf("pop3 %s: %q", verb, line)
}

// readMulti reads the lines of a multi-line reply up to the terminating ".", removes the
// dot-stuffing (a line beginning with "." loses its first dot) and returns at most capBytes
// bytes of lines. A longer reply is read to its end and errPOP3TooLarge returned with the
// lines that fit.
func (c *pop3Conn) readMulti(capBytes int) ([]string, error) {
	var lines []string
	n := 0
	tooLarge := false
	for {
		line, err := c.readLine()
		if err != nil {
			return lines, err
		}
		if line == "." {
			break
		}
		line = strings.TrimPrefix(line, ".")
		if n += len(line) + 2; n > capBytes {
			tooLarge = true
			continue
		}
		lines = append(lines, line)
	}
	if tooLarge {
		return lines, errPOP3TooLarge
	}
	return lines, nil
}

// Login sends USER and PASS.
func (c *pop3Conn) Login(user, pass string) error {
	if _, err := c.cmd("USER %s", user); err != nil {
		return err
	}
	_, err := c.cmd("PASS %s", pass)
	return err
}

// Stat returns the number of messages in the mailbox and their size.
func (c *pop3Conn) Stat() (count, size int, err error) {
	rest, err := c.cmd("STAT")
	if err != nil {
		return 0, 0, err
	}
	if _, err := fmt.Sscanf(rest, "%d %d", &count, &size); err != nil {
		return 0, 0, fmt.Errorf("pop3 STAT %q: %v", rest, err)
	}
	return count, size, nil
}

// pop3Entry is one line of a LIST or UIDL reply: a message number and its size or unique id.
type pop3Entry struct {
	num int
	val string
}

// list sends a LIST or UIDL command without argument and parses its reply.
func (c *pop3Conn) list(verb string) ([]pop3Entry, error) {
	if _, err := c.cmd("%s", verb); err != nil {
		return nil, err
	}
	lines, err := c.readMulti(pop3MaxMessage)
	if err != nil {
		return nil, err
	}
	entries := make([]pop3Entry, 0, len(lines))
	for _, l := range lines {
		num, val, ok := strings.Cut(l, " ")
		n, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("pop3 %s line %q", verb, l)
		}
		entries = append(entries, pop3Entry{n, strings.TrimSpace(val)})
	}
	return entries, nil
}

// List returns the message numbers and sizes.
func (c *pop3Conn) List() ([]pop3Entry, error) { return c.list("LIST") }

// UIDL returns the message numbers and their unique ids (an optional command of RFC 1939).
func (c *pop3Conn) UIDL() ([]pop3Entry, error) { return c.list("UIDL") }

// Retr returns the lines of message n, at most capBytes bytes (see readMulti).
func (c *pop3Conn) Retr(n, capBytes int) ([]string, error) {
	if _, err := c.cmd("RETR %d", n); err != nil {
		return nil, err
	}
	return c.readMulti(capBytes)
}

// Top returns the header of message n and its first body lines (an optional command of RFC
// 1939), at most capBytes bytes.
func (c *pop3Conn) Top(n, body, capBytes int) ([]string, error) {
	if _, err := c.cmd("TOP %d %d", n, body); err != nil {
		return nil, err
	}
	return c.readMulti(capBytes)
}

// Dele marks message n for deletion; QUIT deletes it.
func (c *pop3Conn) Dele(n int) error {
	_, err := c.cmd("DELE %d", n)
	return err
}

// Quit ends the session, which deletes the messages marked with DELE, and closes the
// connection.
func (c *pop3Conn) Quit() error {
	defer c.Close()
	_, err := c.cmd("QUIT")
	return err
}

// mailMessage is a message read from the mail hub.
type mailMessage struct {
	Num    int      // message number in the session that read it
	UID    string   // UIDL id, "" without UIDL
	Header []string // header fields, unfolded
	Body   []string
}

// parseMail splits the lines of a message into its header fields, unfolded (a line beginning
// with a space or tab continues the previous field), and its body.
func parseMail(lines []string) (header, body []string) {
	for i, l := range lines {
		if l == "" {
			return header, lines[i+1:]
		}
		if (l[0] == ' ' || l[0] == '\t') && len(header) > 0 {
			header[len(header)-1] += l
			continue
		}
		header = append(header, l)
	}
	return header, nil
}

// Get returns the value of the first header field called name, or "".
func (m *mailMessage) Get(name string) string { return header(m.Header, name) }

// mailHub is the POP3 mailbox of the test mail hub.
type mailHub struct {
	addr, user, pass string
}

// needMailHub returns the test mail hub, or skips the test when it is not reachable (the
// tests do not run on a host of the test mail network) or EnvMailHub is "none". A hub that
// answers but refuses the login fails the test.
func needMailHub(tb testing.TB) *mailHub {
	tb.Helper()
	h := &mailHub{addr: or(os.Getenv(EnvMailHub), DefaultMailHub),
		user: or(os.Getenv(EnvMailUser), DefaultMailUser),
		pass: or(os.Getenv(EnvMailPass), DefaultMailPass)}
	if h.addr == "none" {
		tb.Skipf("%s=none: mail delivery tests are off", EnvMailHub)
	}
	c, err := dialPOP3(h.addr, pop3DialTimeout)
	if err != nil {
		tb.Skipf("test mail hub %s not reachable (%v): mail delivery tests need a host of the test mail network; set %s to its POP3 address", h.addr, err, EnvMailHub)
	}
	defer c.Close()
	if err := c.Login(h.user, h.pass); err != nil {
		tb.Fatalf("test mail hub %s: login as %s: %v", h.addr, h.user, err)
	}
	count, size, err := c.Stat()
	if err != nil {
		tb.Fatalf("test mail hub %s: %v", h.addr, err)
	}
	tb.Logf("test mail hub %s: %d messages, %d bytes", h.addr, count, size)
	if err := c.Quit(); err != nil {
		tb.Fatalf("test mail hub %s: %v", h.addr, err)
	}
	return h
}

// mailToken returns a string that no other test, run or host uses, to find a test's mail
// among the others in the hub's mailbox.
func mailToken(tb testing.TB) string {
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	name := strings.NewReplacer("/", ".", " ", "_").Replace(tb.Name())
	return fmt.Sprintf("e2e-%s-%s-%d-%d", host, name, time.Now().UnixNano(), idSeq.Add(1))
}

// Wait waits up to d for a message with token in a header field, reads it, deletes it from
// the mailbox and returns it; it fails the test when none arrives. Each poll is a new POP3
// session (a session does not see mail that arrives during it) and reads the header of the
// newest messages not seen yet, at most pop3ScanPerPoll of them.
func (h *mailHub) Wait(tb testing.TB, token string, d time.Duration) *mailMessage {
	tb.Helper()
	seen := map[string]bool{}
	limit := time.Now().Add(d)
	polls, scanned := 0, 0
	var lastErr error
	for {
		polls++
		m, n, err := h.poll(token, seen)
		scanned += n
		if m != nil {
			tb.Logf("test mail hub %s: found the mail with token %s after %d polls (%d headers read): uid %q, Message-ID %s, Subject %q; deleted it",
				h.addr, token, polls, scanned, m.UID, m.Get("Message-ID"), m.Get("Subject"))
			return m
		}
		if err != nil {
			lastErr = err
		}
		if time.Now().After(limit) {
			tb.Fatalf("no mail with %s at the test mail hub %s after %v (%d polls, %d headers read, last error: %v)",
				token, h.addr, d, polls, scanned, lastErr)
		}
		time.Sleep(time.Second)
	}
}

// poll looks once for a message with token in its header and, when it finds one, reads it,
// deletes it and returns it. It returns how many headers it read; seen collects the unique
// ids of the messages without token.
func (h *mailHub) poll(token string, seen map[string]bool) (*mailMessage, int, error) {
	c, err := dialPOP3(h.addr, pop3DialTimeout)
	if err != nil {
		return nil, 0, err
	}
	defer c.Close()
	if err := c.Login(h.user, h.pass); err != nil {
		return nil, 0, err
	}
	entries, err := c.UIDL()
	if err != nil {
		// Without UIDL, number the messages by LIST and read them all again each poll.
		if entries, err = c.List(); err != nil {
			return nil, 0, err
		}
		for i := range entries {
			entries[i].val = ""
		}
	}
	scanned := 0
	for i := len(entries) - 1; i >= 0 && scanned < pop3ScanPerPoll; i-- {
		e := entries[i]
		if e.val != "" && seen[e.val] {
			continue
		}
		scanned++
		head, err := c.Top(e.num, 0, pop3MaxHeader)
		if err != nil && !errors.Is(err, errPOP3TooLarge) {
			// No TOP: read the whole message.
			if head, err = c.Retr(e.num, pop3MaxMessage); err != nil && !errors.Is(err, errPOP3TooLarge) {
				return nil, scanned, err
			}
		}
		fields, _ := parseMail(head)
		if !hasToken(fields, token) {
			if e.val != "" {
				seen[e.val] = true
			}
			continue
		}
		lines, err := c.Retr(e.num, pop3MaxMessage)
		if err != nil {
			return nil, scanned, fmt.Errorf("message %d: %w", e.num, err)
		}
		m := &mailMessage{Num: e.num, UID: e.val}
		m.Header, m.Body = parseMail(lines)
		if err := c.Dele(e.num); err != nil {
			return nil, scanned, err
		}
		if err := c.Quit(); err != nil {
			return nil, scanned, err
		}
		return m, scanned, nil
	}
	return nil, scanned, c.Quit()
}

// hasToken reports whether a header field has token in its value.
func hasToken(fields []string, token string) bool {
	for _, f := range fields {
		if _, v, ok := strings.Cut(f, ":"); ok && strings.Contains(v, token) {
			return true
		}
	}
	return false
}

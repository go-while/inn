package e2e

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// EnvSyslog names the environment variable that holds the path of a syslog file, for
// instance /var/log/messages, to read the messages of INN programs from instead of the
// journal: for systems without journald, such as Alpine with BusyBox syslogd or a container
// running rsyslogd. The user running the tests needs read access to it.
const EnvSyslog = "INN_E2E_SYSLOG"

// syslogLine is one message of a syslog file.
type syslogLine struct {
	raw   string // the whole line, with timestamp and host
	ident string // "innd", "nnrpd" ...
	pid   int    // 0 when the tag has no [pid]
	msg   string // the text after the tag, as journalctl -o cat prints it
}

// syslogTag matches the tag of a syslog line: ident, an optional [pid] and a colon.
var syslogTag = regexp.MustCompile(`^([A-Za-z0-9_.-]+)(?:\[([0-9]+)\])?:$`)

// parseSyslogLine splits a line of a syslog file into its tag and message. The tag is the
// first field ending with a colon; the fields before it are the timestamp ("Oct 24 12:00:00"
// or RFC 3339), the host and, with BusyBox syslogd, facility.priority. ok is false for a line
// without a tag.
func parseSyslogLine(line string) (l syslogLine, ok bool) {
	rest := line
	for {
		var field string
		var found bool
		field, rest, found = strings.Cut(strings.TrimLeft(rest, " "), " ")
		if m := syslogTag.FindStringSubmatch(field); m != nil {
			pid, _ := strconv.Atoi(m[2])
			return syslogLine{raw: line, ident: m[1], pid: pid, msg: rest}, true
		}
		if !found {
			return syslogLine{}, false
		}
	}
}

// syslogFileSize returns the size of the syslog file, the offset where the messages logged
// from now on start.
func syslogFileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// readSyslogFile returns the messages of the syslog file written after offset bytes. The
// whole file is read when it has become smaller than offset (rotated or truncated).
func readSyslogFile(path string, offset int64) ([]syslogLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	var lines []syslogLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if l, ok := parseSyslogLine(sc.Text()); ok {
			lines = append(lines, l)
		}
	}
	return lines, sc.Err()
}

// syslogIdents are the syslog idents of the INN programs whose messages a failed test logs.
var syslogIdents = []string{"innd", "nnrpd", "innfeed", "innxmit", "ctlinnd", "nntpget", "pullnews", "controlchan"}

// fileSyslog returns the text of the messages in the INN_E2E_SYSLOG file after offset for
// which keep is true, or skips the test when the file is not readable.
func (s *Site) fileSyslog(tb testing.TB, offset int64, keep func(syslogLine) bool) []string {
	tb.Helper()
	if s.syslogErr != nil {
		tb.Skipf("%s: %v", EnvSyslog, s.syslogErr)
	}
	lines, err := readSyslogFile(s.syslogFile, offset)
	if err != nil {
		tb.Skipf("%s: %v", EnvSyslog, err)
	}
	var out []string
	for _, l := range lines {
		if keep(l) {
			out = append(out, l.msg)
		}
	}
	return out
}

// dumpSyslogFile logs the lines of the INN_E2E_SYSLOG file that INN programs wrote since the
// site started, for a failed test.
func (s *Site) dumpSyslogFile(tb testing.TB) {
	if s.syslogErr != nil {
		tb.Logf("syslog: %s: %v", EnvSyslog, s.syslogErr)
		return
	}
	lines, err := readSyslogFile(s.syslogFile, s.syslogOffset)
	if err != nil {
		tb.Logf("syslog: %s: %v", EnvSyslog, err)
		return
	}
	var b strings.Builder
	for _, l := range lines {
		if slices.Contains(syslogIdents, l.ident) {
			b.WriteString(l.raw)
			b.WriteByte('\n')
		}
	}
	tb.Logf("syslog (%s since the site started):\n%s", s.syslogFile, b.String())
}

// TestParseSyslogLine: the tag and message of the line formats written by rsyslog
// (traditional and RFC 3339 timestamps) and BusyBox syslogd. It needs no INN installation.
func TestParseSyslogLine(t *testing.T) {
	for _, c := range []struct {
		line, ident string
		pid         int
		msg         string
	}{
		{"Oct 24 12:00:00 host nnrpd[123]: starttls: TLSv1.3 ", "nnrpd", 123, "starttls: TLSv1.3 "},
		{"Oct  4 12:00:00 host innd: SERVER starting", "innd", 0, "SERVER starting"},
		{"2026-10-24T12:00:00.123456+02:00 host innd[7]: e2e.test:1: x: y", "innd", 7, "e2e.test:1: x: y"},
		{"Oct 24 12:00:00 host news.notice nnrpd[42]: 0000 16 03 01", "nnrpd", 42, "0000 16 03 01"},
		{"Oct 24 12:00:00 host innd[9]:", "innd", 9, ""},
	} {
		l, ok := parseSyslogLine(c.line)
		if !ok || l.ident != c.ident || l.pid != c.pid || l.msg != c.msg {
			t.Errorf("parseSyslogLine(%q) = %q, %d, %q, %v; want %q, %d, %q", c.line, l.ident, l.pid, l.msg, ok, c.ident, c.pid, c.msg)
		}
	}
	if _, ok := parseSyslogLine("-- MARK --"); ok {
		t.Errorf("parseSyslogLine(\"-- MARK --\") found a tag")
	}
}

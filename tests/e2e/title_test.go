package e2e

// The addprocesstitleuser access group parameter of readers.conf: nnrpd's process title
// shows the authenticated username (#264).

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// titleReaders is authReaders with extra lines in the auth and the access group.
func titleReaders(authExtra, accessExtra string) string {
	return `auth "users" {
    hosts: "127.0.0.1"
    auth: "ckpasswd -f @etcdir@/passwd.e2e"
` + authExtra + `}
access "users" {
    users: "e2e*"
    newsgroups: "*"
    access: RPA
` + accessExtra + `}
`
}

// nnrpdTitles returns the process titles of the nnrpd processes innd of s has spawned,
// read from /proc/<pid>/cmdline, without the leading "- nnrpd: " of INN's setproctitle
// and the padding after the title.
func (s *Site) nnrpdTitles(tb testing.TB) []string {
	tb.Helper()
	innd := strconv.Itoa(s.Proc.Pid())
	dirs, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		tb.Fatal(err)
	}
	var titles []string
	for _, d := range dirs {
		stat, err := os.ReadFile(filepath.Join(d, "stat"))
		if err != nil {
			continue // the process has exited
		}
		// pid (comm) state ppid ...; comm may contain spaces and parentheses.
		i := bytes.LastIndexByte(stat, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(stat[i+1:]))
		if len(f) < 2 || f[1] != innd {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(d, "cmdline"))
		if err != nil {
			continue
		}
		t := strings.TrimRight(string(bytes.ReplaceAll(cmdline, []byte{0}, []byte{' '})), " ")
		if rest, ok := strings.CutPrefix(t, "- nnrpd: "); ok {
			titles = append(titles, rest)
		}
	}
	return titles
}

// nnrpdTitle returns the title of the one nnrpd of s, waiting up to five seconds until
// done accepts it (done nil: the first title seen).
func (s *Site) nnrpdTitle(tb testing.TB, done func(fields []string) bool) []string {
	tb.Helper()
	limit := time.Now().Add(5 * time.Second)
	for {
		titles := s.nnrpdTitles(tb)
		if len(titles) > 1 {
			tb.Fatalf("more than one nnrpd: %q", titles)
		}
		if len(titles) == 1 {
			f := strings.Fields(titles[0])
			if done == nil || done(f) {
				return f
			}
			if time.Now().After(limit) {
				tb.Fatalf("nnrpd title %q", titles[0])
			}
		} else if time.Now().After(limit) {
			tb.Fatalf("no nnrpd child of innd %d found in /proc", s.Proc.Pid())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// commandTitle sends DATE and returns the fields of nnrpd's title, which nnrpd sets
// before it runs a command: "<host> DATE", or "<user> <host> DATE".
func (s *Site) commandTitle(tb testing.TB, c *client.Conn) []string {
	tb.Helper()
	expect(tb, c, 111, "DATE")
	return s.nnrpdTitle(tb, func(f []string) bool { return len(f) > 0 && f[len(f)-1] == "DATE" })
}

// TestProcessTitleUser: without addprocesstitleuser, nnrpd's title is "<host> <command>"
// also after AUTHINFO USER/PASS; with it, "<user> <host> <command>" once the reader has
// authenticated, the user with "@" and the default-domain of the auth group when it has
// one, also while the connection is idle after AUTHINFO. A failed AUTHINFO GENERIC after
// authentication, which sets nnrpd's user to "<none>", does not change the name in the
// title (Linux only: reads /proc).
func TestProcessTitleUser(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads process titles from /proc/<pid>/cmdline, Linux only")
	}
	s := NewSite(t, SiteOptions{
		Readers: titleReaders("", ""),
		Etc:     map[string]string{"passwd.e2e": authPasswd},
	})
	for _, tc := range []struct {
		name        string
		authExtra   string
		accessExtra string
		user        string // "" = no user in the title
	}{
		{"default off", "", "", ""},
		{"explicitly off", "", "    addprocesstitleuser: false\n", ""},
		{"on", "", "    addprocesstitleuser: true\n", "e2e"},
		{"on with default-domain", "    default-domain: \"example.org\"\n",
			"    addprocesstitleuser: true\n", "e2e@example.org"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.WriteEtc(t, "readers.conf", s.expand(titleReaders(tc.authExtra, tc.accessExtra)))
			c := s.Reader(t)
			if code, err := client.ParseCode(c.Greeting); err != nil || (code != 200 && code != 201) {
				t.Fatalf("greeting %q (does this nnrpd know addprocesstitleuser?)", c.Greeting)
			}

			// Before authentication: "<host> DATE".
			f := s.commandTitle(t, c)
			if len(f) != 2 {
				t.Fatalf("title before authentication %q, want \"<host> DATE\"", f)
			}
			host := f[0]

			expect(t, c, 381, "AUTHINFO USER e2e")
			expect(t, c, 281, "AUTHINFO PASS e2e-secret")
			want := []string{host, "DATE"}
			if tc.user != "" {
				want = []string{tc.user, host, "DATE"}
				// Idle after AUTHINFO, nnrpd shows the user without waiting for
				// the next command.
				idle := []string{tc.user, host, "AUTHINFO"}
				s.nnrpdTitle(t, func(f []string) bool { return slices.Equal(f, idle) })
			}
			if f := s.commandTitle(t, c); !slices.Equal(f, want) {
				t.Errorf("title after authentication %q, want %q", f, want)
			}

			// AUTHINFO GENERIC once authenticated gets 502 but sets nnrpd's user
			// to "<none>"; the title keeps the name the reader authenticated as.
			expect(t, c, 502, "AUTHINFO GENERIC nosuchprogram")
			if f := s.commandTitle(t, c); !slices.Equal(f, want) {
				t.Errorf("title after a failed AUTHINFO GENERIC %q, want %q", f, want)
			}
			expect(t, c, 205, "QUIT")
			// Wait for this nnrpd to exit before the next case looks for one.
			limit := time.Now().Add(5 * time.Second)
			for len(s.nnrpdTitles(t)) > 0 {
				if time.Now().After(limit) {
					t.Fatalf("nnrpd still running after QUIT: %q", s.nnrpdTitles(t))
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}

package e2e

// news2mail, the channel feed which gateways articles into e-mails.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// news2mailGroups are the groups of a news2mail site: e2e.a and e2e.b are gatewayed to
// mailing-lists, e2e.z to the list "list-z" whose mail marks the end of a test.
var news2mailGroups = []Group{{Name: "e2e.a"}, {Name: "e2e.b"}, {Name: "e2e.z"}}

// mail is one call of the MTA by news2mail.
type mail struct {
	args   []string // the command line, without the program
	sender string   // the envelope-from address (the -f argument)
	rcpts  []string // the recipients (the arguments after -pNNTP:...)
	text   string   // what news2mail wrote to the MTA
}

// news2mailSite starts a site whose inn.conf mta is a script recording its command line and
// input in a directory, with a news2mail channel feed running command (for instance
// "@bindir@/news2mail"), the funnel feeds in feeds, and news2mail.cf. list-z
// (z@lists.invalid, fed e2e.z) is always added to both. It returns the site and the
// directory of the recorded mails.
func news2mailSite(t *testing.T, command, feeds, cf string) (*Site, string) {
	t.Helper()
	dir := t.TempDir()
	mta := filepath.Join(t.TempDir(), "mta")
	script := "#! /bin/sh\n" +
		"d='" + dir + "'\n" +
		"n=$(ls \"$d\" | wc -l)\n" +
		"{ printf '%s\\n' \"$@\"; echo; cat; } > \"$d/.tmp\"\n" +
		"mv \"$d/.tmp\" \"$d/$(printf %03d \"$n\")\"\n"
	if err := os.WriteFile(mta, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSite(t, SiteOptions{
		Groups:  news2mailGroups,
		InnConf: map[string]string{"mta": `"` + mta + ` -oi -oem %s"`},
		Newsfeeds: "ME:*::\n" +
			"news2mail!:!*:Ac,Tc,Wn*:" + command + "\n" +
			feeds +
			"list-z:!*,e2e.z:Ap,Tm:news2mail!\n",
		Etc: map[string]string{"news2mail.cf": cf + "list-z z@lists.invalid\n"},
	})
	return s, dir
}

// waitMails waits until news2mail has mailed an article to list-z, and returns the mails
// sent before that one, in order.
func waitMails(t *testing.T, dir string) []mail {
	t.Helper()
	var mails []mail
	waitFor(t, 30*time.Second, "the mail to list-z", func() bool {
		mails = readMails(t, dir)
		return len(mails) > 0 && slices.Contains(mails[len(mails)-1].rcpts, "z@lists.invalid")
	})
	return mails[:len(mails)-1]
}

// readMails returns the mails recorded in dir, in order.
func readMails(t *testing.T, dir string) []mail {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "[0-9][0-9][0-9]"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	var mails []mail
	for _, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		args, text, _ := strings.Cut(string(b), "\n\n")
		m := mail{args: strings.Split(args, "\n"), text: text}
		for i, a := range m.args {
			if f, ok := strings.CutPrefix(a, "-f"); ok && m.sender == "" {
				m.sender = f
			}
			if strings.HasPrefix(a, "-pNNTP:") {
				m.rcpts = m.args[i+1:]
				break
			}
		}
		mails = append(mails, m)
	}
	return mails
}

// TestNews2mailSenders: an article fed to several mailing-lists in one news2mail call (a
// crosspost to groups gatewayed to different lists) is mailed once per envelope-from address
// of news2mail.cf, to the lists using it; lists without one get the news user (issues #116
// and #188). news2mail used to send a single mail to all the lists, from the news user as
// soon as two of them had an envelope-from address.
func TestNews2mailSenders(t *testing.T) {
	s, dir := news2mailSite(t, "@bindir@/news2mail",
		"list-a:!*,e2e.a:Ap,Tm:news2mail!\n"+
			"list-b:!*,e2e.b:Ap,Tm:news2mail!\n"+
			"list-c:!*,e2e.b:Ap,Tm:news2mail!\n"+
			"list-d:!*,e2e.a:Ap,Tm:news2mail!\n",
		"# list address sender\n"+
			"list-a a@lists.invalid sa@senders.invalid\n"+
			"list-b b@lists.invalid sb@senders.invalid\n"+
			"list-c c@lists.invalid sa@senders.invalid\n"+
			"list-d d@lists.invalid\n")
	newsuser := strings.TrimSpace(s.Run(t, "innconfval", "runasuser"))
	id := s.Inject(t, "e2e.a,e2e.b", 1)[0]
	s.Inject(t, "e2e.z", 1)
	mails := waitMails(t, dir)
	for _, m := range mails {
		t.Logf("mail from %q to %q: %q", m.sender, m.rcpts, m.args)
	}

	want := map[string][]string{
		"sa@senders.invalid": {"a@lists.invalid", "c@lists.invalid"},
		"sb@senders.invalid": {"b@lists.invalid"},
		newsuser:             {"d@lists.invalid"},
	}
	got := map[string][]string{}
	for _, m := range mails {
		if _, dup := got[m.sender]; dup {
			t.Errorf("two mails from %q", m.sender)
		}
		rcpts := slices.Clone(m.rcpts)
		slices.Sort(rcpts)
		got[m.sender] = rcpts
		if !strings.Contains(m.text, "Message-ID: "+id+"\n") {
			t.Errorf("mail from %q is not article %s:\n%s", m.sender, id, m.text)
		}
		if to := "To: " + strings.Join(m.rcpts, ",\n\t") + "\n"; !strings.Contains(m.text, to) {
			t.Errorf("mail from %q: no %q header field:\n%s", m.sender, to, m.text)
		}
		if !slices.Contains(m.args, "-odq") {
			t.Errorf("mail from %q: no -odq by default: %q", m.sender, m.args)
		}
	}
	if len(mails) != len(want) {
		t.Errorf("%d mails for the crosspost, want %d (one per envelope-from address)", len(mails), len(want))
	}
	for sender, rcpts := range want {
		if !slices.Equal(got[sender], rcpts) {
			t.Errorf("mail from %q to %q, want to %q", sender, got[sender], rcpts)
		}
	}
}

// TestNews2mailNoQueue: with -n, news2mail does not give the MTA -odq, which only queues the
// mail for a later "sendmail -q" run and which some MTAs reject; without it, it does
// (issue #85). news2mail used to always give -odq.
func TestNews2mailNoQueue(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		queue         bool
	}{
		{"default", "@bindir@/news2mail", true},
		{"-n", "@bindir@/news2mail -n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, dir := news2mailSite(t, tc.command,
				"list-a:!*,e2e.a:Ap,Tm:news2mail!\n",
				"list-a a@lists.invalid sa@senders.invalid\n")
			id := s.Inject(t, "e2e.a", 1)[0]
			s.Inject(t, "e2e.z", 1)
			mails := waitMails(t, dir)
			if len(mails) != 1 {
				t.Fatalf("%d mails for %s, want 1", len(mails), id)
			}
			m := mails[0]
			t.Logf("MTA command line: %q", m.args)
			if q := slices.Contains(m.args, "-odq"); q != tc.queue {
				t.Errorf("-odq given: %v, want %v: %q", q, tc.queue, m.args)
			}
			if m.sender != "sa@senders.invalid" || !slices.Equal(m.rcpts, []string{"a@lists.invalid"}) {
				t.Errorf("mail from %q to %q, want from sa@senders.invalid to a@lists.invalid", m.sender, m.rcpts)
			}
			if !strings.Contains(m.text, "Message-ID: "+id+"\n") {
				t.Errorf("mail is not article %s:\n%s", id, m.text)
			}
		})
	}
}

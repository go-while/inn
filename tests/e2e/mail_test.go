package e2e

// Mail that INN programs send, delivered for real: through the test host's MTA (sendmail) to
// the test mail hub, and read back from the hub's mailbox with POP3 (pop3_test.go). Every
// test puts a token of its own in a header field of the mail and looks for that. The tests
// are skipped unless INN_E2E_MAILHUB names a hub that answers.

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// mailWait bounds the wait for a mail at the hub.
const mailWait = 30 * time.Second

// realMTA returns an inn.conf mta value for the host's sendmail, as configure writes it, or
// skips the test when there is none.
func realMTA(tb testing.TB) string {
	tb.Helper()
	for _, p := range []string{"/usr/sbin/sendmail", "/usr/lib/sendmail"} {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return `"` + p + ` -oi -oem %s"`
		}
	}
	tb.Skip("no sendmail program (/usr/sbin/sendmail, /usr/lib/sendmail): mail delivery needs an MTA")
	return ""
}

// needSMTPUTF8 skips delivery testing on satellites without SMTPUTF8-enabled Postfix.
func needSMTPUTF8(tb testing.TB) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Expand the setting because Postfix's default can depend on compatibility_level.
	cmd := exec.CommandContext(ctx, "postconf", "-xh", "smtputf8_enable")
	var out capped
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		tb.Skipf("SMTPUTF8 delivery requires Postfix: postconf could not read smtputf8_enable: %v", err)
	}
	if enabled := strings.TrimSpace(out.String()); enabled != "yes" {
		tb.Skipf("SMTPUTF8 delivery requires Postfix with smtputf8_enable=yes, got %q", enabled)
	}
}

// checkFields fails the test for each header field of m whose value is not the wanted one.
func checkFields(tb testing.TB, m *mailMessage, want map[string]string) {
	tb.Helper()
	for name, v := range want {
		if got := m.Get(name); got != v {
			tb.Errorf("mail header field %s: %q, want %q\nheader:\n%s", name, got, v, strings.Join(m.Header, "\n"))
		}
	}
}

// TestMailModeratedPost: an article posted through nnrpd to a moderated group without an
// Approved header field is mailed to the group's moderator, the address that the moderators
// file gives for it (with the dots of the group name turned into dashes), with the article's
// header fields; it is not posted to the group.
func TestMailModeratedPost(t *testing.T) {
	hub := needMailHub(t)
	s := NewSite(t, SiteOptions{
		InnConf: map[string]string{"mta": realMTA(t)},
		Etc:     map[string]string{"moderators": "*:%s@moderators.example.org\n"},
	})
	token := mailToken(t)
	id := msgID(t)
	subject := "moderated " + token
	art := newArticle(id, "e2e.moderated", subject, "for the moderator", "token "+token)

	c := s.Reader(t)
	expect(t, c, 340, "POST")
	send(t, c, wire(art))
	t.Logf("POST: %s", status(t, c, 240, "POST to e2e.moderated"))

	m := hub.Wait(t, token, mailWait)
	checkFields(t, m, map[string]string{
		"To":         "e2e-moderated@moderators.example.org",
		"Newsgroups": "e2e.moderated",
		"Subject":    subject,
		"Message-ID": id,
		"From":       "E2E Test <e2e@example.invalid>",
	})
	if !slices.Contains(m.Body, "token "+token) {
		t.Errorf("mail body %q lacks the article's body", m.Body)
	}

	if got := expect(t, c, 211, "GROUP e2e.moderated"); field(got, 1) != "0" {
		t.Errorf("GROUP e2e.moderated: %q, want no article (it went to the moderator)", got)
	}
	expect(t, c, 205, "QUIT")
}

// TestMailModeratedUTF8Post: an unapproved nnrpd post to a UTF-8 group is mailed
// to its moderator. It checks that the To header and the non-ASCII SMTP envelope
// recipient reach the hub intact. It does not check SMTPUTF8 negotiation itself.
func TestMailModeratedUTF8Post(t *testing.T) {
	const group = "e2e.moderated.δοκιμή"
	const recipient = "josé@moderators.example.org"
	needSMTPUTF8(t)
	hub := needMailHub(t)
	s := NewSite(t, SiteOptions{
		Groups:  []Group{{Name: group, Flag: "m"}},
		InnConf: map[string]string{"mta": realMTA(t)},
		Etc:     map[string]string{"moderators": group + ":" + recipient + "\n"},
	})
	token := mailToken(t)
	id := msgID(t)
	subject := "moderated UTF-8 " + token
	body := "token " + token

	c := s.Reader(t)
	expect(t, c, 340, "POST")
	send(t, c, wire(newArticle(id, group, subject, body)))
	status(t, c, 240, "POST to "+group)
	if got := expect(t, c, 211, "GROUP %s", group); field(got, 1) != "0" {
		t.Errorf("GROUP %s: %q, want no article (it went to the moderator)", group, got)
	}
	expect(t, c, 205, "QUIT")

	m := hub.Wait(t, token, mailWait)
	// The hub's Postfix records the original envelope recipient before rewriting it
	// to the shared mailbox address for local delivery.
	checkFields(t, m, map[string]string{
		"To":            recipient,
		"X-Original-To": recipient,
		"Newsgroups":    group,
		"Subject":       subject,
		"Message-ID":    id,
	})
	if !slices.Contains(m.Body, body) {
		t.Errorf("mail body %q lacks the article's body", m.Body)
	}
}

// TestMailInnmail: a mail sent with innmail, with a subject and an added header field,
// reaches its recipient with them and the body read from standard input.
func TestMailInnmail(t *testing.T) {
	hub := needMailHub(t)
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"mta": realMTA(t)}})
	token := mailToken(t)
	rcpt := "innmail-rcpt@mail.example.org"
	subject := "innmail " + token
	out := s.RunInput(t, "innmail body\ntoken "+token+"\n", "innmail",
		"-s", subject, "-a", "X-E2E-Token: "+token, rcpt)
	if out != "" {
		t.Logf("innmail: %s", out)
	}

	m := hub.Wait(t, token, mailWait)
	checkFields(t, m, map[string]string{
		"To":          rcpt,
		"Subject":     subject,
		"X-E2E-Token": token,
	})
	if !slices.Equal(m.Body, []string{"innmail body", "token " + token}) {
		t.Errorf("mail body %q, want the standard input of innmail", m.Body)
	}
}

// TestMailNews2mail: an article in a group that newsfeeds gateways to a mailing-list with
// news2mail reaches the list's address, sent from the envelope-from address of news2mail.cf,
// with the default command line (the MTA gets -odq, only queue the mail) and with -n (no
// -odq). The mail has the article's header fields the way news2mail passes them on: From,
// Subject, Message-ID and Date as they are, Newsgroups renamed to X-Newsgroups, To set to the
// list's address; news2mail drops Path, Xref, Lines, NNTP-Posting-Host and -Date, X-* and the
// article's To.
func TestMailNews2mail(t *testing.T) {
	hub := needMailHub(t)
	mta := realMTA(t)
	for _, tc := range []struct{ name, command string }{
		{"default", "@bindir@/news2mail"},
		{"-n", "@bindir@/news2mail -n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSite(t, SiteOptions{
				Groups:  []Group{{Name: "e2e.a"}},
				InnConf: map[string]string{"mta": mta},
				Newsfeeds: "ME:*::\n" +
					"news2mail!:!*:Ac,Tc,Wn*:" + tc.command + "\n" +
					"list-a:!*,e2e.a:Ap,Tm:news2mail!\n",
				Etc: map[string]string{"news2mail.cf": "list-a a@lists.example.org sa@senders.example.org\n"},
			})
			token := mailToken(t)
			id := msgID(t)
			subject := "news2mail " + token
			p := s.Peer(t)
			expect(t, p, 335, "IHAVE %s", id)
			send(t, p, wire(newArticle(id, "e2e.a", subject, "gatewayed", "token "+token)))
			status(t, p, 235, "IHAVE "+id)
			expect(t, p, 205, "QUIT")

			m := hub.Wait(t, token, mailWait)
			checkFields(t, m, map[string]string{
				"To":           "a@lists.example.org",
				"X-Newsgroups": "e2e.a",
				"Subject":      subject,
				"Message-ID":   id,
				"From":         "E2E Test <e2e@example.invalid>",
			})
			for _, name := range []string{"Newsgroups", "Path"} {
				if v := m.Get(name); v != "" {
					t.Errorf("mail has %s %q, which news2mail does not pass on", name, v)
				}
			}
			if rp := m.Get("Return-Path"); rp != "<sa@senders.example.org>" {
				t.Errorf("Return-Path %q, want <sa@senders.example.org> (the envelope-from of news2mail.cf)", rp)
			}
			if !slices.Contains(m.Body, "token "+token) {
				t.Errorf("mail body %q lacks the article's body", m.Body)
			}
		})
	}
}

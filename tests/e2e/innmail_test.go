package e2e

// innmail, the mail-sending program used by INN for reports to the news administrator.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestInnmailAddresses: innmail gives the MTA (and the To header field) the addresses as they
// were given, when they only have characters allowed in an unquoted local part of an e-mail
// address, and refuses with a warning the others and those beginning with "-", "|" or "/",
// which the MTA could take as an option, a program or a file (issue #186). The mail still
// goes to the remaining addresses, but innmail then exits with status 1. innmail used to
// strip every character but [-a-zA-Z0-9+_.@%], mailing another address, and to give the MTA
// arguments beginning with "-" after a non-option argument or "--".
func TestInnmailAddresses(t *testing.T) {
	dir := t.TempDir()
	mta := filepath.Join(t.TempDir(), "mta")
	script := "#! /bin/sh\n" +
		"d='" + dir + "'\n" +
		"n=$(ls \"$d\" | wc -l)\n" +
		"{ printf '%s\\n' \"$@\"; echo; head -c 4096; } > \"$d/.tmp\"\n" +
		"mv \"$d/.tmp\" \"$d/$(printf %03d \"$n\")\"\n"
	if err := os.WriteFile(mta, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"mta": `"` + mta + ` -oi -oem %s"`}})

	for _, tc := range []struct {
		name  string
		args  []string // the arguments of innmail after -s Test
		rcpts []string // the arguments the MTA gets after -oi -oem; nil = innmail fails
		warn  []string // the refused addresses innmail warns about (then it exits with status 1)
	}{
		{"plain", []string{"joe", "a+b_c%d@x.invalid"},
			[]string{"joe", "a+b_c%d@x.invalid"}, nil},
		{"atext", []string{"o'brien@x.invalid", "bounce=a=b/c!d@x.invalid", "{x|y}~#&*?^`$@x.invalid"},
			[]string{"o'brien@x.invalid", "bounce=a=b/c!d@x.invalid", "{x|y}~#&*?^`$@x.invalid"}, nil},
		{"dash after address", []string{"joe", "-bd"},
			[]string{"joe"}, []string{"-bd"}},
		{"dash after --", []string{"--", "-oQ.", "joe"},
			[]string{"joe"}, []string{"-oQ."}},
		{"unsupported", []string{"--", `"John Doe"@x.invalid`, "<a@x.invalid>", "b@[192.0.2.1]", "joe"},
			[]string{"joe"}, []string{`"John Doe"@x.invalid`, "<a@x.invalid>", "b@[192.0.2.1]"}},
		{"only dash", []string{"--", "-x"}, nil, []string{"-x"}},
		{"pipe", []string{"|prog"}, nil, []string{"|prog"}},
		{"file", []string{"/tmp/x"}, nil, []string{"/tmp/x"}},
		{"pipe and file mixed", []string{"joe", "|prog", "/tmp/x", "a+b%c@x.invalid"},
			[]string{"joe", "a+b%c@x.invalid"}, []string{"|prog", "/tmp/x"}},
		{"pipe and slash inside", []string{"a|b@x.invalid", "c/d@x.invalid"},
			[]string{"a|b@x.invalid", "c/d@x.invalid"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := readMails(t, dir)
			args := append([]string{"-s", "Test"}, tc.args...)
			out, err := s.runInput("body\n", "innmail", args...)
			t.Logf("innmail %q: %v\n%s", args, err, out)
			mails := readMails(t, dir)[len(before):]
			if tc.rcpts == nil {
				if err == nil {
					t.Errorf("innmail exited 0 without a valid address")
				}
				if len(mails) > 0 {
					t.Errorf("MTA called without a valid address: %q", mails[0].args)
				}
			} else {
				var ee *exec.ExitError
				switch {
				case len(tc.warn) == 0 && err != nil:
					t.Errorf("innmail failed: %v", err)
				case len(tc.warn) > 0 && (!errors.As(err, &ee) || ee.ExitCode() != 1):
					t.Errorf("innmail returned %v after refusing an address, want exit status 1", err)
				}
				if len(mails) != 1 {
					t.Fatalf("MTA called %d times, want once", len(mails))
				}
				m := mails[0]
				if want := append([]string{"-oi", "-oem"}, tc.rcpts...); !slices.Equal(m.args, want) {
					t.Errorf("MTA arguments %q, want %q", m.args, want)
				}
				if to := "To: " + strings.Join(tc.rcpts, ",\n\t") + "\n"; !strings.HasPrefix(m.text, to) {
					t.Errorf("mail does not begin with %q:\n%s", to, m.text)
				}
				if !strings.HasSuffix(m.text, "\n\nbody\n") {
					t.Errorf("mail does not end with the body:\n%s", m.text)
				}
			}
			for _, w := range tc.warn {
				if !strings.Contains(out, w) {
					t.Errorf("no warning about %q", w)
				}
			}
		})
	}
}

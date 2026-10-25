package e2e

// RFC 3977, 4643 and 4644 conformance of nnrpd and innd, checked by nntp-testkit's conformance
// package: every reply is read by a strict client (CRLF line ends, dot-stuffing, terminators,
// nothing pending after a reply), each check on its own connection.

import (
	"strings"
	"testing"

	"github.com/go-while/nntp-testkit/conformance"
)

// conformanceGroup is the group the checks read and post into.
const conformanceGroup = "e2e.test"

// runConformance runs every check against s with o, logs the report and returns it. The
// group gets three articles first, so the reader checks have numbers to walk (NEXT/LAST,
// LISTGROUP, OVER).
func runConformance(t *testing.T, s *Site, o conformance.Options) conformance.Report {
	t.Helper()
	s.Inject(t, conformanceGroup, 3)
	o.Addr = s.Addr
	o.PostGroup = conformanceGroup
	r := conformance.Run(t, o)
	t.Logf("conformance report:\n%s", r)
	return r
}

// requirePass fails t for each named check that did not pass (a skip included): those are
// the checks the site was set up to reach.
func requirePass(t *testing.T, r conformance.Report, names ...string) {
	t.Helper()
	for _, res := range r.Results {
		for _, n := range names {
			if res.Name == n && res.Status != conformance.Pass {
				t.Errorf("check %s: %s %s", n, res.Status, res.Detail)
			}
		}
	}
}

// TestConformanceReader: nnrpd, reached from 127.0.0.1 (not a peer), passes the reader and
// posting checks, and with the I permission (readers.conf) the IHAVE checks.
func TestConformanceReader(t *testing.T) {
	readers := strings.Replace(DefaultReaders, "access: RPAN", "access: RPAIN", 1)
	s := NewSite(t, SiteOptions{Readers: readers})
	r := runConformance(t, s, conformance.Options{})
	requirePass(t, r, "greeting", "capabilities", "group", "listgroup", "next-last", "article",
		"over", "list-active", "date", "help", "quit-eof", "ihave", "ihave-have", "ihave-malformed")
}

// TestConformanceReaderAuth: nnrpd with AUTHINFO USER/PASS checked by ckpasswd passes the
// RFC 4643 checks, and the reader checks after logging in.
func TestConformanceReaderAuth(t *testing.T) {
	s := NewSite(t, SiteOptions{
		Readers: authReaders,
		Etc:     map[string]string{"passwd.e2e": authPasswd},
	})
	r := runConformance(t, s, conformance.Options{User: "e2e", Pass: "e2e-secret"})
	requirePass(t, r, "authinfo-user-pass", "authinfo-wrong-pass", "authinfo-480",
		"authinfo-after-auth", "authinfo-capabilities", "group", "article")
}

// TestConformancePeer: innd itself, with 127.0.0.1 listed as a streaming peer, passes the
// IHAVE and streaming checks; the reader checks go through MODE READER to nnrpd.
func TestConformancePeer(t *testing.T) {
	s := NewSite(t, SiteOptions{Incoming: `peer e2e {
    hostname: "127.0.0.1, ` + PeerIP + `"
    streaming: true
}
`})
	r := runConformance(t, s, conformance.Options{})
	requirePass(t, r, "greeting", "mode-stream", "check-new", "check-pipelined", "takethis",
		"check-have", "takethis-duplicate", "ihave", "ihave-have", "mode-reader", "group")
}

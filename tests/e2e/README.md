# End-to-end protocol tests

These tests run INN's programs as separate processes and talk to them over real
TCP connections. They complement the TAP suite in `tests/`, which tests
functions and libraries from the inside.

Each test builds a throwaway INN site in a temporary directory: an `inn.conf`
with `pathnews` pointing there, `innd` listening on a free port of 127.0.0.1, a
small `active` file and an empty history database. The test starts `innd -f`,
talks to it, and stops it with `ctlinnd shutdown`. Nothing outside the temporary
directory is touched, and the sites of different tests don't share anything.

The tests are written in Go and use the
[nntp-testkit](https://github.com/go-while/nntp-testkit) library, which only
needs the Go standard library:

- `client`: a strict NNTP client that fails on bare LF line ends, bad
  dot-stuffing, a missing `.` terminator, or stray bytes after a reply.
- `fakeserver`: fake peers and upstream servers for INN's own clients. They can
  defer or reject articles, refuse streaming, drop the connection in the middle
  of a reply, send replies slowly, or behave like specific real-world servers.
- `proc`: starts and stops `innd`, and prints its output when a test fails.

## What is covered

| test | INN programs | what it checks |
|---|---|---|
| `TestReaderGreeting` | innd, nnrpd | a connection from 127.0.0.1 goes to nnrpd: 200 greeting, CAPABILITIES |
| `TestPostAndRead` | innd, nnrpd | POST, then GROUP, ARTICLE, HEAD, BODY, STAT, OVER, LISTGROUP, HDR and NEWNEWS on the result, with lines that need dot-stuffing |
| `TestPeerIHAVE` | innd | IHAVE from a peer (127.0.0.2), duplicate offer gets 435 |
| `TestPeerStreaming` | innd | MODE STREAM, pipelined CHECK and TAKETHIS, 438 and 439 for held articles |
| `TestPeerSocketBuffers` | innd | innd's end of a peer connection keeps the kernel's socket buffers by default and gets `peerrcvbufsize` and `peersndbufsize` when set (#65; reads them with `ss -tm`, Linux only) |
| `TestSIGTERMWhileIdle` | innd | an idle innd exits on SIGTERM |
| `TestSIGTERMWhileBusy` | innd, Perl filter | a SIGTERM received while innd is busy in a slow `filter_art` takes effect once the filter returns, not at the next connection |
| `TestSIGTERMUnderLoad` | innd | innd exits with status 0 on SIGTERM while peers and readers connect |
| `TestFilterHeadPerl` | innd, Perl filter | `filter_art` gets the whole header in `$hdr{__HEAD__}` (#73), byte for byte, and can reject on a header field innd does not know (437, 439, syslog) |
| `TestFilterHeadPython` | innd, Python filter | the same with `art['__HEAD__']`; skipped when innd was built without Python |
| `TestAuthinfo` | innd, nnrpd, ckpasswd | AUTHINFO USER/PASS: 480 before, 481 for a wrong password, 281, then no AUTHINFO in CAPABILITIES and 502 for another (RFC 4643) |
| `TestAcceptedSizeLongConnection` | innd | the "accepted size" innd logs when a feed closes equals the sum of the articles' `:bytes` after about 40 MB in one connection (#366; reads the journal, skipped without access) |
| `TestStartTLS` | innd, nnrpd | STARTTLS: 382, TLS with the site's certificate, no STARTTLS in CAPABILITIES afterwards, reading and posting over TLS, 502 for a second STARTTLS (RFC 4642) |
| `TestAuthinfoRequiresTLS` | innd, nnrpd, ckpasswd | with `require_encryption`, AUTHINFO gets 483 before STARTTLS and works after it |
| `TestStartTLSWithoutCertificate` | innd, nnrpd | without a certificate, STARTTLS gets 580 and the connection goes on without TLS |
| `TestCompress` | innd, nnrpd | COMPRESS DEFLATE: 206, no COMPRESS or STARTTLS in CAPABILITIES afterwards, a 3000-line article with dot-stuffed lines posted and read back intact, pipelining, 502 for COMPRESS, STARTTLS and AUTHINFO (RFC 8054) |
| `TestCompressAfterStartTLS` | innd, nnrpd | COMPRESS DEFLATE on a STARTTLS connection |
| `TestRnews` | innd, rnews | an rnews batch of two articles reaches innd over its local socket |
| `TestInews` | innd, nnrpd, inews | inews posts through nnrpd; the article gets Injection-Info |
| `TestInnxmitStreaming` | innd, innxmit | a batch is streamed to a peer and the batch file is emptied |
| `TestInnxmitIHAVEFallback` | innd, innxmit | a peer that refuses MODE STREAM gets IHAVE |
| `TestInnxmitRefused` | innd, innxmit | articles the peer has or rejects are dropped from the batch |
| `TestInnxmitDeferred` | innd, innxmit | a 436 requeues the rest of the batch, the next run sends it |
| `TestInnfeedStreaming` | innd, innfeed | new articles reach the peer through innfeed |
| `TestInnfeedIHAVEFallback` | innd, innfeed | innfeed falls back to IHAVE |
| `TestInnfeedDebugSnapshotFiles` | innd, innfeed, inncheck | innfeed.conf's debug-file turns on debugging and SIGINT writes the snapshot to snapshot-file (issue #315) |
| `TestGetlist` | getlist | LIST ACTIVE and LIST NEWSGROUPS of a remote server, with patterns |
| `TestPullnews` | innd, pullnews | articles pulled from an upstream are fed to innd, and the mark is recorded |

## Running

You need an installed INN and Go 1.26 or later. The tests must run as the user
INN was configured for (`--with-news-user`), not as root:

```sh
./configure --prefix=$HOME/inn-e2e --with-news-user=$(id -un) --with-news-group=$(id -gn)
make && make install          # as yourself: innbind is not installed setuid, which is fine here
cd tests/e2e
INN_PREFIX=$HOME/inn-e2e go test ./...
```

Without `INN_PREFIX` every test is skipped. `NNTP_TEST_DEADLINE` (default `60s`)
bounds every blocking step, so a hung server fails the test instead of hanging
it. `pullnews` needs the Perl module `Net::NNTP` (libnet).

The tests need a Linux or other Unix system where 127.0.0.2 can be used as a
source address (Linux routes the whole 127.0.0.0/8 to the loopback interface).

## When a test fails

INN programs log their notices to syslog. When a test fails, the harness prints:

- the files in the site's `log` directory;
- what `innd` wrote to stdout and stderr;
- the syslog messages of `innd`, `nnrpd`, `innfeed`, `innxmit`, `ctlinnd` and
  `pullnews` since the site started, read with `journalctl`.

The user running the tests needs read access to the journal for the last one,
e.g. membership in the `systemd-journal` group. Some tests read these messages
too, and are skipped when they are not readable.

On systems without journald, such as Alpine with BusyBox `syslogd` or a
container running `rsyslogd`, set `INN_E2E_SYSLOG` to the file the syslog daemon
writes, for instance `/var/log/messages`; the user running the tests needs read
access to it. The messages are then read from that file instead of the journal.
The file should not be rotated during the run: BusyBox `syslogd` rotates it at
200 KB unless started with `-s 0`.

`ci/e2e-distro` builds INN as the news user on a Linux distribution, sets up
syslog this way when there is no journal, and runs `make check` and these tests.
The workflow `.github/workflows/e2e-distro.yaml` runs it in containers of about
twenty distribution images, weekly and on demand.

## Writing a test

```go
func TestSomething(t *testing.T) {
	s := NewSite(t, SiteOptions{})             // innd with DefaultGroups, readers and a peer
	ids := s.Inject(t, "e2e.test", 2)          // two articles fed by IHAVE from 127.0.0.2
	c := s.Reader(t)                           // nnrpd, through innd
	expect(t, c, 211, "GROUP e2e.test")
	expect(t, c, 223, "STAT %s", ids[0])
}
```

`SiteOptions` replaces `readers.conf`, `incoming.conf`, `newsfeeds` or any
other file in `etc` (`@bindir@` and `@etcdir@` in them become pathbin and the
site's etc directory), adds `inn.conf` parameters, and with `TLS` writes a
self-signed certificate for STARTTLS (`Site.CertPool` trusts it). Other INN programs run with the site's `INNCONF` through `s.Run`
and `s.Ctlinnd`.

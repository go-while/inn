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
| `TestSIGTERMWhileIdle` | innd | an idle innd exits on SIGTERM |
| `TestInnxmitStreaming` | innd, innxmit | a batch is streamed to a peer and the batch file is emptied |
| `TestInnxmitIHAVEFallback` | innd, innxmit | a peer that refuses MODE STREAM gets IHAVE |
| `TestInnxmitRefused` | innd, innxmit | articles the peer has or rejects are dropped from the batch |
| `TestInnxmitDeferred` | innd, innxmit | a 436 requeues the rest of the batch, the next run sends it |
| `TestInnfeedStreaming` | innd, innfeed | new articles reach the peer through innfeed |
| `TestInnfeedIHAVEFallback` | innd, innfeed | innfeed falls back to IHAVE |
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
e.g. membership in the `systemd-journal` group.

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

`SiteOptions` replaces `readers.conf`, `incoming.conf`, `newsfeeds` (with
`@bindir@` for pathbin) or any other file in `etc`, and adds `inn.conf`
parameters. Other INN programs run with the site's `INNCONF` through `s.Run`
and `s.Ctlinnd`.

package e2e

// Per-peer connection limits of incoming.conf: max-connections and hold-time.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// maxCnxIncoming lists PeerIP as a streaming peer with max-connections and hold-time.
func maxCnxIncoming(maxCnx, holdTime int) string {
	return fmt.Sprintf(`peer e2e {
    hostname: "%s"
    streaming: true
    max-connections: %d
    hold-time: %d
}
`, PeerIP, maxCnx, holdTime)
}

// nntpChannels runs "ctlinnd name" with an empty argument and returns the NNTP channel
// lines and how many of them show the status "paused" (<name>:<number>:nntp:<idle>:paused,
// ctlinnd(8)).
func nntpChannels(tb testing.TB, s *Site) (lines []string, paused int) {
	tb.Helper()
	out := s.Ctlinnd(tb, "name", "")
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if !strings.Contains(line, ":nntp:") {
			continue
		}
		lines = append(lines, line)
		if strings.HasSuffix(strings.TrimSpace(line), ":paused") {
			paused++
		}
	}
	return lines, paused
}

// openPeers opens n peer connections, checks their 200 greeting and turns on streaming, so
// that innd has looked at each of them at least once.
func openPeers(tb testing.TB, s *Site, n int) []*client.Conn {
	tb.Helper()
	var ps []*client.Conn
	for i := range n {
		p := s.Peer(tb)
		if code, err := client.ParseCode(p.Greeting); err != nil || code != 200 {
			tb.Fatalf("peer connection %d: greeting %q, want 200", i+1, p.Greeting)
		}
		expect(tb, p, 203, "MODE STREAM")
		ps = append(ps, p)
	}
	return ps
}

// TestPeerMaxConnections: with max-connections 2 and no hold-time, a peer's third connection
// gets 400 and is closed (incoming.conf(5): "A value of zero specifies immediate close"), the
// accepted ones are not shown as paused by "ctlinnd name", and a connection freed by the
// peer can be used again.
func TestPeerMaxConnections(t *testing.T) {
	s := NewSite(t, SiteOptions{Incoming: maxCnxIncoming(2, 0)})
	ps := openPeers(t, s, 2)

	lines, paused := nntpChannels(t, s)
	t.Logf("ctlinnd name '' with 2 connections:\n%s", strings.Join(lines, "\n"))
	if len(lines) != 2 || paused != 0 {
		t.Errorf("ctlinnd name '': %d NNTP channels, %d paused; want 2, none paused", len(lines), paused)
	}

	p3 := s.Peer(t)
	t.Logf("third connection: greeting %q", p3.Greeting)
	if code, _ := client.ParseCode(p3.Greeting); code != 400 || !strings.Contains(p3.Greeting, "limited to 2 connections") {
		t.Fatalf("third connection of a peer with max-connections 2: greeting %q, want 400 You are limited to 2 connections", p3.Greeting)
	}
	if line, err := p3.ReadLine(); err == nil {
		t.Errorf("third connection: got %q after the 400, want the connection closed", line)
	}

	expect(t, ps[0], 205, "QUIT")
	ps[0].Close()
	waitFor(t, 10*time.Second, "innd to close the first and the third connection", func() bool {
		lines, _ := nntpChannels(t, s)
		return len(lines) == 1
	})
	p4 := s.Peer(t)
	if code, _ := client.ParseCode(p4.Greeting); code != 200 {
		t.Fatalf("connection after one of two was closed: greeting %q, want 200", p4.Greeting)
	}
	expect(t, p4, 203, "MODE STREAM")
}

// TestPeerMaxConnectionsHoldTime: with max-connections 2 and hold-time 3, a peer's third
// connection is greeted but not served and is shown as paused (ctlinnd(8)); when one of the
// first two closes, it is served; a connection that stays over the limit is closed after the
// hold time (incoming.conf(5): "the hold time before closing, if the connection is over
// max-connections").
func TestPeerMaxConnectionsHoldTime(t *testing.T) {
	const holdTime = 3
	s := NewSite(t, SiteOptions{Incoming: maxCnxIncoming(2, holdTime)})
	ps := openPeers(t, s, 2)

	p3 := s.Peer(t)
	if code, _ := client.ParseCode(p3.Greeting); code != 200 {
		t.Fatalf("third connection: greeting %q, want 200 (held, not refused)", p3.Greeting)
	}
	send(t, p3, "MODE STREAM\r\n")
	if err := p3.ExpectNoPending(1500 * time.Millisecond); err != nil {
		t.Fatalf("third connection of a peer with max-connections 2 was served: %v", err)
	}
	lines, paused := nntpChannels(t, s)
	t.Logf("ctlinnd name '' with a held third connection:\n%s", strings.Join(lines, "\n"))
	if len(lines) != 3 || paused != 1 {
		t.Errorf("ctlinnd name '': %d NNTP channels, %d paused; want 3, one paused", len(lines), paused)
	}

	expect(t, ps[0], 205, "QUIT")
	ps[0].Close()
	status(t, p3, 203, "MODE STREAM on the held connection after the first one closed")

	p4 := s.Peer(t)
	if code, _ := client.ParseCode(p4.Greeting); code != 200 {
		t.Fatalf("fourth connection: greeting %q, want 200 (held, not refused)", p4.Greeting)
	}
	start := time.Now()
	send(t, p4, "MODE STREAM\r\n")
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	p4.Deadline = 30 * time.Second
	go func() {
		line, err := p4.ReadLine()
		done <- result{line, err}
	}()
	// innd checks the hold time while its main loop walks the channels, which it does when
	// another channel is busy: keep the second connection busy.
	var r result
	for wait := true; wait; {
		select {
		case r = <-done:
			wait = false
		case <-time.After(300 * time.Millisecond):
			if time.Since(start) > 20*time.Second {
				t.Fatalf("fourth connection still open %v after a hold-time of %ds", time.Since(start), holdTime)
			}
			expect(t, ps[1], 238, "CHECK %s", msgID(t))
		}
	}
	elapsed := time.Since(start)
	t.Logf("fourth connection: %q, %v after %v", r.line, r.err, elapsed.Round(10*time.Millisecond))
	if r.err == nil {
		t.Fatalf("fourth connection of a peer with max-connections 2 was served: %q", r.line)
	}
	if elapsed < (holdTime-1)*time.Second {
		t.Errorf("fourth connection closed after %v, before the hold-time of %ds", elapsed, holdTime)
	}
	expect(t, p3, 238, "CHECK %s", msgID(t))
	expect(t, ps[1], 238, "CHECK %s", msgID(t))
}

// TestPeerMaxConnectionsSample runs innd with the sample incoming.conf that "make install"
// puts in place, which sets max-connections to 8 for every peer and lists 127.0.0.1 as the
// peer ME: the ninth connection from 127.0.0.1 gets 400 and is closed, and innd logs "too
// many connections from ME".
func TestPeerMaxConnectionsSample(t *testing.T) {
	sample, err := os.ReadFile(filepath.Join(prefix(t), "etc", "incoming.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sample), "max-connections:    8") {
		t.Fatalf("the sample incoming.conf does not set max-connections to 8:\n%s", sample)
	}
	s := NewSite(t, SiteOptions{Incoming: string(sample)})
	for i := range 8 {
		c := client.Dial(t, s.Addr)
		if code, _ := client.ParseCode(c.Greeting); code != 200 {
			t.Fatalf("connection %d from 127.0.0.1: greeting %q, want 200", i+1, c.Greeting)
		}
		expect(t, c, 203, "MODE STREAM")
	}
	c9 := client.Dial(t, s.Addr)
	t.Logf("ninth connection from 127.0.0.1: greeting %q", c9.Greeting)
	line, err := c9.ReadLine()
	t.Logf("ninth connection after the greeting: %q, %v", line, err)
	if code, _ := client.ParseCode(c9.Greeting); code != 400 || !strings.Contains(c9.Greeting, "limited to 8 connections") {
		t.Fatalf("ninth connection with the sample incoming.conf: greeting %q, want 400 You are limited to 8 connections", c9.Greeting)
	}
	if err == nil {
		t.Errorf("ninth connection: got %q after the 400, want the connection closed", line)
	}
	lines, paused := nntpChannels(t, s)
	t.Logf("ctlinnd name '':\n%s", strings.Join(lines, "\n"))
	if paused != 0 {
		t.Errorf("ctlinnd name '': %d of %d NNTP channels paused, want none", paused, len(lines))
	}
	var found []string
	for _, l := range s.Syslog(t) {
		if strings.Contains(l, "too many") || strings.Contains(l, "connected") || strings.Contains(l, "closed") {
			found = append(found, l)
		}
	}
	t.Logf("innd syslog:\n%s", strings.Join(found, "\n"))
	if !strings.Contains(strings.Join(found, "\n"), "too many connections from ME") {
		t.Errorf("innd did not log \"too many connections from ME\"")
	}
}

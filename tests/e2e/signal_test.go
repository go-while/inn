package e2e

import (
	"fmt"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestSIGTERMWhileIdle: innd exits on SIGTERM while no connection is active. `docker stop`
// and `kill` rely on that; when innd does not exit, they SIGKILL it after their timeout
// and the active file and history are not flushed.
func TestSIGTERMWhileIdle(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	time.Sleep(500 * time.Millisecond) // let the readiness probe's nnrpd go: innd is idle
	if err := s.Proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Proc.Done():
	case <-time.After(5 * time.Second):
		t.Errorf("innd still running 5s after SIGTERM while idle")
	}
}

// slowFilter is a Perl filter_art that keeps innd busy for 3 seconds per article.
const slowFilter = `sub filter_art {
    sleep 3;
    return "";
}
1;
`

// TestSIGTERMWhileBusy: a SIGTERM that arrives while innd is busy (here in a slow Perl
// filter) is blocked until innd waits for activity again; innd must then exit at once rather
// than at the next connection or timeout.
func TestSIGTERMWhileBusy(t *testing.T) {
	s := NewSite(t, SiteOptions{
		InnConf: map[string]string{"pathfilter": "@etcdir@"},
		Etc:     map[string]string{"filter_innd.pl": slowFilter},
	})
	p := s.Peer(t)
	id := msgID(t)
	expect(t, p, 335, "IHAVE %s", id)
	send(t, p, wire(newArticle(id, "e2e.test", "slow filter", "body")))
	time.Sleep(time.Second) // innd is now in filter_art
	if err := s.Proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Proc.Done():
	case <-time.After(10 * time.Second):
		t.Errorf("innd still running 10s after a SIGTERM received while busy in a 3s filter")
	}
}

// TestSIGTERMUnderLoad: innd exits cleanly (status 0) on SIGTERM while peers and readers are
// connecting and feeding. Each round is a fresh site.
func TestSIGTERMUnderLoad(t *testing.T) {
	for round := range 5 {
		t.Run(fmt.Sprint(round), func(t *testing.T) {
			s := NewSite(t, SiteOptions{})
			stop := make(chan struct{})
			var wg sync.WaitGroup
			for w := range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; ; i++ {
						select {
						case <-stop:
							return
						default:
						}
						load(s, w%2 == 0, fmt.Sprintf("<load.%d.%d.%d.%d@%s>", round, w, i, time.Now().UnixNano(), PathHost))
					}
				}()
			}
			time.Sleep(time.Duration(200+100*round) * time.Millisecond)
			if err := s.Proc.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case <-s.Proc.Done():
			case <-time.After(10 * time.Second):
				t.Errorf("innd still running 10s after SIGTERM under load")
			}
			close(stop)
			wg.Wait()
			if err := s.Proc.Wait(); err != nil {
				t.Errorf("innd exit after SIGTERM under load: %v\n%s", err, s.Proc.Stderr())
			}
		})
	}
}

// load makes one connection to the site and ignores every error: from PeerIP it offers one
// article with IHAVE, from 127.0.0.1 it reads the group list.
func load(s *Site, peer bool, id string) {
	d := net.Dialer{Timeout: time.Second}
	if peer {
		d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(PeerIP)}
	}
	nc, err := d.Dial("tcp", s.Addr)
	if err != nil {
		time.Sleep(10 * time.Millisecond)
		return
	}
	defer func() { _ = nc.Close() }()
	_ = nc.SetDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	if _, err := nc.Read(buf); err != nil {
		return
	}
	if peer {
		_, _ = nc.Write([]byte("IHAVE " + id + "\r\n"))
		if _, err := nc.Read(buf); err != nil {
			return
		}
		_, _ = nc.Write([]byte(wire(newArticle(id, "e2e.test", "load", "body"))))
	} else {
		_, _ = nc.Write([]byte("LIST ACTIVE\r\n"))
	}
	_, _ = nc.Read(buf)
	_, _ = nc.Write([]byte("QUIT\r\n"))
}

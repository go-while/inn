package e2e

import (
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	testkit "github.com/go-while/nntp-testkit"
	"github.com/go-while/nntp-testkit/client"
)

// TestPeerSocketBuffers reads the buffer sizes of innd's end of a peer connection with ss(8)
// (#65). Without peerrcvbufsize and peersndbufsize in inn.conf innd leaves the kernel defaults
// alone (it used to force 24 KB, which also turns off Linux's receive buffer autotuning); with
// them it sets the requested sizes, which Linux reports doubled.
func TestPeerSocketBuffers(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads Linux socket buffer sizes")
	}
	if _, err := exec.LookPath("ss"); err != nil {
		t.Skip("ss(8) not found")
	}
	const old = 2 * 24 * 1024 // what Linux reports for the 24 KB innd used to set

	t.Run("default", func(t *testing.T) {
		initial := sysctlField(t, "/proc/sys/net/ipv4/tcp_rmem", 1)
		if initial <= old {
			t.Skipf("tcp_rmem default %d is not above %d", initial, old)
		}
		s := NewSite(t, SiteOptions{})
		rb, tb := peerSocketBuffers(t, s)
		t.Logf("innd's peer socket: rb %d, tb %d", rb, tb)
		// Autotuning only grows the receive buffer from tcp_rmem's default.
		if rb < initial {
			t.Errorf("receive buffer %d, want the kernel default %d or more", rb, initial)
		}
		if tb == old {
			t.Errorf("send buffer %d, want the kernel default", tb)
		}
	})

	t.Run("set", func(t *testing.T) {
		const rcv, snd = 65536, 32768
		s := NewSite(t, SiteOptions{InnConf: map[string]string{
			"peerrcvbufsize": strconv.Itoa(rcv),
			"peersndbufsize": strconv.Itoa(snd),
		}})
		rb, tb := peerSocketBuffers(t, s)
		t.Logf("innd's peer socket: rb %d, tb %d", rb, tb)
		wantRb := 2 * min(rcv, sysctlField(t, "/proc/sys/net/core/rmem_max", 0))
		wantTb := 2 * min(snd, sysctlField(t, "/proc/sys/net/core/wmem_max", 0))
		if rb != wantRb || tb != wantTb {
			t.Errorf("innd's peer socket: rb %d, tb %d; want rb %d, tb %d", rb, tb, wantRb, wantTb)
		}
	})
}

var skmemRe = regexp.MustCompile(`skmem:\([^)]*\brb(\d+),t\d+,tb(\d+)`)

// peerSocketBuffers connects from PeerIP, waits for the greeting (innd sets the socket
// options before it), and returns the receive and send buffer sizes of innd's end of the
// connection as ss -tm reports them.
func peerSocketBuffers(tb testing.TB, s *Site) (int, int) {
	tb.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(PeerIP)}, Timeout: testkit.Deadline()}
	nc, err := d.Dial("tcp", s.Addr)
	if err != nil {
		tb.Fatalf("dial %s from %s: %v", s.Addr, PeerIP, err)
	}
	c := client.New(tb, nc)
	defer c.Close()
	if g, err := c.ReadGreeting(); err != nil || !strings.HasPrefix(g, "200") {
		tb.Fatalf("peer greeting %q, %v", g, err)
	}
	out, err := exec.Command("ss", "-tmn", "state", "established",
		"src", s.Addr, "dst", nc.LocalAddr().String()).CombinedOutput()
	if err != nil {
		tb.Fatalf("ss: %v\n%s", err, out)
	}
	m := skmemRe.FindSubmatch(out)
	if m == nil {
		tb.Fatalf("no skmem for %s -> %s in ss output:\n%s", s.Addr, nc.LocalAddr(), out)
	}
	rb, _ := strconv.Atoi(string(m[1]))
	sb, _ := strconv.Atoi(string(m[2]))
	return rb, sb
}

// sysctlField returns the n-th number of a file under /proc/sys.
func sysctlField(tb testing.TB, path string, n int) int {
	tb.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		tb.Skipf("read %s: %v", path, err)
	}
	f := strings.Fields(string(b))
	if len(f) <= n {
		tb.Skipf("%s: %q has no field %d", path, b, n)
	}
	v, err := strconv.Atoi(f[n])
	if err != nil {
		tb.Skipf("%s: %v", path, err)
	}
	return v
}

package e2e

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Send actual read-only IPC requests: ordinary WAL NNTP readers bypass the
// server entirely, so merely opening many NNTP readers would not prove this.
func TestOvsqliteIdleWithReaderTraffic(t *testing.T) {
	s := ovsqliteIdleSite(t, "walmode: true\n")
	r := s.Reader(t)
	expect(t, r, 211, "GROUP e2e.test")
	conn, err := net.DialTimeout("unix", filepath.Join(s.Dir, "run", "ovsqlite.sock"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// ovsqlite-private.h: hello=0, protocol version=1, OV_READ=1.
	hello := binary.NativeEndian.AppendUint32(nil, 1)
	hello = binary.NativeEndian.AppendUint32(hello, 1)
	if code, err := ovsqliteReadRPC(conn, 0, hello); err != nil || code != 0 {
		t.Fatalf("read-only hello response %d (%v), want response_ok", code, err)
	}
	type pressureResult struct {
		count  int
		maxGap time.Duration
		err    error
	}
	stop, ready, done := make(chan struct{}), make(chan struct{}), make(chan pressureResult, 1)
	go func() {
		var result pressureResult
		last := time.Now()
		defer func() { done <- result }()
		for {
			select {
			case <-stop:
				return
			default:
			}
			group := "e2e.test"
			payload := binary.NativeEndian.AppendUint16(nil, uint16(len(group)))
			payload = append(payload, group...)
			// request_get_groupinfo=3, response_groupinfo=2.
			if code, err := ovsqliteReadRPC(conn, 3, payload); err != nil || code != 2 {
				result.err = fmt.Errorf("get_groupinfo response %d (%v), want response_groupinfo", code, err)
				return
			}
			now := time.Now()
			if gap := now.Sub(last); gap > result.maxGap {
				result.maxGap = gap
			}
			last = now
			result.count++
			if result.count == 1 {
				close(ready)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	defer func() {
		close(stop)
		result := <-done
		t.Logf("read-only IPC requests completed: %d; max response gap %s", result.count, result.maxGap)
		if result.err != nil || result.count < 10 || result.maxGap >= 200*time.Millisecond {
			t.Errorf("pressure precondition failed: %+v", result)
		}
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("read-only IPC traffic failed to start")
	}
	id := s.Inject(t, "e2e.test", 1)[0]
	accepted := time.Now()
	first := expect(t, r, 211, "GROUP e2e.test")
	if elapsed := time.Since(accepted); elapsed >= 200*time.Millisecond || field(first, 1) != "0" {
		t.Fatalf("pressure precondition failed: first GROUP after %s is %q; need uncommitted row before grace", elapsed, first)
	}
	for n := 0; time.Since(accepted) < 2*time.Second; n++ {
		expect(t, r, 223, "STAT %s", id)
		line := expect(t, r, 211, "GROUP e2e.test")
		if line == "211 1 1 1 e2e.test" {
			t.Logf("visible after %s with active read-only IPC and %d persistent GROUP polls", time.Since(accepted), n+1)
			expect(t, r, 205, "QUIT")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("reader traffic postponed the idle commit beyond two seconds")
}

// A small valid-request client for the local native-endian ovsqlite protocol.
func ovsqliteReadRPC(conn net.Conn, code byte, payload []byte) (byte, error) {
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return 0, err
	}
	request := binary.NativeEndian.AppendUint32(nil, uint32(5+len(payload)))
	request = append(request, code)
	request = append(request, payload...)
	if n, err := conn.Write(request); err != nil || n != len(request) {
		return 0, fmt.Errorf("read-only IPC write %d/%d: %v", n, len(request), err)
	}
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, err
	}
	size := binary.NativeEndian.Uint32(header[:])
	if size < 5 || size > 4096 {
		return 0, fmt.Errorf("unexpected read-only IPC response size %d", size)
	}
	response := make([]byte, int(size)-4)
	if _, err := io.ReadFull(conn, response); err != nil {
		return 0, err
	}
	return response[0], nil
}

func ovsqliteIdleSite(t *testing.T, conf string) *Site {
	t.Helper()
	return NewSite(t, SiteOptions{
		InnConf: map[string]string{"ovmethod": "ovsqlite"},
		Etc:     map[string]string{"ovsqlite.conf": conf},
		BeforeStart: func(tb testing.TB, s *Site) {
			startDaemon(tb, s, filepath.Join(s.Dir, "run", "ovsqlite.sock"), "ovsqlite-server", "-d")
		},
	})
}

// Continuous writes should retain batching, but must not move the original
// transaction deadline. Reject a test run whose own gaps could trigger idle.
func TestOvsqliteIdleBatchingAndTimeCeiling(t *testing.T) {
	for _, tc := range []struct {
		name, limit string
		rows        int
		seeDuring   bool
	}{
		{"batch-until-idle", "5", 8, false},
		{"time-ceiling-during-writes", "1", 30, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ovsqliteIdleSite(t, "walmode: true\ntransrowlimit: 10000\ntranstimelimit: "+tc.limit+"\n")
			p, r := s.Peer(t), s.Reader(t)
			expect(t, r, 211, "GROUP e2e.test")
			var lastAccepted time.Time
			seenDuring := false
			for i := 0; i < tc.rows; i++ {
				id := msgID(t)
				expect(t, p, 335, "IHAVE %s", id)
				send(t, p, wire(newArticle(id, "e2e.test", "paced transaction", "body")))
				status(t, p, 235, "IHAVE "+id)
				now := time.Now()
				if !lastAccepted.IsZero() && now.Sub(lastAccepted) >= 200*time.Millisecond {
					t.Fatalf("test precondition failed: write gap %s could permit an idle commit", now.Sub(lastAccepted))
				}
				lastAccepted = now
				line := expect(t, r, 211, "GROUP e2e.test")
				if field(line, 1) != "0" {
					seenDuring = true
					if !tc.seeDuring {
						t.Fatalf("continuous writes committed before idle or configured limit: %s", line)
					}
				}
				time.Sleep(50 * time.Millisecond)
			}
			expect(t, p, 205, "QUIT")
			if tc.seeDuring && !seenDuring {
				t.Error("one-second transaction ceiling was postponed by continuous writes")
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				line := expect(t, r, 211, "GROUP e2e.test")
				if field(line, 1) == fmt.Sprint(tc.rows) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("quiet transaction not visible within two seconds: %s", line)
				}
				time.Sleep(20 * time.Millisecond)
			}
			expect(t, r, 205, "QUIT")
		})
	}
}

// Diagnostic only: time accepted IHAVEs, not eventual visibility or durability.
// Use the same binary/host for paired before/after measurements and retain all
// samples rather than describing scheduler noise as a throughput guarantee.
func TestOvsqliteIngestThroughput(t *testing.T) {
	if os.Getenv("INN_E2E_BENCHMARK") != "1" {
		t.Skip("set INN_E2E_BENCHMARK=1 for diagnostic ingest measurements")
	}
	for _, conf := range []struct{ name, text string }{
		{"wal-batched", "walmode: true\n"},
		{"wal-rowlimit1", "walmode: true\ntransrowlimit: 1\n"},
		{"non-wal", "walmode: false\n"},
	} {
		for sample := 1; sample <= 3; sample++ {
			t.Run(fmt.Sprintf("%s/sample%d", conf.name, sample), func(t *testing.T) {
				s := ovsqliteIdleSite(t, conf.text)
				p := s.Peer(t)
				body := strings.Repeat("throughput input ", 16)
				feed := func(n int) {
					for i := 0; i < n; i++ {
						id := msgID(t)
						expect(t, p, 335, "IHAVE %s", id)
						send(t, p, wire(newArticle(id, "e2e.test", "throughput sample", body)))
						status(t, p, 235, "IHAVE "+id)
					}
				}
				feed(100)
				start := time.Now()
				feed(1000)
				duration := time.Since(start)
				expect(t, p, 205, "QUIT")
				t.Logf("THROUGHPUT config=%s sample=%d accepted=1000 duration=%s rate=%.1f articles/s",
					conf.name, sample, duration, 1000/duration.Seconds())
			})
		}
	}
}

// Count committed WAL frames without instrumenting production. The test keeps
// the server connection open, disables checkpoint-triggered truncation, and
// reads the final count only after GROUP confirms every row is committed.
func walCommitFrames(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Size() > 16<<20 {
		t.Fatalf("cannot read bounded WAL fixture: info=%v err=%v", info, err)
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 32 {
		t.Fatalf("cannot read WAL header: %v (%d bytes)", err, len(b))
	}
	magic := binary.BigEndian.Uint32(b[:4])
	if magic != 0x377f0682 && magic != 0x377f0683 {
		t.Fatalf("unexpected WAL magic %#x", magic)
	}
	page := int(binary.BigEndian.Uint32(b[8:12]))
	if page == 1 {
		page = 65536
	}
	if page < 512 || page > 65536 || page&(page-1) != 0 {
		t.Fatalf("invalid fixture WAL page size %d", page)
	}
	commits := 0
	for offset := 32; offset+24+page <= len(b); offset += 24 + page {
		frame := b[offset : offset+24]
		if string(frame[8:16]) != string(b[16:24]) {
			break // Stale frames past the current WAL generation.
		}
		if binary.BigEndian.Uint32(frame[4:8]) != 0 {
			commits++
		}
	}
	return commits
}

func TestOvsqliteModerateCommitRate(t *testing.T) {
	if os.Getenv("INN_E2E_BENCHMARK") != "1" {
		t.Skip("set INN_E2E_BENCHMARK=1 for diagnostic commit-rate measurements")
	}
	for _, tc := range []struct {
		name, extra string
		rows        int
		pause       time.Duration
	}{
		{"10-per-second", "", 40, 100 * time.Millisecond},
		{"2-per-second", "", 12, 500 * time.Millisecond},
		{"rowlimit1-control", "transrowlimit: 1\n", 20, 100 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ovsqliteIdleSite(t, "walmode: true\nwalcheckpointthreshold: 1000000\n"+tc.extra)
			wal := filepath.Join(s.Dir, "spool", "overview", "ovsqlite.db-wal")
			before := walCommitFrames(t, wal)
			p := s.Peer(t)
			var last time.Time
			var maxGap time.Duration
			for i := 0; i < tc.rows; i++ {
				id := msgID(t)
				expect(t, p, 335, "IHAVE %s", id)
				send(t, p, wire(newArticle(id, "e2e.test", "moderate commit rate", "body")))
				status(t, p, 235, "IHAVE "+id)
				now := time.Now()
				if !last.IsZero() && now.Sub(last) > maxGap {
					maxGap = now.Sub(last)
				}
				last = now
				if i+1 < tc.rows {
					time.Sleep(tc.pause)
				}
			}
			expect(t, p, 205, "QUIT")
			if tc.pause < 250*time.Millisecond && maxGap >= 200*time.Millisecond {
				t.Fatalf("test precondition failed: moderate-rate gap %s could permit idle commit", maxGap)
			}
			r := s.Reader(t)
			deadline := time.Now().Add(12 * time.Second)
			for field(expect(t, r, 211, "GROUP e2e.test"), 1) != fmt.Sprint(tc.rows) {
				if time.Now().After(deadline) {
					t.Fatal("not all benchmark rows committed within original time ceiling plus margin")
				}
				time.Sleep(20 * time.Millisecond)
			}
			expect(t, r, 205, "QUIT")
			commits := walCommitFrames(t, wal) - before
			if commits <= 0 || (tc.name == "rowlimit1-control" && commits != tc.rows) {
				t.Fatalf("unexpected WAL commit count %d for %d articles", commits, tc.rows)
			}
			t.Logf("COMMIT_RATE config=%s articles=%d commits=%d commits_per_article=%.4f max_accept_gap=%s",
				tc.name, tc.rows, commits, float64(commits)/float64(tc.rows), maxGap)
		})
	}
}

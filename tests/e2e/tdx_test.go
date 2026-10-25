package e2e

// The tradindexed overview method and tdx-util.

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// Layout of group.index on LP64 systems (storage/tradindexed/tdx-structure.h): a header of
// a magic number, TDX_HASH_SIZE bucket heads and the free list head (all int), then
// struct group_entry records with the MD5 hash of the group name at offset 0 and the next
// record of the hash chain at offset 80.
const (
	tdxHashSize    = 16 * 1024
	tdxHeaderSize  = 4 + 4*tdxHashSize + 4
	tdxEntrySize   = 88
	tdxEntryNextAt = 80
)

// tdxBucket is index_bucket() of tdx-group.c for a group name.
func tdxBucket(name string) uint32 {
	h := md5.Sum([]byte(name))
	return binary.NativeEndian.Uint32(h[:4]) % tdxHashSize
}

// capped keeps the first 4 KiB written to it: a program stuck in a loop may warn forever.
// It must not embed bytes.Buffer, whose ReadFrom would let io.Copy bypass Write.
type capped struct{ buf bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := 4<<10 - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }

// runFor runs an INN program of the site like Site.TryRun, but kills it after d and keeps
// only the beginning of its output.
func runFor(s *Site, d time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(s.Prefix, "bin", name), args...)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), s.Env...)
	var out capped
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), fmt.Errorf("%s %s did not finish within %v", name, strings.Join(args, " "), d)
	}
	return out.String(), err
}

// TestTdxUtilHashChainLoop: a loop of length two in a hash chain of group.index is reported
// by tdx-util -A and repaired by tdx-util -F, which used to loop forever on it; afterwards
// the audit is clean and both groups of the chain still work (issue #216).
func TestTdxUtilHashChainLoop(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("the group.index layout is only known here for LP64 systems")
	}
	// Two groups in the same hash bucket.
	var first, second string
	seen := map[uint32]string{}
	for i := 0; first == ""; i++ {
		name := fmt.Sprintf("e2e.loop.%d", i)
		b := tdxBucket(name)
		if other, ok := seen[b]; ok {
			first, second = other, name
		}
		seen[b] = name
	}
	s := NewSite(t, SiteOptions{Groups: append([]Group{{Name: first}, {Name: second}}, DefaultGroups...)})
	s.Inject(t, first, 2)
	s.Inject(t, second, 2)

	path := filepath.Join(s.Dir, "spool", "overview", "group.index")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < tdxHeaderSize || (len(data)-tdxHeaderSize)%tdxEntrySize != 0 {
		t.Skipf("group.index has %d bytes, not the expected layout", len(data))
	}
	recno := func(off int) int32 { return int32(binary.NativeEndian.Uint32(data[off:])) }
	find := func(name string) int32 {
		h := md5.Sum([]byte(name))
		for i := 0; tdxHeaderSize+(i+1)*tdxEntrySize <= len(data); i++ {
			if bytes.Equal(data[tdxHeaderSize+i*tdxEntrySize:][:16], h[:]) {
				return int32(i)
			}
		}
		t.Fatalf("%s not found in group.index", name)
		return -1
	}
	a, b := find(first), find(second)
	bucket := int(tdxBucket(first))
	head := recno(4 + 4*bucket)
	// The chain is head -> x -> y -> -1, with {x, y} = {a, b}; make y point back to x.
	x, y := a, b
	if head == b {
		x, y = b, a
	}
	if head != x || recno(tdxHeaderSize+int(x)*tdxEntrySize+tdxEntryNextAt) != y ||
		recno(tdxHeaderSize+int(y)*tdxEntrySize+tdxEntryNextAt) != -1 {
		t.Fatalf("bucket %d does not hold the chain %d -> %d", bucket, x, y)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	var loop [4]byte
	binary.NativeEndian.PutUint32(loop[:], uint32(x))
	if _, err := f.WriteAt(loop[:], int64(tdxHeaderSize+int(y)*tdxEntrySize+tdxEntryNextAt)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("bucket %d: %d (%s) -> %d (%s) -> %d", bucket, x, first, y, second, x)

	const limit = 20 * time.Second
	out, err := runFor(s, limit, "tdx-util", "-A")
	t.Logf("tdx-util -A:\n%s", out)
	if err != nil && strings.Contains(err.Error(), "did not finish") {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reachable from multiple paths") {
		t.Errorf("tdx-util -A does not report the loop")
	}
	out, err = runFor(s, limit, "tdx-util", "-F")
	t.Logf("tdx-util -F:\n%s", out)
	if err != nil && strings.Contains(err.Error(), "did not finish") {
		t.Fatal(err)
	}
	out, err = runFor(s, limit, "tdx-util", "-A")
	if err != nil {
		t.Errorf("tdx-util -A after -F: %v", err)
	}
	if strings.Contains(out, "tradindexed:") {
		t.Errorf("tdx-util -A after -F is not clean:\n%s", out)
	}

	// innd still files articles in both groups, and nnrpd reads them.
	s.Inject(t, first, 1)
	s.Inject(t, second, 1)
	c := s.Reader(t)
	for _, g := range []string{first, second} {
		if got, want := expect(t, c, 211, "GROUP %s", g), fmt.Sprintf("211 3 1 3 %s", g); got != want {
			t.Errorf("GROUP %s: %q, want %q", g, got, want)
		}
		expect(t, c, 224, "OVER 1-3")
		if got := c.ReadDotLines(); len(got) != 3 {
			t.Errorf("OVER 1-3 in %s: %d lines, want 3: %q", g, len(got), got)
		}
	}
	expect(t, c, 205, "QUIT")
}

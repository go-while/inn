package e2e

// procbatch, which splits a funnel or innfeed-dropped file into backlog files by peer.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	testkit "github.com/go-while/nntp-testkit"
)

// TestProcbatchCheck runs "procbatch -c" on an innfeed-dropped file with the storage tokens
// of two articles in the spool and of a cancelled one, with tradspool and with timehash: the
// tokens of the present articles are kept, the cancelled one is dropped (#189; procbatch used
// to look for a file named after the token in patharticles and dropped every token). Entries
// giving a pathname are still checked for a file in patharticles.
func TestProcbatchCheck(t *testing.T) {
	for _, tc := range []struct{ name, storage string }{
		{"tradspool", ""}, // the sample storage.conf
		{"timehash", "method timehash {\n    newsgroups: *\n    class: 0\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := SiteOptions{}
			if tc.storage != "" {
				o.Etc = map[string]string{"storage.conf": tc.storage}
			}
			s := NewSite(t, o)
			ids := s.Inject(t, "e2e.test", 3)
			tokens := make([]string, len(ids))
			for i, id := range ids {
				tokens[i] = strings.TrimSpace(s.Run(t, "grephistory", id))
				if !strings.HasPrefix(tokens[i], "@") {
					t.Fatalf("grephistory %s = %q, want a storage token", id, tokens[i])
				}
			}
			s.Ctlinnd(t, "cancel", ids[1])
			if out, err := s.TryRun("sm", "-H", tokens[1]); err == nil {
				t.Fatalf("sm -H %s still retrieves the cancelled article:\n%s", tokens[1], out)
			}

			// A pathname entry whose file exists, and one whose file does not.
			old := filepath.Join(s.Dir, "spool", "articles", "e2e", "old")
			if err := os.MkdirAll(old, 0o755); err != nil {
				t.Fatal(err)
			}
			s.writeFile(t, "spool/articles/e2e/old/1", "Message-ID: <old1@e2e.test>\n\nold\n")

			dropped := tokens[0] + " " + ids[0] + " peer1 peer2\n" +
				tokens[1] + " " + ids[1] + " peer1 peer2\n" +
				tokens[2] + " " + ids[2] + " peer2\n" +
				"e2e/old/1 <old1@e2e.test> peer1\n" +
				"e2e/old/2 <old2@e2e.test> peer1\n"
			s.writeFile(t, "tmp/innfeed-dropped.A000001", dropped)
			outDir := filepath.Join(s.Dir, "tmp", "out")
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				t.Fatal(err)
			}
			out := s.Run(t, "procbatch", "-c", "-v", "-d", outDir,
				filepath.Join(s.Dir, "tmp", "innfeed-dropped.A000001"))
			t.Logf("procbatch -c -v:\n%s", out)

			want := map[string]string{
				"peer1.tmp": tokens[0] + " " + ids[0] + "\n" + "e2e/old/1 <old1@e2e.test>\n",
				"peer2.tmp": tokens[0] + " " + ids[0] + "\n" + tokens[2] + " " + ids[2] + "\n",
			}
			for name, w := range want {
				b, err := os.ReadFile(filepath.Join(outDir, name))
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if string(b) != w {
					t.Errorf("%s:\n%s\nwant:\n%s", name, b, w)
				}
			}
			for _, w := range []string{"Dropping article: " + tokens[1], "2 articles dropped"} {
				if !strings.Contains(out, w) {
					t.Errorf("procbatch output lacks %q", w)
				}
			}
		})
	}
}

// runUnderSizeLimit runs the INN program name with args like Site.runInput, but through
// /bin/sh with a file size limit of blocks 512-byte blocks (ulimit -f) and SIGXFSZ ignored,
// so that a write beyond the limit fails with EFBIG, as it would on a full file system.
func runUnderSizeLimit(s *Site, blocks int, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), testkit.Deadline())
	defer cancel()
	script := fmt.Sprintf(`trap '' XFSZ; ulimit -f %d || exit 99; exec "$0" "$@"`, blocks)
	cmd := exec.CommandContext(ctx, "/bin/sh",
		append([]string{"-c", script, filepath.Join(s.Prefix, "bin", name)}, args...)...)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), s.Env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// TestProcbatchWriteError: when procbatch cannot write all the entries of a peer to its
// output file (here because of a file size limit of 4096 bytes, as with a full file system),
// it fails and keeps its input file, with -u alone and with -m -u as news.daily runs it, and
// nothing is appended to the backlog file. procbatch used to ignore the write error, append
// the truncated file to the backlog with -m, remove the input with -u and exit 0, losing the
// other entries. Without the limit, all the entries reach the backlog file. When the limit
// cuts an entry in the middle and peer1.tmp already holds entries of an earlier run,
// procbatch cuts peer1.tmp back to those entries, so that the next run does not append to
// a partial line. When another process holds a lock on peer1.tmp, procbatch fails the same
// way without touching it, so that a rollback cannot cut the entries of another run. While
// procbatch -m appends peer1.tmp to the backlog file (a FIFO here, so that it waits there),
// another run cannot add entries to peer1.tmp that -m would then remove unread.
func TestProcbatchWriteError(t *testing.T) {
	s := NewSite(t, SiteOptions{})

	// 200 entries for peer1, each 64 bytes long in the output file, so that the limit
	// cuts the file at the end of a line and the truncated file looks complete.
	var input, output strings.Builder
	const entries = 200
	for i := range entries {
		line := fmt.Sprintf("e2e/test/%05d <procbatch-%024d@e2e.invalid>", i+1, i+1)
		if len(line)+1 != 64 {
			t.Fatalf("entry of %d bytes, want 64", len(line)+1)
		}
		input.WriteString(line + " peer1\n")
		output.WriteString(line + "\n")
	}

	for _, tc := range []struct {
		name   string
		limit  bool
		tape   bool // -m, with -t
		failed bool // procbatch must fail and keep its input
	}{
		{"-u", true, false, true},
		{"-m -u", true, true, true},
		{"-m -u without limit", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			outDir := filepath.Join(work, "out")
			tapeDir := filepath.Join(work, "tape")
			for _, d := range []string{outDir, tapeDir} {
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			in := filepath.Join(work, "innfeed-dropped.A000001")
			if err := os.WriteFile(in, []byte(input.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"-u", "-d", outDir}
			if tc.tape {
				args = append(args, "-m", "-t", tapeDir)
			}
			args = append(args, in)
			var out string
			var err error
			if tc.limit {
				out, err = runUnderSizeLimit(s, 8, "procbatch", args...)
			} else {
				out, err = s.run("procbatch", args...)
			}
			t.Logf("procbatch %s: %v\n%s", strings.Join(args, " "), err, out)

			_, statErr := os.Stat(in)
			kept := statErr == nil
			tape, _ := os.ReadFile(filepath.Join(tapeDir, "peer1"))
			tmp, _ := os.ReadFile(filepath.Join(outDir, "peer1.tmp"))
			t.Logf("input kept: %v; peer1.tmp: %d bytes; backlog peer1: %d bytes",
				kept, len(tmp), len(tape))
			if tc.failed {
				if err == nil {
					t.Errorf("procbatch exited 0 although it could not write all the entries")
				}
				if !kept {
					t.Errorf("procbatch removed its input file although it could not write all the entries")
				}
				if len(tape) != 0 {
					t.Errorf("procbatch appended %d bytes of a truncated file to the backlog", len(tape))
				}
				return
			}
			if err != nil || kept {
				t.Errorf("procbatch failed (%v) or kept its input (%v)", err, kept)
			}
			if string(tape) != output.String() {
				t.Errorf("backlog peer1 has %d bytes, want all %d entries (%d bytes)",
					len(tape), entries, output.Len())
			}
		})
	}

	t.Run("mid-record with earlier entries", func(t *testing.T) {
		work := t.TempDir()
		outDir := filepath.Join(work, "out")
		if err := os.Mkdir(outDir, 0o755); err != nil {
			t.Fatal(err)
		}
		earlier := "e2e/old/1 <old1@e2e.invalid>\ne2e/old/2 <old2@e2e.invalid>\n"
		tmp := filepath.Join(outDir, "peer1.tmp")
		if err := os.WriteFile(tmp, []byte(earlier), 0o644); err != nil {
			t.Fatal(err)
		}
		// Entries of 62 bytes: 4096 bytes minus the earlier ones is not a multiple.
		var in, want strings.Builder
		want.WriteString(earlier)
		for i := range entries {
			line := fmt.Sprintf("e2e/test/%05d <mid-%028d@e2e.invalid>", i+1, i+1)
			in.WriteString(line + " peer1\n")
			want.WriteString(line + "\n")
		}
		if (4096-len(earlier))%62 == 0 {
			t.Fatal("the limit falls at the end of an entry")
		}
		input := filepath.Join(work, "innfeed-dropped.A000002")
		if err := os.WriteFile(input, []byte(in.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		args := []string{"-u", "-d", outDir, input}
		out, err := runUnderSizeLimit(s, 8, "procbatch", args...)
		t.Logf("procbatch %s under the limit: %v\n%s", strings.Join(args, " "), err, out)
		_, statErr := os.Stat(input)
		got, _ := os.ReadFile(tmp)
		if err == nil || statErr != nil {
			t.Fatalf("procbatch did not fail (%v) or removed its input (%v)", err, statErr)
		}
		if string(got) != earlier {
			t.Fatalf("peer1.tmp after the failure has %d bytes, want only the %d bytes of the earlier entries:\n%q",
				len(got), len(earlier), tail(string(got), 200))
		}

		// The next run, without the limit, appends complete entries.
		out, err = s.run("procbatch", args...)
		t.Logf("procbatch %s: %v\n%s", strings.Join(args, " "), err, out)
		got, _ = os.ReadFile(tmp)
		if err != nil {
			t.Fatalf("procbatch without the limit failed: %v", err)
		}
		if string(got) != want.String() {
			t.Errorf("peer1.tmp after the second run has %d bytes, want %d (earlier entries and all new ones)",
				len(got), want.Len())
		}
	})

	t.Run("locked by another process", func(t *testing.T) {
		work := t.TempDir()
		outDir := filepath.Join(work, "out")
		if err := os.Mkdir(outDir, 0o755); err != nil {
			t.Fatal(err)
		}
		earlier := "e2e/old/1 <old1@e2e.invalid>\n"
		tmp := filepath.Join(outDir, "peer1.tmp")
		if err := os.WriteFile(tmp, []byte(earlier), 0o644); err != nil {
			t.Fatal(err)
		}
		in := filepath.Join(work, "innfeed-dropped.A000003")
		if err := os.WriteFile(in, []byte(input.String()), 0o644); err != nil {
			t.Fatal(err)
		}

		// Another process holds a lock on peer1.tmp, taken with Perl's flock as procbatch does.
		ctx, cancel := context.WithTimeout(context.Background(), testkit.Deadline())
		defer cancel()
		locker := exec.CommandContext(ctx, "perl", "-MFcntl=:flock", "-e",
			`open(F, ">>", $ARGV[0]) or die "open: $!\n"; flock(F, LOCK_EX) or die "flock: $!\n"; `+
				`$| = 1; print "locked\n"; sleep 60;`, tmp)
		stdout, err := locker.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := locker.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { locker.Process.Kill(); locker.Wait() }()
		line := make([]byte, 7)
		if _, err := io.ReadFull(stdout, line); err != nil || string(line) != "locked\n" {
			t.Fatalf("locker: %q, %v", line, err)
		}

		args := []string{"-u", "-d", outDir, in}
		out, err := s.run("procbatch", args...)
		t.Logf("procbatch %s with peer1.tmp locked: %v\n%s", strings.Join(args, " "), err, out)
		_, statErr := os.Stat(in)
		got, _ := os.ReadFile(tmp)
		if err == nil || statErr != nil {
			t.Errorf("procbatch did not fail (%v) or removed its input (%v) with peer1.tmp locked", err, statErr)
		}
		if string(got) != earlier {
			t.Errorf("procbatch changed the locked peer1.tmp: %d bytes, want %d", len(got), len(earlier))
		}

		// Once the lock is released, the run succeeds.
		locker.Process.Kill()
		locker.Wait()
		out, err = s.run("procbatch", args...)
		got, _ = os.ReadFile(tmp)
		if err != nil || string(got) != earlier+output.String() {
			t.Errorf("procbatch after the lock was released: %v, peer1.tmp %d bytes, want %d\n%s",
				err, len(got), len(earlier)+output.Len(), out)
		}
	})

	t.Run("append during -m", func(t *testing.T) {
		// The output directory is also the backlog directory, so -m moves nothing, and the
		// backlog file is a FIFO: the first run waits there until the test reads it.
		dir := t.TempDir()
		fifo := filepath.Join(dir, "peer1")
		if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
			t.Skipf("mkfifo: %v %s", err, out)
		}
		in1 := filepath.Join(dir, "innfeed-dropped.A000004")
		if err := os.WriteFile(in1, []byte(input.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		var in2, out2 strings.Builder
		for i := range 10 {
			line := fmt.Sprintf("e2e/test/%05d <second-%025d@e2e.invalid>", i+1, i+1)
			in2.WriteString(line + " peer1\n")
			out2.WriteString(line + "\n")
		}
		in2Path := filepath.Join(dir, "innfeed-dropped.A000005")
		if err := os.WriteFile(in2Path, []byte(in2.String()), 0o644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), testkit.Deadline())
		defer cancel()
		first := exec.CommandContext(ctx, filepath.Join(s.Prefix, "bin", "procbatch"),
			"-m", "-u", "-d", dir, "-t", dir, in1)
		first.Dir = s.Dir
		first.Env = append(os.Environ(), s.Env...)
		var firstOut bytes.Buffer
		first.Stdout, first.Stderr = &firstOut, &firstOut
		if err := first.Start(); err != nil {
			t.Fatal(err)
		}

		// Wait until the first run has written peer1.tmp, then a little more for it to
		// reach the backlog file (and, before the fix, for cat to read peer1.tmp).
		tmp := filepath.Join(dir, "peer1.tmp")
		for limit := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if fi, err := os.Stat(tmp); err == nil && fi.Size() == int64(output.Len()) {
				break
			}
			if time.Now().After(limit) {
				t.Fatalf("the first run did not write peer1.tmp:\n%s", firstOut.String())
			}
		}
		time.Sleep(time.Second)

		out, err := s.run("procbatch", "-u", "-d", dir, in2Path)
		t.Logf("second procbatch while the first one is in -m: %v\n%s", err, out)
		_, statErr := os.Stat(in2Path)
		secondKept := statErr == nil

		// Read the backlog FIFO: the first run can finish.
		backlog := make(chan []byte, 1)
		go func() {
			f, err := os.Open(fifo) // waits for the writer
			if err != nil {
				backlog <- nil
				return
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			backlog <- b
		}()
		var got []byte
		select {
		case got = <-backlog:
		case <-time.After(20 * time.Second):
			t.Fatalf("the first run never wrote to the backlog file:\n%s", firstOut.String())
		}
		err1 := first.Wait()
		t.Logf("first procbatch -m -u: %v\n%s", err1, firstOut.String())
		if err1 != nil || string(got) != output.String() {
			t.Errorf("first run: %v, backlog %d bytes, want %d", err1, len(got), output.Len())
		}

		// The entries of the second run must not be lost: either it failed and kept its
		// input, or they are in peer1.tmp.
		rest, _ := os.ReadFile(tmp)
		if !secondKept && !strings.Contains(string(rest), out2.String()) {
			t.Errorf("the second run removed its input, but its entries are neither in peer1.tmp (%d bytes) nor in the backlog",
				len(rest))
		}
		if secondKept {
			out, err = s.run("procbatch", "-u", "-d", dir, in2Path)
			rest, _ = os.ReadFile(tmp)
			if err != nil || string(rest) != out2.String() {
				t.Errorf("second run after the first one: %v, peer1.tmp %q\n%s", err, tail(string(rest), 200), out)
			}
		}
	})
}

// tail returns the last n bytes of s.
func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

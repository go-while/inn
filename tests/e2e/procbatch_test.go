package e2e

// procbatch, which splits a funnel or innfeed-dropped file into backlog files by peer.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
// a partial line.
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
}

// tail returns the last n bytes of s.
func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

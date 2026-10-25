package e2e

// procbatch, which splits a funnel or innfeed-dropped file into backlog files by peer.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

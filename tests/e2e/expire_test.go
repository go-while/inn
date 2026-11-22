package e2e

// expire, expireover and news.daily: what happens to the history database when the spool
// or the history filesystem fails during expiration.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// historyToken returns what grephistory prints for id: a storage token, or "/dev/null" for
// a remembered entry without one.
func historyToken(t *testing.T, s *Site, id string) string {
	t.Helper()
	return strings.TrimSpace(s.Run(t, "grephistory", id))
}

// chmodUntilCleanup changes the mode of dir and restores 0755 at the latest when the test
// ends (before its temporary directory is removed); the returned function restores it now.
func chmodUntilCleanup(t *testing.T, dir string, mode os.FileMode) func() {
	t.Helper()
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	restore := func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("restoring the mode of %s: %v", dir, err)
		}
	}
	t.Cleanup(restore)
	return restore
}

// TestExpireKeepsUncheckableArticle: with group-based expiry (the default), expire keeps the
// storage token of an article it cannot check because the spool cannot be read at that
// moment (here a tradspool directory the news user cannot search), instead of removing the
// token from the history database as if the article were gone; the article can still be
// retrieved by its Message-ID once the spool is readable again (HIS-1).
func TestExpireKeepsUncheckableArticle(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	id := s.Inject(t, "e2e.test", 1)[0]
	token := historyToken(t, s, id)
	if !strings.HasPrefix(token, "@") {
		t.Fatalf("grephistory %s = %q before expire, want a storage token", id, token)
	}

	dir := filepath.Join(s.Dir, "spool", "articles", "e2e", "test")
	restore := chmodUntilCleanup(t, dir, 0)
	out, err := runFor(s, time.Minute, "expire", "-v1")
	restore()
	t.Logf("expire -v1 with %s not searchable:\n%s", dir, out)
	if err != nil {
		t.Fatalf("expire -v1: %v", err)
	}

	if got := historyToken(t, s, id); got != token {
		t.Errorf("grephistory %s = %q after expire, want %q: the history entry of a stored "+
			"article lost its token", id, got, token)
	}
	c := s.Reader(t)
	if code, line := c.Cmd("STAT %s", id); code != 223 {
		t.Errorf("STAT %s after expire: %q, want 223", id, line)
	}
	if !strings.Contains(out, "cannot check") {
		t.Errorf("expire did not report the article it could not check")
	}
}

// TestExpireoverTombstoneUnverifiable: with expiretombstone, an article whose removal by
// fastrm failed stays on disk, and its entry stays in expireover.tombstone.NEW.  When the
// next expireover cannot check that article (its directory cannot be read), it must not carry
// the entry into the log that expirerm promotes after the removal of the next batch
// succeeds: expire would then drop the history entry of an article still on disk (HIS-2).
func TestExpireoverTombstoneUnverifiable(t *testing.T) {
	s := NewSite(t, SiteOptions{
		Groups:  []Group{{Name: "e2e.test"}, {Name: "e2e.expire.a"}, {Name: "e2e.expire.b"}},
		InnConf: map[string]string{"expiretombstone": "true"},
		Etc: map[string]string{
			"expire.ctl": "/remember/:11\n*:A:never:never:never\n" +
				"e2e.expire.*:A:" + expireDays + ":" + expireDays + ":" + expireDays + "\n",
		},
	})
	// expirerm moves a processed list to pathlog/OLD.
	if err := os.MkdirAll(filepath.Join(s.Dir, "log", "OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	articles := filepath.Join(s.Dir, "spool", "articles")
	dirA := filepath.Join(articles, "e2e", "expire", "a")
	run := func(d time.Duration, name string, args ...string) (string, error) {
		out, err := runFor(s, d, name, args...)
		t.Logf("%s %s: err=%v\n%s", name, strings.Join(args, " "), err, out)
		return out, err
	}

	// First cycle: A expires, but fastrm cannot remove it (its directory is read-only).
	idA := s.Inject(t, "e2e.expire.a", 1)[0]
	tokenA := historyToken(t, s, idA)
	files, _ := filepath.Glob(filepath.Join(dirA, "*"))
	if len(files) != 1 {
		t.Fatalf("articles in %s: %v, want one", dirA, files)
	}
	fileA := files[0]
	time.Sleep(expireWait)
	restoreA := chmodUntilCleanup(t, dirA, 0o555)
	rm1 := filepath.Join(s.Dir, "tmp", "rm1")
	if _, err := run(time.Minute, "expireover", "-z", rm1); err != nil {
		t.Fatalf("expireover -z: %v", err)
	}
	if _, err := run(time.Minute, "expirerm", rm1); err == nil {
		t.Fatalf("expirerm succeeded although %s could not be removed", fileA)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "db", "expireover.tombstone.NEW")); err != nil {
		t.Fatalf("no expireover.tombstone.NEW after the failed expirerm: %v", err)
	}

	// Second cycle: A cannot be checked (its directory cannot be read either), B expires
	// and is removed, and expirerm promotes the tombstone log.
	if err := os.Chmod(dirA, 0); err != nil {
		t.Fatal(err)
	}
	s.Inject(t, "e2e.expire.b", 1)
	time.Sleep(expireWait)
	rm2 := filepath.Join(s.Dir, "tmp", "rm2")
	if _, err := run(time.Minute, "expireover", "-z", rm2); err != nil {
		t.Fatalf("expireover -z: %v", err)
	}
	if _, err := run(time.Minute, "expirerm", rm2); err != nil {
		t.Fatalf("expirerm of the second batch: %v", err)
	}
	restoreA()
	if _, err := os.Stat(fileA); err != nil {
		t.Fatalf("%s: %v", fileA, err)
	}
	if b, err := os.ReadFile(filepath.Join(s.Dir, "db", "expireover.tombstone")); err == nil &&
		strings.Contains(string(b), tokenA) {
		t.Errorf("the promoted expireover.tombstone lists %s, whose article is still on disk:\n%s",
			tokenA, b)
	}

	// expire must not drop the history entry of A, still on disk.
	if _, err := run(time.Minute, "expire", "-v1"); err != nil {
		t.Fatalf("expire -v1: %v", err)
	}
	if got := historyToken(t, s, idA); got != tokenA {
		t.Errorf("grephistory %s = %q after expire, want %q: the history entry of an article "+
			"still on disk (%s) lost its token", idA, got, tokenA, fileA)
	}

	// With the spool readable again, the next expireover sees that A is still on disk and
	// forgets the entry it could not check.
	if _, err := run(time.Minute, "expireover"); err != nil {
		t.Fatalf("expireover: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(s.Dir, "db", "expireover.tombstone.retry")); err == nil {
		t.Errorf("expireover.tombstone.retry still exists after a successful check:\n%s", b)
	}
}

// TestExpireCancelSnapshotRetry: with expiretombstone, an expire run that finds the
// cancels.tombstone.processing snapshot of an interrupted run and new entries in
// cancels.tombstone keeps both on disk until it has built the new history: when that run
// fails too (here because history.n cannot be created), the next run still drops the
// history entries of both sets of cancelled articles (HIS-3).
func TestExpireCancelSnapshotRetry(t *testing.T) {
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"expiretombstone": "true"}})
	ids := s.Inject(t, "e2e.test", 2)
	tokens := []string{historyToken(t, s, ids[0]), historyToken(t, s, ids[1])}
	for i, tok := range tokens {
		if !strings.HasPrefix(tok, "@") {
			t.Fatalf("grephistory %s = %q, want a storage token", ids[i], tok)
		}
	}
	db := filepath.Join(s.Dir, "db")
	header := "# inn-tombstone v1\n"
	s.writeFile(t, "db/cancels.tombstone.processing", header+tokens[0]+"\n")
	s.writeFile(t, "db/cancels.tombstone", header+tokens[1]+"\n")

	// An expire run that fails before the new history is in place.
	if err := os.Mkdir(filepath.Join(db, "history.n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runFor(s, time.Minute, "expire", "-v1")
	t.Logf("expire -v1 with history.n a directory: err=%v\n%s", err, out)
	if err == nil {
		t.Fatal("expire succeeded although it could not create history.n")
	}
	logs, _ := filepath.Glob(filepath.Join(db, "cancels.tombstone*"))
	var onDisk strings.Builder
	for _, name := range logs {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&onDisk, "%s:\n%s", filepath.Base(name), b)
	}
	for _, tok := range tokens {
		if !strings.Contains(onDisk.String(), tok) {
			t.Errorf("after the failed expire, no cancel tombstone log lists %s:\n%s", tok,
				onDisk.String())
		}
	}

	// The next run succeeds and applies both.
	if err := os.Remove(filepath.Join(db, "history.n")); err != nil {
		t.Fatal(err)
	}
	if out, err := runFor(s, time.Minute, "expire", "-v1"); err != nil {
		t.Fatalf("expire -v1: %v\n%s", err, out)
	}
	for _, id := range ids {
		if got := historyToken(t, s, id); got != "/dev/null" {
			t.Errorf("grephistory %s = %q after expire, want /dev/null (cancelled)", id, got)
		}
	}
}

// TestNewsDailyExpdirInstallFailure: when news.daily, run with expdir, cannot move one of
// the new history files built by expire -d into place, it does not unpause innd, keeps
// history.n.done and notifies the newsmaster, instead of unpausing innd with an empty or
// mixed history database (HIS-7).  The moves fail here because the history directory is
// read-only while news.daily runs.
func TestNewsDailyExpdirInstallFailure(t *testing.T) {
	bin := t.TempDir()
	report := filepath.Join(bin, "report")
	mailer := filepath.Join(bin, "mailer")
	script := fmt.Sprintf("#! /bin/sh\nhead -c 65536 >>'%s'\n", report)
	if err := os.WriteFile(mailer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"mailcmd": mailer}})
	s.Inject(t, "e2e.test", 1)
	expdir := filepath.Join(s.Dir, "expdir")
	if err := os.Mkdir(expdir, 0o755); err != nil {
		t.Fatal(err)
	}

	restore := chmodUntilCleanup(t, filepath.Join(s.Dir, "db"), 0o555)
	out, err := runNewsDaily(s, 150*time.Second, "expdir="+expdir, "noexpireover",
		"noprocbatch", "nologs", "nostat")
	restore()
	t.Logf("news.daily: err=%v\n%s", err, out)
	if err != nil && strings.Contains(err.Error(), "did not finish") {
		t.Fatal(err)
	}
	mail, _ := os.ReadFile(report)
	t.Logf("mail:\n%s", mail)

	if _, err := os.Stat(filepath.Join(expdir, "history.n.done")); err != nil {
		t.Errorf("history.n.done was removed although the new history was not installed: %v", err)
	}
	if mode := s.Ctlinnd(t, "mode"); strings.Contains(mode, "Server running") {
		t.Errorf("innd was unpaused with an incomplete history database:\n%s", mode)
	}
	if !strings.Contains(string(mail), "Cannot move") {
		t.Errorf("the newsmaster was not notified of the failed installation")
	}
}

package e2e

// expire, expireover and news.daily: what happens to the history database when the spool
// or the history filesystem fails during expiration.

import (
	"fmt"
	"os"
	"os/exec"
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

// TestExpireoverTombstoneQuietCycle: with expiretombstone and delayed removal, an entry
// expireover could not verify is kept in expireover.tombstone.retry; once a later run finds
// the article gone, the entry is published in expireover.tombstone even when that run
// schedules no removal, so that news.daily (which runs expirerm only for a non-empty list)
// does not hold it back until an unrelated article expires (HIS-2).
func TestExpireoverTombstoneQuietCycle(t *testing.T) {
	s := NewSite(t, SiteOptions{
		Groups:  []Group{{Name: "e2e.test"}, {Name: "e2e.expire.a"}},
		InnConf: map[string]string{"expiretombstone": "true"},
		Etc: map[string]string{
			"expire.ctl": "/remember/:11\n*:A:never:never:never\n" +
				"e2e.expire.*:A:" + expireDays + ":" + expireDays + ":" + expireDays + "\n",
		},
	})
	if err := os.MkdirAll(filepath.Join(s.Dir, "log", "OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	dirA := filepath.Join(s.Dir, "spool", "articles", "e2e", "expire", "a")
	db := filepath.Join(s.Dir, "db")
	run := func(name string, args ...string) error {
		out, err := runFor(s, time.Minute, name, args...)
		t.Logf("%s %s: err=%v\n%s", name, strings.Join(args, " "), err, out)
		return err
	}

	// Cycle 1: A expires, fastrm cannot remove it.
	idA := s.Inject(t, "e2e.expire.a", 1)[0]
	tokenA := historyToken(t, s, idA)
	files, _ := filepath.Glob(filepath.Join(dirA, "*"))
	if len(files) != 1 {
		t.Fatalf("articles in %s: %v, want one", dirA, files)
	}
	time.Sleep(expireWait)
	restore := chmodUntilCleanup(t, dirA, 0o555)
	rm := filepath.Join(s.Dir, "tmp", "rm")
	if err := run("expireover", "-z", rm+"1"); err != nil {
		t.Fatalf("expireover -z: %v", err)
	}
	if err := run("expirerm", rm+"1"); err == nil {
		t.Fatalf("expirerm succeeded although %s could not be removed", files[0])
	}

	// Cycle 2, nothing expires: A cannot be checked and goes to the retry file.
	if err := os.Chmod(dirA, 0); err != nil {
		t.Fatal(err)
	}
	if err := run("expireover", "-z", rm+"2"); err != nil {
		t.Fatalf("expireover -z: %v", err)
	}
	restore()
	if b, err := os.ReadFile(filepath.Join(db, "expireover.tombstone.retry")); err != nil ||
		!strings.Contains(string(b), tokenA) {
		t.Fatalf("expireover.tombstone.retry does not list %s: %v\n%s", tokenA, err, b)
	}

	// Cycle 3, nothing expires again: A is gone by now (removed by hand), so its entry
	// must be published without expirerm.
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	if err := run("expireover", "-z", rm+"3"); err != nil {
		t.Fatalf("expireover -z: %v", err)
	}
	if b, err := os.ReadFile(rm + "3"); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		t.Fatalf("the third run scheduled removals:\n%s", b)
	}
	b, err := os.ReadFile(filepath.Join(db, "expireover.tombstone"))
	if err != nil || !strings.Contains(string(b), tokenA) {
		t.Errorf("expireover.tombstone does not list %s after a run without removals: %v\n%s",
			tokenA, err, b)
	}
	if b, err := os.ReadFile(filepath.Join(db, "expireover.tombstone.retry")); err == nil {
		t.Errorf("expireover.tombstone.retry still exists:\n%s", b)
	}
	if err := run("expire", "-v1"); err != nil {
		t.Fatalf("expire -v1: %v", err)
	}
	if got := historyToken(t, s, idA); got != "/dev/null" {
		t.Errorf("grephistory %s = %q after expire, want /dev/null (article gone)", idA, got)
	}
}

// TestExpireoverRejectedFinalLog: with expiretombstone and delayed removal, an unconsumed
// expireover.tombstone listing a live article is verified by the next expireover, which
// rejects that entry; when this run's removals then fail (so that expirerm does not promote
// .NEW), expire must not consume the old log and drop the history entry of the live
// article.  An existing expireover.tombstone.NEW that cannot be opened stops expireover
// (HIS-2).
func TestExpireoverRejectedFinalLog(t *testing.T) {
	s := NewSite(t, SiteOptions{
		Groups:  []Group{{Name: "e2e.test"}, {Name: "e2e.expire.a"}},
		InnConf: map[string]string{"expiretombstone": "true"},
		Etc: map[string]string{
			"expire.ctl": "/remember/:11\n*:A:never:never:never\n" +
				"e2e.expire.*:A:" + expireDays + ":" + expireDays + ":" + expireDays + "\n",
		},
	})
	if err := os.MkdirAll(filepath.Join(s.Dir, "log", "OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(s.Dir, "db")
	run := func(name string, args ...string) error {
		out, err := runFor(s, time.Minute, name, args...)
		t.Logf("%s %s: err=%v\n%s", name, strings.Join(args, " "), err, out)
		return err
	}
	live := s.Inject(t, "e2e.test", 1)[0]
	liveToken := historyToken(t, s, live)
	s.writeFile(t, "db/expireover.tombstone", "# inn-tombstone v1\n"+liveToken+"\n")

	// An unopenable .NEW stops expireover before it changes anything.
	newLog := filepath.Join(db, "expireover.tombstone.NEW")
	s.writeFile(t, "db/expireover.tombstone.NEW", "# inn-tombstone v1\n")
	if err := os.Chmod(newLog, 0); err != nil {
		t.Fatal(err)
	}
	if err := run("expireover"); err == nil {
		t.Errorf("expireover succeeded although %s could not be opened", newLog)
	}
	if err := os.Remove(newLog); err != nil {
		t.Fatal(err)
	}

	// A removal that fails: expirerm does not promote .NEW.
	s.Inject(t, "e2e.expire.a", 1)
	time.Sleep(expireWait)
	dirA := filepath.Join(s.Dir, "spool", "articles", "e2e", "expire", "a")
	chmodUntilCleanup(t, dirA, 0o555)
	rm := filepath.Join(s.Dir, "tmp", "rm")
	if err := run("expireover", "-z", rm); err != nil {
		t.Fatalf("expireover -z: %v", err)
	}
	if err := run("expirerm", rm); err == nil {
		t.Fatalf("expirerm succeeded although the article could not be removed")
	}
	if b, err := os.ReadFile(filepath.Join(db, "expireover.tombstone")); err == nil &&
		strings.Contains(string(b), liveToken) {
		t.Errorf("expireover.tombstone still lists the live article %s:\n%s", liveToken, b)
	}
	if err := run("expire", "-v1"); err != nil {
		t.Fatalf("expire -v1: %v", err)
	}
	if got := historyToken(t, s, live); got != liveToken {
		t.Errorf("grephistory %s = %q after expire, want %q: the history entry of a live "+
			"article was dropped", live, got, liveToken)
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

// TestExpireUnreadableCancelSnapshot: with expiretombstone, an expire run that cannot read
// the cancels.tombstone.processing snapshot of an interrupted run (here: no read permission)
// does not remove it after a successful run, so that its cancels are applied once it can be
// read; the live cancels.tombstone is not renamed over it either (HIS-3).
func TestExpireUnreadableCancelSnapshot(t *testing.T) {
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"expiretombstone": "true"}})
	ids := s.Inject(t, "e2e.test", 2)
	tokens := []string{historyToken(t, s, ids[0]), historyToken(t, s, ids[1])}
	db := filepath.Join(s.Dir, "db")
	snapshot := filepath.Join(db, "cancels.tombstone.processing")
	header := "# inn-tombstone v1\n"
	s.writeFile(t, "db/cancels.tombstone.processing", header+tokens[0]+"\n")
	s.writeFile(t, "db/cancels.tombstone", header+tokens[1]+"\n")
	if err := os.Chmod(snapshot, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(snapshot, 0o644) })

	out, err := runFor(s, time.Minute, "expire", "-v1")
	t.Logf("expire -v1 with an unreadable snapshot: err=%v\n%s", err, out)
	if err != nil {
		t.Fatalf("expire -v1: %v", err)
	}
	if _, err := os.Stat(snapshot); err != nil {
		t.Fatalf("the unread snapshot was removed: %v", err)
	}
	if err := os.Chmod(snapshot, 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(snapshot); !strings.Contains(string(b), tokens[0]) {
		t.Errorf("the snapshot lost %s:\n%s", tokens[0], b)
	}

	// Readable again: the next run applies both.
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

// TestNewsDailyExpdirInstall: news.daily run with expdir installs the new history files built
// by expire -d and leaves innd running on them: the injected article is still found by its
// Message-ID, through grephistory and through innd, and news.daily reports no failure to
// unpause innd (expire unpauses it when it finishes, so news.daily has to pause it again for
// the installation; see HIS-7).
func TestNewsDailyExpdirInstall(t *testing.T) {
	// "x-n" contains "-n": news.daily must not take it for expire's -n flag.  The long name
	// would make the pause reason exceed the 80 characters innd accepts if news.daily still
	// put the expdir path in it.
	for _, name := range []string{"x", "x-n", strings.Repeat("long", 12)} {
		t.Run(name, func(t *testing.T) { testNewsDailyExpdirInstall(t, name) })
	}
}

func testNewsDailyExpdirInstall(t *testing.T, name string) {
	bin := t.TempDir()
	report := filepath.Join(bin, "report")
	mailer := filepath.Join(bin, "mailer")
	script := fmt.Sprintf("#! /bin/sh\nhead -c 65536 >>'%s'\n", report)
	if err := os.WriteFile(mailer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"mailcmd": mailer}})
	id := s.Inject(t, "e2e.test", 1)[0]
	token := historyToken(t, s, id)
	expdir := filepath.Join(s.Dir, name)
	if err := os.Mkdir(expdir, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runNewsDaily(s, 150*time.Second, "expdir="+expdir, "noexpireover",
		"noprocbatch", "nologs", "nostat")
	t.Logf("news.daily: err=%v\n%s", err, out)
	mail, _ := os.ReadFile(report)
	t.Logf("mail:\n%s", mail)
	if err != nil {
		t.Errorf("news.daily: %v", err)
	}
	if strings.Contains(string(mail), "Cannot") {
		t.Errorf("news.daily reported a failure")
	}
	if _, err := os.Stat(filepath.Join(expdir, "history.n.done")); err == nil {
		t.Errorf("history.n.done is still in %s", expdir)
	}
	if mode := s.Ctlinnd(t, "mode"); !strings.Contains(mode, "Server running") {
		t.Errorf("innd is not running after news.daily:\n%s", mode)
	}
	if got := historyToken(t, s, id); got != token {
		t.Errorf("grephistory %s = %q after news.daily, want %q", id, got, token)
	}
	// innd answers from the installed history: the article is a duplicate.
	p := s.Peer(t)
	expect(t, p, 435, "IHAVE %s", id)
	expect(t, p, 205, "QUIT")
}

// TestExpireAsideLeavesPaused: expire -d leaves innd paused with the reason given by -r, as
// its manual page says, so that the caller can install the new history files before innd
// writes to them; "ctlinnd go" with that reason resumes it (HIS-7).
func TestExpireAsideLeavesPaused(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	s.Inject(t, "e2e.test", 1)
	dir := filepath.Join(s.Dir, "aside")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	reason := "e2e expire aside"
	out, err := runFor(s, time.Minute, "expire", "-v1", "-d"+dir, "-r"+reason)
	t.Logf("expire -d: err=%v\n%s", err, out)
	if err != nil {
		t.Fatalf("expire -d: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "history.n.done")); err != nil {
		t.Errorf("no history.n.done: %v", err)
	}
	mode := s.Ctlinnd(t, "mode")
	if !strings.Contains(mode, "Server paused "+reason) {
		t.Errorf("innd is not paused with %q after expire -d:\n%s", reason, mode)
	}
	s.Ctlinnd(t, "go", reason)
	if mode := s.Ctlinnd(t, "mode"); !strings.Contains(mode, "Server running") {
		t.Errorf("innd is not running after ctlinnd go:\n%s", mode)
	}
}

// TestFastrmExitStatus: fastrm exits with status 1 when it cannot remove a file, as its
// manual page says, so that expirerm notices the failure; a file that does not exist is
// not a failure.
func TestFastrmExitStatus(t *testing.T) {
	s := NewSite(t, SiteOptions{})
	spool := filepath.Join(s.Dir, "spool", "articles")
	dir := filepath.Join(spool, "e2e", "locked")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s.writeFile(t, "spool/articles/e2e/locked/1", "article\n")
	chmodUntilCleanup(t, dir, 0o555)

	cmd := exec.Command(filepath.Join(s.Prefix, "bin", "fastrm"), "-e", spool)
	cmd.Dir = spool
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Stdin = strings.NewReader("e2e/locked/1\n")
	out, err := cmd.CombinedOutput()
	t.Logf("fastrm with a file it cannot remove: err=%v\n%s", err, out)
	if err == nil {
		t.Errorf("fastrm exited 0 although it could not remove e2e/locked/1")
	}

	cmd = exec.Command(filepath.Join(s.Prefix, "bin", "fastrm"), "-e", spool)
	cmd.Dir = spool
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Stdin = strings.NewReader("e2e/nothere\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("fastrm with a file that does not exist: %v\n%s", err, out)
	}
}

// newsDailyMailer writes a mail command that appends what it gets to a report file, and
// returns both paths.
func newsDailyMailer(t *testing.T) (mailer, report string) {
	t.Helper()
	bin := t.TempDir()
	report = filepath.Join(bin, "report")
	mailer = filepath.Join(bin, "mailer")
	script := fmt.Sprintf("#! /bin/sh\nhead -c 65536 >>'%s'\n", report)
	if err := os.WriteFile(mailer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return mailer, report
}

// TestNewsDailyExpdirPendingInstall: when the expdir directory still holds history.n.done of
// an earlier installation that did not complete, news.daily does not run expire, which would
// overwrite what may be the only complete history, and notifies the newsmaster (HIS-7).
func TestNewsDailyExpdirPendingInstall(t *testing.T) {
	mailer, report := newsDailyMailer(t)
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"mailcmd": mailer}})
	s.Inject(t, "e2e.test", 1)
	expdir := filepath.Join(s.Dir, "x")
	if err := os.Mkdir(expdir, 0o755); err != nil {
		t.Fatal(err)
	}
	s.writeFile(t, "x/history.n", "pending history of an earlier run\n")
	s.writeFile(t, "x/history.n.done", "")

	out, err := runNewsDaily(s, 150*time.Second, "expdir="+expdir, "noexpireover",
		"noprocbatch", "nologs", "nostat")
	t.Logf("news.daily: err=%v\n%s", err, out)
	mail, _ := os.ReadFile(report)
	t.Logf("mail:\n%s", mail)
	if b, _ := os.ReadFile(filepath.Join(expdir, "history.n")); string(b) != "pending history of an earlier run\n" {
		t.Errorf("the pending history.n was overwritten or moved: %q", b)
	}
	if !strings.Contains(string(mail), "earlier installation") {
		t.Errorf("the newsmaster was not told about the earlier installation")
	}
	if mode := s.Ctlinnd(t, "mode"); !strings.Contains(mode, "Server running") {
		t.Errorf("innd is not running (news.daily should not have touched it):\n%s", mode)
	}
}

// TestNewsDailyExpdirTruncateFailure: when news.daily cannot empty the old history file
// (here: the file is read-only), it moves nothing, keeps the new files and history.n.done
// in the expdir directory, does not unpause innd and notifies the newsmaster (HIS-7).
func TestNewsDailyExpdirTruncateFailure(t *testing.T) {
	mailer, report := newsDailyMailer(t)
	s := NewSite(t, SiteOptions{InnConf: map[string]string{"mailcmd": mailer}})
	id := s.Inject(t, "e2e.test", 1)[0]
	token := historyToken(t, s, id)
	expdir := filepath.Join(s.Dir, "x")
	if err := os.Mkdir(expdir, 0o755); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(s.Dir, "db", "history")
	if err := os.Chmod(history, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(history, 0o644) })

	out, err := runNewsDaily(s, 150*time.Second, "expdir="+expdir, "noexpireover",
		"noprocbatch", "nologs", "nostat")
	t.Logf("news.daily: err=%v\n%s", err, out)
	mail, _ := os.ReadFile(report)
	t.Logf("mail:\n%s", mail)
	if _, err := os.Stat(filepath.Join(expdir, "history.n.done")); err != nil {
		t.Errorf("history.n.done was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(expdir, "history.n")); err != nil {
		t.Errorf("history.n was moved although the old history could not be emptied: %v", err)
	}
	if !strings.Contains(string(mail), "Cannot empty") {
		t.Errorf("the newsmaster was not notified")
	}
	if mode := s.Ctlinnd(t, "mode"); strings.Contains(mode, "Server running") {
		t.Errorf("innd was unpaused:\n%s", mode)
	}
	if err := os.Chmod(history, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := historyToken(t, s, id); got != token {
		t.Errorf("grephistory %s = %q, want %q: the old history was changed", id, got, token)
	}
}

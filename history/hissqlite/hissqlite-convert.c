/*
**  Convert an existing history database to the hissqlite (SQLite) backend.
**
**  Usage: hissqlite-convert [-m srcmethod] <srcpath> <dstpath>
**
**  Reads the source history (default method "hisv6") via HISwalk and
**  bulk-loads every entry into a freshly created hissqlite database keyed by
**  hash.  This is a faithful migration (the makedbz analog): it preserves
**  exact timestamps AND remembered (token-less) entries, which a from-spool
**  rebuild (makehistory) cannot reconstruct.
**
**  Insertion is by HASH, not Message-ID: the history text stores only the MD5,
**  and the original Message-ID is unrecoverable, so HISwrite (which re-hashes
**  a Message-ID) cannot be used.  We therefore create the canonical schema via
**  the hissqlite backend, then bulk-insert by hash through a raw SQLite
**  connection.
**
**  Written by Kevin Bowling in 2026.
*/

#include "portable/system.h"

#include "inn/history.h"
#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/messages.h"
#include "inn/paths.h"
#include "inn/storage.h"

#ifndef HAVE_SQLITE3

int
main(void)
{
    die("INN was built without SQLite support");
}

#else

#    include <errno.h>
#    include <fcntl.h>
#    include <signal.h>
#    include <sqlite3.h>
#    include <sys/stat.h>

/* Commit the load transaction every this many rows. */
#    define COMMIT_EVERY 50000

struct convstate {
    sqlite3 *db;
    sqlite3_stmt *insert;
    unsigned long count; /* rows successfully inserted */
    unsigned long dups;  /* duplicate-hash rows skipped (anomalous) */
};

/* The files SQLite may create for a database, as suffixes of its name. */
static const char *const sqlite_suffixes[] = {"", "-journal", "-wal", "-shm"};

/* The private directory the database is built in, the database in it, and
   the paths of the database and of the files SQLite may create beside it.
   They stay allocated until exit, so that the signal handler can use them;
   temp_active tells whether the directory is still there. */
static char *tempdir = NULL;
static char *tempdb = NULL;
static char *tempfiles[ARRAY_SIZE(sqlite_suffixes)];
static volatile sig_atomic_t temp_active = 0;

/*
**  Remove the temporary database, the files SQLite may have left beside it,
**  and its directory.  Only unlink and rmdir are called, which are
**  async-signal-safe, so that the signal handler can call it too.
*/
static void
remove_temp(void)
{
    size_t i;

    if (!temp_active)
        return;
    for (i = 0; i < ARRAY_SIZE(tempfiles); i++)
        unlink(tempfiles[i]);
    rmdir(tempdir);
    temp_active = 0;
}

/* Called by die and sysdie: a failed run leaves nothing behind. */
static int
fatal_cleanup(void)
{
    remove_temp();
    return 1;
}

/* SIGHUP, SIGINT and SIGTERM: remove the temporary directory, then end with
   the signal. */
static void
signal_cleanup(int sig)
{
    remove_temp();
    signal(sig, SIG_DFL);
    raise(sig);
}

/*
**  Create a directory of our own in the directory of dstpath, named
**  hisconv.PID.N (short, whatever the length of the name of dstpath), and set
**  tempdir, tempdb and tempfiles.  mkdir fails if the directory exists, so
**  another conversion, running or failed, never shares it.  The paths are
**  prepared first, and the signals the handler catches are blocked from the
**  creation of the directory until it is marked active, so that a signal
**  cannot leave it behind.
*/
static void
make_temp(const char *dstpath)
{
    char name[64];
    const char *slash;
    char *dir;
    unsigned int n;
    size_t i;
    sigset_t block, saved;
    int status, oerrno;

    xsignal(SIGHUP, signal_cleanup);
    xsignal(SIGINT, signal_cleanup);
    xsignal(SIGTERM, signal_cleanup);
    slash = strrchr(dstpath, '/');
    if (slash == NULL)
        dir = xstrdup(".");
    else if (slash == dstpath)
        dir = xstrdup("/");
    else
        dir = xstrndup(dstpath, slash - dstpath);
    sigemptyset(&block);
    sigaddset(&block, SIGHUP);
    sigaddset(&block, SIGINT);
    sigaddset(&block, SIGTERM);
    for (n = 0;; n++) {
        snprintf(name, sizeof(name), "hisconv.%lu.%u",
                 (unsigned long) getpid(), n);
        tempdir = concatpath(dir, name);
        tempdb = concatpath(tempdir, "history.sqlite");
        for (i = 0; i < ARRAY_SIZE(tempfiles); i++)
            tempfiles[i] = concat(tempdb, sqlite_suffixes[i], (char *) NULL);
        sigprocmask(SIG_BLOCK, &block, &saved);
        status = mkdir(tempdir, 0700);
        oerrno = errno;
        if (status == 0)
            temp_active = 1;
        sigprocmask(SIG_SETMASK, &saved, NULL);
        if (status == 0)
            break;
        if (oerrno != EEXIST || n >= 100) {
            errno = oerrno;
            sysdie("cannot create temporary directory %s", tempdir);
        }
        free(tempdir);
        free(tempdb);
        for (i = 0; i < ARRAY_SIZE(tempfiles); i++)
            free(tempfiles[i]);
    }
    free(dir);
    message_fatal_cleanup = fatal_cleanup;
}

/*
**  Test hook, only with INN_TESTSUITE set in the environment: according to
**  HISSQLITE_CONVERT_TEST, act just before the publication as if something
**  else had meanwhile.  "file" creates the destination (another conversion,
**  or innd), "link" publishes the database already (as a link request
**  retransmitted over NFS would have), "symlink" makes the destination a
**  symbolic link to the database, "wait" sleeps to let a test send a signal.
*/
static void
test_hook(const char *finaldb)
{
    const char *what;
    int fd;

    what = getenv("HISSQLITE_CONVERT_TEST");
    if (getenv(INN_ENV_TESTSUITE) == NULL || what == NULL)
        return;
    if (strcmp(what, "file") == 0) {
        fd = open(finaldb, O_WRONLY | O_CREAT | O_EXCL, 0644);
        if (fd < 0 || write(fd, "racer\n", 6) != 6 || close(fd) != 0)
            syswarn("test hook: cannot create %s", finaldb);
    } else if (strcmp(what, "link") == 0) {
        if (link(tempdb, finaldb) != 0)
            syswarn("test hook: cannot link %s", finaldb);
    } else if (strcmp(what, "symlink") == 0) {
        if (symlink(tempdb, finaldb) != 0)
            syswarn("test hook: cannot create symbolic link %s", finaldb);
    } else if (strcmp(what, "wait") == 0) {
        sleep(60);
    }
}

static void
checked(sqlite3 *db, int status, const char *what)
{
    if (status != SQLITE_OK)
        die("%s: %s", what, sqlite3_errmsg(db));
}

static void
exec(sqlite3 *db, const char *sql)
{
    char *errmsg = NULL;

    if (sqlite3_exec(db, sql, NULL, NULL, &errmsg) != SQLITE_OK)
        die("%s: %s", sql, errmsg ? errmsg : "(unknown)");
}

/*
**  HISwalk callback: insert one source entry into the hissqlite DB by hash.
**  token == NULL marks a remembered entry (stored with a NULL token column).
*/
static bool
convert_cb(void *cookie, const HASH *hash, time_t arrived, time_t posted,
           time_t expires, const TOKEN *token)
{
    struct convstate *st = cookie;
    sqlite3_stmt *ins = st->insert;
    int rc;

    sqlite3_bind_blob(ins, 1, hash, sizeof(HASH), SQLITE_STATIC);
    sqlite3_bind_int64(ins, 2, (sqlite3_int64) arrived);
    sqlite3_bind_int64(ins, 3, (sqlite3_int64) posted);
    sqlite3_bind_int64(ins, 4, (sqlite3_int64) expires);
    if (token == NULL)
        sqlite3_bind_null(ins, 5);
    else
        sqlite3_bind_blob(ins, 5, token, sizeof(TOKEN), SQLITE_STATIC);

    rc = sqlite3_step(ins);
    sqlite3_reset(ins);
    if (rc == SQLITE_DONE) {
        st->count++;
    } else if ((rc & 0xFF) == SQLITE_CONSTRAINT) {
        /* Duplicate hash in the source: anomalous for a valid history, but not
           fatal -- count it rather than silently dropping it. */
        st->dups++;
        return true;
    } else {
        /* A real error (I/O, disk full, corruption): abort.  The half-built
           database is a temp file that is never renamed into place. */
        die("insert failed after %lu entries: %s", st->count,
            sqlite3_errmsg(st->db));
    }

    if (st->count % COMMIT_EVERY == 0) {
        exec(st->db, "commit");
        exec(st->db, "begin");
        if (st->count % (COMMIT_EVERY * 20) == 0)
            notice("%lu entries converted...", st->count);
    }
    return true;
}

int
main(int argc, char *argv[])
{
    const char *srcmethod = "hisv6";
    const char *srcpath, *dstpath;
    char *finaldb, *tempbase;
    struct history *src, *tmp;
    struct convstate st;
    struct stat sb;
    int opt;

    message_program_name = "hissqlite-convert";

    while ((opt = getopt(argc, argv, "m:")) != -1) {
        switch (opt) {
        case 'm':
            srcmethod = optarg;
            break;
        default:
            die("usage: hissqlite-convert [-m srcmethod] <srcpath> <dstpath>");
        }
    }
    if (argc - optind != 2)
        die("usage: hissqlite-convert [-m srcmethod] <srcpath> <dstpath>");
    srcpath = argv[optind];
    dstpath = argv[optind + 1];

    /* The created database honors the hissqlite* tunables (page size in
       particular) from inn.conf, matching what innd will later use. */
    if (!innconf_read(NULL))
        exit(1);

    /* Refuse to overwrite an existing destination: a re-run or a typo must not
       clobber or merge into a live history. */
    finaldb = concat(dstpath, ".sqlite", (char *) NULL);
    if (stat(finaldb, &sb) == 0)
        die("destination %s already exists; refusing to overwrite", finaldb);

    /* Build in a directory of our own next to the destination and publish the
       database only on full success, so a crash or error never leaves a
       usable-but-incomplete destination (this is also what makes
       journal_mode=off below safe).  A private directory, rather than a fixed
       temporary name, keeps two conversions to the same destination from
       removing or publishing each other's unfinished database. */
    make_temp(dstpath);
    tempbase = concatpath(tempdir, "history");

    /* Create the temp DB with the canonical hissqlite schema, then reopen the
       raw SQLite file for a fast by-hash bulk load. */
    tmp = HISopen(tempbase, "hissqlite", HIS_CREAT | HIS_RDWR);
    if (tmp == NULL)
        die("cannot create destination %s", tempdb);
    HISclose(tmp);

    memset(&st, 0, sizeof(st));
    if (sqlite3_open(tempdb, &st.db) != SQLITE_OK)
        die("cannot open %s: %s", tempdb, sqlite3_errmsg(st.db));

    /* Bulk-load tuning: no per-row durability (safe -- a failure leaves the
       temp file, never the final), and drop the secondary index so it is
       built once at the end.  (The CREATE INDEX statement mirrors
       hissqlite-init.sql.) */
    exec(st.db, "pragma synchronous = off");
    exec(st.db, "pragma journal_mode = off");
    /* A large page cache keeps the random-MD5 B-tree hot during the load, so
       inserts don't constantly evict and re-fetch leaf pages.  (The first innd
       open flips the finished DB to WAL.) */
    exec(st.db, "pragma cache_size = -262144"); /* 256 MiB (negative = KiB) */
    exec(st.db, "drop index if exists hist_remember");

    /* Plain INSERT (not OR IGNORE): a duplicate hash returns SQLITE_CONSTRAINT
       so convert_cb can count it instead of silently dropping the row. */
    checked(st.db,
            sqlite3_prepare_v3(st.db,
                               "insert into hist"
                               "(hash, arrived, posted, expires, token)"
                               " values(?1, ?2, ?3, ?4, ?5)",
                               -1, SQLITE_PREPARE_PERSISTENT, &st.insert,
                               NULL),
            "prepare insert");

    src = HISopen(srcpath, srcmethod, HIS_RDONLY);
    if (src == NULL)
        die("cannot open source %s (method %s)", srcpath, srcmethod);

    exec(st.db, "begin");
    if (!HISwalk(src, NULL, &st, convert_cb))
        die("HISwalk failed: %s", HISerror(src));
    /* The walk skips malformed lines (and warns about each of them); it then
       reports how many it skipped. */
    if (HISerror(src) != NULL)
        warn("source not converted completely: %s", HISerror(src));
    exec(st.db, "commit");
    HISclose(src);

    sqlite3_finalize(st.insert);
    notice("rebuilding index...");
    exec(st.db, "create index hist_remember on hist(posted, arrived)"
                " where token is null");
    if (sqlite3_close(st.db) != SQLITE_OK)
        die("cannot close %s", tempdb);

    /* Publish the completed database under its final name without replacing
       a destination created since the check above (by another conversion, or
       by an innd already started with hismethod set to hissqlite): link fails
       if the name exists, so the file system of the destination must support
       hard links.  Over NFS, a retransmitted link request can fail with
       EEXIST although the first one succeeded: the destination is then our
       database, as the same inode tells.  lstat, so that a symbolic link to
       our database is not taken for it. */
    test_hook(finaldb);
    if (link(tempdb, finaldb) != 0) {
        struct stat tsb;
        int oerrno = errno;

        if (oerrno != EEXIST) {
            errno = oerrno;
            sysdie("cannot link %s to %s (the file system of the destination"
                   " must support hard links)",
                   tempdb, finaldb);
        }
        if (stat(tempdb, &tsb) != 0 || lstat(finaldb, &sb) != 0
            || !S_ISREG(sb.st_mode) || tsb.st_dev != sb.st_dev
            || tsb.st_ino != sb.st_ino)
            die("destination %s was created during the conversion; refusing"
                " to overwrite",
                finaldb);
    }
    remove_temp();

    if (st.dups > 0)
        warn("%lu duplicate hashes in the source were skipped", st.dups);
    notice("converted %lu entries into %s", st.count, finaldb);
    free(finaldb);
    free(tempbase);
    return 0;
}

#endif /* HAVE_SQLITE3 */

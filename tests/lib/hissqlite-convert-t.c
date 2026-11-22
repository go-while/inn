/*
**  End-to-end test for the hissqlite-convert migration tool.
**
**  Seeds a small hisv6 history (real + remembered entries, distinct
**  timestamps), runs the hissqlite-convert binary against it, and verifies the
**  resulting hissqlite database through the public HIS API: the faithful
**  migration must preserve tokens, timestamps and -- crucially -- the
**  remembered (token-less) entries that a from-spool rebuild cannot
**  reconstruct.  Also checks the refuse-to-overwrite guard, that the
**  temporary build files are cleaned up, that a conversion leaves alone a
**  temporary file it does not own, and that of two conversions run at the same
**  time to the same destination, exactly one publishes a complete database.
**
**  This drives the real binary (via system()) rather than the library, so it
**  needs inn.conf: a minimal one is written to the temp dir and pointed at
**  with INNCONF, exactly as the shell integration tests do.
**
**  Written by Kevin Bowling in 2026.
*/

#include "portable/system.h"

#include "inn/history.h"
#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/messages.h"
#include "inn/storage.h"
#include "tap/basic.h"

#ifndef HAVE_SQLITE3

int
main(void)
{
    skip_all("not built with SQLite");
    return 0;
}

#else

#    include <dirent.h>
#    include <fcntl.h>
#    include <sys/stat.h>
#    include <sys/wait.h>

#    define BASE ((time_t) 1600000000)

/* Entries of the history converted by two runs at the same time: enough for
   both runs to pass the initial check on the destination before either one
   publishes its database. */
#    define BULK 20000

static const char convert[] = "../../history/hissqlite/hissqlite-convert";

/* Run "<convert> <src> <dst>" with output suppressed; return its exit code,
   or -1 if it could not be run. */
static int
run_convert(const char *src, const char *dst)
{
    char cmd[768]; /* Should provide a PATH_MAX fill in build somewhere */
    int status;

    snprintf(cmd, sizeof(cmd), "%s %s %s >/dev/null 2>&1", convert, src, dst);
    status = system(cmd);
    if (status == -1 || !WIFEXITED(status))
        return -1;
    return WEXITSTATUS(status);
}

/* Start "<convert> <src> <dst>" in the background with output suppressed and
   return its process ID. */
static pid_t
start_convert(const char *src, const char *dst)
{
    pid_t pid;
    int fd;

    pid = fork();
    if (pid < 0)
        sysbail("can't fork");
    if (pid == 0) {
        fd = open("/dev/null", O_WRONLY);
        if (fd >= 0) {
            dup2(fd, 1);
            dup2(fd, 2);
        }
        execl(convert, convert, src, dst, (char *) NULL);
        _exit(127);
    }
    return pid;
}

/* Wait for the process started by start_convert and return its exit code, or
   -1 if it did not exit normally. */
static int
wait_convert(pid_t pid)
{
    int status;

    if (waitpid(pid, &status, 0) != pid)
        sysbail("can't wait for %lu", (unsigned long) pid);
    return WIFEXITED(status) ? WEXITSTATUS(status) : -1;
}

/* Count the entries of dir whose name starts with prefix, apart from the
   entry named except (which may be NULL). */
static int
count_entries(const char *dir, const char *prefix, const char *except)
{
    DIR *d;
    struct dirent *e;
    int n = 0;

    d = opendir(dir);
    if (d == NULL)
        sysbail("can't open %s", dir);
    while ((e = readdir(d)) != NULL)
        if (strncmp(e->d_name, prefix, strlen(prefix)) == 0
            && (except == NULL || strcmp(e->d_name, except) != 0))
            n++;
    closedir(d);
    return n;
}

int
main(void)
{
    char tmpdir[64], conf[128], src[128], dst[128], dstdb[160], tempdb[160];
    char dst2[128], dst2temp[160], bulk[128], dst3[128], msgid[64];
    struct history *h;
    FILE *f;
    TOKEN t1, t2, got;
    bool reals_ok, remembered_ok, absent_ok;
    struct stat sb;

    /* runtests runs us from tests/; move into our own directory so the
       relative path to the binary resolves */
    if (access("hissqlite-convert.t", F_OK) < 0)
        if (access("lib/hissqlite-convert.t", F_OK) == 0)
            if (chdir("lib") < 0)
                sysbail("cannot chdir to lib");
    if (access(convert, X_OK) != 0)
        skip_all("hissqlite-convert not built");

    test_init(13);

    strlcpy(tmpdir, "hissqlite-conv-XXXXXX", sizeof(tmpdir));
    if (mkdtemp(tmpdir) == NULL)
        sysbail("can't create temp directory");
    snprintf(conf, sizeof(conf), "%s/inn.conf", tmpdir);
    snprintf(src, sizeof(src), "%s/src", tmpdir);
    snprintf(dst, sizeof(dst), "%s/dst", tmpdir);
    snprintf(dstdb, sizeof(dstdb), "%s/dst.sqlite", tmpdir);
    snprintf(tempdb, sizeof(tempdb), "%s/dst.new.sqlite", tmpdir);
    snprintf(dst2, sizeof(dst2), "%s/dst2", tmpdir);
    snprintf(dst2temp, sizeof(dst2temp), "%s/dst2.new.sqlite", tmpdir);
    snprintf(bulk, sizeof(bulk), "%s/bulk", tmpdir);
    snprintf(dst3, sizeof(dst3), "%s/dst3", tmpdir);

    /* Minimal inn.conf so both this process and the convert binary can read a
       valid configuration (convert calls innconf_read). */
    f = fopen(conf, "w");
    if (f == NULL)
        sysbail("can't write %s", conf);
    fprintf(f,
            "domain:          news.example.com\n"
            "mta:             \"/bin/true %%s\"\n"
            "hismethod:       hisv6\n"
            "enableoverview:  false\n"
            "wireformat:      true\n"
            "pathnews:        %s\n"
            "pathdb:          %s\n"
            "pathetc:         %s\n",
            tmpdir, tmpdir, tmpdir);
    fclose(f);
    if (setenv("INNCONF", conf, 1) != 0)
        sysbail("can't set INNCONF");
    if (!innconf_read(NULL))
        bail("can't read test inn.conf");

    /* Seed a hisv6 source: two real articles with distinct timestamps/tokens
       and two remembered (token-less) entries. */
    h = HISopen(src, "hisv6", HIS_CREAT | HIS_RDWR);
    if (h == NULL)
        bail("can't create hisv6 source");
    memset(&t1, 0, sizeof(t1));
    t1.type = 1;
    memcpy(t1.token, "0123456789abcdef", sizeof(t1.token));
    memset(&t2, 0, sizeof(t2));
    t2.type = 2;
    memcpy(t2.token, "fedcba9876543210", sizeof(t2.token));
    if (!HISwrite(h, "<r1@conv>", BASE + 1, BASE + 1, BASE + 5000, &t1)
        || !HISwrite(h, "<r2@conv>", BASE + 2, BASE + 7, (time_t) 0, &t2)
        || !HISremember(h, "<m1@conv>", BASE + 3, BASE + 3)
        || !HISremember(h, "<m2@conv>", BASE + 4, BASE + 4))
        bail("can't seed hisv6 source");
    HISsync(h);
    HISclose(h);

    /* Convert hisv6 -> hissqlite. */
    ok(1, run_convert(src, dst) == 0);

    /* Verify the result.  The converter leaves the database in a non-WAL
       journal mode (the first innd open flips it to WAL), so open read/write
       rather than via the WAL-only direct reader. */
    h = HISopen(dst, "hissqlite", HIS_RDWR);
    if (h == NULL)
        bail("can't open converted hissqlite database");

    {
        time_t a = 0, p = 0, e = -1;
        bool r1, r2;

        memset(&got, 0xff, sizeof(got));
        r1 = HISlookup(h, "<r1@conv>", &a, &p, &e, &got) && got.type == 1
             && memcmp(got.token, t1.token, sizeof(t1.token)) == 0
             && a == BASE + 1 && p == BASE + 1 && e == BASE + 5000;
        memset(&got, 0xff, sizeof(got));
        r2 = HISlookup(h, "<r2@conv>", &a, &p, &e, &got) && got.type == 2
             && memcmp(got.token, t2.token, sizeof(t2.token)) == 0
             && a == BASE + 2 && p == BASE + 7;
        reals_ok = r1 && r2;
    }
    ok(2, reals_ok); /* real tokens and timestamps round-trip */

    {
        bool f1, f2;

        /* Remembered entries are known to HIScheck (refused on re-offer) but
           not found by HISlookup (no article), matching hisv6. */
        f1 = HIScheck(h, "<m1@conv>")
             && !HISlookup(h, "<m1@conv>", NULL, NULL, NULL, NULL);
        f2 = HIScheck(h, "<m2@conv>")
             && !HISlookup(h, "<m2@conv>", NULL, NULL, NULL, NULL);
        remembered_ok = f1 && f2;
    }
    ok(3, remembered_ok); /* remembered entries survived the migration */

    absent_ok = !HIScheck(h, "<never@conv>");
    ok(4, absent_ok);

    {
        bool counts;
        /* Existence of every seeded entry, nothing spurious: 2 real + 2
           remembered, all present and distinguishable. */
        counts = HIScheck(h, "<r1@conv>") && HIScheck(h, "<r2@conv>")
                 && HIScheck(h, "<m1@conv>") && HIScheck(h, "<m2@conv>");
        ok(5, counts);
    }
    HISclose(h);

    /* Refuse to overwrite an existing destination: a second run must fail and
       leave the database in place. */
    ok(6, run_convert(src, dst) != 0);
    ok(7, stat(dstdb, &sb) == 0); /* existing database left in place */

    /* The temporary build file must not survive a successful conversion. */
    ok(8, access(tempdb, F_OK) != 0);

    /* A file at the temporary name an older version used, for instance the
       database another conversion is building, is left alone. */
    f = fopen(dst2temp, "w");
    if (f == NULL)
        sysbail("can't write %s", dst2temp);
    fputs("not ours\n", f);
    fclose(f);
    ok(9, run_convert(src, dst2) == 0);
    {
        char buf[32] = "";

        f = fopen(dst2temp, "r");
        if (f != NULL) {
            if (fgets(buf, sizeof(buf), f) == NULL)
                buf[0] = '\0';
            fclose(f);
        }
        ok(10, strcmp(buf, "not ours\n") == 0);
    }

    /* Two conversions to the same destination at the same time: exactly one
       publishes, and what it publishes is complete. */
    h = HISopen(bulk, "hisv6", HIS_CREAT | HIS_RDWR);
    if (h == NULL)
        bail("can't create hisv6 source %s", bulk);
    {
        int i;

        for (i = 0; i < BULK; i++) {
            snprintf(msgid, sizeof(msgid), "<bulk%d@conv>", i);
            if (!HISwrite(h, msgid, BASE + i, BASE + i, (time_t) 0, &t1))
                bail("can't seed %s", bulk);
        }
    }
    HISsync(h);
    HISclose(h);
    {
        pid_t one, two;
        int rc1, rc2, i, found = 0;

        one = start_convert(bulk, dst3);
        two = start_convert(bulk, dst3);
        rc1 = wait_convert(one);
        rc2 = wait_convert(two);
        ok(11, (rc1 == 0) != (rc2 == 0));
        if ((rc1 == 0) == (rc2 == 0))
            diag("exit codes %d and %d", rc1, rc2);

        h = HISopen(dst3, "hissqlite", HIS_RDWR);
        if (h != NULL) {
            for (i = 0; i < BULK; i++) {
                snprintf(msgid, sizeof(msgid), "<bulk%d@conv>", i);
                if (HIScheck(h, msgid))
                    found++;
            }
            HISclose(h);
        }
        ok(12, found == BULK);
        if (found != BULK)
            diag("%d of %d entries in the published database", found, BULK);
    }

    /* No temporary directory or file is left behind, apart from the one
       created by this test. */
    ok(13, count_entries(tmpdir, "dst.new.", NULL)
                   + count_entries(tmpdir, "dst2.new.", "dst2.new.sqlite")
                   + count_entries(tmpdir, "dst3.new.", NULL)
               == 0);

    {
        char cmd[160];
        snprintf(cmd, sizeof(cmd), "/bin/rm -rf %s", tmpdir);
        if (system(cmd) < 0)
            sysdiag("can't clean up %s", tmpdir);
    }
    return 0;
}

#endif /* HAVE_SQLITE3 */

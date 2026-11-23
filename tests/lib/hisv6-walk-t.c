/*
** Tests for the traversal of a hisv6 history file by HISwalk and HISexpire.
**
** An empty history is a valid one:  walking or expiring it must succeed,
** and it must stay empty.  A non-empty file without a single valid line is
** not:  walking or expiring it must fail and leave it in place.  A line too
** long for the read buffer is skipped like any other malformed line, also as
** the last line and without a final newline:  the entries after it must still
** be visited, and must still be in the history written by HISexpire or in the
** database written by hissqlite-convert, which reports the skipped line.
** A short last line without a final newline is reported as skipped too.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <sys/stat.h>
#include <sys/wait.h>

#include "inn/history.h"
#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/storage.h"
#include "tap/basic.h"

/* More than the 32 KB read buffer of QIOread. */
#define LONG_LINE 40000

/* Entries written before and after the overlong line. */
#define N_BEFORE 10
#define N_AFTER  10

struct counts {
    unsigned long tokens;
    unsigned long remembered;
};

static char *
make_msgid(const char *prefix, unsigned long n)
{
    char buf[64];

    snprintf(buf, sizeof(buf), "<%s-%lu@hisv6-walk.test>", prefix, n);
    return bstrdup(buf);
}

static bool
count_cb(void *cookie, const HASH *hash UNUSED, time_t arrived UNUSED,
         time_t posted UNUSED, time_t expires UNUSED, const TOKEN *token)
{
    struct counts *c = cookie;

    if (token != NULL)
        c->tokens++;
    else
        c->remembered++;
    return true;
}

/* HISexpire callback keeping every article. */
static bool
keep_cb(void *cookie UNUSED, time_t arrived UNUSED, time_t posted UNUSED,
        time_t expires UNUSED, TOKEN *token UNUSED)
{
    return true;
}

static void
write_entries(struct history *h, const char *prefix, unsigned long n)
{
    TOKEN token;
    unsigned long i;
    char *msgid;

    memset(&token, 0, sizeof(token));
    token.type = 1;
    for (i = 0; i < n; i++) {
        msgid = make_msgid(prefix, i);
        if (!HISwrite(h, msgid, (time_t) 1000000 + i, (time_t) 1000000 + i,
                      (time_t) 0, &token))
            bail("cannot write %s: %s", msgid, HISerror(h));
        free(msgid);
    }
}

static off_t
file_size(const char *path)
{
    struct stat st;

    if (stat(path, &st) < 0)
        return -1;
    return st.st_size;
}

/* Whether the file at path contains the string want. */
static bool
file_contains(const char *path, const char *want)
{
    char buf[8192];
    size_t n;
    FILE *f;

    f = fopen(path, "r");
    if (f == NULL)
        return false;
    n = fread(buf, 1, sizeof(buf) - 1, f);
    fclose(f);
    buf[n] = '\0';
    return strstr(buf, want) != NULL;
}

/* Create an empty hisv6 history (text file and dbz index). */
static void
create_empty(const char *path)
{
    struct history *h;

    h = HISopen(path, "hisv6", HIS_CREAT | HIS_RDWR);
    if (h == NULL)
        bail("cannot create %s", path);
    HISclose(h);
}

static void
append_text(const char *path, const char *text)
{
    FILE *f;

    f = fopen(path, "a");
    if (f == NULL)
        sysbail("cannot open %s", path);
    fputs(text, f);
    if (fclose(f) == EOF)
        sysbail("cannot write %s", path);
}

/* Append a damaged line, longer than the read buffer. */
static void
append_long(const char *path, bool newline)
{
    FILE *f;
    unsigned long i;

    f = fopen(path, "a");
    if (f == NULL)
        sysbail("cannot open %s", path);
    for (i = 0; i < LONG_LINE; i++)
        putc('x', f);
    if (newline)
        putc('\n', f);
    if (fclose(f) == EOF)
        sysbail("cannot write %s", path);
}

/* A history with N_BEFORE entries, an overlong line, N_AFTER entries and a
   remembered entry. */
static void
create_long(const char *path)
{
    struct history *h;
    char *msgid;

    h = HISopen(path, "hisv6", HIS_CREAT | HIS_RDWR);
    if (h == NULL)
        bail("cannot create %s", path);
    write_entries(h, "before", N_BEFORE);
    HISclose(h);
    append_long(path, true);
    h = HISopen(path, "hisv6", HIS_RDWR);
    if (h == NULL)
        bail("cannot reopen %s", path);
    write_entries(h, "after", N_AFTER);
    msgid = make_msgid("remembered", 0);
    if (!HISremember(h, msgid, (time_t) 2000000, (time_t) 2000000))
        bail("cannot remember %s: %s", msgid, HISerror(h));
    free(msgid);
    HISclose(h);
}

/* Walk the history at path and return the entries seen through *c. */
static bool
walk(const char *path, struct counts *c)
{
    struct history *h;
    bool r;

    memset(c, 0, sizeof(*c));
    h = HISopen(path, "hisv6", HIS_RDONLY);
    if (h == NULL)
        bail("cannot open %s", path);
    r = HISwalk(h, NULL, c, count_cb);
    HISclose(h);
    return r;
}

/* Expire the history at path in place, keeping everything. */
static bool
expire(const char *path)
{
    struct history *h;
    bool r;

    h = HISopen(path, "hisv6", HIS_RDONLY);
    if (h == NULL)
        bail("cannot open %s", path);
    r = HISexpire(h, NULL, NULL, true, NULL, 0, keep_cb);
    HISclose(h);
    return r;
}

static void
test_empty(const char *path)
{
    struct counts c;

    create_empty(path);
    ok(walk(path, &c), "HISwalk of an empty history succeeds");
    is_int(0, (long) (c.tokens + c.remembered), "... and visits nothing");
    ok(expire(path), "HISexpire of an empty history succeeds");
    ok(walk(path, &c), "... and the history is still a valid one");
    is_int(0, (long) file_size(path), "... and still empty");
}

static void
test_all_malformed(const char *path)
{
    struct counts c;
    off_t size;

    create_empty(path);
    append_text(path, "not a history line\nnor this one\r\n");
    size = file_size(path);
    ok(!walk(path, &c), "HISwalk of a history without a valid line fails");
    ok(!expire(path), "HISexpire of a history without a valid line fails");
    is_int((long) size, (long) file_size(path), "... and leaves it in place");
}

static void
test_long_last(const char *path, bool newline)
{
    struct history *h;
    struct counts c;

    h = HISopen(path, "hisv6", HIS_CREAT | HIS_RDWR);
    if (h == NULL)
        bail("cannot create %s", path);
    write_entries(h, "before", N_BEFORE);
    HISclose(h);
    append_long(path, newline);
    ok(walk(path, &c), "HISwalk with an overlong last line%s succeeds",
       newline ? "" : " without a newline");
    is_int(N_BEFORE, (long) c.tokens, "... and visits the articles before it");
}

/* Valid entries, then a short last line without a final newline. */
static void
create_short_tail(const char *path)
{
    struct history *h;

    h = HISopen(path, "hisv6", HIS_CREAT | HIS_RDWR);
    if (h == NULL)
        bail("cannot create %s", path);
    write_entries(h, "before", N_BEFORE);
    HISclose(h);
    append_text(path, "[0123456789ABCDEF] 1000");
}

static void
test_short_tail(const char *path)
{
    struct history *h;
    struct counts c;
    const char *error;

    create_short_tail(path);
    memset(&c, 0, sizeof(c));
    h = HISopen(path, "hisv6", HIS_RDONLY);
    if (h == NULL)
        bail("cannot open %s", path);
    ok(HISwalk(h, NULL, &c, count_cb),
       "HISwalk with a short unterminated last line succeeds");
    is_int(N_BEFORE, (long) c.tokens, "... and visits the articles before it");
    error = HISerror(h);
    ok(error != NULL && strstr(error, "1 malformed lines skipped") != NULL,
       "... and reports the unterminated line as skipped");
    HISclose(h);
}

static void
test_long_line(const char *path)
{
    struct history *h;
    struct counts c;
    unsigned long i;
    char *msgid;
    bool found;

    create_long(path);

    ok(walk(path, &c), "HISwalk with an overlong line succeeds");
    is_int(N_BEFORE + N_AFTER, (long) c.tokens,
           "... and visits the articles after the overlong line");
    is_int(1, (long) c.remembered,
           "... and the remembered entry after the overlong line");

    ok(expire(path), "HISexpire with an overlong line succeeds");
    ok(walk(path, &c), "... HISwalk of the expired history succeeds");
    is_int(N_BEFORE + N_AFTER, (long) c.tokens,
           "... the expired history keeps the articles after the line");
    is_int(1, (long) c.remembered,
           "... and the remembered entry after the line");

    h = HISopen(path, "hisv6", HIS_RDONLY);
    if (h == NULL)
        bail("cannot reopen %s", path);
    found = true;
    for (i = 0; i < N_AFTER; i++) {
        msgid = make_msgid("after", i);
        if (!HISlookup(h, msgid, NULL, NULL, NULL, NULL)) {
            diag("%s not found after HISexpire", msgid);
            found = false;
        }
        free(msgid);
    }
    ok(found, "... and they can be looked up");
    HISclose(h);
}

static void
test_convert(const char *tmpdir)
{
#ifdef HAVE_SQLITE3
    static const char *const programs[] = {
        "../history/hissqlite/hissqlite-convert",
        "../../history/hissqlite/hissqlite-convert",
    };
    const char *program = NULL;
    char *conf, *src, *dst, *db, *err, *cmd;
    struct history *h;
    unsigned long i;
    char *msgid;
    bool found;
    FILE *f;
    int status;

    for (i = 0; i < ARRAY_SIZE(programs); i++)
        if (access(programs[i], X_OK) == 0)
            program = programs[i];
    if (program == NULL) {
        skip_block(9, "hissqlite-convert not built");
        return;
    }

    /* hissqlite-convert and the hissqlite method read inn.conf. */
    conf = concat(tmpdir, "/hisv6-walk-inn.conf", (char *) 0);
    f = fopen(conf, "w");
    if (f == NULL)
        sysbail("cannot write %s", conf);
    fprintf(f,
            "domain: news.example.com\nmta: \"/bin/true %%s\"\n"
            "hismethod: hisv6\nenableoverview: false\n"
            "pathnews: %s\npathdb: %s\npathetc: %s\n",
            tmpdir, tmpdir, tmpdir);
    fclose(f);
    if (setenv("INNCONF", conf, 1) != 0)
        sysbail("cannot set INNCONF");
    if (!innconf_read(NULL))
        bail("cannot read %s", conf);
    err = concat(tmpdir, "/hisv6-walk-conv.err", (char *) 0);

    /* A history with an overlong line in the middle. */
    src = concat(tmpdir, "/hisv6-walk-conv-src", (char *) 0);
    dst = concat(tmpdir, "/hisv6-walk-conv-dst", (char *) 0);
    create_long(src);
    cmd = concat(program, " ", src, " ", dst, " >/dev/null 2>", err,
                 (char *) 0);
    status = system(cmd);
    free(cmd);
    is_int(0, WIFEXITED(status) ? WEXITSTATUS(status) : -1,
           "hissqlite-convert with an overlong line succeeds");
    ok(file_contains(err, "1 malformed lines skipped"),
       "... and reports the skipped line");
    h = HISopen(dst, "hissqlite", HIS_RDWR);
    if (h == NULL)
        bail("cannot open the converted database %s", dst);
    found = true;
    for (i = 0; i < N_AFTER; i++) {
        msgid = make_msgid("after", i);
        if (!HISlookup(h, msgid, NULL, NULL, NULL, NULL)) {
            diag("%s not in the converted database", msgid);
            found = false;
        }
        free(msgid);
    }
    ok(found, "... the articles after the line are in the database");
    msgid = make_msgid("remembered", 0);
    ok(HIScheck(h, msgid), "... and the remembered entry");
    free(msgid);
    HISclose(h);
    free(src);
    free(dst);

    /* A short unterminated last line. */
    src = concat(tmpdir, "/hisv6-walk-conv-tail", (char *) 0);
    dst = concat(tmpdir, "/hisv6-walk-conv-taildst", (char *) 0);
    create_short_tail(src);
    cmd = concat(program, " ", src, " ", dst, " >/dev/null 2>", err,
                 (char *) 0);
    status = system(cmd);
    free(cmd);
    is_int(0, WIFEXITED(status) ? WEXITSTATUS(status) : -1,
           "hissqlite-convert with a short unterminated last line succeeds");
    ok(file_contains(err, "unterminated last line")
           && file_contains(err, "1 malformed lines skipped"),
       "... and reports the line as skipped");
    free(src);
    free(dst);

    /* A file without a single valid line. */
    src = concat(tmpdir, "/hisv6-walk-conv-bad", (char *) 0);
    dst = concat(tmpdir, "/hisv6-walk-conv-baddst", (char *) 0);
    create_empty(src);
    append_text(src, "not a history line\n");
    cmd = concat(program, " ", src, " ", dst, " >/dev/null 2>&1", (char *) 0);
    status = system(cmd);
    free(cmd);
    ok(WIFEXITED(status) && WEXITSTATUS(status) != 0,
       "hissqlite-convert refuses a file without a valid line");
    db = concat(dst, ".sqlite", (char *) 0);
    ok(file_size(db) < 0, "... and publishes no database");
    free(db);
    free(src);
    free(dst);

    /* An empty history. */
    src = concat(tmpdir, "/hisv6-walk-conv-empty", (char *) 0);
    dst = concat(tmpdir, "/hisv6-walk-conv-emptydst", (char *) 0);
    create_empty(src);
    cmd = concat(program, " ", src, " ", dst, " >/dev/null 2>&1", (char *) 0);
    status = system(cmd);
    free(cmd);
    db = concat(dst, ".sqlite", (char *) 0);
    ok(WIFEXITED(status) && WEXITSTATUS(status) == 0 && file_size(db) > 0,
       "hissqlite-convert converts an empty history");
    free(db);
    free(src);
    free(dst);
    free(err);
    free(conf);
#else
    skip_block(9, "not built with SQLite");
    (void) tmpdir;
#endif
}

int
main(void)
{
    char *tmpdir, *path;
    char *cmd;

    plan(5 + 3 + 2 + 2 + 3 + 8 + 9);

    tmpdir = test_tmpdir();

    path = concat(tmpdir, "/hisv6-walk-empty", (char *) 0);
    test_empty(path);
    free(path);

    path = concat(tmpdir, "/hisv6-walk-bad", (char *) 0);
    test_all_malformed(path);
    free(path);

    path = concat(tmpdir, "/hisv6-walk-last", (char *) 0);
    test_long_last(path, true);
    free(path);

    path = concat(tmpdir, "/hisv6-walk-noeol", (char *) 0);
    test_long_last(path, false);
    free(path);

    path = concat(tmpdir, "/hisv6-walk-tail", (char *) 0);
    test_short_tail(path);
    free(path);

    path = concat(tmpdir, "/hisv6-walk-long", (char *) 0);
    test_long_line(path);
    free(path);

    test_convert(tmpdir);

    cmd = concat("rm -f ", tmpdir, "/hisv6-walk-*", (char *) 0);
    if (system(cmd) != 0)
        diag("cannot remove the test history files");
    free(cmd);
    test_tmpdir_free(tmpdir);
    return 0;
}

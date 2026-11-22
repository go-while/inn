/*
** Tests for the traversal of a hisv6 history file by HISwalk and HISexpire.
**
** An empty history is a valid history: walking or expiring it must succeed.
** A line too long for the read buffer is skipped like any other malformed
** line: the entries after it must still be visited, and must still be in the
** history written by HISexpire.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/history.h"
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
    struct history *h;
    struct counts c;

    h = HISopen(path, "hisv6", HIS_CREAT | HIS_RDWR);
    if (h == NULL)
        bail("cannot create %s", path);
    HISclose(h);

    ok(walk(path, &c), "HISwalk of an empty history succeeds");
    is_int(0, (long) (c.tokens + c.remembered), "... and visits nothing");
    ok(expire(path), "HISexpire of an empty history succeeds");
    ok(walk(path, &c), "... and the history is still a valid empty one");
}

static void
test_long_line(const char *path)
{
    struct history *h;
    struct counts c;
    FILE *f;
    unsigned long i;
    char *msgid;
    bool found;

    h = HISopen(path, "hisv6", HIS_CREAT | HIS_RDWR);
    if (h == NULL)
        bail("cannot create %s", path);
    write_entries(h, "before", N_BEFORE);
    HISclose(h);

    /* A damaged line, longer than the read buffer, in the middle of the
       file. */
    f = fopen(path, "a");
    if (f == NULL)
        sysbail("cannot open %s", path);
    for (i = 0; i < LONG_LINE; i++)
        putc('x', f);
    putc('\n', f);
    if (fclose(f) == EOF)
        sysbail("cannot write %s", path);

    h = HISopen(path, "hisv6", HIS_RDWR);
    if (h == NULL)
        bail("cannot reopen %s", path);
    write_entries(h, "after", N_AFTER);
    msgid = make_msgid("remembered", 0);
    if (!HISremember(h, msgid, (time_t) 2000000, (time_t) 2000000))
        bail("cannot remember %s: %s", msgid, HISerror(h));
    free(msgid);
    HISclose(h);

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

int
main(void)
{
    char *tmpdir, *path;
    char *cmd;

    plan(4 + 8);

    tmpdir = test_tmpdir();

    path = concat(tmpdir, "/hisv6-walk-empty", (char *) 0);
    test_empty(path);
    free(path);

    path = concat(tmpdir, "/hisv6-walk-long", (char *) 0);
    test_long_line(path);
    free(path);

    cmd = concat("rm -f ", tmpdir, "/hisv6-walk-*", (char *) 0);
    if (system(cmd) != 0)
        diag("cannot remove the test history files");
    free(cmd);
    test_tmpdir_free(tmpdir);
    return 0;
}

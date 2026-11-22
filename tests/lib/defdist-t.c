/*  Test suite for DDstart and the LIST exchange of CAlistopen.
**
**  The server replies are read from a temporary file, and the LIST
**  commands sent to the server go to /dev/null.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>

#include "inn/innconf.h"
#include "inn/libinn.h"
#include "tap/basic.h"

static char etcdir[64];
static char tmpdir[64];

/* Number of files left in tmpdir. */
static int
tmpfiles(void)
{
    DIR *dir;
    struct dirent *de;
    int n = 0;

    dir = opendir(tmpdir);
    if (dir == NULL)
        sysbail("can't open %s", tmpdir);
    while ((de = readdir(dir)) != NULL)
        if (strcmp(de->d_name, ".") != 0 && strcmp(de->d_name, "..") != 0)
            n++;
    closedir(dir);
    return n;
}

/* A stream to read the given server replies from. */
static FILE *
server(const char *reply)
{
    FILE *f;

    f = tmpfile();
    if (f == NULL)
        sysbail("can't create a temporary file");
    fputs(reply, f);
    rewind(f);
    return f;
}

/* Run DDstart, DDcheck on local.test and DDend.  With reply NULL, DDstart
   gets no server; otherwise the server sends reply.  Returns the
   Distribution from DDend, and sets *handle to whether DDstart returned a
   handle. */
static char *
ddrun(const char *reply, bool *handle)
{
    FILE *from = NULL, *to = NULL;
    struct _DDHANDLE *h;
    char group[] = "local.test";
    char *p;

    if (reply != NULL) {
        from = server(reply);
        to = fopen("/dev/null", "w");
        if (to == NULL)
            sysbail("can't open /dev/null");
    }
    h = DDstart(from, to);
    *handle = (h != NULL);
    DDcheck(h, group);
    p = DDend(h);
    if (from != NULL)
        fclose(from);
    if (to != NULL)
        fclose(to);
    return p;
}

int
main(void)
{
    FILE *from, *to, *active;
    char buff[BUFSIZ];
    char *p;
    bool handle;

    plan(17);

    strlcpy(etcdir, "defdist-etc-XXXXXX", sizeof(etcdir));
    strlcpy(tmpdir, "defdist-tmp-XXXXXX", sizeof(tmpdir));
    if (mkdtemp(etcdir) == NULL || mkdtemp(tmpdir) == NULL)
        sysbail("can't create temporary directories");
    innconf = xcalloc(1, sizeof(*innconf));
    innconf->pathetc = xstrdup(etcdir);
    innconf->pathtmp = xstrdup(tmpdir);

    /* No local distrib.pats: DDstart asks the server with LIST
       DISTRIB.PATS into a file of its own, not through CAlistopen. */
    p = ddrun("502 Permission denied\r\n", &handle);
    ok(!handle, "rejected LIST DISTRIB.PATS: no handle");
    is_string("", p, "rejected LIST DISTRIB.PATS: empty Distribution");
    is_int(0, tmpfiles(), "rejected LIST DISTRIB.PATS: no file left");
    free(p);

    p = ddrun("", &handle);
    ok(!handle, "no reply to LIST DISTRIB.PATS: no handle");
    is_int(0, tmpfiles(), "no reply to LIST DISTRIB.PATS: no file left");
    free(p);

    p = ddrun("215 Follows\r\n10:local.*:local\r\n", &handle);
    ok(!handle, "truncated LIST DISTRIB.PATS: no handle");
    is_int(0, tmpfiles(), "truncated LIST DISTRIB.PATS: no file left");
    free(p);

    p = ddrun("215 Follows\r\n10:local.*:local\r\n.\r\n", &handle);
    ok(handle, "LIST DISTRIB.PATS: handle");
    is_string("local", p, "LIST DISTRIB.PATS: Distribution");
    is_int(0, tmpfiles(), "LIST DISTRIB.PATS: no file left");
    free(p);

    /* CAlistopen keeps its file until CAclose. */
    from = server("502 Permission denied\r\n");
    to = fopen("/dev/null", "w");
    if (to == NULL)
        sysbail("can't open /dev/null");
    ok(CAlistopen(from, to, NULL) == NULL, "rejected LIST: no file");
    is_int(0, tmpfiles(), "rejected LIST: no file left");
    CAclose();
    fclose(from);

    from = server("215 Follows\r\nlocal.test 0000000002 0000000001 y\r\n"
                  ".\r\n");
    active = CAlistopen(from, to, NULL);
    ok(active != NULL, "LIST: file");
    if (active != NULL && fgets(buff, sizeof(buff), active) != NULL)
        is_string("local.test 0000000002 0000000001 y\n", buff,
                  "LIST: active line");
    else
        ok(false, "LIST: active line");
    is_int(1, tmpfiles(), "LIST: file kept until CAclose");
    CAclose();
    is_int(0, tmpfiles(), "LIST: file removed by CAclose");
    fclose(from);
    fclose(to);

    ok(rmdir(tmpdir) == 0 && rmdir(etcdir) == 0,
       "temporary directories removed");
    free(innconf->pathetc);
    free(innconf->pathtmp);
    free(innconf);
    innconf = NULL;
    return 0;
}

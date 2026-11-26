/*
**  Test suite for compressed overview records in ovdb.
**
**  With compress set in ovdb.conf, ovdb compresses overview records longer
**  than 600 bytes, and its readers refuse a compressed record whose original
**  size is over 100000 bytes.  Every stored record must still be readable,
**  whatever its size.  Needs Berkeley DB and zlib; skipped otherwise.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <fcntl.h>
#include <pwd.h>
#include <sys/stat.h>

#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/messages.h"
#include "inn/overview.h"
#include "inn/storage.h"
#include "tap/basic.h"

#if defined(HAVE_BDB) && defined(HAVE_ZLIB)

/* Directory for the database, ovdb.conf and the ovdb_monitor files. */
#    define OVDIR "ovdb-compress-tmp"

/* The largest original size ovdb reads back from a compressed record. */
#    define MAX_UNZIP_SZ 100000

/* Used as the artificial token for all articles inserted into overview. */
static const TOKEN faketoken = {1, 1, ""};

/* Lengths of the stored overview records, article number, tab and CRLF
   included, indexed by article number minus one. */
static const size_t sizes[] = {
    1000,             /* compressed */
    MAX_UNZIP_SZ + 1, /* just too large to read back compressed */
    MAX_UNZIP_SZ,     /* the largest that can be read back compressed */
    150000,           /* well above the limit */
    1000,             /* compressed, after the large ones */
};
#    define NARTS (sizeof(sizes) / sizeof(sizes[0]))

/* Build a stripped-down innconf struct with the settings the overview API
   and ovdb care about.  ovdb refuses to open the database unless it runs as
   runasuser, so set it to the current user. */
static void
fake_innconf(const char *user)
{
    innconf = xmalloc(sizeof(*innconf));
    memset(innconf, 0, sizeof(*innconf));
    innconf->enableoverview = true;
    innconf->groupbaseexpiry = true;
    innconf->keepmmappedthreshold = 1024;
    innconf->nfsreader = false;
    innconf->overcachesize = 20;
    innconf->ovflushcount = 50;
    innconf->ovgrouppat = NULL;
    innconf->ovmethod = xstrdup("ovdb");
    innconf->pathdb = xstrdup(OVDIR);
    innconf->pathetc = xstrdup(OVDIR);
    innconf->pathoverview = xstrdup(OVDIR);
    innconf->pathrun = xstrdup(OVDIR);
    innconf->runasuser = xstrdup(user);
}

/* Create an empty database directory with an ovdb.conf that turns on
   compression, and the files ovdb checks to see that ovdb_monitor is
   running (see tests/overview/api-t.c for why the PID of the test itself
   is enough). */
static void
setup_ovdir(void)
{
    FILE *f;
    int fd;

    if (system("/bin/rm -rf " OVDIR) < 0)
        sysbail("cannot remove " OVDIR);
    if (mkdir(OVDIR, 0755) < 0)
        sysbail("cannot create " OVDIR);
    f = fopen(OVDIR "/ovdb.conf", "w");
    if (f == NULL)
        sysbail("cannot create " OVDIR "/ovdb.conf");
    fprintf(f, "numdbfiles 2\ncompress true\nreadserver false\n");
    if (fclose(f) == EOF)
        sysbail("cannot write " OVDIR "/ovdb.conf");
    fd = open(OVDIR "/ovdb.sem", O_CREAT | O_WRONLY, 0660);
    if (fd < 0)
        sysbail("cannot create " OVDIR "/ovdb.sem");
    close(fd);
    f = fopen(OVDIR "/ovdb_monitor.pid", "w");
    if (f == NULL)
        sysbail("cannot create " OVDIR "/ovdb_monitor.pid");
    fprintf(f, "%lu\n", (unsigned long) getpid());
    if (fclose(f) == EOF)
        sysbail("cannot write " OVDIR "/ovdb_monitor.pid");
}

/* Return the overview data to pass to overview_add for an article, so that
   the stored record (with the article number, a tab and CRLF added by
   overview_add) is size bytes long.  The caller frees it. */
static char *
make_data(ARTNUM artnum, size_t size)
{
    static const char filler[] = "<0123456789.abcdefghijklmnopqrstuvwxyz@"
                                 "example.test> ";
    char *head, *data;
    size_t len, i, n;

    xasprintf(&head, "%lu\t", artnum);
    len = size - strlen(head) - 2;
    free(head);
    xasprintf(&head,
              "Subject %lu\tposter@example.test\t"
              "Thu, 26 Nov 2026 12:00:00 +0000\t<%lu@example.test>\t",
              artnum, artnum);
    data = xmalloc(len + 1);
    n = strlen(head);
    memcpy(data, head, n);
    free(head);
    for (i = n; i < len; i++)
        data[i] = filler[i % (sizeof(filler) - 1)];
    data[len] = '\0';
    return data;
}

/* Search for a single article and check that the record found is the one
   stored: article number, token, length, data and the trailing CRLF. */
static bool
check_article(struct overview *overview, const char *group, ARTNUM artnum,
              const char *data)
{
    void *search;
    struct overview_data article;
    char *prefix;
    size_t plen, len;
    bool status;

    search = overview_search_open(overview, group, artnum, artnum);
    if (search == NULL)
        return false;
    status = overview_search(overview, search, &article);
    if (status) {
        xasprintf(&prefix, "%lu\t", artnum);
        plen = strlen(prefix);
        len = strlen(data);
        status = (article.number == artnum
                  && memcmp(&article.token, &faketoken, sizeof(faketoken)) == 0
                  && article.overlen == plen + len + 2
                  && memcmp(article.overview, prefix, plen) == 0
                  && memcmp(article.overview + plen, data, len) == 0
                  && memcmp(article.overview + plen + len, "\r\n", 2) == 0);
        if (!status)
            diag("article %lu: number %lu, length %lu", artnum,
                 article.number, (unsigned long) article.overlen);
        free(prefix);
        if (overview_search(overview, search, &article))
            status = false;
    }
    overview_search_close(overview, search);
    return status;
}

int
main(void)
{
    const char *group = "example.test";
    struct passwd *pwd;
    struct overview *overview;
    struct overview_group stats = {0, 0, 0, NF_FLAG_OK};
    struct overview_data article;
    char *data[NARTS];
    void *search;
    ARTNUM n, last;
    size_t i, count;
    bool added;

    pwd = getpwuid(geteuid());
    if (pwd == NULL)
        skip_all("cannot find the name of the current user");
    fake_innconf(pwd->pw_name);
    setup_ovdir();

    plan(3 + NARTS + 2);

    overview = overview_open(OV_READ | OV_WRITE);
    if (overview == NULL)
        bail("opening the overview database failed");
    ok(overview_group_add(overview, group, &stats), "group added");

    added = true;
    for (i = 0; i < NARTS; i++) {
        n = (ARTNUM) i + 1;
        data[i] = make_data(n, sizes[i]);
        article.number = n;
        article.overview = data[i];
        article.overlen = strlen(data[i]);
        article.token = faketoken;
        article.arrived = (time_t) n * 10;
        article.expires = 0;
        if (!overview_add(overview, group, &article))
            added = false;
    }
    ok(added, "%lu records added", (unsigned long) NARTS);
    ok(overview_group(overview, group, &stats) && stats.count == NARTS,
       "group has %lu articles", (unsigned long) NARTS);

    for (i = 0; i < NARTS; i++)
        ok(check_article(overview, group, (ARTNUM) i + 1, data[i]),
           "record %lu of %lu bytes read back", (unsigned long) i + 1,
           (unsigned long) sizes[i]);

    /* A search over the whole range must return every article in order. */
    count = 0;
    last = 0;
    search = overview_search_open(overview, group, 1, (ARTNUM) NARTS);
    if (search != NULL) {
        while (overview_search(overview, search, &article))
            if (article.number == last + 1) {
                last = article.number;
                count++;
            }
        overview_search_close(overview, search);
    }
    ok(search != NULL, "search over all articles opened");
    is_int((long) NARTS, (long) count, "search returns all articles");

    overview_close(overview);
    for (i = 0; i < NARTS; i++)
        free(data[i]);
    if (system("/bin/rm -rf " OVDIR) < 0)
        sysdiag("cannot remove " OVDIR);
    return 0;
}

#else /* !(HAVE_BDB && HAVE_ZLIB) */

int
main(void)
{
    skip_all("ovdb compression not available (needs Berkeley DB and zlib)");
    return 0;
}

#endif

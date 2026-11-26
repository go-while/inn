/* Writer half of the ovsqlite direct reader integration test.
 *
 * Opens overview in OV_READ|OV_WRITE mode (connects to ovsqlite-server),
 * creates groups and inserts articles via the standard OV API, then closes.
 * The server creates the real database schema and handles compression.
 *
 * Written by Kevin Bowling in 2026.
 */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <errno.h>
#include <sys/stat.h>

#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/messages.h"
#include "inn/ov.h"
#include "inn/storage.h"
#include "tap/basic.h"

#include "../storage/ovsqlite/ovsqlite.h"

/* The same fake token used by overview-t.c. */
static const TOKEN faketoken = {1, 1, ""};

/* Parse a line from the overview data file.  Nul-terminates the group name
 * at the beginning of the buffer and returns a pointer to the start of the
 * overview data (the artnum field).  Sets *artnum via the passed pointer.
 * This is the same parsing logic used by overview-t.c. */
static char *
overview_data_parse(char *data, unsigned long *artnum)
{
    char *start;

    if (data[strlen(data) - 1] != '\n')
        die("Line too long in input data");

    start = strchr(data, ':');
    if (start == NULL)
        die("No colon found in input data");
    *start = '\0';
    start++;
    *artnum = strtoul(start, NULL, 10);
    if (*artnum == 0)
        die("Cannot parse article number in input data");
    return start;
}

/* Check both the active-file cutoff used by makehistory -I and the normal
 * first-article low water mark when cutoff is disabled. */
static bool
group_stats(char *group, ARTNUM wantlow, ARTNUM wanthigh, int wantcount)
{
    ARTNUM low, high;
    int count, flag;

    return OVgroupstats(group, &low, &high, &count, &flag) && low == wantlow
           && high == wanthigh && count == wantcount;
}

static bool
add_article(const char *group, ARTNUM artnum)
{
    char data[] = "overview\r\n";

    return ovsqlite_add(group, artnum, faketoken, data, strlen(data), 0, 0);
}

static int
cutoff_test(void)
{
    char flag[] = NF_FLAG_OK_STRING;
    char group[] = "cutoff.test";
    char uncut[] = "cutoff.disabled";
    char closed[] = "cutoff.closed";
    bool cutoff = true;
    TOKEN token;

    plan(45);
    ok(OVopen(OV_READ | OV_WRITE), "open SQLite overview for writes");
    ok(OVgroupadd(group, 10, 100, flag), "add group cutoff.test");
    ok(OVctl(OVCUTOFFLOW, &cutoff), "enable the low article cutoff");
    ok(!add_article(group, 9), "refuse cutoff.test 9 below the cutoff");
    ok(add_article(group, 30), "add cutoff.test 30");
    ok(group_stats(group, 10, 30, 1),
       "cutoff.test has low 10, high 30, count 1");
    ok(add_article(group, 20), "add cutoff.test 20 below the high mark");
    ok(add_article(group, 10), "add cutoff.test 10 at the cutoff");
    ok(!add_article(group, 9), "refuse cutoff.test 9 below the cutoff");
    ok(!OVgetartinfo(group, 9, &token), "cutoff.test 9 is not stored");
    ok(OVgetartinfo(group, 20, &token), "cutoff.test 20 is stored");
    ok(group_stats(group, 10, 30, 3),
       "cutoff.test has low 10, high 30, count 3");
    ok(!add_article(group, 20), "refuse cutoff.test 20 a second time");
    ok(group_stats(group, 10, 30, 3),
       "cutoff.test still has low 10, high 30, count 3");

    /* Turning cutoff off permits older articles again. */
    cutoff = false;
    ok(OVctl(OVCUTOFFLOW, &cutoff), "disable the low article cutoff");
    ok(add_article(group, 9), "add cutoff.test 9 without the cutoff");
    ok(group_stats(group, 9, 30, 4),
       "cutoff.test has low 9, high 30, count 4");
    cutoff = true;
    ok(OVctl(OVCUTOFFLOW, &cutoff), "enable the low article cutoff again");
    ok(!add_article(group, 8), "refuse cutoff.test 8 below the cutoff");

    /* Emptying a group advances its low mark.  The first subsequent add
     * must preserve that cutoff too, not replace it with its own number. */
    ok(ovsqlite_cancel(group, 9) && ovsqlite_cancel(group, 10)
           && ovsqlite_cancel(group, 20) && ovsqlite_cancel(group, 30),
       "cancel cutoff.test 9, 10, 20 and 30");
    ok(group_stats(group, 30, 29, 0),
       "emptied cutoff.test has low 30, high 29, count 0");
    ok(add_article(group, 50), "add cutoff.test 50 to the empty group");
    ok(add_article(group, 40), "add cutoff.test 40");
    ok(!add_article(group, 29), "refuse cutoff.test 29 below the kept cutoff");
    ok(group_stats(group, 30, 50, 2),
       "cutoff.test has low 30, high 50, count 2");

    /* Turning cutoff off sets the low mark kept as the cutoff to the lowest
     * article, as ovsqlite-util -A expects. */
    cutoff = false;
    ok(OVctl(OVCUTOFFLOW, &cutoff), "disable the low article cutoff");
    ok(group_stats(group, 40, 50, 2),
       "cutoff.test low mark moves to 40, high 50, count 2");

    /* Without cutoff, the first article still sets the actual low mark. */
    ok(OVgroupadd(uncut, 10, 100, flag), "add group cutoff.disabled");
    ok(add_article(uncut, 30), "add cutoff.disabled 30");
    ok(group_stats(uncut, 30, 30, 1),
       "cutoff.disabled has low 30, high 30, count 1");
    ok(add_article(uncut, 20), "add cutoff.disabled 20");
    ok(group_stats(uncut, 20, 30, 2),
       "cutoff.disabled has low 20, high 30, count 2");
    ok(add_article(uncut, 9), "add cutoff.disabled 9");
    ok(group_stats(uncut, 9, 30, 3),
       "cutoff.disabled has low 9, high 30, count 3");

    /* A disconnect keeps the low mark kept as the cutoff, since
     * makehistory -F adds each batch of articles over a new connection. */
    cutoff = true;
    ok(OVctl(OVCUTOFFLOW, &cutoff), "enable the low article cutoff");
    ok(OVgroupadd(closed, 10, 100, flag), "add group cutoff.closed");
    ok(add_article(closed, 30), "add cutoff.closed 30");
    ok(add_article(closed, 20), "add cutoff.closed 20");
    ok(group_stats(closed, 10, 30, 2),
       "cutoff.closed has low 10, high 30, count 2");
    OVclose();
    ok(OVopen(OV_READ | OV_WRITE), "reopen SQLite overview for writes");
    ok(group_stats(closed, 10, 30, 2),
       "cutoff.closed keeps low 10, high 30, count 2 after reconnecting");

    /* Cutoff is per connection, and defaults to disabled on reconnect. */
    ok(add_article(uncut, 8), "add cutoff.disabled 8 on the new connection");
    ok(group_stats(uncut, 8, 30, 4),
       "cutoff.disabled has low 8, high 30, count 4");

    /* Disabling cutoff on a new connection, as makehistory -F does at the
     * end, also sets the low mark to the lowest article. */
    cutoff = false;
    ok(OVctl(OVCUTOFFLOW, &cutoff),
       "disable the low article cutoff on the new connection");
    ok(group_stats(closed, 20, 30, 2),
       "cutoff.closed low mark moves to 20, high 30, count 2");
    OVclose();
    return 0;
}

int
main(int argc, char *argv[])
{
    FILE *f;
    char buffer[4096];
    char *start;
    unsigned long artnum;
    char flag[] = NF_FLAG_OK_STRING;
    /* Track which groups we've already added. */
    char *seen_groups[64];
    int ngroups = 0;
    int narticles = 0;
    int i;
    bool seen;

    if (argc == 2 && strcmp(argv[1], "--cutoff") == 0)
        return cutoff_test();

    plan(3);

    if (access("../data/overview/basic", F_OK) == 0) {
        if (chdir("../data") < 0)
            sysbail("cannot chdir to ../data");
    } else if (access("data/overview/basic", F_OK) == 0) {
        if (chdir("data") < 0)
            sysbail("cannot chdir to data");
    } else if (access("tests/data/overview/basic", F_OK) == 0) {
        if (chdir("tests/data") < 0)
            sysbail("cannot chdir to tests/data");
    }

    /* Test 1: open overview in read/write mode (connects to server). */
    ok(OVopen(OV_READ | OV_WRITE), "open SQLite overview for writes");

    /* Load test data from the same file used by overview-t.c. */
    f = fopen("overview/basic", "r");
    if (f == NULL)
        sysdie("Cannot open overview/basic");

    while (fgets(buffer, sizeof(buffer), f) != NULL) {
        start = overview_data_parse(buffer, &artnum);

        /* Add group if we haven't seen it yet. */
        seen = false;
        for (i = 0; i < ngroups; i++) {
            if (strcmp(seen_groups[i], buffer) == 0) {
                seen = true;
                break;
            }
        }
        if (!seen) {
            if (ngroups >= 64)
                die("Too many groups in test data");
            seen_groups[ngroups] = xstrdup(buffer);
            ngroups++;
            if (!OVgroupadd(buffer, 0, 0, flag))
                die("Cannot add group %s", buffer);
        }

        /* OVadd expects the overview data *without* the "artnum\t" prefix
         * and without trailing \r\n -- it adds those itself.  But it does
         * need the Xref field present so it can determine group:artnum.
         *
         * In the data file, the format after overview_data_parse is:
         *   "artnum\tSubject...\tXref: host group:artnum\n"
         *
         * We need to pass the data starting from the first tab character
         * (skipping the artnum), without the trailing \n.  OVadd will
         * prepend "artnum\t" and append "\r\n" before storing. */
        {
            char *tab = strchr(start, '\t');
            int len;

            if (tab == NULL)
                die("No tab after artnum in data for %s:%lu", buffer, artnum);
            tab++; /* point past the tab to the actual overview fields */
            len = strlen(tab);
            /* Strip trailing \n. */
            if (len > 0 && tab[len - 1] == '\n')
                len--;

            if (OVadd(faketoken, tab, len, artnum * 10,
                      (artnum % 5 == 0) ? artnum * 100 : artnum)
                != OVADDCOMPLETED)
                die("Cannot add %s:%lu", buffer, artnum);
        }
        narticles++;
    }
    fclose(f);

    /* Test 2: all articles were loaded. */
    ok(narticles > 0, "write at least one fixture article");

    /* Test 3: close succeeds. */
    OVclose();
    ok(true, "close SQLite overview after writing");

    for (i = 0; i < ngroups; i++)
        free(seen_groups[i]);

    return 0;
}

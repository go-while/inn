/* Writer half of the ovsqlite direct reader integration test.
 *
 * Opens overview in OV_READ|OV_WRITE mode (connects to ovsqlite-server),
 * creates groups and inserts articles via the standard OV API, then closes.
 * The server creates the real database schema and handles compression.
 *
 * Written by Kevin Bowling in 2026.
 */

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
group_stats(char *group, int wantlow, int wanthigh, int wantcount)
{
    int low, high, count, flag;

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
    bool cutoff = true;
    TOKEN token;

    test_init(36);
    ok(1, OVopen(OV_READ | OV_WRITE));
    ok(2, OVgroupadd(group, 10, 100, flag));
    ok(3, OVctl(OVCUTOFFLOW, &cutoff));
    ok(4, !add_article(group, 9));
    ok(5, add_article(group, 30));
    ok(6, group_stats(group, 10, 30, 1));
    ok(7, add_article(group, 20));
    ok(8, add_article(group, 10));
    ok(9, !add_article(group, 9));
    ok(10, !OVgetartinfo(group, 9, &token));
    ok(11, OVgetartinfo(group, 20, &token));
    ok(12, group_stats(group, 10, 30, 3));
    ok(13, !add_article(group, 20));
    ok(14, group_stats(group, 10, 30, 3));

    /* Turning cutoff off permits older articles again. */
    cutoff = false;
    ok(15, OVctl(OVCUTOFFLOW, &cutoff));
    ok(16, add_article(group, 9));
    ok(17, group_stats(group, 9, 30, 4));
    cutoff = true;
    ok(18, OVctl(OVCUTOFFLOW, &cutoff));
    ok(19, !add_article(group, 8));

    /* Emptying a group advances its low mark.  The first subsequent add
     * must preserve that cutoff too, not replace it with its own number. */
    ok(20, ovsqlite_cancel(group, 9) && ovsqlite_cancel(group, 10)
               && ovsqlite_cancel(group, 20) && ovsqlite_cancel(group, 30));
    ok(21, group_stats(group, 30, 29, 0));
    ok(22, add_article(group, 50));
    ok(23, add_article(group, 40));
    ok(24, !add_article(group, 29));
    ok(25, group_stats(group, 30, 50, 2));

    /* Without cutoff, the first article still sets the actual low mark. */
    cutoff = false;
    ok(26, OVctl(OVCUTOFFLOW, &cutoff));
    ok(27, OVgroupadd(uncut, 10, 100, flag));
    ok(28, add_article(uncut, 30));
    ok(29, group_stats(uncut, 30, 30, 1));
    ok(30, add_article(uncut, 20));
    ok(31, group_stats(uncut, 20, 30, 2));
    ok(32, add_article(uncut, 9));
    ok(33, group_stats(uncut, 9, 30, 3));

    /* Cutoff is per connection, and defaults to disabled on reconnect. */
    cutoff = true;
    OVctl(OVCUTOFFLOW, &cutoff);
    OVclose();
    ok(34, OVopen(OV_READ | OV_WRITE));
    ok(35, add_article(uncut, 8));
    ok(36, group_stats(uncut, 8, 30, 4));
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

    test_init(3);

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
    ok(1, OVopen(OV_READ | OV_WRITE));

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
    ok(2, narticles > 0);

    /* Test 3: close succeeds. */
    OVclose();
    ok(3, true);

    for (i = 0; i < ngroups; i++)
        free(seen_groups[i]);

    return 0;
}

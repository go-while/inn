/* Test that dbzinit rejects invalid table sizes in the .dir file. */

#include "portable/system.h"

#include <limits.h>

#include "inn/dbz.h"
#include "inn/messages.h"
#include "tap/basic.h"

#define NAME    "dbz-test.history"
#define DIRFILE NAME ".dir"

static char orig[4096];

/* Rewrite the .dir file with the table size replaced by tsize. */
static bool
write_dir(const char *tsize)
{
    const char *rest;
    FILE *f;

    rest = strchr(orig + strlen("dbz 6 "), ' ');
    if (rest == NULL)
        return false;
    f = fopen(DIRFILE, "w");
    if (f == NULL)
        return false;
    fprintf(f, "dbz 6 %s%s", tsize, rest);
    return fclose(f) == 0;
}

/* Remove the test database files (RemoveDBZ is not in libinn). */
static void
remove_db(void)
{
    unlink(NAME ".dir");
    unlink(NAME ".index");
    unlink(NAME ".hash");
    unlink(NAME ".pag");
}

/* Try to open the database with tsize; true if dbzinit accepted it. */
static bool
open_with(const char *tsize)
{
    if (!write_dir(tsize))
        sysbail("cannot rewrite %s", DIRFILE);
    if (!dbzinit(NAME))
        return false;
    dbzclose();
    return true;
}

int
main(void)
{
    char big[64];
    char tsize[64] = "";
    size_t n;
    FILE *f;
    dbzoptions opt;

    test_init(7);
    message_handlers_warn(0);

    /* Both tables from disk, as hisv6 does when opened without HIS_MMAP.  With
       the default mmap of the exists table, mmap itself fails for some of
       these sizes. */
    dbzgetoptions(&opt);
    opt.pag_incore = INCORE_NO;
    opt.exists_incore = INCORE_NO;
    dbzsetoptions(opt);

    remove_db();
    ok(1, dbzfresh(NAME, 0));
    ok(2, dbzclose());

    f = fopen(DIRFILE, "r");
    if (f == NULL)
        sysbail("cannot open %s", DIRFILE);
    n = fread(orig, 1, sizeof(orig) - 1, f);
    fclose(f);
    orig[n] = '\0';
    ok(3, strncmp(orig, "dbz 6 ", 6) == 0
              && sscanf(orig + 6, "%63s", tsize) == 1);

    ok(4, !open_with("0"));
    ok(5, !open_with("-3"));
    snprintf(big, sizeof(big), "%ld", LONG_MAX);
    ok(6, !open_with(big));
    ok(7, open_with(tsize));

    remove_db();
    return 0;
}

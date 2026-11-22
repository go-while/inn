/*
**  Test suite for tradindexed group index size validation.
**
**  Written by Kevin Bowling in 2026.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <errno.h>
#include <limits.h>
#include <sys/stat.h>

#include "inn/innconf.h"

#include "tap/basic.h"

#include "../../storage/tradindexed/tdx-private.h"
#include "../../storage/tradindexed/tdx-structure.h"

/* Expiry should retain the backing files and low mark of an empty group. */
static void
expire_empty(void)
{
    struct innconf config = {0};
    struct group_index *index;
    struct stat idx, dat, after;
    struct group_entry *entry;
    ARTNUM low = 0;
    char *tmp = test_tmpdir();

    if (chdir(tmp) < 0 || mkdir("expire-empty", 0700) < 0
        || chdir("expire-empty") < 0)
        sysbail("cannot create temporary overview directory");
    config.pathoverview = ".";
    config.tradindexedmmap = true;
    innconf = &config;
    index = tdx_index_open(true);
    if (index == NULL || !tdx_index_add(index, "empty", 42, 41, "y"))
        bail("cannot create empty group");
    tdx_index_close(index);
    if (stat("e/empty.IDX", &idx) < 0 || stat("e/empty.DAT", &dat) < 0)
        sysbail("cannot stat original group index");
    ok(tdx_expire("empty", &low, NULL), "expire previously empty group");
    is_int(42, low, "keep low mark of empty group");
    if (stat("e/empty.IDX", &after) < 0)
        sysbail("cannot stat expired group index");
    ok(idx.st_ino == after.st_ino, "retain empty group index inode");
    idx = after;
    if (stat("e/empty.DAT", &after) < 0)
        sysbail("cannot stat expired group data");
    ok(dat.st_ino == after.st_ino, "retain empty group data inode");
    dat = after;

    /* Group creation normalizes zero to one.  Model an older unnormalized
       entry in the on-disk group index. */
    index = tdx_index_open(true);
    if (index == NULL || (entry = tdx_index_entry(index, "empty")) == NULL)
        bail("cannot reopen empty group");
    entry->low = entry->high = 0;
    tdx_index_close(index);
    ok(tdx_expire("empty", &low, NULL), "expire noncanonical empty group");
    is_int(1, low, "normalize zero low mark to one");
    index = tdx_index_open(false);
    if (index == NULL || (entry = tdx_index_entry(index, "empty")) == NULL)
        bail("cannot inspect normalized group");
    is_int(1, entry->low, "normalized low mark persisted");
    is_int(0, entry->high, "normalized high mark persisted");
    tdx_index_close(index);
    if (stat("e/empty.IDX", &after) < 0)
        sysbail("cannot stat rebuilt group index");
    ok(idx.st_ino != after.st_ino, "noncanonical group index replaced");
    if (stat("e/empty.DAT", &after) < 0)
        sysbail("cannot stat rebuilt group data");
    ok(dat.st_ino != after.st_ino, "noncanonical group data replaced");
    unlink("e/empty.IDX");
    unlink("e/empty.DAT");
    rmdir("e");
    unlink("group.index");
    if (chdir("..") < 0 || rmdir("expire-empty") < 0)
        sysbail("cannot remove temporary overview directory");
    innconf = NULL;
    test_tmpdir_free(tmp);
}

int
main(void)
{
    uintmax_t bytes;
    off_t size;
    bool valid;
    int count, oerrno;

    bytes = sizeof(struct group_header)
            + ((uintmax_t) INT_MAX + 1) * sizeof(struct group_entry);
    size = (off_t) bytes;
    if (size < 0 || (uintmax_t) size != bytes)
        skip_all("off_t cannot represent the oversized test index");

    plan(14);
    ok(tdx_index_entry_count(sizeof(struct group_header), &count),
       "accept an empty index");
    is_int(0, count, "empty index has no entries");
    errno = 0;
    valid = tdx_index_entry_count(size, &count);
    oerrno = errno;
    ok(!valid, "reject an index with more than INT_MAX entries");
    is_int(EOVERFLOW, oerrno, "oversized entry count sets errno");
    expire_empty();
    return 0;
}

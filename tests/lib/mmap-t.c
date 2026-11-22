/* Test synchronization of an unaligned range spanning two mapped pages.
 *
 * With a unified page cache, pread sees the written bytes whether or not
 * msync worked, so the read-back below cannot detect a missing or broken
 * synchronization.  What the test does detect is a failing msync call:
 * EINVAL if the range handed to msync were not rounded to page boundaries,
 * or any other error on a bad mapping. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"
#include "portable/mmap.h"

#include "inn/concat.h"
#include "inn/mmap.h"
#include "tap/basic.h"

int
main(void)
{
    static const char input[] = "INN";
    char stored[sizeof(input) - 1];
    char *dir, *path, *mapping;
    size_t length;
    off_t offset;
    int fd, pagesize, result, unmapped;

    pagesize = getpagesize();
    if (pagesize < 2)
        bail("cannot determine page size");
    length = (size_t) pagesize * 2;
    offset = pagesize - 1;

    dir = test_tmpdir();
    path = concatpath(dir, "mmap-XXXXXX");
    fd = mkstemp(path);
    if (fd < 0)
        sysbail("cannot create temporary mapping file");
    if (unlink(path) < 0)
        sysbail("cannot unlink temporary mapping file");
    free(path);
    test_tmpdir_free(dir);

    if (ftruncate(fd, (off_t) length) < 0)
        sysbail("cannot size temporary mapping file");
    mapping = mmap(NULL, length, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
    if (mapping == MAP_FAILED)
        sysbail("cannot map temporary file");

    plan(5);
    memcpy(mapping + offset, input, sizeof(stored));
    is_int(0, inn_msync_page(mapping + offset, sizeof(stored), MS_SYNC),
           "sync a range starting mid-page and spanning two pages");
    if (is_int(sizeof(stored), pread(fd, stored, sizeof(stored), offset),
               "read the changed range from the backing file"))
        is_blob(input, stored, sizeof(stored), "file contains the mapped bytes");
    else
        skip("backing file read failed");
    result = munmap(mapping, length);
    unmapped = inn_msync_page(mapping + offset, sizeof(stored), MS_SYNC);
    is_int(0, result, "unmap temporary file");
    is_int(-1, unmapped, "sync of an unmapped range fails");
    close(fd);
    return 0;
}

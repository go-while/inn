/*  Test suite for reserved stdio file descriptors.
**
**  Written by Kevin Bowling in 2026.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/libinn.h"
#include "tap/basic.h"

int
main(void)
{
    FILE *file;

    plan(11);

    ok(fdreserve(4), "reserve four descriptors");
    ok(fdreserve(2), "shrink the reserve to two");
    ok(fdreserve(4), "grow the reserve back to four");

    file = Fopen("/dev/null", "r", 3);
    ok(file != NULL, "open the last reserved slot");
    is_int(0, Fclose(file), "close the last reserved slot");

    /* The upper bound is exclusive and must fall back to ordinary fopen. */
    file = Fopen("/dev/null", "r", 4);
    ok(file != NULL, "fall back to fopen at the exclusive bound");
    is_int(0, Fclose(file), "close the ordinary stream");

    /* A failed freopen closes its stream; the reserved slot must be rebuilt
       with fopen before it can be reused. */
    file = Fopen("/this/path/does/not/exist", "r", 0);
    ok(file == NULL, "missing file closes the reserved stream");
    file = Fopen("/dev/null", "r", 0);
    ok(file != NULL, "reuse the reserved slot after a failed open");
    is_int(0, Fclose(file), "close the reused stream");

    ok(fdreserve(0), "release the descriptor reserve");
    return 0;
}

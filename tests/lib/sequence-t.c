/* Test sequence-space comparisons. */

#include "portable/system.h"

#include "inn/sequence.h"
#include "tap/basic.h"

int
main(void)
{
    const unsigned long halfway = ULONG_MAX / 2 + 1;

    test_init(8);
    ok_int(1, 0, seq_lcompare(42, 42));
    ok_int(2, -1, seq_lcompare(4, 9));
    ok_int(3, 1, seq_lcompare(9, 4));
    ok_int(4, -1, seq_lcompare(ULONG_MAX, 0));
    ok_int(5, 1, seq_lcompare(0, ULONG_MAX));
    ok_int(6, -1, seq_lcompare(0, halfway - 1));
    ok_int(7, INT_MAX, seq_lcompare(0, halfway));
    ok_int(8, INT_MAX, seq_lcompare(halfway, 0));

    return 0;
}

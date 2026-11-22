/* hash test suite. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/libinn.h"
#include "tap/basic.h"

int
main(void)
{
    HASH h1, h2;
    const char *text;

    plan(17);

    h1 = HashMessageID("<lhs@test.invalid>");
    h2 = HashMessageID("<lhs@TEST.invalid>");
    ok(HashCompare(&h1, &h2) == 0,
       "domain comparison ignores uppercase letters");
    h2 = HashMessageID("<lhs@test.INVALID>");
    ok(HashCompare(&h1, &h2) == 0, "domain suffix is case insensitive");
    h2 = HashMessageID("<Lhs@test.invalid>");
    ok(HashCompare(&h1, &h2) != 0,
       "local part distinguishes initial uppercase");
    h2 = HashMessageID("<lhS@test.invalid>");
    ok(HashCompare(&h1, &h2) != 0,
       "local part distinguishes final uppercase");
    h1 = HashMessageID("<test.invalid>");
    h2 = HashMessageID("<TEST.invalid>");
    ok(HashCompare(&h1, &h2) != 0,
       "hash without at sign distinguishes initial uppercase");
    h2 = HashMessageID("<test.INVALID>");
    ok(HashCompare(&h1, &h2) != 0,
       "hash without at sign distinguishes suffix uppercase");
    h1 = HashMessageID("<postmaster@test.invalid>");
    h2 = HashMessageID("<POSTMASTER@test.invalid>");
    ok(HashCompare(&h1, &h2) == 0, "postmaster matches when uppercase");
    h2 = HashMessageID("<PostMaster@test.invalid>");
    ok(HashCompare(&h1, &h2) == 0, "postmaster matches in mixed case");
    h2 = HashMessageID("<postmasteR@test.invalid>");
    ok(HashCompare(&h1, &h2) == 0,
       "postmaster matches with a final uppercase letter");
    h2 = HashMessageID("<postmaster@TEST.invalid>");
    ok(HashCompare(&h1, &h2) == 0, "postmaster domain is case insensitive");
    h2 = HashMessageID("<postmaster@test.INVALID>");
    ok(HashCompare(&h1, &h2) == 0,
       "postmaster domain suffix is case insensitive");
    h1 = HashMessageID("<postmaster.test.invalid>");
    h2 = HashMessageID("<POSTMASTER.test.invalid>");
    ok(HashCompare(&h1, &h2) != 0,
       "postmaster exception requires an at sign");

    h1 = HashMessageID("<87is2w1u6i.fsf@windlord.stanford.edu>");
    text = HashToText(h1);
    is_string("A0D432DC9718979BEFB4ACADA4BAD863", text,
              "hash has the expected text encoding");
    h2 = TextToHash("A0D432DC9718979BEFB4ACADA4BAD863");
    ok(HashCompare(&h1, &h2) == 0, "text encoding round trips");

    /* The last bytes of a hash are compared first, like dbz does. */
    h1 = TextToHash("FF000000000000000000000000000001");
    h2 = TextToHash("00000000000000000000000000000002");
    ok(HashCompare(&h1, &h2) < 0, "last byte determines forward order");
    ok(HashCompare(&h2, &h1) > 0, "last byte determines reverse order");
    h2 = TextToHash("00000000000000000000000000000001");
    ok(HashCompare(&h1, &h2) > 0, "earlier byte breaks a tie");

    return 0;
}

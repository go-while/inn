/*  hex test suite.
**
**  Written by Russ Allbery in 2005.
**
**  Various bug fixes, code and documentation improvements since then
**  in 2014, 2021, 2026.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/utility.h"
#include "tap/basic.h"

int
main(void)
{
    static const unsigned char d1[] = {'\0', '\377', '\377', '\0'};
    static const unsigned char d2[] = {'\1', '\2', '\276', '\277'};
    static const unsigned char d3[] = {'\17', '\160', '\0', '\0'};
    static const char t1[] = "00FFFF00";
    static const char t2[] = "0102BEBF";
    static const char t3[] = "0F7!!!";
    static const char t4[] = "0102bebf";
    unsigned char dout[4];
    char tout[9];

    plan(17);

    inn_encode_hex(d1, sizeof(d1), tout, sizeof(tout));
    is_string(t1, tout, "encode zero and high bytes");
    inn_encode_hex(d2, sizeof(d2), tout, sizeof(tout));
    is_string(t2, tout, "encode four bytes as uppercase hex");

    inn_encode_hex(d1, 1, tout, sizeof(tout));
    is_string("00", tout, "encode only the requested input byte");
    inn_encode_hex(d2, 0, tout, sizeof(tout));
    is_string("", tout, "empty input encodes as empty");
    inn_encode_hex(d2, sizeof(d2), tout, 2);
    is_string("0", tout, "two output bytes hold one digit");
    inn_encode_hex(d2, sizeof(d2), tout, 3);
    is_string("01", tout, "three output bytes hold two digits");
    inn_encode_hex(d2, sizeof(d2), tout, 6);
    is_string("0102B", tout, "short output retains the first five digits");
    inn_encode_hex(d2, sizeof(d2), tout, 0);
    is_string("0102B", tout, "zero output size leaves the buffer alone");
    inn_encode_hex(d2, sizeof(d2), tout, 1);
    is_string("", tout, "one output byte holds the terminator");
    inn_encode_hex(d2, SIZE_MAX / 2 + 1, tout, 3);
    is_string("01", tout, "large input length does not overflow the limit");

    inn_decode_hex(t1, dout, sizeof(dout));
    ok(memcmp(d1, dout, sizeof(dout)) == 0, "decode zero and high bytes");
    inn_decode_hex(t2, dout, sizeof(dout));
    ok(memcmp(d2, dout, sizeof(dout)) == 0, "decode uppercase hex");
    inn_decode_hex(t3, dout, sizeof(dout));
    ok(memcmp(d3, dout, sizeof(dout)) == 0,
       "decode stops at an invalid digit");
    inn_decode_hex(t4, dout, sizeof(dout));
    ok(memcmp(d2, dout, sizeof(dout)) == 0, "decode lowercase hex");

    inn_decode_hex(t2, dout, sizeof(dout));
    inn_decode_hex(t1, dout, 1);
    ok(memcmp("", dout, 1) == 0, "one-byte output is set");
    ok(memcmp("\2\276\277", dout + 1, 3) == 0,
       "bytes past the output limit stay unchanged");
    inn_decode_hex(t2, dout, sizeof(dout));
    inn_decode_hex(t1, dout, 0);
    ok(memcmp(d2, dout, sizeof(dout)) == 0,
       "zero output size leaves the decoded buffer alone");

    return 0;
}

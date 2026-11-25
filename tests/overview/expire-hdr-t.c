/*
 * Overview records handed to OVgroupbasedexpire are not nul-terminated; the
 * backends pass a pointer and a length.  Records that lack a field must be
 * dropped without reading past the given length.
 */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/storage.h"
#include "inn/vector.h"
#include "inn/xmalloc.h"
#include "tap/basic.h"

#include "../../storage/ovinterface.h"

/* Copy a record into a buffer of exactly its length, without a nul. */
static char *
record(const char *text, int *len)
{
    char *copy;

    *len = (int) strlen(text);
    copy = xmalloc(*len);
    memcpy(copy, text, *len);
    return copy;
}

int
main(void)
{
    TOKEN token;
    char *data;
    int len;

    plan(2);

    innconf = xcalloc(1, sizeof(*innconf));
    innconf->extraoverviewadvertised = vector_new();
    innconf->extraoverviewhidden = vector_new();
    memset(&token, 0, sizeof(token));
    token.type = TOKEN_EMPTY;

    /* Expire on the Date field; the record ends after the Subject field. */
    OVusepost = true;
    data = record("1\tSubject only\r\n", &len);
    ok(OVgroupbasedexpire(token, "example.test", data, len, 0, 0),
       "record without a Date field is dropped");
    free(data);

    /* Arrival time instead of Date; the record has no Xref field. */
    OVusepost = false;
    data = record("2\tSubject\tauthor@example.com\t"
                  "Sat, 06 Mar 2004 21:39:44 -0800\t<x@example.com>\t\t"
                  "100\t2\r\n",
                  &len);
    ok(OVgroupbasedexpire(token, "example.test", data, len, 0, 0),
       "record without an Xref field is dropped");
    free(data);

    return 0;
}

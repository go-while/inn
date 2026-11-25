/*  Test suite for the Message-ID cache of nnrpd.
**
**  The cache must hold up to msgidcachesize entries, and adding a
**  Message-ID already in the cache must not throw away another entry.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/storage.h"
#include "tap/basic.h"

#include "../../nnrpd/cache.h"

/* A distinct storage token for each test article. */
static TOKEN
token(int n)
{
    TOKEN t;

    memset(&t, 0, sizeof(t));
    t.type = 1;
    t.class = (STORAGECLASS) n;
    t.token[0] = (char) n;
    return t;
}

static HASH
hash(int n)
{
    char id[64];

    snprintf(id, sizeof(id), "<cache-%d@example.test>", n);
    return HashMessageID(id);
}

static void
add(int n)
{
    cache_add(hash(n), token(n));
}

/* Whether article n is in the cache with its own token.  cache_get()
   answers a repeated lookup of its last hit from a copy, so the tests never
   ask twice in a row for the same article. */
static bool
cached(int n)
{
    TOKEN t, want;

    t = cache_get(hash(n), false);
    if (t.type == TOKEN_EMPTY)
        return false;
    want = token(n);
    return memcmp(&t, &want, sizeof(t)) == 0;
}

int
main(void)
{
    int i, round;

    plan(13);

    innconf = xcalloc(1, sizeof(*innconf));

    /* A cache of one entry keeps its entry. */
    innconf->msgidcachesize = 1;
    add(1);
    ok(cached(1), "size 1: the only entry is kept");
    add(2);
    ok(cached(2), "size 1: a new entry replaces it");
    ok(!cached(1), "size 1: the old entry is gone");

    /* Grow to three entries: 2, 3 and 4. */
    innconf->msgidcachesize = 3;
    add(3);
    add(4);
    ok(cached(3), "size 3: entry 3 kept");
    ok(cached(2), "size 3: entry 2 kept");
    ok(cached(4), "size 3: entry 4 kept");

    /* Adding entries already in the cache, as a client reading the same
       overview again does, must not drain it. */
    for (round = 0; round < 10; round++)
        for (i = 2; i <= 4; i++)
            add(i);
    ok(cached(2), "duplicates: entry 2 kept");
    ok(cached(3), "duplicates: entry 3 kept");
    ok(cached(4), "duplicates: entry 4 kept");

    /* A new entry throws away the oldest one only. */
    add(5);
    ok(!cached(2), "new entry: the oldest entry 2 is gone");
    ok(cached(3), "new entry: entry 3 kept");
    ok(cached(4), "new entry: entry 4 kept");
    ok(cached(5), "new entry: entry 5 added");

    free(innconf);
    innconf = NULL;
    return 0;
}

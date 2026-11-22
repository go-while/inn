/* Test suite for lib/hashtab.c. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <sys/stat.h>

#include "inn/hashtab.h"
#include "inn/libinn.h"
#include "inn/messages.h"
#include "tap/basic.h"

struct wordref {
    const char *word;
    int count;
};

static const void *
string_key(const void *entry)
{
    return entry;
}

static bool
string_equal(const void *key, const void *entry)
{
    const char *p, *q;

    p = key;
    q = entry;
    return !strcmp(p, q);
}

static void
string_delete(void *entry)
{
    free(entry);
}

static void
string_traverse(void *entry, void *data)
{
    int i;
    struct wordref *wordrefs = data;

    for (i = 0; wordrefs[i].word != NULL; i++)
        if (!strcmp(entry, wordrefs[i].word)) {
            wordrefs[i].count++;
            return;
        }
    wordrefs[3].count++;
}

int
main(void)
{
    struct hash *hash;
    FILE *words;
    bool reported;
    int i;
    char buffer[1024];
    char *word;
    char *test, *testing, *strange, *change, *foo, *bar;

    struct wordref wordrefs[4] = {
        {"test",    0},
        {"testing", 0},
        {"change",  0},
        {NULL,      0}
    };

    test = xstrdup("test");
    testing = xstrdup("testing");
    strange = xstrdup("strange");
    change = xstrdup("change");

    plan(38);
    hash =
        hash_create(4, hash_string, string_key, string_equal, string_delete);
    ok(hash != NULL, "create a table with an even requested size");
    if (hash == NULL)
        die("Unable to create hash, cannot continue");

    ok(hash_insert(hash, "test", test), "insert the first entry");
    ok(hash_collisions(hash) == 0, "first insertion has no collisions");
    ok(hash_expansions(hash) == 0,
       "first insertion does not expand the table");
    ok(hash_searches(hash) == 1, "first insertion counts one search");
    ok(hash_count(hash) == 1, "table contains one entry");
    word = hash_lookup(hash, "test");
    ok(word != NULL && !strcmp("test", word), "look up the entry");
    ok(hash_delete(hash, "test"), "delete the entry");
    test = xstrdup("test");
    ok(hash_lookup(hash, "test") == NULL, "deleted key is absent");
    ok(!hash_delete(hash, "test"), "cannot delete the absent key");
    ok(!hash_replace(hash, "test", testing),
       "cannot replace the absent key");
    ok(hash_count(hash) == 0, "table is empty after deletion");
    ok(hash_insert(hash, "test", test), "reinsert the deleted key");
    ok(hash_insert(hash, "testing", testing),
       "insert a key with the same prefix");
    ok(hash_insert(hash, "strange", strange), "insert another key");
    ok(hash_expansions(hash) == 0,
       "first three entries do not expand the table");
    ok(hash_insert(hash, "change", change), "insert the fourth entry");
    ok(hash_expansions(hash) == 1, "fourth entry expands the table");
    ok(hash_count(hash) == 4, "expanded table contains four entries");
    word = hash_lookup(hash, "testing");
    ok(word != NULL && !strcmp("testing", word),
       "lookup survives expansion");
    word = hash_lookup(hash, "strange");
    ok(word != NULL && !strcmp("strange", word),
       "second lookup survives expansion");
    ok(hash_lookup(hash, "thingie") == NULL, "unknown key is absent");
    ok(!hash_delete(hash, "thingie"), "cannot delete an unknown key");
    ok(hash_delete(hash, "strange"), "delete the third key");
    ok(hash_lookup(hash, "strange") == NULL,
       "third key is absent after deletion");
    ok(hash_count(hash) == 3, "table contains three entries after deletion");

    hash_traverse(hash, string_traverse, &wordrefs[0]);
    reported = false;
    for (i = 0; wordrefs[i].word != NULL; i++)
        if (wordrefs[i].count != 1 && !reported) {
            reported = true;
        }
    ok(!reported, "traversal visits every remaining entry once");
    ok(wordrefs[3].count == 0, "traversal does not visit the deleted entry");

    hash_free(hash);

    /* Test hash creation with an odd size.  This previously could result
       in the wrong table size being allocated. */
    test = xstrdup("test");
    testing = xstrdup("testing");
    strange = xstrdup("strange");
    change = xstrdup("change");
    foo = xstrdup("foo");
    bar = xstrdup("bar");
    hash =
        hash_create(5, hash_string, string_key, string_equal, string_delete);
    ok(hash != NULL, "create a table with an odd requested size");
    if (hash == NULL)
        die("Unable to create hash, cannot continue");
    ok(hash_insert(hash, "test", test),
       "insert first entry into odd-sized table");
    ok(hash_insert(hash, "testing", testing),
       "insert second entry into odd-sized table");
    ok(hash_insert(hash, "strange", strange),
       "insert third entry into odd-sized table");
    ok(hash_insert(hash, "change", change),
       "insert fourth entry into odd-sized table");
    ok(hash_insert(hash, "foo", foo),
       "insert fifth entry into odd-sized table");
    ok(hash_insert(hash, "bar", bar),
       "insert sixth entry into odd-sized table");
    ok(hash_count(hash) == 6, "odd-sized table contains six entries");
    hash_free(hash);

    words = fopen("/usr/dict/words", "r");
    if (words == NULL)
        words = fopen("/usr/share/dict/words", "r");
    if (words == NULL) {
        skip_block(2, "/usr/share/dict/words not available");
        exit(0);
    }

    hash =
        hash_create(4, hash_string, string_key, string_equal, string_delete);
    reported = false;
    if (hash == NULL)
        reported = true;
    else {
        while (fgets(buffer, sizeof(buffer), words)) {
            buffer[strlen(buffer) - 1] = '\0';
            word = xstrdup(buffer);
            if (!hash_insert(hash, word, word)) {
                reported = true;
            }
        }
    }
    ok(!reported, "insert every dictionary word");

    if (fseek(words, 0, SEEK_SET) < 0) {
        fclose(words);
        sysdie("Unable to rewind words file");
    }
    reported = false;
    if (hash == NULL)
        reported = true;
    else {
        while (fgets(buffer, sizeof(buffer), words)) {
            buffer[strlen(buffer) - 1] = '\0';
            word = hash_lookup(hash, buffer);
            if (!word || strcmp(word, buffer) != 0) {
                reported = true;
            }
        }
    }
    ok(!reported, "retrieve every dictionary word");

    hash_free(hash);
    fclose(words);

    return 0;
}

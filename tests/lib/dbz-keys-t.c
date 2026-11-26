/*
**  Test duplicate insertions in both dbz index formats.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/dbz.h"
#include "inn/history.h"
#include "inn/storage.h"
#include "tap/basic.h"

#ifdef DO_TAGGED_HASH
/*
**  Use a three-slot table and find two Message-IDs with the same initial
**  slot and seven-bit tag.  Try one more ID than there are possible pairs
**  so a collision is guaranteed on either byte order.
*/
static void
test_bulk_history(const char *path)
{
    unsigned int seen[3 * 128] = {0};
    unsigned int i;
    char ids[2][64];
    FILE *file;
    struct history *history;
    TOKEN token = {0};

    for (i = 1; i <= ARRAY_SIZE(seen) + 1; i++) {
        HASH hash;
        unsigned int prefix;
        unsigned int slot_tag;

        snprintf(ids[1], sizeof(ids[1]), "<bulk-%u@example.test>", i);
        hash = HashMessageID(ids[1]);
        memcpy(&prefix, &hash, sizeof(prefix));
        slot_tag = (prefix >> 1) % ARRAY_SIZE(seen);
        if (seen[slot_tag] != 0) {
            snprintf(ids[0], sizeof(ids[0]), "<bulk-%u@example.test>",
                     seen[slot_tag]);
            break;
        }
        seen[slot_tag] = i;
    }
    if (i > ARRAY_SIZE(seen) + 1)
        bail("cannot find a tagged collision");

    file = fopen(path, "w");
    if (file == NULL || fclose(file) == EOF)
        sysbail("cannot create bulk history text");
    if (!dbzfresh(path, 3) || !dbzclose())
        bail("cannot initialize small tagged index");

    history = HISopen(path, "hisv6", HIS_RDWR | HIS_INCORE);
    if (history == NULL)
        bail("cannot open bulk history writer");
    token.type = 1;
    for (i = 0; i < ARRAY_SIZE(ids); i++)
        ok(HISwrite(history, ids[i], 1000 + i, 900, 0, &token),
           "bulk writer inserts colliding key %u", i + 1);
    for (i = 0; i < ARRAY_SIZE(ids); i++) {
        time_t arrived = 0;

        ok(HISlookup(history, ids[i], &arrived, NULL, NULL, NULL)
               && arrived == 1000 + i,
           "bulk writer looks up colliding key %u", i + 1);
    }
    if (!HISclose(history))
        bail("cannot close bulk history");
}
#endif

int
main(void)
{
    static const char first[] = "<dup@example.test>\t1\n";
    static const char second[] = "<dup@example.test>\t2\n";
    static const char third[] = "<other@example.test>\t3\n";
    static const char *const filenames[] = {
        "history", "history.dir", "history.pag", "history.index", "history.hash"
    };
    char directory[] = "dbz-XXXXXX";
    char path[sizeof(directory) + 20];
    HASH duplicate = HashMessageID("<dup@example.test>");
    HASH other = HashMessageID("<other@example.test>");
    FILE *history;
    off_t offset = -1;
    const off_t second_offset = sizeof(first) - 1;
    const off_t third_offset = sizeof(first) + sizeof(second) - 2;
    size_t i;

#ifdef DO_TAGGED_HASH
    plan(13);
#else
    plan(9);
#endif
    if (mkdtemp(directory) == NULL)
        sysbail("cannot create temporary directory");

    snprintf(path, sizeof(path), "%s/history", directory);
    history = fopen(path, "w");
    if (history == NULL)
        sysbail("cannot open temporary history");
    if (fputs(first, history) == EOF || fputs(second, history) == EOF
        || fputs(third, history) == EOF || fclose(history) == EOF)
        sysbail("cannot write temporary history");
    if (!dbzfresh(path, 101))
        bail("cannot initialize dbz");

    is_int(DBZSTORE_OK, dbzstore(duplicate, 0), "insert the first key");
    is_int(DBZSTORE_EXISTS, dbzstore(duplicate, second_offset),
           "same key at another offset is a duplicate");
    ok(dbzfetch(duplicate, &offset), "the duplicate key remains readable");
    is_int(0, offset, "the original offset is retained");
    is_int(DBZSTORE_OK, dbzstore(other, third_offset), "insert another key");
    ok(dbzclose(), "close dbz");
    if (!dbzinit(path))
        bail("cannot reopen dbz");
    offset = -1;
    ok(dbzfetch(duplicate, &offset), "the original key survives reopening");
    is_int(0, offset, "the original offset survives reopening");
    ok(dbzclose(), "close reopened dbz");

    for (i = 0; i < ARRAY_SIZE(filenames); i++) {
        snprintf(path, sizeof(path), "%s/%s", directory, filenames[i]);
        unlink(path);
    }
#ifdef DO_TAGGED_HASH
    snprintf(path, sizeof(path), "%s/bulk", directory);
    test_bulk_history(path);
    unlink(path);
    snprintf(path, sizeof(path), "%s/bulk.dir", directory);
    unlink(path);
    snprintf(path, sizeof(path), "%s/bulk.pag", directory);
    unlink(path);
#endif
    rmdir(directory);
    return 0;
}

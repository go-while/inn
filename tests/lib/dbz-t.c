/*
**  Test duplicate insertions in both dbz index formats.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/dbz.h"
#include "tap/basic.h"

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

    plan(9);
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
    rmdir(directory);
    return 0;
}

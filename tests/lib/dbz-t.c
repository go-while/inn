/*
**  dbz test suite.
**
**  Checks that dbzinit refuses a .dir file whose table size cannot come from
**  dbzfresh or dbzagain, as a damaged file could have, instead of using it in
**  the hash arithmetic of a later lookup.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/dbz.h"
#include "inn/libinn.h"
#include "inn/messages.h"
#include "tap/basic.h"
#include "tap/messages.h"
#include "tap/string.h"

/*
**  Replace the table size in the first line of the .dir file of the database
**  at path, keeping the rest of the file.
*/
static void
set_table_size(const char *path, const char *size)
{
    char *dirpath, *contents, *rest, *rewritten;
    char *first;
    FILE *f;
    size_t length;

    basprintf(&dirpath, "%s.dir", path);
    contents = ReadInFile(dirpath, NULL);
    if (contents == NULL)
        sysbail("cannot read %s", dirpath);
    rest = strchr(contents, '\n');
    if (rest == NULL || strncmp(contents, "dbz 6 ", 6) != 0)
        bail("unexpected contents of %s: %s", dirpath, contents);

    /* The first line is "dbz 6 <size> <other fields>".  Keep the other
       fields as they are. */
    first = strchr(contents + 6, ' ');
    if (first == NULL || first > rest)
        bail("unexpected first line in %s: %s", dirpath, contents);
    basprintf(&rewritten, "dbz 6 %s%s", size, first);

    f = fopen(dirpath, "w");
    if (f == NULL)
        sysbail("cannot write %s", dirpath);
    length = strlen(rewritten);
    if (fwrite(rewritten, 1, length, f) != length || fclose(f) != 0)
        sysbail("cannot write %s", dirpath);
    free(rewritten);
    free(contents);
    free(dirpath);
}

/*
**  Try to open the database at path, closing it again if that worked.
**  Returns whether it could be opened.
*/
static bool
try_open(const char *path)
{
    bool opened;

    errors_capture();
    opened = dbzinit(path);
    errors_uncapture();
    if (opened)
        dbzclose();
    free(errors);
    errors = NULL;
    return opened;
}

int
main(void)
{
    char *tmpdir, *path, *dirpath;
    char *original;
    dbzoptions opt;
    HASH key;
    off_t value = 0;
    const char *bad[] = {"0", "-1", "-65536", "1"};
    const char *suffixes[] = {".dir", ".index", ".hash", ".pag"};
    size_t i, nbad;
    bool opened;
    FILE *f;

    nbad = ARRAY_SIZE(bad);
    plan(4 + nbad + 2);

    tmpdir = test_tmpdir();
    basprintf(&path, "%s/dbz-history", tmpdir);
    basprintf(&dirpath, "%s.dir", path);

    /* Read the tables from disk, as hisv6 does for readers (without the
       HIS_MMAP flag). */
    dbzgetoptions(&opt);
    opt.pag_incore = INCORE_NO;
    opt.exists_incore = INCORE_NO;
    dbzsetoptions(opt);

    /* A valid database with one entry. */
    key = HashMessageID("<dbz-test@example.invalid>");
    ok(dbzfresh(path, dbzsize(100)), "dbzfresh");
    is_int(DBZSTORE_OK, dbzstore(key, 42), "dbzstore");
    ok(dbzclose(), "dbzclose");
    original = ReadInFile(dirpath, NULL);
    if (original == NULL)
        sysbail("cannot read %s", dirpath);

    if (!dbzinit(path))
        sysbail("cannot open the valid database");
    ok(dbzfetch(key, &value) && value == 42, "valid database can be read");
    dbzclose();

    /* Table sizes that dbzfresh and dbzagain never write. */
    for (i = 0; i < nbad; i++) {
        set_table_size(path, bad[i]);
        ok(!try_open(path), "table size %s is refused", bad[i]);
    }

    /* The database is usable again with its original .dir file. */
    f = fopen(dirpath, "w");
    if (f == NULL || fputs(original, f) == EOF || fclose(f) != 0)
        sysbail("cannot restore %s", dirpath);
    free(original);
    value = 0;
    opened = dbzinit(path);
    ok(opened, "restored database can be opened");
    ok(opened && dbzfetch(key, &value) && value == 42, "and read");
    if (opened)
        dbzclose();

    for (i = 0; i < ARRAY_SIZE(suffixes); i++) {
        char *file;

        basprintf(&file, "%s%s", path, suffixes[i]);
        unlink(file);
        free(file);
    }
    free(dirpath);
    free(path);
    test_tmpdir_free(tmpdir);
    return 0;
}

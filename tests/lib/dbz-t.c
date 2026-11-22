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

/*
**  Write the history text file at path: a first line for another article,
**  then one for the article with the given hash.  The tagged hash format
**  checks the key against this file in dbzfetch.  Returns the offset of the
**  second line, to store in the database.
*/
static off_t
write_history(const char *path, HASH key)
{
    static const char first[] = "[00000000000000000000000000000000]\t1~-~1\n";
    FILE *f;

    f = fopen(path, "w");
    if (f == NULL)
        sysbail("cannot write %s", path);
    if (fputs(first, f) == EOF
        || fprintf(f, "[%s]\t2~-~2\n", HashToText(key)) < 0 || fclose(f) != 0)
        sysbail("cannot write %s", path);
    return (off_t) strlen(first);
}

/*
**  The smallest table size that dbz accepts, computed as MINSIZE in lib/dbz.c:
**  the minimum applied to a given size, or DEFSIZE when that is smaller (a
**  DEFSIZE override given in CFLAGS also applies here).
*/
#ifdef DO_TAGGED_HASH
#    define TEST_MINSIZE 2L
#else
#    define TEST_MINSIZE (64L * 1024)
#endif
#if defined(DEFSIZE)
#    if DEFSIZE < TEST_MINSIZE
#        undef TEST_MINSIZE
#        define TEST_MINSIZE ((long) DEFSIZE)
#    endif
#endif

int
main(void)
{
    char *tmpdir, *path, *dirpath;
    char *original;
    dbzoptions opt;
    HASH key;
    off_t offset;
    off_t value = 0;
    /* Table sizes that dbzfresh and dbzagain never write, ending with the
       one just below the smallest they write (65535 with the stock untagged
       build, 1 with the tagged hash), and that smallest one (65536 or 2). */
    char below[32], smallest[32];
    const char *bad[] = {"0", "-1", "-65536", "1", NULL};
    const char *suffixes[] = {"", ".dir", ".index", ".hash", ".pag"};
    size_t i, nbad;
    bool opened;
    FILE *f;

    snprintf(below, sizeof(below), "%ld", TEST_MINSIZE - 1);
    snprintf(smallest, sizeof(smallest), "%ld", TEST_MINSIZE);
    bad[ARRAY_SIZE(bad) - 1] = below;
    nbad = ARRAY_SIZE(bad);
    if (TEST_MINSIZE - 1 == 1)
        nbad--; /* "1" is already in the list */
    plan(4 + nbad + 1 + 2);

    tmpdir = test_tmpdir();
    basprintf(&path, "%s/dbz-history", tmpdir);
    basprintf(&dirpath, "%s.dir", path);

    /* Read the tables from disk, as hisv6 does for readers (without the
       HIS_MMAP flag). */
    dbzgetoptions(&opt);
    opt.pag_incore = INCORE_NO;
    opt.exists_incore = INCORE_NO;
    dbzsetoptions(opt);

    /* A valid database with one entry, pointing to its history line. */
    key = HashMessageID("<dbz-test@example.invalid>");
    offset = write_history(path, key);
    ok(dbzfresh(path, dbzsize(100)), "dbzfresh");
    is_int(DBZSTORE_OK, dbzstore(key, offset), "dbzstore");
    ok(dbzclose(), "dbzclose");
    original = ReadInFile(dirpath, NULL);
    if (original == NULL)
        sysbail("cannot read %s", dirpath);

    if (!dbzinit(path))
        sysbail("cannot open the valid database");
    ok(dbzfetch(key, &value) && value == offset, "valid database can be read");
    dbzclose();

    for (i = 0; i < nbad; i++) {
        set_table_size(path, bad[i]);
        ok(!try_open(path), "table size %s is refused", bad[i]);
    }
    set_table_size(path, smallest);
    ok(try_open(path), "table size %s is accepted", smallest);

    /* The database is usable again with its original .dir file. */
    f = fopen(dirpath, "w");
    if (f == NULL || fputs(original, f) == EOF || fclose(f) != 0)
        sysbail("cannot restore %s", dirpath);
    free(original);
    value = 0;
    opened = dbzinit(path);
    ok(opened, "restored database can be opened");
    ok(opened && dbzfetch(key, &value) && value == offset, "and read");
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

/* Test expire-tombstone file operations.
 *
 * tests/expire/tombstone-t.c covers token parsing, deduplication, blank
 * lines, renaming and the header.  This test adds only what that one does
 * not check: the out_error flag of tombstone_read, a CRLF-terminated token,
 * the exact pathname returned by tombstone_rename_for_processing, and the
 * exact bytes of the renamed file and of the file after
 * tombstone_ensure_header. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/concat.h"
#include "inn/libinn.h"
#include "inn/tombstone.h"
#include "tap/basic.h"

#define FIRST_TOKEN  "@02014A2DD6231FCC00000000000000000000@"
#define SECOND_TOKEN "@02014A2DD6231FCC00000000000000000001@"

struct paths {
    char *base;
    char *dir;
    char *live;
    char *snapshot;
    char *non_directory;
};

static void
cleanup(int success UNUSED, int primary, void *data)
{
    struct paths *paths = data;

    if (!primary)
        return;
    unlink(paths->live);
    unlink(paths->snapshot);
    rmdir(paths->dir);
    free(paths->non_directory);
    free(paths->snapshot);
    free(paths->live);
    free(paths->dir);
    test_tmpdir_free(paths->base);
    free(paths);
}

int
main(void)
{
    static const char contents[] = FIRST_TOKEN "\r\n" SECOND_TOKEN "\n";
    static const char with_header[] =
        TOMBSTONE_HEADER FIRST_TOKEN "\r\n" SECOND_TOKEN "\n";
    struct paths *paths;
    struct hash *hash;
    FILE *file;
    char *text, *snapshot;
    bool error;

    plan(9);
    paths = bcalloc(1, sizeof(*paths));
    paths->base = test_tmpdir();
    paths->dir = concatpath(paths->base, "tombstone-XXXXXX");
    if (mkdtemp(paths->dir) == NULL)
        sysbail("cannot create tombstone test directory");
    paths->live = concatpath(paths->dir, "live");
    paths->snapshot = concatpath(paths->dir, "live.processing");
    paths->non_directory = concatpath(paths->live, "child");
    test_cleanup_register_with_data(cleanup, paths);

    file = fopen(paths->live, "w");
    if (file == NULL)
        sysbail("cannot create tombstone test file");
    if (fputs(contents, file) == EOF || fclose(file) == EOF)
        sysbail("cannot write tombstone fixture");

    hash = tombstone_hash_create(4);
    error = true;
    is_int(2, tombstone_read(hash, paths->live, &error),
           "read a CRLF-terminated and an LF-terminated token");
    ok(!error, "successful read clears the error flag");

    error = true;
    is_int(0, tombstone_read(hash, paths->snapshot, &error),
           "missing file has no tokens");
    ok(!error, "missing file clears the error flag");
    is_int(0, tombstone_read(hash, paths->non_directory, &error),
           "open failure has no tokens");
    ok(error, "open failure sets the error flag");

    snapshot = tombstone_rename_for_processing(paths->live);
    is_string(paths->snapshot, snapshot, "rename returns the snapshot path");
    free(snapshot);
    text = ReadInFile(paths->snapshot, NULL);
    is_string(contents, text, "renamed file has the original bytes");
    free(text);

    tombstone_ensure_header(paths->snapshot);
    text = ReadInFile(paths->snapshot, NULL);
    is_string(with_header, text, "header is prepended before the data");
    free(text);
    hash_free(hash);
    return 0;
}

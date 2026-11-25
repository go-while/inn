/* Helper for overview/ovsqlite-corrupt.t.
 *
 * usage: ovsqlite-damage.t damage
 *        ovsqlite-damage.t direct|server GROUP BAD1 BAD2 HIGH COUNT
 *
 * damage: with ovsqlite-server stopped, picks the group with the most
 * articles in the database the writer helper filled, replaces the
 * overview blob of its second article with one whose length prefix starts
 * with 0xff (no valid prefix starts that way), and the blob of its third
 * article with a valid length prefix followed by data that is not a zlib
 * stream.  Prints "GROUP BAD1 BAD2 HIGH COUNT LATER" (LATER: compressed
 * articles after BAD2), or "nocompress" when the database is not
 * compressed.
 *
 * direct: opens overview read-only (the direct reader) and searches the
 * whole group; every article except the two damaged ones must be returned.
 *
 * server: opens overview read-write (through ovsqlite-server), searches
 * BAD1 alone, then BAD2+1 to HIGH, which must return COUNT - 3 articles.
 *
 * Exit status 0 when the checks hold, 1 otherwise, 2 for nocompress.
 */

#include "portable/system.h"

#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/messages.h"
#include "inn/ov.h"
#include "inn/storage.h"

#ifdef HAVE_SQLITE3
#    include <sqlite3.h>
#endif

#ifdef HAVE_SQLITE3

static const unsigned char bad_prefix[] = {0xff, 0x01, 0x02, 0x03,
                                           0x04, 0x05, 0x06, 0x07,
                                           0x08, 0x09, 0x0a, 0x0b};
static const unsigned char bad_stream[] = {0x64, 0xff, 0xff, 0xff,
                                           0xff, 0xff, 0xff, 0xff};

static sqlite3_stmt *
prepare(sqlite3 *db, const char *sql)
{
    sqlite3_stmt *stmt;

    if (sqlite3_prepare_v2(db, sql, -1, &stmt, NULL) != SQLITE_OK)
        die("cannot prepare %s: %s", sql, sqlite3_errmsg(db));
    return stmt;
}

static void
set_overview(sqlite3 *db, const char *group, unsigned long artnum,
             const unsigned char *blob, int size)
{
    sqlite3_stmt *stmt;

    stmt = prepare(db, "update artinfo set overview = ?1"
                       " where artnum = ?2 and groupid ="
                       " (select groupid from groupinfo"
                       "  where deleted = 0 and groupname = ?3)");
    sqlite3_bind_blob(stmt, 1, blob, size, SQLITE_STATIC);
    sqlite3_bind_int64(stmt, 2, (sqlite3_int64) artnum);
    sqlite3_bind_blob(stmt, 3, group, (int) strlen(group), SQLITE_STATIC);
    if (sqlite3_step(stmt) != SQLITE_DONE || sqlite3_changes(db) != 1)
        die("cannot update %s:%lu: %s", group, artnum, sqlite3_errmsg(db));
    sqlite3_finalize(stmt);
}

static int
damage(void)
{
    sqlite3 *db;
    sqlite3_stmt *stmt;
    char *path, *group;
    unsigned long artnum, bad1 = 0, bad2 = 0, high = 0;
    int count = 0, later = 0, n;
    const unsigned char *blob;

    path = concatpath(innconf->pathoverview, "ovsqlite.db");
    if (sqlite3_open_v2(path, &db, SQLITE_OPEN_READWRITE, NULL) != SQLITE_OK)
        die("cannot open %s: %s", path, sqlite3_errmsg(db));
    free(path);

    stmt = prepare(db, "select value from misc where key = 'compress'");
    if (sqlite3_step(stmt) != SQLITE_ROW || sqlite3_column_int(stmt, 0) == 0) {
        sqlite3_finalize(stmt);
        sqlite3_close(db);
        printf("nocompress\n");
        return 2;
    }
    sqlite3_finalize(stmt);

    stmt = prepare(db, "select groupname from groupinfo natural join artinfo"
                       " where deleted = 0 group by groupid"
                       " order by count(*) desc, groupname limit 1");
    if (sqlite3_step(stmt) != SQLITE_ROW)
        die("no articles in the database");
    group = xstrndup((const char *) sqlite3_column_blob(stmt, 0),
                     (size_t) sqlite3_column_bytes(stmt, 0));
    sqlite3_finalize(stmt);

    stmt = prepare(db, "select artnum, overview from groupinfo"
                       " natural join artinfo"
                       " where deleted = 0 and groupname = ?1"
                       " order by artnum");
    sqlite3_bind_blob(stmt, 1, group, (int) strlen(group), SQLITE_STATIC);
    while (sqlite3_step(stmt) == SQLITE_ROW) {
        artnum = (unsigned long) sqlite3_column_int64(stmt, 0);
        blob = sqlite3_column_blob(stmt, 1);
        n = sqlite3_column_bytes(stmt, 1);
        if (count == 1)
            bad1 = artnum;
        else if (count == 2)
            bad2 = artnum;
        else if (count > 2 && blob != NULL && n > 0 && blob[0] != 0)
            later++;
        high = artnum;
        count++;
    }
    sqlite3_finalize(stmt);
    if (count < 5)
        die("group %s has only %d articles", group, count);

    set_overview(db, group, bad1, bad_prefix, (int) sizeof(bad_prefix));
    set_overview(db, group, bad2, bad_stream, (int) sizeof(bad_stream));
    if (sqlite3_close(db) != SQLITE_OK)
        die("cannot close the database");

    printf("%s %lu %lu %lu %d %d\n", group, bad1, bad2, high, count, later);
    free(group);
    return 0;
}

/* Search group from low to high and count the articles found.  Fails if
 * one of the damaged articles is returned. */
static int
search(char *group, unsigned long low, unsigned long high, unsigned long bad1,
       unsigned long bad2)
{
    void *handle;
    ARTNUM artnum;
    char *data;
    int len, found = 0;
    TOKEN token;
    time_t arrived;

    handle = OVopensearch(group, (int) low, (int) high);
    if (handle == NULL)
        return -1;
    while (OVsearch(handle, &artnum, &data, &len, &token, &arrived)) {
        if (artnum == bad1 || artnum == bad2) {
            warn("damaged article %lu returned", artnum);
            found = -1000000;
        }
        found++;
    }
    OVclosesearch(handle);
    return found;
}

#endif /* HAVE_SQLITE3 */

int
main(int argc, char *argv[])
{
#ifdef HAVE_SQLITE3
    char *group;
    unsigned long bad1, bad2, high;
    int count, found, want;
#endif

    message_program_name = "ovsqlite-damage";
    if (!innconf_read(NULL))
        die("cannot read inn.conf");
#ifndef HAVE_SQLITE3
    (void) argc;
    (void) argv;
    printf("nosqlite\n");
    return 2;
#else
    if (argc == 2 && strcmp(argv[1], "damage") == 0)
        return damage();
    if (argc != 7)
        die("usage: ovsqlite-damage.t damage"
            " | direct|server GROUP BAD1 BAD2 HIGH COUNT");
    group = argv[2];
    bad1 = strtoul(argv[3], NULL, 10);
    bad2 = strtoul(argv[4], NULL, 10);
    high = strtoul(argv[5], NULL, 10);
    count = atoi(argv[6]);

    if (strcmp(argv[1], "direct") == 0) {
        if (!OVopen(OV_READ))
            die("cannot open overview for reading");
        found = search(group, 1, high, bad1, bad2);
        want = count - 2;
    } else if (strcmp(argv[1], "server") == 0) {
        if (!OVopen(OV_READ | OV_WRITE))
            die("cannot open overview through the server");
        found = search(group, bad1, bad1, bad1, bad2);
        printf("search of %s:%lu alone: %d\n", group, bad1, found);
        found = search(group, bad2 + 1, high, bad1, bad2);
        want = count - 3;
    } else {
        die("unknown mode %s", argv[1]);
    }
    OVclose();
    printf("%s: %d articles returned, want %d\n", argv[1], found, want);
    return found == want ? 0 : 1;
#endif
}

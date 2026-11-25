/* Test SQLite helper initialization, statement execution and cleanup. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/sqlite-helper.h"
#include "tap/basic.h"

#ifndef HAVE_SQLITE3

int
main(void)
{
    skip_all("SQLite support is unavailable");
}

#else

int
main(void)
{
    static const sqlite_helper_t valid = {
        2,
        "CREATE TABLE numbers (n INTEGER); INSERT INTO numbers VALUES (7);\0"
        "read_max\0SELECT max(n) FROM numbers\0"
        "insert\0INSERT INTO numbers VALUES (9)\0",
    };
    static const sqlite_helper_t invalid = {
        2,
        "\0"
        "valid\0SELECT 1\0"
        "broken\0SELECT FROM\0",
    };
    sqlite3 *db, *other;
    sqlite3_stmt *sentinel[2];
    sqlite3_stmt *stmts[2] = {NULL, NULL};
    char *error = NULL;
    int result;

    plan(17);
    if (sqlite3_open(":memory:", &db) != SQLITE_OK)
        bail("cannot open in-memory SQLite database");

    result = sqlite_helper_init(&valid, stmts, db, 0, &error);
    is_int(SQLITE_OK, result, "execute initialization and prepare statements");
    is_string(NULL, error, "successful initialization returns no error");
    if (result != SQLITE_OK || stmts[0] == NULL || stmts[1] == NULL)
        bail("cannot exercise the prepared statements");
    sqlite3_free(error);
    error = NULL;

    if (is_int(SQLITE_ROW, sqlite3_step(stmts[0]), "select an initialized row"))
        is_int(7, sqlite3_column_int(stmts[0], 0), "initialization inserted 7");
    else
        skip("no initialized row");
    is_int(SQLITE_OK, sqlite3_reset(stmts[0]), "reset selected statement");
    is_int(SQLITE_DONE, sqlite3_step(stmts[1]), "execute the second statement");
    if (is_int(SQLITE_ROW, sqlite3_step(stmts[0]), "select after inserting"))
        is_int(9, sqlite3_column_int(stmts[0], 0), "second statement inserted 9");
    else
        skip("no row after inserting");

    sqlite_helper_term(&valid, stmts);
    ok(stmts[0] == NULL && stmts[1] == NULL, "termination clears both slots");
    ok(sqlite3_next_stmt(db, NULL) == NULL, "termination finalizes statements");

    is_int(SQLITE_ERROR, sqlite_helper_init(&invalid, stmts, db, 0, &error),
           "report a named statement's preparation error");
    ok(error != NULL && strncmp(error, "broken: ", 8) == 0,
       "preparation error identifies the failed statement");
    ok(stmts[0] == NULL && stmts[1] == NULL, "failure clears prepared slots");
    ok(sqlite3_next_stmt(db, NULL) == NULL, "failure finalizes earlier statements");
    sqlite3_free(error);
    error = NULL;
    sqlite_helper_term(&invalid, stmts);

    /* Preload the slots with real prepared statements of another
     * connection as sentinels; the initialization script fails because the
     * table already exists, so no statement may be prepared on db and the
     * slots must stay as they are. */
    if (sqlite3_open(":memory:", &other) != SQLITE_OK)
        bail("cannot open second in-memory SQLite database");
    if (sqlite3_prepare_v2(other, "SELECT 1", -1, &sentinel[0], NULL)
            != SQLITE_OK
        || sqlite3_prepare_v2(other, "SELECT 2", -1, &sentinel[1], NULL)
               != SQLITE_OK)
        bail("cannot prepare sentinel statements");
    stmts[0] = sentinel[0];
    stmts[1] = sentinel[1];
    is_int(SQLITE_ERROR, sqlite_helper_init(&valid, stmts, db, 0, &error),
           "recreating the table fails during initialization");
    ok(error != NULL, "initialization error provides a message");
    ok(sqlite3_next_stmt(db, NULL) == NULL
           && stmts[0] == sentinel[0] && stmts[1] == sentinel[1],
       "initialization error prepares nothing and leaves the slots alone");
    sqlite3_free(error);
    sqlite3_finalize(sentinel[0]);
    sqlite3_finalize(sentinel[1]);
    sqlite3_close(other);
    sqlite3_close(db);
    return 0;
}

#endif

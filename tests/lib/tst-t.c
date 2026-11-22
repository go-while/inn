/* Test suite for ternary search tries. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/libinn.h"
#include "inn/messages.h"
#include "inn/tst.h"
#include "tap/basic.h"

/* Used for strings of unsigned characters (called SUC instead of U
   because it otherwise conflicts with Unicode strings). */
#define SUC        (const unsigned char *)

/* An unsigned char version of strlen. */
#define ustrlen(s) strlen((const char *) s)

int
main(void)
{
    struct tst *tst;
    FILE *words;
    unsigned char buffer[1024];
    bool reported;
    void *existing;
    unsigned char *word;

    char test[] = "test";
    char t[] = "t";
    char foo[] = "foo";
    char testing[] = "testing";
    char Strange[] = "Strange";
    char change[] = "\231hange";

    plan(38);

    tst = tst_init(2);
    ok(tst != NULL, "allocate a tree");
    ok(tst_insert(tst, SUC "test", test, 0, NULL) == TST_OK,
       "insert the first key");
    is_string("test", tst_search(tst, SUC "test"), "find the first key");
    ok(tst_insert(tst, SUC "test", foo, 0, &existing) == TST_DUPLICATE_KEY,
       "detect a duplicate key");
    is_string("test", existing, "return the existing value for a duplicate");
    ok(tst_insert(tst, SUC "test", foo, TST_REPLACE, &existing) == TST_OK,
       "replace the existing value");
    is_string("test", existing, "return the former value on replacement");
    is_string("foo", tst_search(tst, SUC "test"),
              "lookup returns the replacement");
    ok(tst_insert(tst, SUC "testing", testing, 0, NULL) == TST_OK,
       "insert a key that extends an existing key");
    ok(tst_insert(tst, SUC "t", t, 0, NULL) == TST_OK,
       "insert a prefix of existing keys");
    ok(tst_insert(tst, SUC "Strange", Strange, 0, NULL) == TST_OK,
       "insert a capitalized key");
    ok(tst_insert(tst, SUC "\231hange", change, 0, NULL) == TST_OK,
       "insert a key containing a high byte");
    ok(tst_insert(tst, SUC "", foo, 0, NULL) == TST_NULL_KEY,
       "reject an empty key");
    ok(tst_insert(tst, NULL, foo, 0, NULL) == TST_NULL_KEY,
       "reject a null key");
    is_string("testing", tst_search(tst, SUC "testing"),
              "find the extended key");
    is_string("t", tst_search(tst, SUC "t"), "find the prefix key");
    is_string("Strange", tst_search(tst, SUC "Strange"),
              "find the capitalized key");
    is_string("\231hange", tst_search(tst, SUC "\231hange"),
              "find the high-byte key");
    is_string("foo", tst_search(tst, SUC "test"), "find the replaced value");
    ok(tst_search(tst, SUC "") == NULL, "empty key is absent");
    ok(tst_search(tst, SUC "Peter") == NULL, "unrelated key is absent");
    ok(tst_search(tst, SUC "foo") == NULL, "a value is not a key");
    ok(tst_search(tst, SUC "te") == NULL, "uninserted prefix is absent");
    is_string("Strange", tst_delete(tst, SUC "Strange"),
              "delete capitalized key and return its value");
    ok(tst_search(tst, SUC "Strange") == NULL,
       "capitalized key is absent after deletion");
    is_string("t", tst_delete(tst, SUC "t"), "delete the short prefix key");
    ok(tst_search(tst, SUC "t") == NULL,
       "short prefix is absent after deletion");
    is_string("testing", tst_search(tst, SUC "testing"),
              "deleting a prefix preserves its extension");
    is_string("foo", tst_search(tst, SUC "test"),
              "deleting a prefix preserves existing key");
    is_string("testing", tst_delete(tst, SUC "testing"),
              "delete one extended key");
    is_string("foo", tst_search(tst, SUC "test"),
              "deleting an extension preserves its prefix");
    is_string("\231hange", tst_delete(tst, SUC "\231hange"),
              "delete the high-byte key");
    is_string("foo", tst_delete(tst, SUC "test"),
              "delete the key with the replaced value");
    ok(tst_search(tst, NULL) == NULL, "null key lookup is absent");
    ok(tst_delete(tst, NULL) == NULL, "null key deletion is absent");
    tst_cleanup(tst);
    ok(true, "clean up the tree");

    words = fopen("/usr/dict/words", "r");
    if (words == NULL)
        words = fopen("/usr/share/dict/words", "r");
    if (words == NULL) {
        skip_block(2, "/usr/share/dict/words not available");
        exit(0);
    }

    tst = tst_init(1000);
    reported = false;
    if (tst == NULL)
        reported = true;
    else {
        while (fgets((char *) buffer, sizeof(buffer), words)) {
            buffer[ustrlen(buffer) - 1] = '\0';
            if (buffer[0] == '\0')
                continue;
            word = (unsigned char *) xstrdup((char *) buffer);
            if (tst_insert(tst, buffer, word, 0, NULL) != TST_OK) {
                if (!reported) {
                    diag("Failed insert of word %s\n", word);
                }
                reported = true;
            }
        }
    }
    ok(!reported, "insert each nonempty dictionary word");

    if (fseek(words, 0, SEEK_SET) < 0) {
        fclose(words);
        sysdie("Unable to rewind words file");
    }
    reported = false;
    if (tst == NULL)
        reported = true;
    else {
        while (fgets((char *) buffer, sizeof(buffer), words)) {
            buffer[ustrlen(buffer) - 1] = '\0';
            if (buffer[0] == '\0')
                continue;
            word = tst_search(tst, buffer);
            if (word == NULL || strcmp((char *) word, (char *) buffer) != 0) {
                if (!reported) {
                    diag("Failed search of word %s\n", word);
                }
                reported = true;
            }
            word = tst_delete(tst, buffer);
            if (word == NULL || strcmp((char *) word, (char *) buffer) != 0) {
                if (!reported) {
                    diag("Failed delete of word %s\n", word);
                }
                reported = true;
            }
            free(word);
        }
    }
    tst_cleanup(tst);
    fclose(words);
    ok(!reported, "find and delete dictionary words");

    return 0;
}

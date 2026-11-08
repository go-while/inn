/*  Test suite for the :bytes and :lines values of overview_build.
**
**  They must be the same as the ones innd computes while parsing an article:
**  lines end at CRLF only, so a bare LF or a bare CR neither ends a line nor
**  makes a following dot a dot-stuffing one.
*/

#include "portable/system.h"

#include "inn/buffer.h"
#include "inn/libinn.h"
#include "inn/overview.h"
#include "inn/vector.h"
#include "tap/basic.h"

/* The headers of every test article, with the empty line that ends them. */
#define HEADERS "Subject: s\r\nMessage-ID: <a@b>\r\n\r\n"

struct test {
    const char *name;
    const char *article; /* Without the final ".\r\n". */
    unsigned long bytes; /* Expected :bytes. */
    unsigned long lines; /* Expected :lines. */
};

/* The size of the headers; the expected :bytes of an article is its size
   without the dots of the dot-stuffed lines. */
#define HLEN (sizeof(HEADERS) - 1)

static const struct test tests[] = {
    {"empty body",                                HEADERS,                             HLEN,      0},
    {"two lines",                                 HEADERS "a\r\nb\r\n",                HLEN + 6,  2},
    {"empty line",                                HEADERS "\r\na\r\n",                 HLEN + 5,  2},
    {"dot-stuffed first line",                    HEADERS "..x\r\n",                   HLEN + 4,  1},
    {"dot-stuffed lines",                         HEADERS "a\r\n..b\r\n.c\r\n",        HLEN + 10, 3},
    {"dot-stuffed line alone",                    HEADERS "a\r\n..\r\n",               HLEN + 6,  2},
    {"dot in the middle of a line",               HEADERS "a.b\r\n",                   HLEN + 5,  1},
    {"bare LF",                                   HEADERS "a\nb\r\n",                  HLEN + 5,  1},
    {"bare LF before a dot",                      HEADERS "a\n.b\r\n",                 HLEN + 6,  1},
    {"bare LF before a dot, then a stuffed line", HEADERS "a\n.b\r\n..c\r\n",
     HLEN + 10,                                                                                   2},
    {"bare CR before a dot",                      HEADERS "a\r.b\r\n",                 HLEN + 6,  1},
    {"CR before CRLF and a dot",                  HEADERS "a\r\r\n.b\r\n",             HLEN + 7,  2},
    {"dot-stuffed first header line",             ".X: y\r\n" HEADERS "x\r\n",
     HLEN + 6 + 3,                                                                                1},
    {"dot-stuffed header line",                   "X: y\r\n.Z: z\r\n" HEADERS "x\r\n",
     HLEN + 12 + 3,                                                                               1},
    {"bare LF before a dot in the headers",       "X: y\n.Z: z\r\n" HEADERS "x\r\n",
     HLEN + 12 + 3,                                                                               1},
};

/* Return the text of the field (a number from the overview fields) in the
   given overview line, in a static buffer. */
static const char *
field_text(const struct buffer *overview, unsigned int field)
{
    static char text[32];
    struct cvector *vector;
    size_t length;

    vector = overview_split(overview->data, overview->left, NULL, NULL);
    length = vector->strings[field + 1] - vector->strings[field] - 1;
    if (length >= sizeof(text))
        length = sizeof(text) - 1;
    memcpy(text, vector->strings[field], length);
    text[length] = '\0';
    cvector_free(vector);
    return text;
}

int
main(void)
{
    struct buffer *overview;
    char expected[32], *article;
    size_t i, length;

    plan_lazy();

    for (i = 0; i < ARRAY_SIZE(tests); i++) {
        length = strlen(tests[i].article) + 3;
        article = xmalloc(length + 1);
        memcpy(article, tests[i].article, length - 3);
        memcpy(article + length - 3, ".\r\n", 4);

        overview = overview_build(1, article, length, NULL, NULL);
        snprintf(expected, sizeof(expected), "%lu", tests[i].bytes);
        is_string(expected, field_text(overview, 5), ":bytes of %s",
                  tests[i].name);
        snprintf(expected, sizeof(expected), "%lu", tests[i].lines);
        is_string(expected, field_text(overview, 6), ":lines of %s",
                  tests[i].name);
        buffer_free(overview);
        free(article);
    }
    return 0;
}

/*
**  Rebuild dbz file for history db.
*/

#include "portable/system.h"

#include <ctype.h>
#include <errno.h>
#include <syslog.h>

#include "inn/dbz.h"
#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/messages.h"
#include "inn/newsuser.h"
#include "inn/paths.h"
#include "inn/qio.h"
#include "inn/storage.h"

/* FIXME: once we figure out how to integrate this stuff with the
 * history API this external visibility of internal voodoo should
 * go */
#define HIS_FIELDSEP '\t'

static char *TextFile = NULL;
static char *HistoryDir = NULL;
static char *HISTORY = NULL;

static void Usage(void) __attribute__((__noreturn__));

/*
**  Remove the DBZ files for the specified base text file.
*/
static void
RemoveDBZFiles(char *p)
{
    char buff[SMBUF];

    snprintf(buff, sizeof(buff), "%s.dir", p);
    if (unlink(buff) && errno != ENOENT)
        syswarn("cannot unlink %s", buff);
#ifdef DO_TAGGED_HASH
    snprintf(buff, sizeof(buff), "%s.pag", p);
    if (unlink(buff) && errno != ENOENT)
        syswarn("cannot unlink %s", buff);
#else
    snprintf(buff, sizeof(buff), "%s.index", p);
    if (unlink(buff) && errno != ENOENT)
        syswarn("cannot unlink %s", buff);
    snprintf(buff, sizeof(buff), "%s.hash", p);
    if (unlink(buff) && errno != ENOENT)
        syswarn("cannot unlink %s", buff);
#endif
}


/*
**  Count lines in the history text.  A long-winded way of saying "wc -l"
*/
static off_t
Countlines(void)
{
    QIOSTATE *qp;
    off_t count;

    /* Open the text file. */
    qp = QIOopen(TextFile);
    if (qp == NULL)
        sysdie("cannot open %s", TextFile);

    /* Loop through all lines in the text file.  Lines too long for QIO are
       counted too:  Rebuild() skips them but they take a line. */
    count = 0;
    while (QIOread(qp) != NULL || QIOtoolong(qp))
        count++;
    if (QIOerror(qp))
        sysdie("cannot read %s near line %lu", TextFile,
               (unsigned long) count);

    QIOclose(qp);
    return count;
}


/*
**  Check the key of a history line:  "[", the hash as hexadecimal digits,
**  "]" and nothing else.  TextToHash would silently decode anything.
*/
static bool
ValidHashText(const char *p)
{
    size_t i;

    if (*p != '[')
        return false;
    for (i = 1; i <= sizeof(HASH) * 2; i++)
        if (!isxdigit((unsigned char) p[i]))
            return false;
    return p[i] == ']' && p[i + 1] == '\0';
}


/*
**  Rebuild the DBZ file from the text file.  Malformed lines are reported
**  with their line number and skipped, so that one bad line does not leave
**  the whole history without a dbz index.  The articles of those lines
**  cannot be found by message-ID; expire discards the lines when it rewrites
**  the text file.
*/
static void
Rebuild(off_t size, bool IgnoreOld, bool Overwrite)
{
    QIOSTATE *qp;
    char *p;
    char *save;
    off_t count;
    unsigned long skipped = 0;
    off_t where;
    HASH key;
    char temp[SMBUF];
    dbzoptions opt;

    if (chdir(HistoryDir) < 0)
        sysdie("cannot chdir to %s", HistoryDir);

    /* If we are ignoring the old database and the user didn't specify a table
       size, determine one ourselves from the size of the text history file.
       Note that this will still use the defaults in dbz if the text file is
       empty, since size will still be left set to 0. */
    if (IgnoreOld == true && size == 0) {
        size = Countlines();
        size += (size / 10);
        if (size > 0)
            warn("no size specified, using %lu", (unsigned long) size);
    }

    /* Open the text file. */
    qp = QIOopen(TextFile);
    if (qp == NULL)
        sysdie("cannot open %s", TextFile);

    /* If using the standard history file, force DBZ to use history.n. */
    if (strcmp(TextFile, HISTORY) == 0 && !Overwrite) {
        snprintf(temp, sizeof(temp), "%s.n", HISTORY);
        if (link(HISTORY, temp) < 0)
            sysdie("cannot create temporary link to %s", temp);
        RemoveDBZFiles(temp);
        p = temp;
    } else {
        temp[0] = '\0';
        /*
        ** only do removedbz files if a. we're using something besides
        ** $pathhistory/history, or b. we're ignoring the old db.
        */
        if (strcmp(TextFile, HISTORY) != 0 || IgnoreOld)
            RemoveDBZFiles(TextFile);
        p = TextFile;
    }

    /* Open the new database, using the old file if desired and possible. */
    dbzgetoptions(&opt);
    opt.pag_incore = INCORE_MEM;
#ifndef DO_TAGGED_HASH
    opt.exists_incore = INCORE_MEM;
#endif
    dbzsetoptions(opt);
    if (IgnoreOld) {
        if (!dbzfresh(p, dbzsize(size))) {
            syswarn("cannot do dbzfresh");
            if (temp[0])
                unlink(temp);
            exit(1);
        }
    } else {
        if (!dbzagain(p, HISTORY)) {
            syswarn("cannot do dbzagain");
            if (temp[0])
                unlink(temp);
            exit(1);
        }
    }

    /* Loop through all lines in the text file. */
    count = 0;
    for (where = QIOtell(qp);; where = QIOtell(qp)) {
        if ((p = QIOread(qp)) == NULL) {
            if (!QIOtoolong(qp))
                break;
            count++;
            warn("line %lu: too long, skipping", (unsigned long) count);
            skipped++;
            continue;
        }
        count++;
        if ((save = strchr(p, HIS_FIELDSEP)) == NULL) {
            warn("line %lu: field separator missing, skipping: %.40s",
                 (unsigned long) count, p);
            skipped++;
            continue;
        }
        *save = '\0';
        if (*p != '[') {
            warn("line %lu: invalid message ID %.40s, skipping",
                 (unsigned long) count, p);
            skipped++;
            continue;
        }
        if (!ValidHashText(p)) {
            warn("line %lu: invalid hash %.40s, skipping",
                 (unsigned long) count, p);
            skipped++;
            continue;
        }
        key = TextToHash(p + 1);
        switch (dbzstore(key, where)) {
        case DBZSTORE_EXISTS:
            warn("line %lu: duplicate message ID %s in history text",
                 (unsigned long) count, p);
            break;
        case DBZSTORE_ERROR:
            syswarn("cannot store %s", p);
            if (temp[0])
                unlink(temp);
            exit(1);
        default:
            break;
        }
    }
    if (QIOerror(qp)) {
        syswarn("cannot read %s near line %lu", TextFile,
                (unsigned long) count);
        if (temp[0])
            unlink(temp);
        exit(1);
    }
    if (skipped > 0)
        warn("%lu malformed line%s skipped in %s", skipped,
             skipped == 1 ? "" : "s", TextFile);

    /* Close files. */
    QIOclose(qp);
    if (!dbzclose()) {
        syswarn("cannot close history");
        if (temp[0])
            unlink(temp);
        exit(1);
    }

    if (temp[0])
        unlink(temp);
}

static void
Usage(void)
{
    fprintf(stderr, "Usage: makedbz [-f histfile] [-s numlines] [-i] [-o]\n");
    exit(1);
}


int
main(int argc, char **argv)
{
    bool Overwrite;
    bool IgnoreOld;
    off_t size = 0;
    int i;
    char *p;

    /* First thing, set up logging and our identity. */
    openlog("makedbz", L_OPENLOG_FLAGS | LOG_PID, LOG_INN_PROG);
    message_program_name = "makedbz";

    /* Set defaults. */
    if (!innconf_read(NULL))
        exit(1);
    TextFile = concatpath(innconf->pathhistory, INN_PATH_HISTORY);
    HISTORY = concatpath(innconf->pathhistory, INN_PATH_HISTORY);
    HistoryDir = innconf->pathhistory;
    IgnoreOld = false;
    Overwrite = false;

    while ((i = getopt(argc, argv, "s:iof:")) != EOF) {
        switch (i) {
        default:
            Usage();
        case 'f':
            TextFile = optarg;
            break;
        case 's':
            size = atol(optarg);
            IgnoreOld = true;
            break;
        case 'o':
            Overwrite = true;
            break;
        case 'i':
            IgnoreOld = true;
            break;
        }
    }

    argc -= optind;
    if (argc) {
        Usage();
    }

    if ((p = strrchr(TextFile, '/')) == NULL) {
        /* find the default history file directory */
        HistoryDir = innconf->pathhistory;
    } else {
        *p = '\0';
        HistoryDir = xstrdup(TextFile);
        *p = '/';
    }

    if (chdir(HistoryDir) < 0)
        sysdie("cannot chdir to %s", HistoryDir);

    /* Change to the runasuser user and runasgroup group if necessary. */
    ensure_news_user_grp(true, true);

    Rebuild(size, IgnoreOld, Overwrite);
    closelog();
    exit(0);
}

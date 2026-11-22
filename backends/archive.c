/*
**  Read batchfiles on standard input and archive them.
*/

#include "portable/system.h"

#include <errno.h>
#include <sys/stat.h>
#include <time.h>

#include "inn/buffer.h"
#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/messages.h"
#include "inn/paths.h"
#include "inn/qio.h"
#include "inn/storage.h"
#include "inn/vector.h"
#include "inn/wire.h"

#ifndef PATH_MAX
#    define PATH_MAX 4096
#endif


/* Holds various configuration options and command-line parameters. */
struct config {
    const char *root;    /* Root of the archive. */
    const char *pattern; /* Wildmat pattern of groups to process. */
    FILE *index;         /* Where to put the index entries. */
    bool concat;         /* Concatenate articles together. */
    bool flat;           /* Use a flat directory structure. */
};


/*
**  Try to make one directory.  Return false on error.
*/
static bool
MakeDir(char *Name)
{
    struct stat Sb;

    if (mkdir(Name, GROUPDIR_MODE) >= 0)
        return true;

    /* See if it failed because it already exists. */
    return stat(Name, &Sb) >= 0 && S_ISDIR(Sb.st_mode);
}


/*
**  Given an entry, comp/foo/bar/1123, create the directory and all
**  parent directories needed.  Return false on error.
*/
static bool
mkpath(const char *file)
{
    char *path, *delim;
    bool status;

    path = xstrdup(file);
    delim = strrchr(path, '/');
    if (delim == NULL) {
        free(path);
        return false;
    }
    *delim = '\0';

    /* Optimize common case -- parent almost always exists. */
    if (MakeDir(path)) {
        free(path);
        return true;
    }

    /* Try to make each of comp and comp/foo in turn. */
    for (delim = path; *delim != '\0'; delim++)
        if (*delim == '/' && delim != path) {
            *delim = '\0';
            if (!MakeDir(path)) {
                free(path);
                return false;
            }
            *delim = '/';
        }
    status = MakeDir(path);
    free(path);
    return status;
}


/*
**  Write an article from memory into a file on disk.  Takes the handle of the
**  article, the file name into which to write it, and a flag saying whether
**  to concatenate the message to the end of an existing file if any.
*/
static bool
same_content(const char *file, const char *text, size_t length)
{
    FILE *in;
    char buf[BUFSIZ];
    size_t n, done = 0;
    bool same = true;

    in = fopen(file, "r");
    if (in == NULL)
        return false;
    while (same && (n = fread(buf, 1, sizeof(buf), in)) > 0) {
        if (done + n > length || memcmp(buf, text + done, n) != 0)
            same = false;
        done += n;
    }
    if (ferror(in) || done != length)
        same = false;
    fclose(in);
    return same;
}


static bool
write_article(ARTHANDLE *article, const char *file, bool concatenate)
{
    FILE *out;
    char *text = NULL;
    char *tmpname = NULL;
    const char *dest;
    size_t length = 0;
    int fd;

    /* A copy which exists already with the right content, from a previous run
     * which failed for another group, is left as it is (it can be linked to
     * the other copies). */
    if (!concatenate) {
        text = wire_to_native(article->data, article->len, &length);
        if (same_content(file, text, length)) {
            free(text);
            return true;
        }
        free(text);
        text = NULL;
    }

    /* Open the output file.  A copy which is not appended to is written to a
     * new file next to it, which replaces it at the end: an existing copy
     * can be a hard link to another copy of the article, and a failed retry
     * must not damage them. */
    if (concatenate) {
        dest = file;
        out = fopen(file, "a");
        if (out == NULL && errno == ENOENT) {
            if (!mkpath(file)) {
                syswarn("cannot mkdir for %s", file);
                return false;
            }
            out = fopen(file, "a");
        }
    } else {
        xasprintf(&tmpname, "%s.XXXXXX", file);
        dest = tmpname;
        fd = mkstemp(tmpname);
        if (fd < 0 && errno == ENOENT) {
            if (!mkpath(file)) {
                syswarn("cannot mkdir for %s", file);
                free(tmpname);
                return false;
            }
            /* mkstemp() can have replaced the X's even though it failed. */
            strcpy(tmpname + strlen(tmpname) - 6, "XXXXXX");
            fd = mkstemp(tmpname);
        }
        out = NULL;
        if (fd >= 0) {
            /* mkstemp() makes a private file. */
            fchmod(fd, 0666 & ~NEWSUMASK);
            out = fdopen(fd, "w");
            if (out == NULL) {
                close(fd);
                unlink(tmpname);
            }
        }
    }
    if (out == NULL) {
        syswarn("cannot open %s for writing", dest);
        free(tmpname);
        return false;
    }

    /* Get the data in wire format and write it out to the file. */
    text = wire_to_native(article->data, article->len, &length);
    if (concatenate)
        fprintf(out, "-----------\n");
    if (fwrite(text, length, 1, out) != 1) {
        syswarn("cannot write to %s", dest);
        fclose(out);
        if (!concatenate)
            unlink(tmpname);
        free(tmpname);
        free(text);
        return false;
    }
    free(text);

    /* Flush and close the output. */
    if (ferror(out) || fflush(out) == EOF) {
        syswarn("cannot flush %s", dest);
        fclose(out);
        if (!concatenate)
            unlink(tmpname);
        free(tmpname);
        return false;
    }
    if (fclose(out) == EOF) {
        syswarn("cannot close %s", dest);
        if (!concatenate)
            unlink(tmpname);
        free(tmpname);
        return false;
    }
    if (!concatenate && rename(tmpname, file) < 0) {
        syswarn("cannot rename %s to %s", tmpname, file);
        unlink(tmpname);
        free(tmpname);
        return false;
    }
    free(tmpname);
    return true;
}


/*
**  Link an article.  First try a hard link, then a soft link, and if both
**  fail, write the article out again to the new path.
*/
static bool
link_article(const char *oldpath, const char *newpath, ARTHANDLE *art)
{
    struct stat oldst, newst;
    char target[PATH_MAX];
    ssize_t n;

    /* A previous run which failed for another group may have made this link
     * already: leave it. */
    if (stat(oldpath, &oldst) == 0 && lstat(newpath, &newst) == 0) {
        if (S_ISLNK(newst.st_mode)) {
            n = readlink(newpath, target, sizeof(target) - 1);
            if (n > 0) {
                target[n] = '\0';
                if (strcmp(target, oldpath) == 0)
                    return true;
            }
        } else if (oldst.st_dev == newst.st_dev
                   && oldst.st_ino == newst.st_ino) {
            return true;
        }
    }
    if (link(oldpath, newpath) < 0) {
        if (!mkpath(newpath)) {
            syswarn("cannot mkdir for %s", newpath);
            return false;
        }
        if (link(oldpath, newpath) < 0)
            if (symlink(oldpath, newpath) < 0)
                if (!write_article(art, newpath, false))
                    return false;
    }
    return true;
}


/*
**  Write out a single header field to stdout, applying the standard overview
**  transformation to it.  This code is partly stolen from overdata.c; it
**  would be nice to find a way to only write this in one place.
*/
static void
write_index_header(FILE *index, ARTHANDLE *art, const char *header)
{
    const char *start, *end, *p;

    start = wire_findheader(art->data, art->len, header, false);
    if (start == NULL) {
        fprintf(index, "<none>");
        return;
    }
    end = wire_endheader(start, art->data + art->len - 1);
    if (end == NULL) {
        fprintf(index, "<none>");
        return;
    }
    for (p = start; p <= end; p++) {
        if (*p == '\r' && p < end && p[1] == '\n') {
            p++;
            continue;
        }
        if (*p == '\0' || *p == '\t' || *p == '\r' || *p == '\n')
            putc(' ', index);
        else
            putc(*p, index);
    }
}


/*
**  Write an index entry to standard output.  This is the path (without the
**  archive root), the message-ID of the article, and the subject.
*/
static void
write_index(FILE *index, ARTHANDLE *art, const char *file)
{
    fprintf(index, "%s ", file);
    write_index_header(index, art, "Subject");
    putc(' ', index);
    write_index_header(index, art, "Message-ID");
    fprintf(index, "\n");
    if (ferror(index) || fflush(index) == EOF)
        syswarn("cannot write index for %s", file);
}


/*
**  Build the archive path for a particular article.  Takes a pointer to the
**  (nul-terminated) group name and a pointer to the article number as a
**  string, as well as the config struct.  Also takes a buffer to use to build
**  the path, which may be NULL to allocate a new buffer.  Returns the path to
**  which to write the article as a buffer (but still nul-terminated).
*/
static struct buffer *
build_path(const char *group, const char *number, struct config *config,
           struct buffer *path)
{
    char *p;

    /* Initialize the path buffer to config-root followed by /. */
    if (path == NULL)
        path = buffer_new();
    buffer_set(path, config->root, strlen(config->root));
    buffer_append(path, "/", 1);

    /* Append the group name, replacing dots with slashes unless we're using a
       flat structure. */
    p = path->data + path->left;
    buffer_append(path, group, strlen(group));
    if (!config->flat)
        for (; (size_t) (p - path->data) < path->left; p++)
            if (*p == '.')
                *p = '/';

    /* If we're saving by date, append the date now.  Otherwise, append the
       group number. */
    if (config->concat) {
        struct tm *tm;
        time_t now;
        int year, month;

        now = time(NULL);
        tm = localtime(&now);
        year = tm->tm_year + 1900;
        month = tm->tm_mon + 1;
        buffer_append_sprintf(path, "/%04d%02d", year, month);
    } else {
        buffer_append(path, "/", 1);
        buffer_append(path, number, strlen(number));
    }
    buffer_append(path, "", 1);
    return path;
}


/*
**  Process a single article, saving it to the appropriate file or files (if
**  crossposted).  Return false if it could not be saved to one of them.
*/
static bool
process_article(ARTHANDLE *art, const char *token, struct config *config)
{
    char *start, *end, *xref, *delim, *p, *first;
    const char *group;
    size_t i;
    struct cvector *groups;
    struct buffer *path = NULL;
    bool ok = true;

    /* Determine the groups from the Xref header field.  In groups will be the
     * split Xref header field body; from the second string on should be a
     * group, a colon, and an article number. */
    start = wire_findheader(art->data, art->len, "Xref", true);
    if (start == NULL) {
        warn("cannot find Xref header field in %s", token);
        return true;
    }
    end = wire_endheader(start, art->data + art->len - 1);
    if (end == NULL) {
        warn("unterminated Xref header field in %s", token);
        return true;
    }
    xref = xstrndup(start, end - start);
    for (p = xref; *p != '\0'; p++)
        if (*p == '\r' || *p == '\n')
            *p = ' ';
    groups = cvector_split_space(xref, NULL);
    if (groups->count < 2) {
        warn("bogus Xref header field in %s", token);
        free(xref);
        cvector_free(groups);
        return true;
    }

    /* Walk through each newsgroup, saving the article in the appropriate
       location. */
    first = NULL;
    for (i = 1; i < groups->count; i++) {
        group = groups->strings[i];
        delim = (char *) strchr(group, ':');
        if (delim == NULL) {
            warn("bogus Xref entry %s in %s", group, token);
            continue;
        }
        *delim = '\0';

        /* Skip newsgroups that don't match our pattern, if provided. */
        if (config->pattern != NULL) {
            if (uwildmat_poison(group, config->pattern) != UWILDMAT_MATCH)
                continue;
        }

        /* Get the path to which to write the article. */
        path = build_path(group, delim + 1, config, path);

        /* If this isn't the first group, and we're not saving by date, try to
           just link or symlink between the archive directories rather than
           writing out multiple copies. */
        if (first == NULL || config->concat) {
            if (!write_article(art, path->data, config->concat)) {
                ok = false;
                continue;
            }
            if (groups->count > 2)
                first = xstrdup(path->data);
        } else {
            if (!link_article(first, path->data, art)) {
                ok = false;
                continue;
            }
        }

        /* Write out the index if desired. */
        if (config->index)
            write_index(config->index, art,
                        path->data + strlen(config->root) + 1);
    }
    free(xref);
    cvector_free(groups);
    if (path != NULL)
        buffer_free(path);
    if (first != NULL)
        free(first);
    return ok;
}


/*
**  Open the file in which the tokens still to archive are spooled, for the
**  given input file (NULL for the standard input).  Return the stream and
**  set *file to the name of the spool file.
*/
static FILE *
open_spool(const char *input, char **file)
{
    FILE *spool;

    if (input == NULL)
        *file = concatpath(innconf->pathoutgoing, "archive");
    else if (input[0] == '/')
        *file = concat(input, ".bch", (char *) 0);
    else
        *file = concat(innconf->pathoutgoing, "/", input, ".bch", (char *) 0);
    spool = fopen(*file, "a");
    if (spool == NULL)
        sysdie("cannot spool to %s", *file);
    return spool;
}


int
main(int argc, char *argv[])
{
    struct config config = {NULL, NULL, NULL, 0, 0};
    int option, status = 0;
    bool redirect = true;
    QIOSTATE *qp;
    char *line, *file;
    TOKEN token;
    ARTHANDLE *art;
    FILE *spool = NULL;
    bool failed = false;
    char buffer[BUFSIZ];

    /* First thing, set up our identity. */
    message_program_name = "archive";

    /* Set defaults. */
    if (!innconf_read(NULL))
        exit(1);
    config.root = innconf->patharchive;
    umask(NEWSUMASK);

    /* Parse options. */
    while ((option = getopt(argc, argv, "a:cfi:p:r")) != EOF)
        switch (option) {
        default:
            die("usage error");
            /* NOTREACHED */
        case 'a':
            config.root = optarg;
            break;
        case 'c':
            config.flat = true;
            config.concat = true;
            break;
        case 'f':
            config.flat = true;
            break;
        case 'i':
            config.index = fopen(optarg, "a");
            if (config.index == NULL)
                sysdie("cannot open index %s for output", optarg);
            break;
        case 'p':
            config.pattern = optarg;
            break;
        case 'r':
            redirect = false;
            break;
        }

    /* Parse arguments, which should just be the batch file. */
    argc -= optind;
    argv += optind;
    if (argc > 1)
        die("usage error");
    if (redirect) {
        file = concatpath(innconf->pathlog, INN_PATH_ERRLOG);
        if (freopen(file, "a", stderr) == NULL)
            sysdie("cannot open %s for error output", file);
    }
    if (argc == 1)
        if (freopen(argv[0], "r", stdin) == NULL)
            sysdie("cannot open %s for input", argv[0]);

    /* Initialize the storage manager. */
    if (!SMinit())
        die("cannot initialize storage manager: %s", SMerrorstr);

    /* Read input. */
    qp = QIOfdopen(fileno(stdin));
    if (qp == NULL)
        sysdie("cannot reopen input");
    while ((line = QIOread(qp)) != NULL) {
        if (*line == '\0' || *line == '#')
            continue;

        /* Currently, we only handle tokens.  It would be good to handle
           regular files as well, if for no other reason than for testing, but
           we need a good way of faking an ARTHANDLE from a file. */
        if (IsToken(line)) {
            token = TextToToken(line);
            art = SMretrieve(token, RETR_ALL);
            if (art == NULL) {
                warn("cannot retrieve %s", line);
                continue;
            }
            if (!process_article(art, line, &config)) {
                /* Some copy could not be written, for instance because the
                 * archive is full.  Keep the token to try it again. */
                warn("cannot archive %s completely, keeping it", line);
                failed = true;
                if (spool == NULL)
                    spool = open_spool(argv[0], &file);
                if (fprintf(spool, "%s\n", line) == EOF) {
                    syswarn("cannot write to spool");
                    status = 1;
                }
            }
            SMfreearticle(art);
        } else {
            warn("%s is not a token", line);
        }
    }

    /* Close down the storage manager API. */
    SMshutdown();

    /* If we read all our input and archived everything, try to remove the
     * file, and we're done. */
    if (!QIOerror(qp) && !failed) {
        fclose(stdin);
        if (argv[0])
            unlink(argv[0]);
        exit(0);
    }

    /* If we could not read all the named input, leave it as it is: QIO has
     * read an unknown part of it, so that there is no way to tell what was
     * not read.  The tokens already archived are archived again by the next
     * run. */
    if (QIOerror(qp) && argv[0] != NULL) {
        warn("cannot read %s completely, keeping it unchanged", argv[0]);
        if (spool != NULL) {
            fclose(spool);
            unlink(file);
        }
        exit(1);
    }

    /* Otherwise, make an appropriate spool file, which holds the tokens
     * which could not be archived already if there are any. */
    if (spool == NULL)
        spool = open_spool(argv[0], &file);

    /* If we could not read all our input, write the rest of stdin to the
     * spool file. */
    if (QIOerror(qp)) {
        if (fprintf(spool, "%s\n", line != NULL ? line : "") == EOF) {
            syswarn("cannot start spool");
            status = 1;
        }
        while (fgets(buffer, sizeof(buffer), stdin) != NULL)
            if (fputs(buffer, spool) == EOF) {
                syswarn("cannot write to spool");
                status = 1;
                break;
            }
        if (ferror(stdin)) {
            syswarn("cannot read the rest of the input");
            status = 1;
        }
    }
    if (fclose(spool) == EOF) {
        syswarn("cannot close spool");
        status = 1;
    }

    /* If we had a named input file, replace it with the spool, but only if
     * the spool is complete.  Otherwise keep the input file as it is. */
    if (argv[0] != NULL) {
        if (status != 0) {
            warn("keeping %s unchanged", argv[0]);
            unlink(file);
        } else if (rename(file, argv[0]) < 0) {
            syswarn("cannot rename spool");
            status = 1;
        }
    }

    exit(failed ? 1 : status);
}

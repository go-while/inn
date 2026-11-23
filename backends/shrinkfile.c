/*
**  Shrink files on line boundaries.
**
**  Written by Landon Curt Noll <chongo@toad.com>, and placed in the
**  public domain.  Rewritten for INN by Rich Salz.
**
**  Usage:
**      shrinkfile [-nv] [-m maxsize] [-s size] file [file ...]
**      -n              No writes, exit 0 if any file is too large, 1 otherwise
**      -s size         Truncation size (0 default); suffix may be k, m,
**                      or g to scale.  Must not be larger than 2^31 - 1.
**      -m maxsize      Maximum size allowed before truncation.  If maxsize
**                      <= size, then it is reset to size.  Default == size.
**      -v              Print status line.
**
**  Files will be shrunk at end of line boundary.  In no case will the
**  file be longer than size bytes if it was longer than maxsize bytes.
**  If the first line is longer than the absolute value of size, the file
**  will be truncated to zero length.
**
**  The -n flag may be used to determine if any file is too large.  No
**  files will be altered in this mode.
*/

#include "portable/system.h"

#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>

#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/messages.h"

#define MAX_SIZE 0x7fffffffUL

#ifndef PATH_MAX
#    define PATH_MAX 4096
#endif

static void Usage(void) __attribute__((__noreturn__));


/*
**  Create a temporary file next to the file to replace, so that the final
**  rename stays on the same file system.  Return the stream and set *tmpname
**  to the name of the file, to free by the caller.
*/
static FILE *
OpenStaging(const char *name, char **tmpname)
{
    FILE *F;
    int fd;

    xasprintf(tmpname, "%s.shrinkXXXXXX", name);
    fd = mkstemp(*tmpname);
    if (fd < 0) {
        syswarn("cannot create temporary file %s", *tmpname);
        free(*tmpname);
        *tmpname = NULL;
        return NULL;
    }
    F = fdopen(fd, "w");
    if (F == NULL) {
        syswarn("cannot fdopen %s", *tmpname);
        close(fd);
        unlink(*tmpname);
        free(*tmpname);
        *tmpname = NULL;
    }
    return F;
}


/*
**  Does file end with \n?  Assume it does on I/O error, to avoid doing I/O.
*/
static int
EndsWithNewline(FILE *F)
{
    int c;

    if (fseeko(F, -1, SEEK_END) < 0) {
        /* An empty file has no last byte, and needs no newline. */
        if (errno != EINVAL)
            syswarn("cannot seek to end of file");
        return true;
    }

    /* return the actual character or EOF */
    if ((c = fgetc(F)) == EOF) {
        if (ferror(F))
            syswarn("cannot read last byte");
        return true;
    }
    return c == '\n';
}


/*
**  Add a newline to location of a file.
*/
static bool
AppendNewline(char *name)
{
    FILE *F;

    if ((F = xfopena(name)) == NULL) {
        syswarn("cannot add newline");
        return false;
    }

    if (fputc('\n', F) == EOF || fflush(F) == EOF || ferror(F)
        || fclose(F) == EOF) {
        syswarn("cannot add newline");
        return false;
    }

    return true;
}

/*
**  Just check if it is too big.
*/
static bool
TooBig(FILE *F, off_t maxsize)
{
    struct stat Sb;

    /* Get the file's size. */
    if (fstat((int) fileno(F), &Sb) < 0) {
        syswarn("cannot fstat");
        return false;
    }

    /* return true if too large */
    return (maxsize > Sb.st_size ? false : true);
}

/*
**  This routine does all the work.
*/
static bool
Process(FILE *F, char *name, off_t size, off_t maxsize, bool *Changedp)
{
    off_t len;
    FILE *tmp;
    struct stat Sb;
    char buff[BUFSIZ + 1];
    char resolved[PATH_MAX];
    char *tmpname;
    struct stat Tb;
    int c;
    size_t i;
    size_t copied = 0;
    bool err;
    bool need_newline = false;

    /* Get the file's size. */
    if (fstat((int) fileno(F), &Sb) < 0) {
        syswarn("cannot fstat");
        return false;
    }
    len = Sb.st_size;

    /* Process a zero size request. */
    if (size == 0 && len > maxsize) {
        if (len > 0) {
            fclose(F);
            if ((F = fopen(name, "w")) == NULL) {
                syswarn("cannot overwrite");
                return false;
            }
            fclose(F);
            *Changedp = true;
        }
        return true;
    }

    /* See if already small enough. */
    if (len <= maxsize) {
        /* Newline already present? */
        if (EndsWithNewline(F)) {
            fclose(F);
            return true;
        }

        /* No newline, add it if it fits.  Else the file is small enough, and
         * is left as it is. */
        fclose(F);
        if (len < size) {
            *Changedp = true;
            return AppendNewline(name);
        }
        return true;
    } else if (!EndsWithNewline(F)) {
        /* The newline is added to the new file, never to the original, which
         * stays as it is until the new file is complete. */
        need_newline = true;
    }

    /* We now have a file that ends with a newline that is bigger than
     * we want.  Starting from {size} bytes from end, move forward
     * until we get a newline. */
    if (fseeko(F, -size, SEEK_END) < 0) {
        syswarn("cannot fseeko");
        fclose(F);
        return false;
    }

    while ((c = getc(F)) != '\n')
        if (c == EOF) {
            /* The missing final newline ends the line. */
            if (need_newline && !ferror(F)) {
                need_newline = false;
                break;
            }
            syswarn("cannot read");
            fclose(F);
            return false;
        }

    /* Copy the rest of the file to a temporary file in the same directory.
     * The original is replaced only when the copy is complete and has
     * reached the disk. */
    if (realpath(name, resolved) == NULL) {
        syswarn("cannot find the real name of %s", name);
        fclose(F);
        return false;
    }
    tmp = OpenStaging(resolved, &tmpname);
    if (tmp == NULL) {
        fclose(F);
        return false;
    }
    err = false;
    while ((i = fread(buff, 1, sizeof buff, F)) > 0) {
        copied += i;
        if (fwrite(buff, 1, i, tmp) != i) {
            err = true;
            break;
        }
    }
    if (ferror(F))
        err = true;
    if (!err && need_newline && copied > 0 && fputc('\n', tmp) == EOF)
        err = true;
    if (err) {
        syswarn("cannot copy to temporary file %s", tmpname);
        fclose(F);
        fclose(tmp);
        unlink(tmpname);
        free(tmpname);
        return false;
    }
    fclose(F);

    /* Give the new file the owner and the mode of the old one, in this
     * order as changing the owner can remove set-ID bits, and check that
     * it worked. */
    if (fflush(tmp) == EOF || ferror(tmp) || fsync(fileno(tmp)) < 0) {
        syswarn("cannot write temporary file %s", tmpname);
        err = true;
    } else {
        /* An unprivileged user cannot always give a file to another one;
         * it is only an error if the owner or group would change. */
        if (fchown(fileno(tmp), Sb.st_uid, Sb.st_gid) < 0 && errno != EPERM)
            syswarn("cannot change the owner of %s", tmpname);
        if (fchmod(fileno(tmp), Sb.st_mode & 07777) < 0
            || fstat(fileno(tmp), &Tb) < 0 || Tb.st_uid != Sb.st_uid
            || Tb.st_gid != Sb.st_gid
            || (Tb.st_mode & 07777) != (Sb.st_mode & 07777)) {
            syswarn("cannot give the owner and permissions of %s to %s", name,
                    tmpname);
            err = true;
        }
    }
    if (fclose(tmp) == EOF && !err) {
        syswarn("cannot close temporary file %s", tmpname);
        err = true;
    }
    if (!err && rename(tmpname, resolved) < 0) {
        syswarn("cannot replace %s", resolved);
        err = true;
    }
    if (err)
        unlink(tmpname);
    free(tmpname);
    if (err)
        return false;
    *Changedp = true;
    return true;
}


/*
**  Convert size argument to numeric value.  Return -1 on error.
*/
static off_t
ParseSize(char *p)
{
    off_t scale;
    unsigned long str_num;
    char *q;

    /* Skip leading spaces */
    while (ISWHITE(*p))
        p++;
    if (*p == '\0')
        return -1;

    /* determine the scaling factor */
    q = &p[strlen(p) - 1];
    switch (*q) {
    default:
        return -1;
    case '0':
    case '1':
    case '2':
    case '3':
    case '4':
    case '5':
    case '6':
    case '7':
    case '8':
    case '9':
        scale = 1;
        break;
    case 'k':
    case 'K':
        scale = 1024;
        *q = '\0';
        break;
    case 'm':
    case 'M':
        scale = 1024 * 1024;
        *q = '\0';
        break;
    case 'g':
    case 'G':
        scale = 1024 * 1024 * 1024;
        *q = '\0';
        break;
    }

    /* Convert string to number. */
    if (sscanf(p, "%lud", &str_num) != 1)
        return -1;
    if (str_num > MAX_SIZE / scale)
        die("size is too big");

    return scale * str_num;
}


/*
**  Print usage message and exit.
*/
static void
Usage(void)
{
    fprintf(
        stderr,
        "Usage: shrinkfile [-nv] [-m maxsize] [-s size] file [file ...]\n");
    exit(1);
}


int
main(int ac, char *av[])
{
    bool Changed;
    bool Failed = false;
    bool Verbose;
    bool no_op;
    FILE *F;
    char *p;
    int i;
    off_t size = 0;
    off_t maxsize = 0;

    /* First thing, set up our identity. */
    message_program_name = "shrinkfile";

    /* Set defaults. */
    Verbose = false;
    no_op = false;
    umask(NEWSUMASK);

    if (!innconf_read(NULL))
        exit(1);

    /* Parse JCL. */
    while ((i = getopt(ac, av, "m:s:vn")) != EOF)
        switch (i) {
        default:
            Usage();
            /* NOTREACHED */
        case 'n':
            no_op = true;
            break;
        case 'm':
            if ((maxsize = ParseSize(optarg)) < 0)
                Usage();
            break;
        case 's':
            if ((size = ParseSize(optarg)) < 0)
                Usage();
            break;
        case 'v':
            Verbose = true;
            break;
        }
    if (maxsize < size) {
        maxsize = size;
    }
    ac -= optind;
    av += optind;
    if (ac == 0)
        Usage();

    while ((p = *av++) != NULL) {
        if ((F = fopen(p, "r")) == NULL) {
            syswarn("cannot open %s", p);
            Failed = true;
            continue;
        }

        /* If -n (no_op). */
        if (no_op) {
            /* Check if too big and exit zero if it is. */
            if (TooBig(F, maxsize)) {
                if (Verbose)
                    notice("%s is too large", p);
                exit(0);
                /* NOTREACHED */
            }
        } else {
            /* No -n, do some real work. */
            Changed = false;
            if (!Process(F, p, size, maxsize, &Changed)) {
                syswarn("cannot shrink %s", p);
                Failed = true;
            } else if (Verbose && Changed)
                notice("shrunk %s", p);
        }
    }
    if (no_op && Verbose) {
        notice("did not find a file that was too large");
    }

    /* If -n, then exit non-zero to indicate no file too big.  Otherwise, exit
     * non-zero if a file could not be shrunk. */
    exit(no_op || Failed ? 1 : 0);
    /* NOTREACHED */
}

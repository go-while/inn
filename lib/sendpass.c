#include "portable/system.h"

#include <ctype.h>
#include <errno.h>

#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/nntp.h"
#include "inn/paths.h"


/*
**  Send authentication information to an NNTP server.  On failure, errno
**  describes a local error when available and is zero for a rejected reply
**  or a clean EOF from the peer.
*/
int
NNTPsendpassword(char *server, FILE *FromServer, FILE *ToServer)
{
    FILE *F;
    char *p;
    char *path;
    char buff[SMBUF];
    char input[SMBUF];
    char *user;
    char *pass;
    char *style;
    int oerrno;
    bool authenticated;

    /* Default to innconf->server.  If that's not set either, error out.  Fake
       errno since some of our callers rely on it. */
    if (server == NULL)
        server = innconf->server;
    if (server == NULL) {
        errno = EINVAL;
        return -1;
    }

    /* Open the password file; coarse check on errno, but good enough. */
    path = concatpath(innconf->pathetc, INN_PATH_NNTPPASS);
    F = fopen(path, "r");
    oerrno = errno;
    free(path);
    errno = oerrno;
    if (F == NULL)
        return errno == EPERM ? -1 : 0;

    /* Scan the file, skipping blank and comment lines. */
    for (;;) {
        int response;

        errno = 0;
        if (fgets(buff, sizeof buff, F) == NULL)
            break;
        if ((p = strchr(buff, '\n')) != NULL)
            *p = '\0';
        if (buff[0] == '\0' || buff[0] == '#')
            continue;

        /* Parse the line. */
        if ((user = strchr(buff, ':')) == NULL)
            continue;
        *user++ = '\0';
        if ((pass = strchr(user, ':')) == NULL)
            continue;
        *pass++ = '\0';
        if ((style = strchr(pass, ':')) != NULL) {
            *style++ = '\0';
            if (strcmp(style, "authinfo") != 0) {
                errno = E2BIG;
                break;
            }
        }

        if (strcasecmp(server, buff) != 0)
            continue;

        authenticated = false;

        if (*user) {
            /* Send the first part of the command, get a reply. */
            errno = 0;
            if (fprintf(ToServer, "AUTHINFO USER %s\r\n", user) < 0
                || fflush(ToServer) == EOF || ferror(ToServer))
                break;
            errno = 0;
            if (fgets(input, sizeof input, FromServer) == NULL)
                break;
            response = atoi(input);
            errno = 0;
            if (response == NNTP_OK_AUTHINFO)
                authenticated = true;
            else if (response != NNTP_CONT_AUTHINFO)
                break;
        }

        if (*pass && !authenticated) {
            /* Send the second part of the command, get a reply. */
            errno = 0;
            if (fprintf(ToServer, "AUTHINFO PASS %s\r\n", pass) < 0
                || fflush(ToServer) == EOF || ferror(ToServer))
                break;
            errno = 0;
            if (fgets(input, sizeof input, FromServer) == NULL)
                break;
            response = atoi(input);
            errno = 0;
            if (response != NNTP_OK_AUTHINFO)
                break;
        }

        /* Authenticated. */
        fclose(F);
        return 0;
    }

    /* End of file without finding a password, that's okay. */
    if (feof(F)) {
        fclose(F);
        return 0;
    }

    /* Save errno, close the file, fail. */
    oerrno = errno;
    fclose(F);
    errno = oerrno;
    return -1;
}

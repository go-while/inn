/*
**  User and post tracking database.
*/

#include "portable/system.h"

#include "inn/innconf.h"
#include "nnrpd.h"

#define MAX_LEN 180

/* Match the complete name, allowing a wildcard between two literal parts. */
static bool
match_host(const char *host, const char *pattern)
{
    const char *star = strchr(pattern, '*');
    size_t length, prefix, suffix;

    if (star == NULL)
        return strcasecmp(host, pattern) == 0;
    length = strlen(host);
    prefix = star - pattern;
    suffix = strlen(star + 1);
    return length >= prefix + suffix && strncasecmp(host, pattern, prefix) == 0
           && strcasecmp(host + length - suffix, star + 1) == 0;
}

/*
**  TrackClient determines whether or not
**  we are interested in tracking the activities
**  of the currently connected host.  We have to
**  rely on an external process to set up the
**  entries in the database though, which makes
**  this only as reliable as the process that
**  sets this up...
*/

/*
**  Format of the input line is the following one.
**    <host>:<username>
*/

int
TrackClient(char *client, char *user, size_t len)
{
    int RARTon;
    FILE *fd;
    char line[MAX_LEN], *p;
    char *dbfile;

    dbfile = concatpath(innconf->pathetc, "nnrpd.track");

    RARTon = false;
    strlcpy(user, "unknown", len);

    if ((fd = fopen(dbfile, "r")) != NULL) {
        while ((fgets(line, (MAX_LEN - 1), fd)) != NULL) {
            if (line[0] == '#' || line[0] == '\n')
                continue;
            if ((p = strchr(line, ' ')) != NULL)
                *p = '\0';
            if ((p = strchr(line, '\n')) != NULL)
                *p = '\0';
            if ((p = strchr(line, ':')) != NULL) {
                *p++ = '\0';
            } else {
                p = NULL;
            }
            if (line[0] != '\0' && match_host(client, line)) {
                RARTon = true;
                if (p != NULL)
                    strlcpy(user, p, len);
                break;
            }
        }
        fclose(fd);
    } else {
        RARTon = false;
        syslog(L_NOTICE, "%s No logging -- can't read %s", Client.host,
               dbfile);
    }

    free(dbfile);
    return RARTon;
}

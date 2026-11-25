/*
**  User and post tracking database.
*/

#include "portable/system.h"

#include "inn/innconf.h"
#include "inn/messages.h"
#include "nnrpd.h"

#define MAX_LEN 180

/* A leading or trailing dot also accepts legacy suffix and prefix rules. */
static bool
match_host(const char *host, const char *pattern)
{
    const char *star = strchr(pattern, '*');
    size_t length = strlen(host);
    size_t prefix, suffix;

    if (star == NULL) {
        size_t n = strlen(pattern);

        if (pattern[0] == '.')
            return length >= n && strcasecmp(host + length - n, pattern) == 0;
        if (pattern[n - 1] == '.')
            return strncasecmp(host, pattern, n) == 0;
        return strcasecmp(host, pattern) == 0;
    }
    prefix = star - pattern;
    suffix = strlen(star + 1);
    return length >= prefix + suffix && strncasecmp(host, pattern, prefix) == 0
           && strcasecmp(host + length - suffix, star + 1) == 0;
}

/* Readers run in separate processes.  Use a marker in the run directory
 * so that a legacy catch-all does not generate a warning per connection. */
static void
warn_empty_host(void)
{
    static bool warned;
    char *marker;
    int fd;

    if (warned)
        return;
    marker = concatpath(innconf->pathrun, "nnrpd.track.warned");
    fd = open(marker, O_WRONLY | O_CREAT | O_EXCL, 0600);
    if (fd >= 0 || errno != EEXIST)
        warn("empty host field in nnrpd.track matches every client");
    if (fd >= 0)
        close(fd);
    free(marker);
    warned = true;
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
TrackClient(const char *client, char *user, size_t len)
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
            if (line[0] == '\0') {
                if (p == NULL)
                    continue;
                warn_empty_host();
            } else if (!match_host(client, line)) {
                continue;
            }
            RARTon = true;
            if (p != NULL)
                strlcpy(user, p, len);
            break;
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

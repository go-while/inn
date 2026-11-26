/*
**  Test suite for dropping root privileges with ensure_news_user_grp().
**
**  Started as root with supplementary groups that the news user does not
**  have, the process must keep none of them once it runs as the news user
**  and group.  Needs root and the news user and group; skipped otherwise.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <grp.h>
#include <sys/wait.h>

#include "inn/libinn.h"
#include "inn/newsuser.h"
#include "tap/basic.h"

/* Supplementary groups given to the process before the drop: the group of
   root and an arbitrary GID, none of them the news group. */
#define EXTRA_GID ((gid_t) 54321)

/* Results reported by the child process. */
struct result {
    int user_ok;   /* Real and effective UIDs are the news user. */
    int group_ok;  /* Real and effective GIDs are the news group. */
    int ngroups;   /* Number of supplementary groups after the drop. */
    int foreign;   /* Number of them that are not the news group. */
};

int
main(void)
{
    uid_t uid;
    gid_t gid;
    gid_t start[2];
    gid_t *groups;
    int fds[2];
    int n, i, status;
    pid_t pid;
    struct result r;
    ssize_t got;

    if (geteuid() != 0)
        skip_all("must be run as root");
    if (get_news_uid_gid(&uid, &gid, false) != 0)
        skip_all("news user or group not found");
    if (uid == 0 || gid == 0 || gid == EXTRA_GID)
        skip_all("news user or group unsuitable for this test");

    plan(5);

    if (pipe(fds) < 0)
        sysbail("cannot create pipe");
    pid = fork();
    if (pid < 0)
        sysbail("cannot fork");
    if (pid == 0) {
        close(fds[0]);
        memset(&r, 0, sizeof(r));
        start[0] = 0;
        start[1] = EXTRA_GID;
        if (setgroups(2, start) < 0)
            _exit(2);
        ensure_news_user_grp(true, true);
        r.user_ok = (getuid() == uid && geteuid() == uid);
        r.group_ok = (getgid() == gid && getegid() == gid);
        n = getgroups(0, NULL);
        if (n < 0)
            _exit(3);
        groups = xcalloc(n + 1, sizeof(gid_t));
        n = getgroups(n, groups);
        if (n < 0)
            _exit(3);
        r.ngroups = n;
        for (i = 0; i < n; i++)
            if (groups[i] != gid)
                r.foreign++;
        if (write(fds[1], &r, sizeof(r)) != (ssize_t) sizeof(r))
            _exit(4);
        _exit(0);
    }
    close(fds[1]);
    memset(&r, 0, sizeof(r));
    got = read(fds[0], &r, sizeof(r));
    close(fds[0]);
    if (waitpid(pid, &status, 0) != pid)
        sysbail("cannot wait for child");

    ok(got == (ssize_t) sizeof(r) && WIFEXITED(status)
           && WEXITSTATUS(status) == 0,
       "child dropped privileges and reported");
    ok(r.user_ok, "runs as the news user");
    ok(r.group_ok, "runs as the news group");
    is_int(0, r.foreign, "no supplementary group of root is kept");
    ok(r.ngroups <= 1, "at most the news group is left (%d groups)",
       r.ngroups);
    return 0;
}

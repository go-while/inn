/* Test suite for raising the soft file descriptor limit. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <errno.h>
#if HAVE_GETRLIMIT && HAVE_SETRLIMIT
#    if HAVE_SYS_TIME_H
#        include <sys/time.h>
#    endif
#    include <sys/resource.h>
#endif

#include "inn/libinn.h"
#include "tap/basic.h"


int
main(void)
{
#if HAVE_GETRLIMIT && HAVE_SETRLIMIT && defined(RLIMIT_NOFILE)
    struct rlimit original, limited, probe, current;
    bool hard_accepted = false;
    bool fallback_rises = false;

    if (getrlimit(RLIMIT_NOFILE, &original) < 0)
        sysbail("cannot read the file descriptor limits");
    if (original.rlim_max != RLIM_INFINITY && original.rlim_max <= 16)
        skip_all("hard file descriptor limit is too low");
    diag("hard limit %s, soft limit %s",
         original.rlim_max == RLIM_INFINITY ? "infinite" : "finite",
         original.rlim_cur == RLIM_INFINITY ? "infinite" : "finite");

    /* Does the system accept a finite hard limit as the soft limit?  macOS
       does not when it is above OPEN_MAX. */
    if (original.rlim_max != RLIM_INFINITY) {
        probe = original;
        probe.rlim_cur = original.rlim_max;
        hard_accepted = (setrlimit(RLIMIT_NOFILE, &probe) == 0);
    }

    /* When the hard limit is infinite or refused as a soft limit, the library
       falls back to a finite maximum; OPEN_MAX, where the system defines it,
       is a usable one above 16 (it must also fit below a finite hard limit),
       so the soft limit has to rise. */
#    ifdef OPEN_MAX
    if (OPEN_MAX > 16
        && (original.rlim_max == RLIM_INFINITY
            || (!hard_accepted && (rlim_t) OPEN_MAX < original.rlim_max)))
        fallback_rises = true;
#    endif

    limited = original;
    limited.rlim_cur = 16;
    if (setrlimit(RLIMIT_NOFILE, &limited) < 0)
        sysbail("cannot lower the soft file descriptor limit");

    plan(fallback_rises ? 7 : 6);
    is_int(0, raisefdlimit(), "raise the soft file descriptor limit");
    if (getrlimit(RLIMIT_NOFILE, &current) < 0)
        sysbail("cannot read the updated file descriptor limits");
    if (current.rlim_cur != RLIM_INFINITY)
        diag("soft limit raised from 16 to %llu",
             (unsigned long long) current.rlim_cur);
    ok(current.rlim_cur != RLIM_INFINITY, "soft limit stays finite");
    ok(current.rlim_cur >= 16, "soft limit is not lowered");
    if (fallback_rises)
        ok(current.rlim_cur > 16, "soft limit rose with the finite fallback");
    if (hard_accepted)
        ok(current.rlim_cur == original.rlim_max,
           "soft limit reaches the hard limit");
    else
        ok(current.rlim_cur <= original.rlim_max,
           "soft limit is a finite maximum within the hard limit");
    ok(current.rlim_max == original.rlim_max, "hard limit is unchanged");
    is_int(0, raisefdlimit(), "succeeds again at the new limit");

    if (setrlimit(RLIMIT_NOFILE, &original) < 0)
        diag("cannot restore the original file descriptor limits");
#else
    int result, error;

    plan(2);
    result = raisefdlimit();
    error = errno;
    is_int(-1, result, "cannot raise the limit on this platform");
    is_int(ENOSYS, error, "reports unavailable resource limits");
#endif
    return 0;
}

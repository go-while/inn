/* Test timer summaries without relying on elapsed wall time.
 *
 * The test checks invocation counts, nesting (a timer started inside
 * another is reported as inner/outer) and the reset after a summary.  It
 * does not check the elapsed times, which are not reproducible. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/messages.h"
#include "inn/timer.h"
#include "tap/basic.h"

static char summary[512];

static void
capture_notice(size_t length UNUSED, const char *format, va_list args,
               int error UNUSED)
{
    vsnprintf(summary, sizeof(summary), format, args);
}

int
main(void)
{
    enum { OUTER = TMR_APPLICATION, INNER, TIMER_COUNT };
    static const char *const labels[] = {"outer", "inner"};
    unsigned long interval, outer_time, inner_time;
    unsigned long outer_count = 0, inner_count = 0;

    plan(4);
    message_handlers_notice(1, capture_notice);
    TMRinit(TIMER_COUNT);

    TMRstart(OUTER);
    TMRstart(INNER);
    TMRstop(INNER);
    TMRstop(OUTER);
    TMRstart(OUTER);
    TMRstop(OUTER);
    TMRsummary("profile", labels);

    is_int(5, sscanf(summary, "profile time %lu outer %lu(%lu) inner/outer %lu(%lu)",
                     &interval, &outer_time, &outer_count, &inner_time,
                     &inner_count),
           "summary identifies the child and its parent");
    is_int(2, outer_count, "outer counts both invocations");
    is_int(1, inner_count, "inner counts its one invocation");

    TMRsummary(NULL, labels);
    ok(strstr(summary, " outer 0(0) inner/outer 0(0)") != NULL,
       "next summary reports reset durations and counts");
    TMRfree();
    message_handlers_reset();
    return 0;
}

/* Test host selection from nnrpd.track without starting a reader. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/innconf.h"
#include "tap/basic.h"

#include "../../nnrpd/nnrpd.h"

/* Referenced by the file-open error message in TrackClient. */
struct client Client;

int
main(void)
{
    struct innconf config = {0};
    char identity[64];
    static const struct {
        char *host;
        const char *identity;
    } cases[] = {
        {"router.example.test", "Router"},
        {"other.router.example.test", "Rest"},
        {"dns.lab.example.test", "Lab"},
        {"dns.east.lab.example.test", "Lab"},
        {"lab.example.test", "Rest"},
        {"dns.lab.example.test.invalid", "Rest"},
        {"192.0.2.1", "Address"},
        {"node7.cluster.test", "Cluster"},
        {"worker.cluster.test", "Rest"},
        {"other.192.0.2.1", "Rest"},
    };

    plan(ARRAY_SIZE(cases));
    config.pathetc = test_file_path("data/etc");
    if (config.pathetc == NULL)
        bail("cannot find test configuration directory");
    innconf = &config;
    for (size_t i = 0; i < ARRAY_SIZE(cases); i++) {
        if (!TrackClient(cases[i].host, identity, sizeof(identity)))
            bail("no rule selected for %s", cases[i].host);
        is_string(cases[i].identity, identity, "%s", cases[i].host);
    }
    test_file_path_free(config.pathetc);
    return 0;
}

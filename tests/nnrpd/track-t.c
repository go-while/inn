/* Test host selection from nnrpd.track without starting a reader. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <sys/wait.h>

#include "inn/innconf.h"
#include "tap/basic.h"
#include "tap/messages.h"

#include "../../nnrpd/nnrpd.h"

/* Referenced by the file-open error message in TrackClient. */
struct client Client;

int
main(void)
{
    struct innconf config = {0};
    const char *warning = "empty host field in nnrpd.track matches every client\n";
    char identity[64];
    char tmpdir[] = "track-XXXXXX";
    char *path, *marker;
    FILE *rules;
    pid_t child;
    int status;
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
        {"192.168.1.42", "Subnet"},
        {"192.168.10.42", "Rest"},
        {"1192.168.1.42", "Rest"},
        {"mx.eu.legacy.example", "Legacy"},
        {"legacy.example", "Rest"},
        {"mx.legacy.example.test", "Rest"},
    };

    plan(ARRAY_SIZE(cases) + 5);
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

    if (mkdtemp(tmpdir) == NULL)
        sysbail("cannot create temporary directory");
    config.pathetc = config.pathrun = tmpdir;
    path = concatpath(tmpdir, "nnrpd.track");
    marker = concatpath(tmpdir, "nnrpd.track.warned");
    rules = fopen(path, "w");
    if (rules == NULL || fputs("  \n:Everyone\n", rules) == EOF
        || fclose(rules) == EOF)
        sysbail("cannot write test tracking rules");
    errors_capture();
    child = fork();
    if (child == -1)
        sysbail("cannot start first reader");
    if (child == 0) {
        TrackClient("one.test", identity, sizeof(identity));
        _exit(errors == NULL || strcmp(errors, warning) != 0);
    }
    if (waitpid(child, &status, 0) == -1)
        sysbail("cannot wait for first reader");
    ok(WIFEXITED(status) && WEXITSTATUS(status) == 0,
       "first reader warned about the empty host");
    ok(TrackClient("two.test", identity, sizeof(identity)), "empty host matches");
    is_string("Everyone", identity, "empty host supplies its identity");
    ok(errors == NULL, "second reader did not repeat the warning");
    ok(access(marker, F_OK) == 0, "record the warning for other readers");
    errors_uncapture();
    free(errors);
    unlink(marker);
    unlink(path);
    rmdir(tmpdir);
    free(marker);
    free(path);
    return 0;
}

/* innconf test suite. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/innconf.h"
#include "inn/messages.h"
#include "tap/basic.h"
#include "tap/messages.h"

/* We will have strings, integers, bools and lists. */
static const char grep[] =
    "(egrep '^(mta|organization|ovmethod|hismethod|path|port|extraoverview"
    "|pgpverify|docancels)'\
 ../../samples/inn.conf; echo 'domain: \"news.example.org\"';\
 echo 'fromhost: \"news.example.org\"';) > config/tmp";

static const char cat[] =
    "(cat ../../samples/inn.conf | egrep -v '^#(domain|fromhost)';\
 echo 'domain: \"news.example.org\"'; echo 'fromhost: \"news.example.org\"';)\
 > config/tmp2";

int
main(void)
{
    struct innconf *standard;
    FILE *config;

    if (access("../data/config/valid", F_OK) == 0) {
        if (chdir("../data") < 0) {
            sysbail("cannot chdir to ../data");
        }
    } else if (access("data/config/valid", F_OK) == 0) {
        if (chdir("data") < 0) {
            sysbail("cannot chdir to data");
        }
    } else if (access("tests/data/config/valid", F_OK) == 0) {
        if (chdir("tests/data") < 0) {
            sysbail("cannot chdir to tests/data");
        }
    }

    plan(9);

    if (system(cat) != 0)
        die("Unable to create stripped configuration file");
    if (!innconf_read("config/tmp2"))
        bail("cannot read generated config/tmp2");
    ok(true, "read and save the reference configuration");
    standard = innconf;
    innconf = NULL;
    if (system(grep) != 0)
        die("Unable to create stripped configuration file");
    if (!innconf_read("config/tmp"))
        bail("cannot read generated config/tmp");
    ok(true, "write and read the new configuration");
    unlink("config/tmp");
    unlink("config/tmp2");
    ok(innconf_compare(standard, innconf), "the configurations agree");
    innconf_free(standard);
    innconf_free(innconf);
    innconf = NULL;
    ok(true, "release the comparison configurations");

    /* Checking inn.conf. */
    errors_capture();
    if (system(grep) != 0)
        die("Unable to create stripped configuration file");
    ok(innconf_check("config/tmp"),
       "innconf_check accepts the configuration");
    ok(errors == NULL, "valid configuration emits no diagnostics");
    innconf_free(innconf);
    innconf = NULL;
    config = fopen("config/tmp", "a");
    if (config == NULL)
        sysdie("Unable to open stripped configuration file for append");
    fputs("foo: bar\n", config);
    fclose(config);
    ok(!innconf_check("config/tmp"), "innconf_check rejects an unknown key");
    unlink("config/tmp");
    is_string("config/tmp:30: unknown parameter foo\n", errors,
              "the diagnostic identifies the unknown key");
    errors_uncapture();
    free(errors);
    errors = NULL;
    innconf_free(innconf);
    innconf = NULL;
    ok(true, "release test files and configuration");

    return 0;
}

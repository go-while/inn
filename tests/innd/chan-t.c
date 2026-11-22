/* Test suite for general channel handling. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <fcntl.h>
#ifdef HAVE_SYS_TIME_H
#    include <sys/time.h>
#endif
#include <time.h>

#include "inn/buffer.h"
#include "inn/innconf.h"
#include "inn/libinn.h"
#include "tap/basic.h"

#include "../../innd/innd.h"

/* Prototypes. */
static void reader(CHANNEL *cp UNUSED);
static void writedone(CHANNEL *cp UNUSED);
static void wakeup(CHANNEL *cp UNUSED);

/* Initialize things enough to be able to call channel functions.  This only
   has to be called once. */
static void
initialize(void)
{
    if (access("../data/etc/inn.conf", F_OK) < 0)
        if (access("data/etc/inn.conf", F_OK) == 0)
            if (chdir("innd") != 0)
                sysdie("cannot cd to innd");
    if (!innconf_read("../data/etc/inn.conf"))
        exit(1);
    Log = fopen("/dev/null", "w");
    if (Log == NULL)
        sysdie("cannot open /dev/null");
    CHANsetup(32);
    gettimeofday(&Now, NULL);
}

/* This just gets things started.  Then we call CHANreadloop, which should
   dispatch control to writedone, where we create a new channel to read what
   we just wrote and go to sleep.  The reader function verifies the data was
   written out correctly and then wakes up the first channel to test the sleep
   and wakeup functionality. */
int
main(void)
{
    int fd;
    CHANNEL *cp;

    plan(15);
    initialize();
    message_handlers_notice(0);

    /* If we lose on waking up a sleeping channel, this will finish off
       matters before we wake up anyway. */
    alarm(5);

    fd = open("output", O_WRONLY | O_CREAT | O_TRUNC, 0644);
    if (fd < 0)
        sysdie("cannot create output");
    if (!isvalidfd(fd))
        sysdie("file descriptor %d too high (see rlimitnofile in inn.conf)",
               fd);
    cp = CHANcreate(fd, CTany, CSwriting, reader, writedone);
    is_int(fd, cp->fd, "new channel uses the supplied descriptor");
    is_int(CTany, cp->Type, "new channel has the requested type");
    is_int(CSwriting, cp->State, "new channel has the requested state");
    ok(!CHANsleeping(cp), "new channel is awake");
    WCHANset(cp, "some output", strlen("some output"));
    WCHANadd(cp);
    CHANreadloop();

    /* NOTREACHED */
    /* CHANreadloop should never return. */
    die("fell through main");
    return 1;
}

static void
writedone(CHANNEL *cp)
{
    CHANNEL *newcp;
    int fd;
    time_t now;

    ok(true, "writer callback ran");
    is_int(cp->Out.left, 0, "output buffer is drained");
    close(cp->fd);
    cp->fd = -1;
    fd = open("output", O_RDONLY | O_EXCL);
    if (fd < 0)
        sysdie("cannot open output");
    if (!isvalidfd(fd))
        sysdie("file descriptor %d too high (see rlimitnofile in inn.conf)",
               fd);
    newcp = CHANcreate(fd, CTany, CSgetbody, reader, NULL);
    RCHANadd(newcp);
    ok(!CHANsleeping(newcp), "new channel remains awake");
    now = time(NULL);
    SCHANadd(cp, now + 60, &Now, wakeup, xstrdup("some random text"));
    ok(CHANsleeping(cp), "put the channel to sleep");
}

static void
reader(CHANNEL *cp)
{
    ok(true, "input is ready to read");
    is_int(strlen("some output"), CHANreadtext(cp),
           "read the expected byte count");
    is_int(strlen("some output"), cp->In.used,
           "record the expected number of input bytes");
    /* FIXME: As long as the In buffer does not use the normal meanings of
       .used and .left, we cannot use buffer_append.
       buffer_append(&cp->In, "", 1); */
    memcpy(cp->In.data + cp->In.used, "", 1);
    cp->In.used++;
    cp->In.left--;
    is_string("some output", cp->In.data, "input contains the written text");
    SCHANwakeup(&Now);
}

__attribute__((__noreturn__)) static void
wakeup(CHANNEL *cp)
{
    ok(true, "the wakeup callback ran");
    is_string("some random text", cp->Argument,
              "the callback argument survives sleep");
    CHANclose(cp, CHANname(cp));
    is_int(CTfree, cp->Type, "close frees the channel");
    CHANshutdown();
    unlink("output");
    exit(0);
}

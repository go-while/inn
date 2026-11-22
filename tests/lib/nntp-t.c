/*
**  Tests for the NNTP protocol utility functions in lib/nntp.c.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <errno.h>
#include <signal.h>
#include <sys/wait.h>

#ifdef HAVE_SYS_TIME_H
#    include <sys/time.h>
#endif
#if HAVE_SYS_SELECT_H
#    include <sys/select.h>
#endif

#include "inn/nntp.h"
#include "tap/basic.h"


/*
**  Return true if fd has data to read right now.
*/
static bool
readable(int fd)
{
    fd_set mask;
    struct timeval tv;

    FD_ZERO(&mask);
    FD_SET(fd, &mask);
    tv.tv_sec = 0;
    tv.tv_usec = 0;
    return select(fd + 1, &mask, NULL, NULL, &tv) > 0;
}


/*
**  Read what is available on fd into buffer, nul-terminated.  Returns the
**  number of bytes read.
*/
static size_t
drain(int fd, char *buffer, size_t size)
{
    size_t total = 0;
    ssize_t count;

    while (total < size - 1 && readable(fd)) {
        count = read(fd, buffer + total, size - 1 - total);
        if (count <= 0)
            break;
        total += count;
    }
    buffer[total] = '\0';
    return total;
}


/*
**  Write a string to fd or bail.
*/
static void
put(int fd, const char *data)
{
    size_t length = strlen(data);

    if (write(fd, data, length) != (ssize_t) length)
        sysbail("cannot write test data");
}


/*
**  A timeout of 0 means wait forever: a line that arrives after a delay is
**  read, not reported as a timeout.  A non-zero timeout still expires.
*/
static void
test_timeout(void)
{
    struct nntp *nntp;
    int fds[2];
    char *line;
    pid_t child;
    int status;

    if (pipe(fds) < 0)
        sysbail("cannot create pipe");
    child = fork();
    if (child < 0)
        sysbail("cannot fork");
    else if (child == 0) {
        close(fds[0]);
        sleep(2);
        put(fds[1], "200 delayed\r\n");
        _exit(0);
    }
    close(fds[1]);
    nntp = nntp_new(fds[0], -1, 0, 0);
    line = NULL;
    is_int(NNTP_READ_OK, nntp_read_line(nntp, &line),
           "timeout 0 waits for a delayed line");
    is_string("200 delayed", line, "...and reads it");
    waitpid(child, &status, 0);

    /* The writer has gone; reset to a short timeout on an idle pipe. */
    nntp_free(nntp);
    if (pipe(fds) < 0)
        sysbail("cannot create pipe");
    nntp = nntp_new(fds[0], -1, 0, 1);
    is_int(NNTP_READ_TIMEOUT, nntp_read_line(nntp, &line),
           "timeout 1 expires on an idle connection");
    nntp_timeout(nntp, 0);
    put(fds[1], "205 bye\r\n");
    is_int(NNTP_READ_OK, nntp_read_line(nntp, &line),
           "nntp_timeout 0 then reads the waiting line");
    is_string("205 bye", line, "...with the right content");
    close(fds[1]);
    nntp_free(nntp);
}


/*
**  The noflush variants only buffer their output; nntp_flush sends it.
*/
static void
test_noflush(void)
{
    struct nntp *nntp;
    int fds[2];
    char buffer[256];

    if (pipe(fds) < 0)
        sysbail("cannot create pipe");
    nntp = nntp_new(-1, fds[1], 0, 0);

    nntp_respond_noflush(nntp, NNTP_INFO_HELP, "help text follows");
    ok(nntp_send_line_noflush(nntp, "line %d", 1),
       "nntp_send_line_noflush returns true");
    ok(nntp_send_line_noflush(nntp, "."), "nntp_send_line_noflush for .");
    ok(!readable(fds[0]), "noflush variants send nothing");
    ok(nntp_flush(nntp), "nntp_flush succeeds");
    drain(fds[0], buffer, sizeof(buffer));
    is_string("100 help text follows\r\nline 1\r\n.\r\n", buffer,
              "nntp_flush sends the buffered lines in order");

    ok(nntp_send_line(nntp, "QUIT"), "nntp_send_line succeeds");
    drain(fds[0], buffer, sizeof(buffer));
    is_string("QUIT\r\n", buffer, "nntp_send_line flushes");

    close(fds[0]);
    nntp_free(nntp);
}


int
main(void)
{
    /* A broken wait for "forever" must not hang the test suite. */
    alarm(60);
    signal(SIGPIPE, SIG_IGN);

    plan(12);

    test_timeout();
    test_noflush();

    return 0;
}

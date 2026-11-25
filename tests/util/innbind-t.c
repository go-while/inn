/* innbind test suite. */

#include "portable/system.h"

#include "portable/socket.h"
#include <errno.h>
#ifdef HAVE_STREAMS_SENDFD
#    include <stropts.h>
#endif
#include <sys/wait.h>

/* Make socket() fail in innbind with a seccomp filter, where available. */
#if defined(__linux__) && defined(__has_include)
#    if __has_include(<linux/seccomp.h>) && __has_include(<linux/filter.h>)
#        include <linux/filter.h>
#        include <linux/seccomp.h>
#        include <stddef.h>
#        include <sys/prctl.h>
#        include <sys/syscall.h>
#        if defined(__NR_socket) && defined(PR_SET_NO_NEW_PRIVS) \
            && defined(SECCOMP_MODE_FILTER)
#            define HAVE_SOCKET_FILTER 1
#        endif
#    endif
#endif

#include "inn/messages.h"
#include "tap/basic.h"

/* If SO_REUSEADDR isn't available, make calls to set_reuseaddr go away. */
#ifndef SO_REUSEADDR
#    define set_reuseaddr(fd) /* empty */
#endif

/* The path to the uninstalled innbind helper program. */
static const char innbind[] = "../../backends/innbind";

/* Set SO_REUSEADDR on a socket if possible. */
#ifdef SO_REUSEADDR
static void
set_reuseaddr(int fd)
{
    int flag = 1;

    if (setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &flag, sizeof(flag)) < 0)
        syswarn("cannot mark bind address reusable");
}
#endif

/* The server portion of the test.  Listens to a socket and accepts a
   connection, making sure what is printed on that connection matches what the
   client is supposed to print. */
static int
listener(int fd, int n)
{
    int client;
    FILE *out;
    char buffer[512];

    alarm(1);
    client = accept(fd, NULL, NULL);
    close(fd);
    if (client < 0) {
        syswarn("cannot accept connection from socket");
        ok_block(n, 2, false);
        return n + 2;
    }
    ok(n++, true);
    out = fdopen(client, "r");
    if (fgets(buffer, sizeof(buffer), out) == NULL) {
        syswarn("cannot read from socket");
        ok(n++, false);
        return n;
    }
    ok_string(n++, "socket test\r\n", buffer);
    fclose(out);
    return n;
}

/* Send a constant string to a socket, used to finish the client side of the
   testing. */
__attribute__((__noreturn__)) static void
client_send(int fd)
{
    FILE *out;

    out = fdopen(fd, "w");
    if (out == NULL)
        _exit(1);
    fputs("socket test\r\n", out);
    fclose(out);
    _exit(0);
}

/* Create a client IPv4 connection to the local server and then send a static
   string to that connection. */
__attribute__((__noreturn__)) static void
client_ipv4(void)
{
    struct sockaddr_in server;
    int fd;

    fd = socket(AF_INET, SOCK_STREAM, IPPROTO_IP);
    if (fd < 0)
        _exit(1);
    memset(&server, 0, sizeof(server));
    server.sin_family = AF_INET;
    server.sin_port = htons(11119);
    if (inet_aton("127.0.0.1", &server.sin_addr) == 0)
        _exit(1);
    if (connect(fd, (struct sockaddr *) &server, sizeof(server)) < 0)
        _exit(1);
    client_send(fd);
}

/* Create a client IPv6 connection to the local server and then send a static
   string to that connection. */
#ifdef HAVE_INET6
__attribute__((__noreturn__)) static void
client_ipv6(void)
{
    struct sockaddr_in6 server;
    struct in6_addr addr;
    int fd;

    fd = socket(AF_INET6, SOCK_STREAM, IPPROTO_IP);
    if (fd < 0)
        _exit(1);
    memset(&server, 0, sizeof(server));
    server.sin6_family = AF_INET6;
    server.sin6_port = htons(11119);
    if (inet_pton(AF_INET6, "::1", &addr) < 1)
        _exit(1);
    server.sin6_addr = addr;
    if (connect(fd, (struct sockaddr *) &server, sizeof(server)) < 0)
        sysdie("connect failed");
    client_send(fd);
}
#endif

/* Run innbind.  Takes the current test number, the file descriptor, the
   address family, and the address to bind to.  Returns the new test
   number. */
static int
run_innbind(int n, int fd, int family, const char *addr)
{
    int pipefds[2];
    char buffer[128];
    pid_t child, result;
    int status;

    if (pipe(pipefds) < 0)
        sysdie("cannot create pipe");
    child = fork();
    if (child < 0)
        sysdie("cannot fork");
    else if (child == 0) {
        close(1);
        if (dup2(pipefds[1], 1) < 0)
            _exit(1);
        close(pipefds[0]);
        snprintf(buffer, sizeof(buffer), "%d,%d,%s,11119", fd, family, addr);
        if (execl(innbind, innbind, buffer, (char *) 0) < 0)
            _exit(1);
    } else {
        close(pipefds[1]);
        status = read(pipefds[0], buffer, 3);
        if (status < 3) {
            syswarn("read failed (return %d)", status);
            ok(n++, false);
        } else {
            buffer[3] = '\0';
            ok_string(n++, "ok\n", buffer);
        }
        result = waitpid(child, &status, 0);
        if (result != child)
            die("cannot wait for innbind");
        ok(n++, WIFEXITED(status) && WEXITSTATUS(status) == 0);
    }
    return n;
}

/* Run a test of binding for IPv4.  Create a socket, use innbind to bind it to
   port 11119 on the loopback address, and then fork a child to connect to it.
   Make sure that connecting to it works correctly. */
static int
test_ipv4(int n)
{
    int fd;
    pid_t child;

    fd = socket(AF_INET, SOCK_STREAM, IPPROTO_IP);
    if (fd < 0)
        sysdie("cannot create socket");
    set_reuseaddr(fd);
    n = run_innbind(n, fd, AF_INET, "127.0.0.1");
    if (listen(fd, 1) < 0) {
        ok_block(n, 3, false);
        n += 3;
    } else {
        ok(n++, true);
        child = fork();
        if (child < 0)
            sysdie("cannot fork");
        else if (child == 0)
            client_ipv4();
        else
            n = listener(fd, n);
    }
    return n;
}

/* Run a test of binding for IPv6.  Create a socket, use innbind to bind it to
   port 11119 on the loopback address, and then fork a child to connect to it.
   Make sure that connecting to it works correctly. */
#ifdef HAVE_INET6
static int
test_ipv6(int n)
{
    int fd;
    pid_t child;

    fd = socket(AF_INET6, SOCK_STREAM, IPPROTO_IP);
    if (fd < 0) {
        if (errno == EAFNOSUPPORT || errno == EPROTONOSUPPORT) {
            skip_block(n, 5, "IPv6 not supported");
            return n + 5;
        } else
            sysdie("cannot create socket");
    }
    set_reuseaddr(fd);
    n = run_innbind(n, fd, AF_INET6, "::");
    if (listen(fd, 1) < 0) {
        ok_block(n, 3, false);
        n += 3;
    } else {
        ok(n++, true);
        child = fork();
        if (child < 0)
            sysdie("cannot fork");
        else if (child == 0)
            client_ipv6();
        else
            n = listener(fd, n);
    }
    return n;
}
#else  /* !HAVE_INET6 */
static int
test_ipv6(int n)
{
    skip_block(n, 5, "IPv6 not supported");
    return n + 5;
}
#endif /* !HAVE_INET6 */

/* Test file descriptor passing on systems that support it.  Use innbind to
   create a socket bound to port 11119, receive the socket, and then fork a
   child to connect to it.  Make sure that connecting works correctly. */
#ifdef HAVE_STREAMS_SENDFD
static int
test_sendfd(int n)
{
    int pipefds[2], fd;
    char buffer[128];
    struct strrecvfd fdrec;
    pid_t child, result;
    int status;

    if (pipe(pipefds) < 0)
        sysdie("cannot create pipe");
    child = fork();
    fd = -1;
    if (child < 0)
        sysdie("cannot fork");
    else if (child == 0) {
        close(1);
        if (dup2(pipefds[1], 1) < 0)
            _exit(1);
        close(pipefds[0]);
        snprintf(buffer, sizeof(buffer), "0,%d,127.0.0.1,11119", AF_INET);
        if (execl(innbind, innbind, "-p", buffer, (char *) 0) < 0)
            _exit(1);
    } else {
        close(pipefds[1]);
        status = read(pipefds[0], buffer, 3);
        if (status < 3) {
            syswarn("read failed (return %d)", status);
            ok(n++, false);
        } else {
            buffer[3] = '\0';
            ok_string(n++, "no\n", buffer);
        }
        if (ioctl(pipefds[0], I_RECVFD, &fdrec) < 0)
            ok(n++, false);
        else
            ok(n++, true);
        fd = fdrec.fd;
        result = waitpid(child, &status, 0);
        if (result != child)
            die("cannot wait for innbind");
        ok(n++, WIFEXITED(status) && WEXITSTATUS(status) == 0);
    }
    if (fd < 0 || listen(fd, 1) < 0) {
        ok(n++, false);
        ok(n++, false);
    } else {
        child = fork();
        if (child < 0)
            sysdie("cannot fork");
        else if (child == 0)
            client_ipv4();
        else
            n = listener(fd, n);
    }
    return n;
}
#else  /* !HAVE_STREAMS_SENDFD */
static int
test_sendfd(int n)
{
    skip_block(n, 5, "SENDFD not supported");
    return n + 5;
}
#endif /* !HAVE_INET6 */

/* Test that innbind reports a failure of socket() as such.  With -p, innbind
   creates a new socket; a seccomp filter in the child makes socket() fail
   for AF_INET with EMFILE.  innbind must then die with "cannot create socket"
   rather than go on and use the invalid descriptor (which would make it die
   later with another message, such as "cannot mark socket reusable"). */
#ifdef HAVE_SOCKET_FILTER
#    if __BYTE_ORDER__ == __ORDER_BIG_ENDIAN__
#        define ARG0_LOW (offsetof(struct seccomp_data, args[0]) + 4)
#    else
#        define ARG0_LOW (offsetof(struct seccomp_data, args[0]))
#    endif
static int
test_socket_failure(int n)
{
    struct sock_filter filter[] = {
        BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_socket, 0, 3),
        BPF_STMT(BPF_LD | BPF_W | BPF_ABS, ARG0_LOW),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, AF_INET, 0, 1),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | (EMFILE & SECCOMP_RET_DATA)),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
    };
    struct sock_fprog prog;
    int outfds[2], errfds[2], fd;
    char buffer[128];
    char errors[1024];
    size_t got;
    ssize_t status;
    pid_t child, result;
    int wstatus;

    prog.len = (unsigned short) (sizeof(filter) / sizeof(filter[0]));
    prog.filter = filter;
    if (pipe(outfds) < 0 || pipe(errfds) < 0)
        sysbail("cannot create pipe");
    child = fork();
    if (child < 0)
        sysbail("cannot fork");
    else if (child == 0) {
        if (dup2(outfds[1], 1) < 0 || dup2(errfds[1], 2) < 0)
            _exit(1);
        close(outfds[0]);
        close(outfds[1]);
        close(errfds[0]);
        close(errfds[1]);
        if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) < 0
            || prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &prog) < 0)
            _exit(3);
        fd = socket(AF_INET, SOCK_STREAM, IPPROTO_IP);
        if (fd >= 0)
            _exit(3);
        snprintf(buffer, sizeof(buffer), "0,%d,127.0.0.1,11119", AF_INET);
        execl(innbind, innbind, "-p", buffer, (char *) 0);
        _exit(1);
    }
    close(outfds[1]);
    close(errfds[1]);
    status = read(outfds[0], buffer, 3);
    if (status < 0)
        status = 0;
    buffer[status] = '\0';
    got = 0;
    while (got < sizeof(errors) - 1) {
        status = read(errfds[0], errors + got, sizeof(errors) - 1 - got);
        if (status <= 0)
            break;
        got += (size_t) status;
    }
    errors[got] = '\0';
    close(outfds[0]);
    close(errfds[0]);
    result = waitpid(child, &wstatus, 0);
    if (result != child)
        sysbail("cannot wait for innbind");
    if (WIFEXITED(wstatus) && WEXITSTATUS(wstatus) == 3) {
        skip_block(n, 3, "cannot make socket() fail with seccomp");
        return n + 3;
    }
    ok_string(n++, "no\n", buffer);
    ok(n++, WIFEXITED(wstatus) && WEXITSTATUS(wstatus) != 0);
    if (strstr(errors, "cannot create socket for") == NULL)
        diag("innbind said: %s", errors);
    ok(n++, strstr(errors, "cannot create socket for") != NULL);
    return n;
}
#else  /* !HAVE_SOCKET_FILTER */
static int
test_socket_failure(int n)
{
    skip_block(n, 3, "cannot make socket() fail on this system");
    return n + 3;
}
#endif /* !HAVE_SOCKET_FILTER */

int
main(void)
{
    int n;

    if (access("innbind.t", F_OK) < 0)
        if (access("util/innbind.t", F_OK) == 0)
            if (chdir("util") < 0)
                sysbail("cannot chdir to util");

    test_init(18);

    n = test_ipv4(1);          /* Tests  1-5.  */
    n = test_ipv6(n);          /* Tests  6-10. */
    n = test_sendfd(n);        /* Tests 11-15. */
    test_socket_failure(n);    /* Tests 16-18. */

    return 0;
}

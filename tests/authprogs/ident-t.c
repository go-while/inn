/* ident test suite. */

#include "portable/system.h"

#include "portable/socket.h"
#include <pwd.h>
#if HAVE_SYS_SELECT_H
#    include <sys/select.h>
#endif
#include <sys/wait.h>

#include "inn/messages.h"
#include "inn/network.h"
#include "inn/xwrite.h"
#include "tap/basic.h"

/* The path to the uninstalled ident resolver. */
static const char ident[] = "../../authprogs/ident";

/* This is the client side of the connection.  It's here just to set up a
   network connection we can ask identd about.  Wait for a line from the
   server and then close the connection. */
static void
client(const char *host)
{
    int fd;
    char buffer[32];

    fd = network_connect_host(host, 11119, NULL, DEFAULT_TIMEOUT);
    if (fd < 0)
        _exit(1);
    if (read(fd, buffer, sizeof(buffer)) < 0)
        sysbail("cannot read");
    close(fd);
}

/* The server side of the connection.  Listen on port 11119, fork off a client
   connection, accept the connection, and then run the ident resolver to get
   the results. */
static int
server_ipv4(int n)
{
    int fd, conn;
    pid_t child, result;
    struct sockaddr_in sin;
    socklen_t size;
    unsigned short port;
    int input[2];
    int output[2];
    FILE *infile;
    char buffer[256], wanted[256];
    struct passwd *pwd;
    int status;
    bool success = true;

    /* Make sure we can figure out our username.  If we can't, ident isn't
       going to be able to do so either. */
    pwd = getpwuid(getuid());
    if (pwd == NULL) {
        skip_block(n, 4, "unknown username");
        return n + 4;
    }
    snprintf(wanted, sizeof(wanted), "User:%s\r\n", pwd->pw_name);

    /* Create the network connection so ident has something to look at. */
    fd = network_bind_ipv4(SOCK_STREAM, "127.0.0.1", 11119);
    if (fd < 0)
        sysdie("cannot bind");
    if (listen(fd, 1) < 0)
        sysdie("cannot listen");
    child = fork();
    if (child < 0)
        sysdie("cannot fork");
    if (child == 0) {
        client("127.0.0.1");
        _exit(0);
    }
    size = sizeof(sin);
    conn = accept(fd, (struct sockaddr *) &sin, &size);
    close(fd);
    port = ntohs(sin.sin_port);

    /* Now, fork off the ident resolver and make sure it works. */
    if (pipe(input) < 0 || pipe(output) < 0)
        sysdie("cannot create pipe");
    child = fork();
    if (child < 0)
        sysdie("cannot fork");
    else if (child == 0) {
        if (dup2(input[0], 0) < 0)
            _exit(1);
        close(input[1]);
        if (dup2(output[1], 1) < 0)
            _exit(1);
        if (dup2(output[1], 2) < 0)
            _exit(1);
        close(output[0]);
        if (execl(ident, ident, (char *) 0) < 0)
            _exit(1);
    } else {
        close(input[0]);
        close(output[1]);
        infile = fdopen(input[1], "w");
        if (infile == NULL)
            sysdie("cannot fdopen");
        fprintf(infile, "ClientHost: localhost\r\n");
        fprintf(infile, "ClientIP: 127.0.0.1\r\n");
        fprintf(infile, "ClientPort: %hu\r\n", port);
        fprintf(infile, "LocalIP: 127.0.0.1\r\n");
        fprintf(infile, "LocalPort: 11119\r\n");
        fprintf(infile, ".\r\n");
        if (fclose(infile) == EOF)
            sysdie("cannot flush output to ident");
        ok(n++, true);
        status = read(output[0], buffer, sizeof(buffer) - 1);
        if (status < 0)
            ok(n++, false);
        else {
            buffer[status] = '\0';
            if (strncmp("ident: ", buffer, 7) == 0) {
                skip(n++, "ident server not running or not responding");
                success = false;
            } else
                ok_string(n++, wanted, buffer);
        }
        result = waitpid(child, &status, 0);
        if (result != child)
            die("cannot wait for innbind");
        ok(n++, WIFEXITED(status));
        if (success)
            ok(n++, WEXITSTATUS(status) == 0);
        else
            ok(n++, WEXITSTATUS(status) == 1);
    }
    close(conn);
    return n;
}

/* Find a TCP service name for ident -p whose port is at least 1024, whose
   port with its two bytes swapped is another port, and which can be bound on
   127.0.0.1.  Returns the listening socket and sets name and port, or returns
   -1. */
static int
service_listener(char *name, size_t size, unsigned short *port)
{
    struct servent *s;
    struct sockaddr_in sin;
    unsigned short p, swapped;
    int fd;

    setservent(1);
    while ((s = getservent()) != NULL) {
        if (strcmp(s->s_proto, "tcp") != 0)
            continue;
        p = ntohs((unsigned short) s->s_port);
        swapped = (unsigned short) (((p & 0xffU) << 8) | (p >> 8));
        if (p < 1024 || swapped == p || strlen(s->s_name) >= size)
            continue;
        fd = socket(PF_INET, SOCK_STREAM, IPPROTO_IP);
        if (fd < 0)
            continue;
        network_set_reuseaddr(fd);
        memset(&sin, 0, sizeof(sin));
        sin.sin_family = AF_INET;
        sin.sin_port = htons(p);
        sin.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
        if (bind(fd, (struct sockaddr *) &sin, sizeof(sin)) < 0
            || listen(fd, 1) < 0) {
            close(fd);
            continue;
        }
        strlcpy(name, s->s_name, size);
        *port = p;
        endservent();
        return fd;
    }
    endservent();
    return -1;
}

/* ident -p with a service name must connect to the port of that service.
   This test is the ident server: it listens on the service port, answers
   ident's query with a fixed user, and checks what ident prints. */
static int
server_service(int n)
{
    int fd, conn = -1, maxfd, status;
    pid_t child, result;
    int input[2];
    int output[2];
    FILE *infile;
    char name[256], buffer[256];
    unsigned short port = 0;
    struct servent *s;
    fd_set fds;
    struct timeval tv;
    ssize_t got;
    size_t used;
    static const char reply[] = "4242 , 4243 : USERID : UNIX : identuser\r\n";

    if (htons(1) == 1) {
        skip_block(n, 4, "port byte order is host byte order");
        return n + 4;
    }
    fd = service_listener(name, sizeof(name), &port);
    if (fd < 0) {
        skip_block(n, 4, "no usable TCP service name above port 1023");
        return n + 4;
    }
    s = getservbyname(name, "tcp");
    if (s == NULL || ntohs((unsigned short) s->s_port) != port) {
        close(fd);
        skip_block(n, 4, "service name lookup is not consistent");
        return n + 4;
    }
    diag("ident -p %s (port %hu)", name, port);

    if (pipe(input) < 0 || pipe(output) < 0)
        sysbail("cannot create pipe");
    child = fork();
    if (child < 0)
        sysbail("cannot fork");
    else if (child == 0) {
        close(fd);
        if (dup2(input[0], 0) < 0)
            _exit(1);
        close(input[1]);
        if (dup2(output[1], 1) < 0)
            _exit(1);
        if (dup2(output[1], 2) < 0)
            _exit(1);
        close(output[0]);
        if (execl(ident, ident, "-p", name, (char *) 0) < 0)
            _exit(1);
    }
    close(input[0]);
    close(output[1]);
    infile = fdopen(input[1], "w");
    if (infile == NULL)
        sysbail("cannot fdopen");
    fprintf(infile, "ClientHost: localhost\r\n");
    fprintf(infile, "ClientIP: 127.0.0.1\r\n");
    fprintf(infile, "ClientPort: 4242\r\n");
    fprintf(infile, "LocalIP: 127.0.0.1\r\n");
    fprintf(infile, "LocalPort: 4243\r\n");
    fprintf(infile, ".\r\n");
    if (fclose(infile) == EOF)
        sysbail("cannot flush output to ident");

    /* Wait for ident to connect, or to give up and print its error. */
    FD_ZERO(&fds);
    FD_SET(fd, &fds);
    FD_SET(output[0], &fds);
    maxfd = (fd > output[0]) ? fd : output[0];
    tv.tv_sec = 20;
    tv.tv_usec = 0;
    if (select(maxfd + 1, &fds, NULL, NULL, &tv) > 0 && FD_ISSET(fd, &fds))
        conn = accept(fd, NULL, NULL);
    close(fd);
    ok(n++, conn >= 0);

    /* Read the query and answer it. */
    buffer[0] = '\0';
    if (conn >= 0) {
        used = 0;
        while (used < sizeof(buffer) - 1
               && (used == 0 || buffer[used - 1] != '\n')) {
            got = read(conn, buffer + used, sizeof(buffer) - 1 - used);
            if (got <= 0)
                break;
            used += (size_t) got;
        }
        buffer[used] = '\0';
        if (xwrite(conn, reply, strlen(reply)) < 0)
            sysdiag("cannot write the ident reply");
        close(conn);
    }
    ok_string(n++, "4242 , 4243\r\n", buffer);

    /* Check what ident prints and how it exits. */
    used = 0;
    while (used < sizeof(buffer) - 1) {
        got = read(output[0], buffer + used, sizeof(buffer) - 1 - used);
        if (got <= 0)
            break;
        used += (size_t) got;
    }
    buffer[used] = '\0';
    close(output[0]);
    ok_string(n++, "User:identuser\r\n", buffer);
    result = waitpid(child, &status, 0);
    if (result != child)
        sysbail("cannot wait for ident");
    ok(n++, WIFEXITED(status) && WEXITSTATUS(status) == 0);
    return n;
}

int
main(void)
{
    if (access("ident.t", F_OK) < 0) {
        if (access("authprogs/ident.t", F_OK) == 0) {
            if (chdir("authprogs") < 0) {
                sysbail("cannot chdir to authprogs");
            }
        }
    }

    test_init(8);
    server_service(server_ipv4(1));

    return 0;
}

/*
 * innxmit test suite: replies from the peer that start with a dot.
 *
 * innxmit reads the peer's replies with REMread(), which undoes the dot
 * escape of a line starting with "." and treats a line consisting of only
 * "." as the end of the data.  These tests run innxmit against a small
 * server in this process that sends dot-escaped replies, and check that
 * innxmit acts on the unescaped reply: a ".203" reply to MODE STREAM enables
 * streaming, and a ".335" reply to IHAVE makes innxmit send the article.
 */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "portable/socket.h"
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <sys/select.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/wait.h>

#include "tap/basic.h"
#include "tap/string.h"

#define MSGID "<innxmit-dot@example.com>"

static const char article[] = "Path: example.com!not-for-mail\n"
                              "From: Test <test@example.com>\n"
                              "Newsgroups: example.test\n"
                              "Subject: innxmit dot test\n"
                              "Message-ID: " MSGID "\n"
                              "Date: Wed, 25 Nov 2026 00:00:00 GMT\n"
                              "\n"
                              "Body line.\n";

/* Paths used by all tests, set up by setup(). */
static char *dir;
static char *innxmit;
static char *tmpdir;
static bool failed = false;


/* Write a string to a file, bailing out on error. */
static void
write_file(const char *path, const char *data)
{
    FILE *f;

    f = fopen(path, "w");
    if (f == NULL)
        sysbail("cannot create %s", path);
    if (fputs(data, f) == EOF || fclose(f) == EOF)
        sysbail("cannot write %s", path);
}


/* Create the configuration, the article and the spool for innxmit. */
static void
setup(void)
{
    char *path, *data;
    char cwd[4096];

    innxmit = test_file_path("../backends/innxmit");
    if (innxmit == NULL)
        skip_all("innxmit not built");
    tmpdir = test_tmpdir();
    if (tmpdir[0] == '/')
        basprintf(&dir, "%s/innxmit", tmpdir);
    else {
        if (getcwd(cwd, sizeof(cwd)) == NULL)
            sysbail("cannot get the current directory");
        basprintf(&dir, "%s/%s/innxmit", cwd, tmpdir);
    }
    basprintf(&path, "rm -rf '%s'", dir);
    if (system(path) != 0)
        bail("cannot remove %s", dir);
    free(path);

    if (mkdir(dir, 0777) < 0)
        sysbail("cannot create %s", dir);
    basprintf(&path, "%s/etc", dir);
    if (mkdir(path, 0777) < 0)
        sysbail("cannot create %s", path);
    free(path);
    basprintf(&path, "%s/spool", dir);
    if (mkdir(path, 0777) < 0)
        sysbail("cannot create %s", path);
    free(path);
    basprintf(&path, "%s/spool/articles", dir);
    if (mkdir(path, 0777) < 0)
        sysbail("cannot create %s", path);
    free(path);

    basprintf(&path, "%s/inn.conf", dir);
    basprintf(&data,
              "domain: example.com\n"
              "mta: \"/bin/true %%s\"\n"
              "hismethod: hisv6\n"
              "enableoverview: false\n"
              "pathnews: %s\n",
              dir);
    write_file(path, data);
    free(path);
    free(data);
    basprintf(&path, "%s/etc/storage.conf", dir);
    write_file(path, "method timehash {\n"
                     "    newsgroups: *\n"
                     "    class: 0\n"
                     "}\n");
    free(path);
    basprintf(&path, "%s/article", dir);
    write_file(path, article);
    free(path);
}


/* Remove the files that the tests created. */
static void
cleanup(void)
{
    char *command;

    basprintf(&command, "rm -rf '%s'", dir);
    if (system(command) != 0)
        diag("cannot remove %s", dir);
    free(command);
    free(dir);
    test_tmpdir_free(tmpdir);
}


/* Write the batch file with the article, and return its path. */
static char *
write_batch(void)
{
    char *batch, *data;

    basprintf(&batch, "%s/batch", dir);
    basprintf(&data, "%s/article %s\n", dir, MSGID);
    write_file(batch, data);
    free(data);
    return batch;
}


/* Print innxmit's output as diagnostics after a failed test. */
static void
show_log(void)
{
    char *path;
    char line[1024];
    FILE *f;

    basprintf(&path, "%s/innxmit.log", dir);
    f = fopen(path, "r");
    free(path);
    if (f == NULL)
        return;
    while (fgets(line, sizeof(line), f) != NULL) {
        line[strcspn(line, "\r\n")] = '\0';
        diag("innxmit: %s", line);
    }
    fclose(f);
}


/* Listen on an unused port of the loopback address. */
static int
listen_local(unsigned int *port)
{
    struct sockaddr_in addr;
    socklen_t length;
    int fd;

    fd = socket(AF_INET, SOCK_STREAM, IPPROTO_IP);
    if (fd < 0)
        sysbail("cannot create socket");
    memset(&addr, 0, sizeof(addr));
    addr.sin_family = AF_INET;
    addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    addr.sin_port = 0;
    if (bind(fd, (struct sockaddr *) &addr, sizeof(addr)) < 0)
        sysbail("cannot bind socket");
    if (listen(fd, 1) < 0)
        sysbail("cannot listen on socket");
    length = sizeof(addr);
    if (getsockname(fd, (struct sockaddr *) &addr, &length) < 0)
        sysbail("cannot get socket address");
    *port = ntohs(addr.sin_port);
    return fd;
}


/* Accept the connection from innxmit within 30 seconds.  Replies are read
   with a timeout of 30 seconds too.  Returns -1 on failure. */
static int
accept_peer(int fd)
{
    fd_set set;
    struct timeval tv;
    int client;

    FD_ZERO(&set);
    FD_SET(fd, &set);
    tv.tv_sec = 30;
    tv.tv_usec = 0;
    if (select(fd + 1, &set, NULL, NULL, &tv) <= 0)
        return -1;
    client = accept(fd, NULL, NULL);
    if (client < 0)
        return -1;
    tv.tv_sec = 30;
    tv.tv_usec = 0;
    if (setsockopt(client, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv)) < 0)
        sysdiag("cannot set a receive timeout");
    return client;
}


/* Read a line from innxmit without its CRLF.  Returns false on EOF or
   timeout, with an empty string in buffer. */
static bool
read_line(FILE *in, char *buffer, size_t size)
{
    if (fgets(buffer, (int) size, in) == NULL) {
        buffer[0] = '\0';
        return false;
    }
    buffer[strcspn(buffer, "\r\n")] = '\0';
    return true;
}


/* Send a line to innxmit, adding CRLF. */
static void
send_line(int fd, const char *line)
{
    char *data;
    size_t length;

    basprintf(&data, "%s\r\n", line);
    length = strlen(data);
    if (write(fd, data, length) != (ssize_t) length)
        sysdiag("cannot send %s", line);
    free(data);
}


/* Start innxmit for the batch file, connecting to port.  With stream false,
   innxmit is told not to try streaming (-s). */
static pid_t
start_innxmit(int listener, unsigned int port, const char *batch, bool stream)
{
    char *conf, *logpath;
    char portbuf[16];
    pid_t child;
    int fd;

    basprintf(&conf, "%s/inn.conf", dir);
    basprintf(&logpath, "%s/innxmit.log", dir);
    snprintf(portbuf, sizeof(portbuf), "%u", port);
    child = fork();
    if (child < 0)
        sysbail("cannot fork");
    else if (child == 0) {
        close(listener);
        fd = open(logpath, O_WRONLY | O_CREAT | O_TRUNC, 0644);
        if (fd < 0 || dup2(fd, 1) < 0 || dup2(fd, 2) < 0)
            _exit(1);
        if (setenv("INNCONF", conf, 1) < 0)
            _exit(1);
        if (stream)
            execl(innxmit, innxmit, "-d", "-t", "10", "-T", "60", "-P",
                  portbuf, "127.0.0.1", batch, (char *) 0);
        else
            execl(innxmit, innxmit, "-d", "-s", "-t", "10", "-T", "60", "-P",
                  portbuf, "127.0.0.1", batch, (char *) 0);
        _exit(1);
    }
    free(conf);
    free(logpath);
    return child;
}


/* Wait for innxmit and return its exit status, or -1. */
static int
wait_innxmit(pid_t child)
{
    int status;

    if (waitpid(child, &status, 0) != child)
        sysbail("cannot wait for innxmit");
    if (!WIFEXITED(status))
        return -1;
    return WEXITSTATUS(status);
}


/* MODE STREAM answered with ".203": innxmit must stream (CHECK). */
static void
test_stream(int listener, unsigned int port)
{
    char *batch;
    char mode[512] = "", command[512] = "", last[512] = "";
    FILE *in;
    pid_t child;
    int client, status;

    batch = write_batch();
    child = start_innxmit(listener, port, batch, true);
    client = accept_peer(listener);
    if (client < 0) {
        kill(child, SIGTERM);
        wait_innxmit(child);
        ok_block(4, false, "innxmit did not connect");
        show_log();
        free(batch);
        return;
    }
    in = fdopen(client, "r");
    if (in == NULL)
        sysbail("cannot fdopen socket");
    send_line(client, "200 innxmit test server");
    read_line(in, mode, sizeof(mode));
    send_line(client, ".203 Streaming permitted");
    read_line(in, command, sizeof(command));
    if (strncmp(command, "CHECK ", 6) == 0)
        send_line(client, "438 " MSGID);
    else if (strncmp(command, "IHAVE ", 6) == 0)
        send_line(client, "435 Duplicate");
    read_line(in, last, sizeof(last));
    send_line(client, "205 Bye");
    fclose(in);
    status = wait_innxmit(child);

    if (!is_string("MODE STREAM", mode, "innxmit asks for streaming"))
        failed = true;
    if (!is_string("CHECK " MSGID, command,
                   "a .203 reply to MODE STREAM enables streaming"))
        failed = true;
    if (!is_string("QUIT", last, "innxmit sends QUIT at the end"))
        failed = true;
    if (!is_int(0, status, "innxmit exits with status 0"))
        failed = true;
    if (failed)
        show_log();
    free(batch);
}


/* IHAVE answered with ".335": innxmit must send the article. */
static void
test_ihave(int listener, unsigned int port)
{
    char *batch;
    char command[512] = "", line[1024] = "", last[512] = "";
    bool sent = false, saw_msgid = false;
    FILE *in;
    pid_t child;
    int client, status;
    struct stat st;

    failed = false;
    batch = write_batch();
    child = start_innxmit(listener, port, batch, false);
    client = accept_peer(listener);
    if (client < 0) {
        kill(child, SIGTERM);
        wait_innxmit(child);
        ok_block(5, false, "innxmit did not connect");
        show_log();
        free(batch);
        return;
    }
    in = fdopen(client, "r");
    if (in == NULL)
        sysbail("cannot fdopen socket");
    send_line(client, "200 innxmit test server");
    read_line(in, command, sizeof(command));
    send_line(client, ".335 Send it");
    if (read_line(in, line, sizeof(line)) && strcmp(line, "QUIT") != 0) {
        /* The article, up to the terminating dot. */
        while (strcmp(line, ".") != 0) {
            if (strcmp(line, "Message-ID: " MSGID) == 0)
                saw_msgid = true;
            if (!read_line(in, line, sizeof(line)))
                break;
        }
        sent = (strcmp(line, ".") == 0 && saw_msgid);
        send_line(client, "235 Article transferred OK");
        read_line(in, last, sizeof(last));
    } else
        strlcpy(last, line, sizeof(last));
    send_line(client, "205 Bye");
    fclose(in);
    status = wait_innxmit(child);

    if (!is_string("IHAVE " MSGID, command, "innxmit offers the article"))
        failed = true;
    if (!ok(sent, "a .335 reply to IHAVE makes innxmit send the article"))
        failed = true;
    if (!ok(stat(batch, &st) < 0 && errno == ENOENT,
            "the batch file is removed after the article was taken"))
        failed = true;
    if (!is_string("QUIT", last, "innxmit sends QUIT at the end"))
        failed = true;
    if (!is_int(0, status, "innxmit exits with status 0"))
        failed = true;
    if (failed)
        show_log();
    free(batch);
}


int
main(void)
{
    int listener;
    unsigned int port;

    signal(SIGPIPE, SIG_IGN);
    setup();
    plan(9);

    listener = listen_local(&port);
    test_stream(listener, port); /* Tests 1-4. */
    test_ihave(listener, port);  /* Tests 5-9. */
    close(listener);

    cleanup();
    return 0;
}

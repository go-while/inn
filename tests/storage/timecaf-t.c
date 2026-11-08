/* Tests for releasing and reopening the cached timecaf writer. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <sys/wait.h>

#include "../../storage/timecaf/timecaf.h"
#include "inn/innconf.h"
#include "inn/libinn.h"
#include "tap/basic.h"

static const char data[] =
    "Subject: timecaf flush test\r\n\r\narticle body\r\n";


/* POSIX record locks belong to the process, so probe from a child.  Use a
   nonblocking lock and bound the whole test, including waitpid, with alarm. */
static int
lock_status(const char *path)
{
    pid_t child;
    int status;

    child = fork();
    if (child < 0)
        sysbail("cannot fork lock probe");
    if (child == 0) {
        struct flock lock;
        int fd, result, error;

        alarm(5);
        fd = open(path, O_RDWR);
        if (fd < 0)
            _exit(2);
        memset(&lock, 0, sizeof(lock));
        lock.l_type = F_WRLCK;
        lock.l_whence = SEEK_SET;
        result = fcntl(fd, F_SETLK, &lock);
        error = errno;
        close(fd);
        if (result == 0)
            _exit(0);
        _exit(error == EACCES || error == EAGAIN ? 1 : 2);
    }
    while (waitpid(child, &status, 0) < 0)
        if (errno != EINTR)
            sysbail("cannot wait for lock probe");
    if (!WIFEXITED(status) || WEXITSTATUS(status) > 1)
        bail("lock probe failed");
    return WEXITSTATUS(status);
}


static TOKEN
store_article(void)
{
    ARTHANDLE article = ARTHANDLE_INITIALIZER;
    struct iovec iov;
    TOKEN token;

    iov.iov_base = (void *) data;
    iov.iov_len = sizeof(data) - 1;
    article.iov = &iov;
    article.iovcnt = 1;
    article.len = iov.iov_len;
    /* Fix the arrival time so every store uses the same CAF, regardless of
       wall-clock bucket boundaries or how long the test takes. */
    article.arrived = 1700000000;
    token = timecaf_store(article, 0);
    if (token.type == TOKEN_EMPTY)
        bail("cannot store timecaf test article");
    return token;
}


static bool
article_matches(TOKEN token)
{
    ARTHANDLE *article;
    bool matches;

    article = timecaf_retrieve(token, RETR_ALL);
    if (article == NULL)
        return false;
    matches = article->len == sizeof(data) - 1
              && memcmp(article->data, data, sizeof(data) - 1) == 0;
    timecaf_freearticle(article);
    return matches;
}


int
main(void)
{
    char directory[] = "timecaf-test-XXXXXX";
    char *explanation, *path, *p;
    SMATTRIBUTE attr;
    TOKEN first, second, third;
    ARTHANDLE *article;

    plan(25);
    alarm(30);
    if (mkdtemp(directory) == NULL)
        sysbail("cannot create timecaf test directory");
    innconf = xcalloc(1, sizeof(*innconf));
    innconf->patharticles = xstrdup(directory);
    if (!timecaf_init(&attr))
        bail("cannot initialize timecaf");

    first = store_article();
    explanation = timecaf_explaintoken(first);
    p = strstr(explanation, " file=");
    if (p == NULL)
        bail("no CAF path in token explanation");
    path = xstrdup(p + 6);
    free(explanation);
    is_int(1, lock_status(path), "writer keeps its lock between stores");
    ok(timecaf_flushcacheddata(SM_HEAD), "header-only flush succeeds");
    is_int(1, lock_status(path), "header-only flush retains writer");
    ok(timecaf_flushcacheddata(SM_CANCELLEDART), "cancel-only flush succeeds");
    is_int(1, lock_status(path),
           "cancel-only flush without cancels retains writer");
    ok(timecaf_flushcacheddata(SM_ALL), "full flush succeeds");
    is_int(0, lock_status(path), "full flush releases the writer lock");

    /* Do not retrieve while testing lock retention: opening and closing the
       same file in this process would itself release its POSIX record lock. */
    ok(article_matches(first), "completed article survives full flush");
    ok(timecaf_flushcacheddata(SM_ALL),
       "full flush without a writer succeeds");

    second = store_article();
    is_int(1, lock_status(path), "next store reopens and locks the same CAF");
    ok(memcmp(&first, &second, sizeof(first)) != 0,
       "reopening assigns a distinct article token");
    ok(timecaf_cancel(first), "queue cancellation while writer is cached");
    ok(timecaf_flushcacheddata(SM_ALL),
       "flush closes writer and drains cancels");
    is_int(0, lock_status(path), "cancellation flush leaves no write lock");
    article = timecaf_retrieve(first, RETR_ALL);
    ok(article == NULL, "queued article was removed");
    if (article != NULL)
        timecaf_freearticle(article);
    ok(article_matches(second), "cancellation preserves the other article");

    third = store_article();
    is_int(1, lock_status(path), "store after cancellation reacquires lock");
    ok(memcmp(&second, &third, sizeof(second)) != 0,
       "reopening after cancellation assigns a new token");
    ok(timecaf_flushcacheddata(SM_ALL), "flush after reopening succeeds");
    is_int(0, lock_status(path), "reopened writer lock is released");
    ok(article_matches(second), "earlier retained article survives reopening");
    ok(article_matches(third), "new article survives reopening and flush");
    ok(timecaf_flushcacheddata(SM_ALL), "repeated full flush is harmless");
    timecaf_shutdown();
    is_int(0, lock_status(path), "shutdown after flush leaves CAF unlocked");

    if (unlink(path) < 0)
        sysbail("cannot unlink test CAF");
    for (int i = 0; i < 2; i++) {
        p = strrchr(path, '/');
        if (p == NULL)
            bail("invalid test CAF path");
        *p = '\0';
        if (rmdir(path) < 0)
            sysbail("cannot remove CAF test subdirectory");
    }
    is_int(0, rmdir(directory), "test directory removed");
    free(path);
    free(innconf->patharticles);
    free(innconf);
    alarm(0);
    return 0;
}

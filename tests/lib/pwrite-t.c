/* pwrite test suite. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>

#include "inn/messages.h"
#include "tap/basic.h"

ssize_t test_pwrite(int fd, const void *buf, size_t nbyte, off_t offset);

int
main(void)
{
    unsigned char buf[256], result[256];
    unsigned char c;
    int i, fd;
    ssize_t status;

    for (c = 0, i = 0; i < 256; i++, c++)
        buf[i] = c;
    fd = open(".testout", O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0)
        sysdie("Can't create .testout");
    memset(result, 0, sizeof(result));

    plan(6);

    ok(test_pwrite(fd, buf + 129, 127, 129) == 127,
       "write the last 127 bytes at an explicit offset");
    ok(write(fd, buf, 64) == 64,
       "ordinary write starts at the original offset");
    ok(test_pwrite(fd, buf + 64, 65, 64) == 65,
       "write the middle bytes at an explicit offset");
    status = read(fd, result, 64);
    ok((status == 64) && !memcmp(result, buf + 64, 64),
       "ordinary read starts after the ordinary write");

    if (lseek(fd, 0, SEEK_SET) == (off_t) -1)
        sysdie("Can't rewind .testout");
    status = read(fd, result, 256);
    ok((status == 256) && !memcmp(result, buf, 256),
       "all bytes are in the correct places");

    close(fd);
    errno = 0;
    status = test_pwrite(fd, result, 1, 0);
    ok((status == -1) && (errno == EBADF),
       "pwrite on a closed descriptor sets EBADF");

    if (unlink(".testout") < 0)
        sysdie("Can't unlink .testout");

    return 0;
}

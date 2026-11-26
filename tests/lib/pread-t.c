/* pread test suite. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>

#include "inn/libinn.h"
#include "inn/messages.h"
#include "tap/basic.h"

ssize_t test_pread(int fd, void *buf, size_t nbyte, off_t offset);

int
main(void)
{
    unsigned char buf[256], result[256];
    unsigned char c;
    int i, fd;
    ssize_t status;
    off_t position;

    for (c = 0, i = 0; i < 256; i++, c++)
        buf[i] = c;
    fd = open(".testout", O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0)
        sysdie("Can't create .testout");
    if (xwrite(fd, buf, 256) < 0)
        sysdie("Can't write to .testout");
    if (lseek(fd, 0, SEEK_SET) == (off_t) -1)
        sysdie("Can't rewind .testout");
    memset(result, 0, sizeof(result));

    plan(6);

    status = test_pread(fd, result, 128, 128);
    ok((status == 128) && !memcmp(result, buf + 128, 128),
       "read the second half at an explicit offset");
    status = read(fd, result, 64);
    ok((status == 64) && !memcmp(result, buf, 64),
       "ordinary read starts at the original offset");
    status = test_pread(fd, result, 1, 256);
    ok(status == 0, "read at end of file returns zero");
    status = test_pread(fd, result, 256, 0);
    ok((status == 256) && !memcmp(result, buf, 256),
       "read the full file from an explicit offset");
    position = lseek(fd, 0, SEEK_CUR);
    ok(position == 64, "pread does not change the current offset");

    close(fd);
    errno = 0;
    status = test_pread(fd, result, 1, 0);
    ok((status == -1) && (errno == EBADF),
       "pread on a closed descriptor sets EBADF");

    if (unlink(".testout") < 0)
        sysdie("Can't unlink .testout");

    return 0;
}

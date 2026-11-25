/*
 * Tests for the size checks of the innfeed Buffer class.
 *
 * Every case runs in a child process, because a wrong size is answered with
 * die() and a missing check can crash.  The parent checks how the child
 * ended and what it printed.
 */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <errno.h>
#include <stdarg.h>
#include <sys/wait.h>

#include "inn/libinn.h"
#include "tap/basic.h"

#include "../../innfeed/buffer.h"

/* Stubs for debugging helpers used by buffer.c. */
void
d_printf(unsigned int level UNUSED, const char *format UNUSED, ...)
{
}

const char *
boolToString(bool value)
{
    return value ? "true" : "false";
}

/* Exit status of a child whose case function returns. */
#define CHILD_OK  0
#define CHILD_BAD 2

static Buffer
filled_buffer(size_t size, const char *data, size_t length)
{
    Buffer buffer = newBuffer(size);

    memcpy(bufferBase(buffer), data, length);
    bufferSetDataSize(buffer, length);
    return buffer;
}

/* Decrementing the data size by less than the data size is allowed. */
static int
decr_valid(void)
{
    Buffer buffer = filled_buffer(3, "abc", 3);

    bufferDecrDataSize(buffer, 2);
    printf("data size %zu\n", bufferDataSize(buffer));
    return bufferDataSize(buffer) == 1 ? CHILD_OK : CHILD_BAD;
}

/* Decrementing the data size below zero must die. */
static int
decr_too_much(void)
{
    Buffer buffer = filled_buffer(3, "a", 1);

    bufferDecrDataSize(buffer, 2);
    printf("data size %zu after decrement by 2\n", bufferDataSize(buffer));
    return CHILD_BAD;
}

/* Incrementing the data size past SIZE_MAX must die, not wrap. */
static int
incr_wrap(void)
{
    Buffer buffer = filled_buffer(4, "ab", 2);

    bufferIncrDataSize(buffer, SIZE_MAX);
    printf("data size %zu after increment by SIZE_MAX\n",
           bufferDataSize(buffer));
    return CHILD_BAD;
}

/* Duplicating a NULL array gives an empty array. */
static int
dup_null(void)
{
    Buffer *array = dupBufferArray(NULL);

    if (array == NULL || array[0] != NULL)
        return CHILD_BAD;
    free(array);
    return CHILD_OK;
}

/*
 * Growing a full 8-byte buffer by SIZE_MAX - 7 would wrap the memory size to
 * 0.  The buffer must stay as it was; then writing the terminating nul byte
 * after the data is within the allocation.
 */
static int
expand_wrap(void)
{
    Buffer buffer = filled_buffer(8, "abcdefgh", 8);
    bool grown;

    grown = expandBuffer(buffer, SIZE_MAX - 7);
    printf("expandBuffer returned %s, size %zu\n", grown ? "true" : "false",
           bufferSize(buffer));
    bufferAddNullByte(buffer);
    return !grown && bufferSize(buffer) == 8 ? CHILD_OK : CHILD_BAD;
}

/*
 * Growing a 1-byte buffer by SIZE_MAX - 1 makes the memory size SIZE_MAX, and
 * the allocation of memory size + 1 would be 0 bytes.
 */
static int
expand_to_max(void)
{
    Buffer buffer = filled_buffer(1, "a", 1);
    bool grown;

    grown = expandBuffer(buffer, SIZE_MAX - 1);
    printf("expandBuffer returned %s, size %zu\n", grown ? "true" : "false",
           bufferSize(buffer));
    bufferAddNullByte(buffer);
    return !grown && bufferSize(buffer) == 1 ? CHILD_OK : CHILD_BAD;
}

/* A buffer of SIZE_MAX bytes would need SIZE_MAX + 1 bytes of memory. */
static int
new_max(void)
{
    Buffer buffer = newBuffer(SIZE_MAX);

    printf("newBuffer(SIZE_MAX) returned a buffer of size %zu\n",
           bufferSize(buffer));
    return CHILD_BAD;
}

/* A buffer over existing memory cannot hold more data than its size. */
static int
charp_too_much(void)
{
    static const char data[] = "abc";
    Buffer buffer = newBufferByCharP(data, 2, 3);

    printf("newBufferByCharP returned size %zu, data size %zu\n",
           bufferSize(buffer), bufferDataSize(buffer));
    return CHILD_BAD;
}

/*
 * Run a case in a child process with stdout and stderr on a pipe.  Returns
 * the wait status and stores up to length - 1 bytes of output in output.
 */
static int
run_case(int (*function)(void), char *output, size_t length)
{
    int fds[2], status;
    pid_t child;
    size_t used = 0;
    ssize_t count;
    char discard[512];

    fflush(stdout);
    fflush(stderr);
    if (pipe(fds) < 0)
        sysbail("cannot create pipe");
    child = fork();
    if (child < 0)
        sysbail("cannot fork");
    if (child == 0) {
        close(fds[0]);
        if (dup2(fds[1], 1) < 0 || dup2(fds[1], 2) < 0)
            _exit(255);
        close(fds[1]);
        status = (*function)();
        fflush(stdout);
        _exit(status);
    }
    close(fds[1]);
    for (;;) {
        if (used < length - 1)
            count = read(fds[0], output + used, length - 1 - used);
        else
            count = read(fds[0], discard, sizeof(discard));
        if (count < 0 && errno == EINTR)
            continue;
        if (count <= 0)
            break;
        if (used < length - 1)
            used += (size_t) count;
    }
    output[used] = '\0';
    close(fds[0]);
    while (waitpid(child, &status, 0) < 0)
        if (errno != EINTR)
            sysbail("waitpid failed");
    return status;
}

/* Print the first lines of the child's output as diagnostics. */
static void
show_output(const char *output)
{
    const char *start, *end;
    int lines = 0;

    for (start = output; *start != '\0' && lines < 12; start = end) {
        end = strchr(start, '\n');
        if (end == NULL)
            end = start + strlen(start);
        diag("  %.*s", (int) (end - start > 200 ? 200 : end - start), start);
        lines++;
        if (*end == '\n')
            end++;
    }
}

/*
 * Run a case and check that the child exited with the wanted status and, if
 * text is not NULL, that its output contains text.
 */
static void
check_case(int (*function)(void), int wanted, const char *text,
           const char *description)
{
    char output[4096];
    int status;
    bool success;

    status = run_case(function, output, sizeof(output));
    if (WIFSIGNALED(status))
        diag("%s: child killed by signal %d", description, WTERMSIG(status));
    else
        diag("%s: child exit status %d", description, WEXITSTATUS(status));
    show_output(output);
    success = WIFEXITED(status) && WEXITSTATUS(status) == wanted;
    if (success && text != NULL)
        success = (strstr(output, text) != NULL);
    ok(success, "%s", description);
}

int
main(void)
{
    plan(8);

    check_case(decr_valid, CHILD_OK, NULL,
               "bufferDecrDataSize by less than the data size");
    check_case(decr_too_much, 1, "size <= buff->dataSize",
               "bufferDecrDataSize below zero dies");
    check_case(incr_wrap, 1, "data size bigger than its memory alloc",
               "bufferIncrDataSize by SIZE_MAX dies");
    check_case(dup_null, CHILD_OK, NULL,
               "dupBufferArray(NULL) returns an empty array");
    check_case(expand_wrap, CHILD_OK, NULL,
               "expandBuffer refuses a growth that wraps the size");
    check_case(expand_to_max, CHILD_OK, NULL,
               "expandBuffer refuses a size of SIZE_MAX");
    check_case(new_max, 1, "Cannot allocate an oversized buffer",
               "newBuffer(SIZE_MAX) dies");
    check_case(charp_too_much, 1, "dataSize <= size",
               "newBufferByCharP with more data than size dies");

    return 0;
}

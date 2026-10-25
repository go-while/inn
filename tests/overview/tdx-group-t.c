/*
**  Test suite for tradindexed group index size validation and auditing.
**
**  Written by Kevin Bowling in 2026.
*/

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <sys/stat.h>
#include <sys/wait.h>

#include "inn/innconf.h"
#include "inn/libinn.h"
#include "inn/messages.h"
#include "tap/basic.h"

#include "../../storage/tradindexed/tdx-private.h"
#include "../../storage/tradindexed/tdx-structure.h"

/* Number of groups in the test index.  Enough for some of them to share a
   hash bucket in the group index. */
#define TEST_GROUPS 512

/* Warnings seen during an audit, recorded by audit_warning. */
static unsigned long audit_warnings;
static bool audit_loop;


/*
**  Build a stripped-down innconf struct that contains only those settings
**  that the tradindexed overview method cares about.
*/
static void
fake_innconf(void)
{
    innconf = xcalloc(1, sizeof(*innconf));
    innconf->enableoverview = true;
    innconf->groupbaseexpiry = true;
    innconf->keepmmappedthreshold = 1024;
    innconf->nfsreader = false;
    innconf->overcachesize = 20;
    innconf->ovmethod = xstrdup("tradindexed");
    innconf->pathdb = xstrdup("tdx-tmp");
    innconf->pathoverview = xstrdup("tdx-tmp");
    innconf->pathrun = xstrdup("tdx-tmp");
    innconf->tradindexedmmap = true;
}


/*
**  Message handler recording the warnings of an audit.
*/
static void audit_warning(size_t, const char *, va_list, int)
    __attribute__((__format__(printf, 2, 0)));

static void
audit_warning(size_t len UNUSED, const char *fmt, va_list args, int err)
{
    char buffer[BUFSIZ];

    vsnprintf(buffer, sizeof(buffer), fmt, args);
    if (audit_warnings < 10) {
        if (err != 0)
            diag("audit: %s: %s", buffer, strerror(err));
        else
            diag("audit: %s", buffer);
    }
    audit_warnings++;
    if (strstr(buffer, "reachable from multiple paths") != NULL)
        audit_loop = true;
}


/*
**  Run tdx_index_audit in a child process, so that an audit stuck in a loop
**  is killed by an alarm instead of hanging the test suite.  Returns false if
**  the audit did not finish, and otherwise sets the number of warnings and
**  whether a loop was reported.
*/
static bool
run_audit(bool fix, unsigned long *warnings, bool *loop)
{
    pid_t child;
    int status;

    *warnings = 0;
    *loop = false;
    fflush(stdout);
    fflush(stderr);
    child = fork();
    if (child < 0)
        sysbail("cannot fork");
    if (child == 0) {
        alarm(30);
        audit_warnings = 0;
        audit_loop = false;
        message_handlers_warn(1, audit_warning);
        tdx_index_audit(fix);
        fflush(stdout);
        if (audit_warnings > 127)
            audit_warnings = 127;
        _exit((int) (audit_warnings << 1) | (audit_loop ? 1 : 0));
    }
    if (waitpid(child, &status, 0) != child)
        sysbail("cannot wait for audit");
    if (!WIFEXITED(status)) {
        diag("audit killed by signal %d",
             WIFSIGNALED(status) ? WTERMSIG(status) : 0);
        return false;
    }
    *warnings = (unsigned long) WEXITSTATUS(status) >> 1;
    *loop = (WEXITSTATUS(status) & 1) != 0;
    return true;
}


/*
**  Create a group index and an active file with TEST_GROUPS groups.
*/
static void
create_index(void)
{
    struct group_index *index;
    FILE *active;
    char name[64];
    int i;

    if (system("/bin/rm -rf tdx-tmp") < 0)
        sysbail("cannot remove tdx-tmp");
    if (mkdir("tdx-tmp", 0755) < 0)
        sysbail("cannot create tdx-tmp");
    active = fopen("tdx-tmp/active", "w");
    if (active == NULL)
        sysbail("cannot create tdx-tmp/active");
    index = tdx_index_open(true);
    if (index == NULL)
        bail("cannot create the group index");
    for (i = 0; i < TEST_GROUPS; i++) {
        snprintf(name, sizeof(name), "test.group.%d", i);
        fprintf(active, "%s 0000000000 0000000001 y\n", name);
        if (!tdx_index_add(index, name, 1, 0, "y"))
            bail("cannot add %s to the group index", name);
    }
    if (fclose(active) == EOF)
        sysbail("cannot write tdx-tmp/active");
    tdx_index_close(index);
}


/*
**  Corrupt the group index file: turn a hash chain holding at least two
**  entries into a loop of length two, and do the same with the free list.
**  Returns false if no hash chain holds two entries.
*/
static bool
corrupt_index(void)
{
    struct group_header header;
    struct group_entry first, second;
    off_t base, offset, entry_size;
    int fd, bucket;

    fd = open("tdx-tmp/group.index", O_RDWR);
    if (fd < 0)
        sysbail("cannot open tdx-tmp/group.index");
    if (pread(fd, &header, sizeof(header), 0) != (ssize_t) sizeof(header))
        sysbail("cannot read the group index header");
    base = (off_t) sizeof(struct group_header);
    entry_size = (off_t) sizeof(struct group_entry);

    /* Find a chain A -> B and make B point back to A. */
    for (bucket = 0; bucket < TDX_HASH_SIZE; bucket++) {
        if (header.hash[bucket].recno < 0)
            continue;
        offset = base + (off_t) header.hash[bucket].recno * entry_size;
        if (pread(fd, &first, sizeof(first), offset)
            != (ssize_t) sizeof(first))
            sysbail("cannot read group entry");
        if (first.next.recno >= 0)
            break;
    }
    if (bucket == TDX_HASH_SIZE) {
        close(fd);
        return false;
    }
    offset = base + (off_t) first.next.recno * entry_size;
    if (pread(fd, &second, sizeof(second), offset) != (ssize_t) sizeof(second))
        sysbail("cannot read group entry");
    second.next.recno = header.hash[bucket].recno;
    if (pwrite(fd, &second, sizeof(second), offset)
        != (ssize_t) sizeof(second))
        sysbail("cannot write group entry");
    diag("hash chain loop %d -> %d -> %d in bucket %d",
         header.hash[bucket].recno, first.next.recno,
         header.hash[bucket].recno, bucket);

    /* Same for the first two entries of the free list. */
    if (header.freelist.recno < 0)
        bail("empty free list");
    offset = base + (off_t) header.freelist.recno * entry_size;
    if (pread(fd, &first, sizeof(first), offset) != (ssize_t) sizeof(first))
        sysbail("cannot read free entry");
    if (first.next.recno < 0)
        bail("free list with only one entry");
    offset = base + (off_t) first.next.recno * entry_size;
    if (pread(fd, &second, sizeof(second), offset) != (ssize_t) sizeof(second))
        sysbail("cannot read free entry");
    second.next.recno = header.freelist.recno;
    if (pwrite(fd, &second, sizeof(second), offset)
        != (ssize_t) sizeof(second))
        sysbail("cannot write free entry");
    diag("free list loop %d -> %d -> %d", header.freelist.recno,
         first.next.recno, header.freelist.recno);

    if (close(fd) < 0)
        sysbail("cannot write tdx-tmp/group.index");
    return true;
}


/*
**  Check that every test group can be found in the group index and that a
**  new group can still be added.
*/
static void
check_index(void)
{
    struct group_index *index;
    char name[64];
    int i, found;

    index = tdx_index_open(true);
    if (index == NULL)
        bail("cannot open the group index");
    for (found = 0, i = 0; i < TEST_GROUPS; i++) {
        snprintf(name, sizeof(name), "test.group.%d", i);
        if (tdx_index_entry(index, name) != NULL)
            found++;
    }
    is_int(TEST_GROUPS, found, "all groups found after repair");
    ok(tdx_index_add(index, "test.new", 1, 0, "y"),
       "a group can be added after repair");
    ok(tdx_index_entry(index, "test.new") != NULL, "the new group is found");
    tdx_index_close(index);
}


int
main(void)
{
    uintmax_t bytes;
    off_t size;
    bool valid, loop, finished;
    int count, oerrno;
    unsigned long warnings;

    plan(15);

    bytes = sizeof(struct group_header)
            + ((uintmax_t) INT_MAX + 1) * sizeof(struct group_entry);
    size = (off_t) bytes;
    if (size < 0 || (uintmax_t) size != bytes)
        skip_block(4, "off_t cannot represent the oversized test index");
    else {
        ok(tdx_index_entry_count(sizeof(struct group_header), &count),
           "accept an empty index");
        is_int(0, count, "empty index has no entries");
        errno = 0;
        valid = tdx_index_entry_count(size, &count);
        oerrno = errno;
        ok(!valid, "reject an index with more than INT_MAX entries");
        is_int(EOVERFLOW, oerrno, "oversized entry count sets errno");
    }

    /* Hash chain and free list loops of length two (issue #216). */
    fake_innconf();
    create_index();
    if (!corrupt_index()) {
        skip_block(11, "no hash chain with two entries");
        return 0;
    }
    finished = run_audit(false, &warnings, &loop);
    ok(finished, "audit finishes despite loops");
    ok(finished && loop, "audit reports the loops");
    finished = run_audit(true, &warnings, &loop);
    ok(finished, "repair finishes despite loops");
    ok(finished && loop, "repair reports the loops");
    ok(finished && warnings > 0, "repair reports problems");
    finished = run_audit(false, &warnings, &loop);
    ok(finished, "audit after repair finishes");
    ok(finished && warnings == 0, "audit after repair is clean");
    ok(finished && !loop, "no loop after repair");
    check_index();
    if (system("/bin/rm -rf tdx-tmp") < 0)
        sysdiag("cannot remove tdx-tmp");
    return 0;
}

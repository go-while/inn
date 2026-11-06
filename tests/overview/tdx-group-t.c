/*
**  Test suite for tradindexed group index auditing.
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
**  Open the group index file read-write and read its header.  Returns the
**  file descriptor.
*/
static int
open_group_index(struct group_header *header)
{
    int fd;

    fd = open("tdx-tmp/group.index", O_RDWR);
    if (fd < 0)
        sysbail("cannot open tdx-tmp/group.index");
    if (pread(fd, header, sizeof(*header), 0) != (ssize_t) sizeof(*header))
        sysbail("cannot read the group index header");
    return fd;
}


/*
**  Read or write the group entry with the given number.
*/
static off_t
entry_offset(long recno)
{
    return (off_t) sizeof(struct group_header)
           + (off_t) recno * (off_t) sizeof(struct group_entry);
}

static void
read_entry(int fd, long recno, struct group_entry *entry)
{
    if (pread(fd, entry, sizeof(*entry), entry_offset(recno))
        != (ssize_t) sizeof(*entry))
        sysbail("cannot read group entry %ld", recno);
}

static void
write_entry(int fd, long recno, const struct group_entry *entry)
{
    if (pwrite(fd, entry, sizeof(*entry), entry_offset(recno))
        != (ssize_t) sizeof(*entry))
        sysbail("cannot write group entry %ld", recno);
}

static void
write_header(int fd, const struct group_header *header)
{
    if (pwrite(fd, header, sizeof(*header), 0) != (ssize_t) sizeof(*header))
        sysbail("cannot write the group index header");
}


/*
**  Corrupt the group index file: turn a hash chain holding at least two
**  entries into a loop of length two, and do the same with the free list.
**  Returns false if no hash chain holds two entries.
*/
static bool
corrupt_loops(void)
{
    struct group_header header;
    struct group_entry first, second;
    int fd, bucket;

    fd = open_group_index(&header);

    /* Find a chain A -> B and make B point back to A. */
    for (bucket = 0; bucket < TDX_HASH_SIZE; bucket++) {
        if (header.hash[bucket].recno < 0)
            continue;
        read_entry(fd, header.hash[bucket].recno, &first);
        if (first.next.recno >= 0)
            break;
    }
    if (bucket == TDX_HASH_SIZE) {
        close(fd);
        return false;
    }
    read_entry(fd, first.next.recno, &second);
    second.next.recno = header.hash[bucket].recno;
    write_entry(fd, first.next.recno, &second);
    diag("hash chain loop %d -> %d -> %d in bucket %d",
         header.hash[bucket].recno, first.next.recno,
         header.hash[bucket].recno, bucket);

    /* Same for the first two entries of the free list. */
    if (header.freelist.recno < 0)
        bail("empty free list");
    read_entry(fd, header.freelist.recno, &first);
    if (first.next.recno < 0)
        bail("free list with only one entry");
    read_entry(fd, first.next.recno, &second);
    second.next.recno = header.freelist.recno;
    write_entry(fd, first.next.recno, &second);
    diag("free list loop %d -> %d -> %d", header.freelist.recno,
         first.next.recno, header.freelist.recno);

    if (close(fd) < 0)
        sysbail("cannot write tdx-tmp/group.index");
    return true;
}


/*
**  Corrupt the group index file: take count live groups out of their hash
**  chains and put them into the free list, which then holds them followed by
**  free entries.  If position is 0, they come first in the free list;
**  otherwise they follow the first free entry.  Each group is the only entry
**  of its hash chain, so it is no longer reachable from the hash table.
**  Returns false if there are not enough such groups.
*/
static bool
corrupt_orphans(int count, int position)
{
    struct group_header header;
    struct group_entry entry, free_entry;
    int fd, bucket, found, i;
    int buckets[4];
    long recno, head, tail;

    if (count > 4)
        bail("too many orphans requested");
    fd = open_group_index(&header);
    if (header.freelist.recno < 0)
        bail("empty free list");
    head = header.freelist.recno;

    /* Find groups alone in their hash chain. */
    for (found = 0, bucket = 0; bucket < TDX_HASH_SIZE && found < count;
         bucket++) {
        if (header.hash[bucket].recno < 0)
            continue;
        read_entry(fd, header.hash[bucket].recno, &entry);
        if (entry.next.recno < 0)
            buckets[found++] = bucket;
    }
    if (found < count) {
        close(fd);
        return false;
    }

    /* Where the orphans are inserted and what follows them. */
    if (position == 0) {
        tail = head;
    } else {
        read_entry(fd, head, &free_entry);
        tail = free_entry.next.recno;
    }
    if (tail < 0)
        bail("free list too short");

    /* Chain the orphans in front of tail, last one first. */
    for (i = count - 1; i >= 0; i--) {
        recno = header.hash[buckets[i]].recno;
        read_entry(fd, recno, &entry);
        entry.next.recno = tail;
        write_entry(fd, recno, &entry);
        header.hash[buckets[i]].recno = -1;
        tail = recno;
    }
    if (position == 0) {
        header.freelist.recno = tail;
    } else {
        free_entry.next.recno = tail;
        write_entry(fd, head, &free_entry);
    }
    write_header(fd, &header);
    diag("%d live group%s moved from the hash table into the free list at"
         " position %d, first is entry %ld",
         count, count == 1 ? "" : "s", position, tail);
    if (close(fd) < 0)
        sysbail("cannot write tdx-tmp/group.index");
    return true;
}


/*
**  Corrupt the group index file: make the first free entry point to the
**  head of a hash chain, so that the hash chain and the free list share their
**  entries.  Returns false if the hash table is empty.
*/
static bool
corrupt_shared(void)
{
    struct group_header header;
    struct group_entry free_entry;
    int fd, bucket;

    fd = open_group_index(&header);
    if (header.freelist.recno < 0)
        bail("empty free list");
    for (bucket = 0; bucket < TDX_HASH_SIZE; bucket++)
        if (header.hash[bucket].recno >= 0)
            break;
    if (bucket == TDX_HASH_SIZE) {
        close(fd);
        return false;
    }
    read_entry(fd, header.freelist.recno, &free_entry);
    free_entry.next.recno = header.hash[bucket].recno;
    write_entry(fd, header.freelist.recno, &free_entry);
    diag("free list entry %d continues with entry %d of bucket %d",
         header.freelist.recno, header.hash[bucket].recno, bucket);
    if (close(fd) < 0)
        sysbail("cannot write tdx-tmp/group.index");
    return true;
}


/*
**  Count the entries of the free list of the group index file, which must
**  not be damaged, and the entries of the file.  Returns -1 if the free list
**  is longer than the file or contains an invalid link.
*/
static long
count_free_entries(long *total)
{
    struct group_header header;
    struct group_entry entry;
    struct stat st;
    long recno, count;
    int fd;

    fd = open_group_index(&header);
    if (fstat(fd, &st) < 0)
        sysbail("cannot stat tdx-tmp/group.index");
    *total = (long) ((st.st_size - (off_t) sizeof(header))
                     / (off_t) sizeof(struct group_entry));
    count = 0;
    recno = header.freelist.recno;
    while (recno != -1) {
        if (recno < 0 || recno >= *total || count > *total) {
            close(fd);
            return -1;
        }
        read_entry(fd, recno, &entry);
        recno = entry.next.recno;
        count++;
    }
    close(fd);
    return count;
}


/*
**  Check that every test group can be found in the group index, that the
**  free list holds all the entries not used by a group, and that a new group
**  can still be added.
*/
static void
check_index(void)
{
    struct group_index *index;
    char name[64];
    long free_count, total;
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
    free_count = count_free_entries(&total);
    is_int(total - TEST_GROUPS - 1, free_count,
           "the free list holds all other entries");
}


/*
**  Audit and repair the group index with each damaged layout, checking that
**  the audits finish.  A layout either creates loops, which the audits must
**  report, or leaves entries on the wrong list, which the audits report as
**  other problems.  Runs 12 tests.
*/
static void
repair_index(bool expect_loop)
{
    bool loop, finished;
    unsigned long warnings;

    finished = run_audit(false, &warnings, &loop);
    ok(finished, "audit finishes");
    ok(finished && warnings > 0, "audit reports problems");
    finished = run_audit(true, &warnings, &loop);
    ok(finished, "repair finishes");
    ok(finished && warnings > 0, "repair reports problems");
    ok(finished && loop == expect_loop, "repair reports loops as expected");
    finished = run_audit(false, &warnings, &loop);
    ok(finished, "audit after repair finishes");
    ok(finished && warnings == 0, "audit after repair is clean");
    ok(finished && !loop, "no loop after repair");
    check_index();
}


int
main(void)
{
    plan(4 * 12);

    /* Hash chain and free list loops of length two (issue #216). */
    fake_innconf();
    create_index();
    if (!corrupt_loops())
        skip_block(12, "no hash chain with two entries");
    else
        repair_index(true);

    /* A live group in the free list, followed by free entries. */
    create_index();
    if (!corrupt_orphans(1, 0))
        skip_block(12, "no group alone in its hash chain");
    else
        repair_index(false);

    /* Two live groups in the middle of the free list. */
    create_index();
    if (!corrupt_orphans(2, 1))
        skip_block(12, "no two groups alone in their hash chains");
    else
        repair_index(false);

    /* A free list continuing into a hash chain. */
    create_index();
    if (!corrupt_shared())
        skip_block(12, "empty hash table");
    else
        repair_index(true);

    if (system("/bin/rm -rf tdx-tmp") < 0)
        sysdiag("cannot remove tdx-tmp");
    return 0;
}

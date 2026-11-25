/* Drive tinyleaf's handling of library statuses without filling the input
   buffer.  The NNTP greeting, command dispatch and replies are real. */
#include "portable/system.h"
#include "inn/nntp.h"
#include "inn/vector.h"

static enum nntp_status
read_command_status(struct nntp *nntp, struct cvector *command)
{
    enum nntp_status status = nntp_read_command(nntp, command);

    if (status == NNTP_READ_OK && command->count > 0
        && strcmp(command->strings[0], "LONG") == 0)
        return NNTP_READ_LONG;
    return status;
}

static enum nntp_status
read_article_status(struct nntp *nntp UNUSED, char **data UNUSED,
                    size_t *length UNUSED)
{
    return NNTP_READ_LONG;
}

#define nntp_read_command   read_command_status
#define nntp_read_multiline read_article_status
#include "../../innd/tinyleaf.c"

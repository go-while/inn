/* dispatch test suite. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/dispatch.h"
#include "inn/vector.h"
#include "tap/basic.h"

/* Prototypes for command callbacks. */
static void command_0(struct cvector *, void *);
static void command_1(struct cvector *, void *);
static void command_4(struct cvector *, void *);

/* Test command dispatch table. */
static const struct dispatch commands[] = {
    {"0", command_0, 0, 0, NULL},
    {"1", command_1, 1, 1, NULL},
    {"4", command_4, 2, 4, NULL}
};

/* Global string indicating the last command handler that was called. */
static const char *last_handler = NULL;

/* Global flag indicating whether the last handler was okay. */
static bool last_args_valid = false;

/* Command that takes 0 arguments. */
static void
command_0(struct cvector *command, void *cookie)
{
    last_handler = "0";
    last_args_valid = true;
    if (command->count != 1 || strcmp(command->strings[0], "0") != 0)
        last_args_valid = false;
    if (cookie != &last_handler)
        last_args_valid = false;
}

/* Command that takes 1 argument. */
static void
command_1(struct cvector *command, void *cookie)
{
    last_handler = "1";
    last_args_valid = true;
    if (command->count != 2 || strcmp(command->strings[0], "1") != 0)
        last_args_valid = false;
    if (cookie != &last_handler)
        last_args_valid = false;
}

/* Command that takes 2-4 arguments. */
static void
command_4(struct cvector *command, void *cookie)
{
    last_handler = "4";
    last_args_valid = true;
    if (command->count < 3 || command->count > 5)
        last_args_valid = false;
    if (strcmp(command->strings[0], "4") != 0)
        last_args_valid = false;
    if (cookie != &last_handler)
        last_args_valid = false;
}

/* Handler for unknown commands. */
static void
command_unknown(struct cvector *command UNUSED, void *cookie)
{
    last_handler = "unknown";
    last_args_valid = (cookie == &last_handler);
}

/* Handler for syntax errors. */
static void
command_syntax(struct cvector *command UNUSED, void *cookie)
{
    last_handler = "syntax";
    last_args_valid = (cookie == &last_handler);
}

int
main(void)
{
    struct cvector *command;

    plan(18);

    command = cvector_new();
    cvector_resize(command, 6);
    last_handler = NULL;
    cvector_add(command, "0");
    dispatch(command, commands, ARRAY_SIZE(commands), command_unknown,
             command_syntax, &last_handler);
    is_string("0", last_handler, "dispatch a command with no operands");
    ok(last_args_valid, "zero-operand command sees the expected vector");
    command->strings[0] = "1";
    cvector_add(command, "arg1");
    dispatch(command, commands, ARRAY_SIZE(commands), command_unknown,
             command_syntax, &last_handler);
    is_string("1", last_handler, "dispatch a command with one operand");
    ok(last_args_valid, "one-operand command sees the expected vector");
    command->strings[0] = "4";
    dispatch(command, commands, ARRAY_SIZE(commands), command_unknown,
             command_syntax, &last_handler);
    is_string("syntax", last_handler, "reject too few operands");
    ok(last_args_valid, "syntax handler sees the incomplete vector");
    cvector_add(command, "arg2");
    dispatch(command, commands, ARRAY_SIZE(commands), command_unknown,
             command_syntax, &last_handler);
    is_string("4", last_handler, "dispatch with two operands");
    ok(last_args_valid, "two-operand command sees the expected vector");
    cvector_add(command, "arg3");
    dispatch(command, commands, ARRAY_SIZE(commands), command_unknown,
             command_syntax, &last_handler);
    is_string("4", last_handler, "dispatch with three operands");
    ok(last_args_valid, "three-operand command sees the expected vector");
    cvector_add(command, "arg4");
    dispatch(command, commands, ARRAY_SIZE(commands), command_unknown,
             command_syntax, &last_handler);
    is_string("4", last_handler, "dispatch with four operands");
    ok(last_args_valid, "four-operand command sees the expected vector");
    cvector_add(command, "arg5");
    dispatch(command, commands, ARRAY_SIZE(commands), command_unknown,
             command_syntax, &last_handler);
    is_string("syntax", last_handler, "reject too many operands");
    ok(last_args_valid, "syntax handler sees the oversized vector");
    command->strings[0] = "5";
    dispatch(command, commands, ARRAY_SIZE(commands), command_unknown,
             command_syntax, &last_handler);
    is_string("unknown", last_handler, "select the unknown command handler");
    ok(last_args_valid, "unknown command handler sees the expected vector");
    cvector_resize(command, 0);
    dispatch(command, commands, ARRAY_SIZE(commands), command_unknown,
             command_syntax, &last_handler);
    is_string("unknown", last_handler,
              "select the unknown command handler for an empty vector");
    ok(last_args_valid, "unknown command handler sees the empty vector");

    return 0;
}

/* Test suite for list routines. */

#define LIBTEST_NEW_FORMAT 1

#include "portable/system.h"

#include "inn/libinn.h"
#include "inn/list.h"
#include "inn/messages.h"
#include "tap/basic.h"

int
main(void)
{
    struct list list;
    struct node a, b, c;

    plan(28);

    list_new(&list);
    ok(list_isempty(&list), "new list is empty");

    ok(list_addhead(&list, &a) == &a, "addhead returns the inserted node");
    ok(!list_isempty(&list), "addhead makes the list nonempty");
    ok(list_head(&list) == &a, "head finds the sole node");
    ok(list_tail(&list) == &a, "tail finds the sole node after addhead");
    ok(list_remhead(&list) == &a, "remhead returns the sole node");
    ok(list_isempty(&list), "remhead empties the list");

    ok(list_addhead(&list, &a) == &a, "addhead can reuse the removed node");
    ok(list_remtail(&list) == &a, "remtail finds a node added at the head");
    ok(list_isempty(&list), "remtail empties the list");

    ok(list_addtail(&list, &a) == &a, "addtail returns the inserted node");
    ok(!list_isempty(&list), "addtail makes the list nonempty");
    ok(list_head(&list) == &a, "head finds a node added at the tail");
    ok(list_tail(&list) == &a, "tail finds the sole node after addtail");
    ok(list_remhead(&list) == &a, "remhead finds a node added at the tail");
    ok(list_isempty(&list), "remhead empties the list after addtail");

    list_addtail(&list, &a);
    ok(list_remtail(&list) == &a, "remtail finds a node added at the tail");
    ok(list_isempty(&list), "remtail empties the list after addtail");

    list_addhead(&list, &a);
    ok(list_remove(&a) == &a, "remove returns the unlinked node");
    ok(list_isempty(&list), "remove empties a single-node list");

    list_addtail(&list, &a);
    list_addtail(&list, &b);
    list_insert(&list, &c, &a);
    ok(list_succ(&c) == &b, "middle node has the following node");
    ok(list_pred(&c) == &a, "middle node has the previous node");
    list_remove(&c);
    list_insert(&list, &c, &b);
    ok(list_succ(&c) == NULL, "last node has no successor");
    ok(list_pred(&c) == &b, "last node has the previous node");
    list_remove(&c);
    list_insert(&list, &c, NULL);
    ok(list_succ(&c) == &a, "first node has the following node");
    ok(list_pred(&c) == NULL, "first node has no predecessor");
    list_remove(&c);
    ok(list_head(&list) == &a,
       "head is unchanged after removing inserted nodes");
    ok(list_tail(&list) == &b,
       "tail is unchanged after removing inserted nodes");

    return 0;
}

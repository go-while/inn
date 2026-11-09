dnl Test for a working C99 snprintf.
dnl
dnl Check for a working snprintf.  Some old systems have an snprintf that
dnl doesn't nul-terminate if the buffer isn't large enough.  Others return -1
dnl if the string doesn't fit into the buffer instead of returning the number
dnl of characters that would have been formatted.  Still others don't support
dnl NULL as the buffer argument (just to get a count of the formatted length).
dnl
dnl Provides INN_FUNC_SNPRINTF, which makes configure fail unless a fully
dnl working snprintf is found.  INN no longer ships a replacement.
dnl
dnl The canonical version of this file was maintained (until 2020) in the
dnl rra-c-util package, available at
dnl <https://www.eyrie.org/~eagle/software/rra-c-util/>.
dnl
dnl Written by Russ Allbery <eagle@eyrie.org>
dnl Copyright 2006, 2008-2009
dnl     The Board of Trustees of the Leland Stanford Junior University
dnl
dnl This file is free software; the authors give unlimited permission to copy
dnl and/or distribute it, with or without modifications, as long as this
dnl notice is preserved.
dnl
dnl SPDX-License-Identifier: FSFULLR

dnl Source used by INN_FUNC_SNPRINTF.
AC_DEFUN([_INN_FUNC_SNPRINTF_SOURCE], [[
#include <stdarg.h>
#include <stdio.h>

char buf[2];

int
test(char *format, ...)
{
    va_list args;
    int count;

    va_start(args, format);
    count = vsnprintf(buf, sizeof buf, format, args);
    va_end(args);
    return count;
}

int
main()
{
    return ((test("%s", "abcd") == 4 && buf[0] == 'a' && buf[1] == '\0'
             && snprintf(NULL, 0, "%s", "abcd") == 4)
                ? 0
                : 1);
}
]])

dnl The user-callable test.  A working snprintf and vsnprintf are required
dnl (C99 or later); there is no replacement.  When cross-compiling, the check
dnl cannot run and the C99 behaviour is assumed.
AC_DEFUN([INN_FUNC_SNPRINTF],
[AC_CACHE_CHECK([for working snprintf], [inn_cv_func_snprintf_works],
    [AC_RUN_IFELSE([AC_LANG_SOURCE([_INN_FUNC_SNPRINTF_SOURCE])],
        [inn_cv_func_snprintf_works=yes],
        [inn_cv_func_snprintf_works=no],
        [inn_cv_func_snprintf_works=yes])])
 AS_IF([test x"$inn_cv_func_snprintf_works" != xyes],
    [AC_MSG_ERROR([a working C99 snprintf and vsnprintf are required])])])

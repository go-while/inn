#! /bin/sh
#
# Test that tinyleaf ends the session after an overlong command line.

tinyleaf="../../innd/tinyleaf"
dirs='. innd tests/innd'
for dir in $dirs; do
    if [ -r "$dir/tinyleaf-long.t" ]; then
        cd $dir
        break
    fi
done
if [ ! -x "$tinyleaf" ]; then
    echo 'Could not find tinyleaf' >&2
    exit 1
fi

echo 2

spool=tinyleaf-spool.$$
rm -rf "$spool"
mkdir "$spool"

# 1 MiB plus 16 bytes with no newline fills tinyleaf's 1 MiB input buffer.
# A tinyleaf that keeps going after NNTP_READ_LONG never reads again; the
# short timeout ends it.
perl -e 'print "a" x (1024 * 1024 + 16)' \
    | timeout 3 "$tinyleaf" "$spool" >out 2>/dev/null
rc=$?

if [ "$rc" = 0 ]; then
    echo "ok 1 - tinyleaf exits 0 after an overlong line"
else
    echo "not ok 1 - tinyleaf exits 0 after an overlong line (rc=$rc)"
fi
if grep -q '^20[01] ' out; then
    echo "ok 2 - banner was sent"
else
    echo "not ok 2 - banner was sent"
fi

rm -rf "$spool" out

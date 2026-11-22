#! /bin/sh
#
# Test suite for the --store option of delayer.

# The count starts at 1 and is updated each time ok is printed.  printcount
# takes "ok" or "not ok".
count=1
printcount() {
    echo "$1 $count $2"
    count=$(expr $count + 1)
}

# Takes a shell condition and a description; prints ok if the condition holds.
check() {
    if eval "$1"; then
        printcount "ok" "$2"
    else
        printcount "not ok" "$2"
    fi
}

# Find delayer.
delayer=
for dir in ../backends ../../backends backends; do
    if [ -x "$dir/delayer" ]; then
        delayer="$dir/delayer"
        break
    fi
done
if [ -z "$delayer" ]; then
    echo "Could not find delayer" >&2
    exit 1
fi

tmp="delayer-tmp.$$"
rm -rf "$tmp"
mkdir "$tmp" || exit 1

# Print out the number of tests.
echo 12

# The store cannot be written (its directory does not exist): the queued lines
# must be written out instead of being lost.
printf 'line one\nline two\n' \
    | "$delayer" --delay 600 --store "$tmp/missing/queue" \
        -- sh -c "cat >$tmp/out1" 2>"$tmp/err1"
printf 'line one\nline two\n' >"$tmp/want1"
check 'cmp -s "$tmp/want1" "$tmp/out1"' \
    "lines written out when the store cannot be saved"
check 'grep "cannot save" "$tmp/err1" >/dev/null' \
    "failed save reported"

# The store cannot be read back: it must be kept as .bad.
echo 'not a saved queue' >"$tmp/queue2"
cp "$tmp/queue2" "$tmp/want2"
"$delayer" --delay 600 --store "$tmp/queue2" -- sh -c "cat >$tmp/out2" \
    </dev/null 2>"$tmp/err2"
check 'cmp -s "$tmp/want2" "$tmp/queue2.bad"' \
    "unreadable store kept as .bad"
check 'grep "cannot restore" "$tmp/err2" >/dev/null' \
    "failed restore reported"

# Another store which cannot be read back: the first one kept aside must not
# be replaced.
echo 'not a saved queue either' >"$tmp/queue2"
cp "$tmp/queue2" "$tmp/want2b"
"$delayer" --delay 600 --store "$tmp/queue2" -- sh -c "cat >$tmp/out2" \
    </dev/null 2>"$tmp/err2"
check 'cmp -s "$tmp/want2" "$tmp/queue2.bad"' \
    "earlier .bad file kept"
check 'cmp -s "$tmp/want2b" "$tmp/queue2.bad.1"' \
    "second unreadable store kept as .bad.1"

# The store cannot be saved and the lines cannot be written out either: the
# failure must be reported in the exit status.
printf 'line one\n' \
    | "$delayer" --delay 600 --store "$tmp/missing/queue" \
        -- sh -c "exit 3" 2>"$tmp/err6"
status=$?
check '[ "$status" != 0 ] && grep "cannot write out" "$tmp/err6" >/dev/null' \
    "failed write of the queued lines reported"

# Normal use: the lines are saved, restored on the next start and saved again
# (their delay has not expired), and nothing else is left behind or touched
# (queue3.new could be the store of another feed).
echo 'another file' >"$tmp/queue3.new"
cp "$tmp/queue3.new" "$tmp/want3"
printf 'a\nb\n' \
    | "$delayer" --delay 600 --store "$tmp/queue3" \
        -- sh -c "cat >$tmp/out3" 2>"$tmp/err3"
check '[ -s "$tmp/queue3" ] && [ ! -s "$tmp/out3" ] && [ ! -s "$tmp/err3" ]' \
    "lines saved, not written out"
"$delayer" --delay 600 --store "$tmp/queue3" -- sh -c "cat >$tmp/out4" \
    </dev/null 2>"$tmp/err4"
n=$(perl -MStorable -e 'print scalar @{ Storable::retrieve($ARGV[0]) }' \
    "$tmp/queue3" 2>/dev/null)
check '[ "$n" = 2 ] && [ ! -s "$tmp/out4" ] && [ ! -s "$tmp/err4" ]' \
    "lines restored and saved again"
perl -MStorable \
    -e 'for (@{ Storable::retrieve($ARGV[0]) }) { s/^[^:]+://; print }' \
    "$tmp/queue3" >"$tmp/saved3" 2>/dev/null
printf 'a\nb\n' >"$tmp/want4"
check 'cmp -s "$tmp/want4" "$tmp/saved3"' \
    "saved lines unchanged"
left=$(cd "$tmp" && ls | grep '^queue3\.' | grep -v '^queue3\.new$')
check '[ -z "$left" ] && cmp -s "$tmp/want3" "$tmp/queue3.new"' \
    "no temporary file left, other files untouched"
printf 'a\nb\n' >"$tmp/want5"
"$delayer" --delay 600 -- sh -c "cat >$tmp/out5" <"$tmp/want5"
check 'cmp -s "$tmp/want5" "$tmp/out5"' \
    "without --store, lines written out at the end of the input"

# Clean up.
rm -rf "$tmp"

#! /bin/sh
#
# Test suite for mod-active, with a stand-in for ctlinnd.  Checks that the
# server is resumed when mod-active fails after pausing it.

. "$C_TAP_SOURCE/tap/libtap.sh"

script="$C_TAP_SOURCE/../backends/mod-active.in"
tmpdir=$(test_tmpdir)
fixture="$tmpdir/mod-active-$$"
rm -rf "$fixture"
mkdir -p "$fixture/bin" "$fixture/lib/INN" || bail "cannot create $fixture"
export fixture

plan 33

# The script is run from the source with this module instead of the
# INN::Config module that fixscript loads in the installed script.
cat >"$fixture/lib/INN/Config.pm" <<'EOF'
package INN::Config;
our $active = "$ENV{fixture}/active";
our $activetimes = "$ENV{fixture}/active.times";
our $newsbin = "$ENV{fixture}/bin";
our $newsuser = getpwuid($>);
1;
EOF

# The stand-in for ctlinnd logs its commands to ctl.calls and keeps the pause
# reason in the file paused.  Like innd, pause fails when the server is
# already paused, and go only resumes a pause with the same reason.  The flag
# file gofail makes go fail, reloadfail makes reload fail, and pausesig makes
# pause send SIGTERM to mod-active once the server is paused.
cat >"$fixture/bin/ctlinnd" <<'EOF'
#! /bin/sh
echo "$*" >>"$fixture/ctl.calls"
test "$1" = -s && shift
command=$1
shift
case "$command" in
pause)
    if [ -f "$fixture/paused" ]; then
        echo 'Already paused'
        exit 1
    fi
    echo "$*" >"$fixture/paused"
    test -f "$fixture/pausesig" && kill -TERM "$PPID"
    ;;
go)
    if [ -f "$fixture/gofail" ]; then
        echo 'go failed (simulated)'
        exit 1
    fi
    if [ ! -f "$fixture/paused" ] || [ "$(cat "$fixture/paused")" != "$*" ]
    then
        echo 'Wrong reason'
        exit 1
    fi
    rm -f "$fixture/paused"
    ;;
reload)
    if [ -f "$fixture/reloadfail" ]; then
        echo 'reload failed (simulated)'
        exit 1
    fi
    ;;
esac
exit 0
EOF
chmod +x "$fixture/bin/ctlinnd"
mkdir "$fixture/nodiff" || bail "cannot create $fixture/nodiff"
printf '#! /bin/sh\nexit 0\n' >"$fixture/nodiff/diff"
chmod +x "$fixture/nodiff/diff"

# Writer of the active file when it is a FIFO.  Opening the FIFO waits until
# mod-active opens it, after the pause.  It then sends SIGTERM to mod-active
# and writes one line.
cat >"$fixture/writer.pl" <<'EOF'
alarm 20;
$SIG{PIPE} = 'IGNORE';
open(my $fh, '>', $ARGV[0]) or die "open $ARGV[0]: $!\n";
kill('TERM', $ARGV[1]) or die "kill $ARGV[1]: $!\n";
print $fh "test.old 0000000001 0000000001 y\n";
close($fh);
EOF

echo 'ctlinnd newgroup test.new y' >"$fixture/input"

# Start every case from a fresh active file and a running server.
reset() {
    rm -rf "$fixture"/active* "$fixture/ctl.calls" "$fixture/paused" \
        "$fixture/gofail" "$fixture/reloadfail" "$fixture/pausesig"
    printf 'test.old 0000000001 0000000001 y\n' >"$fixture/active"
}

# Run mod-active on the input file.  It is started by a shell that keeps its
# PID for perl and first runs the commands given as argument, for instance to
# prepare the temporary new active file named after that PID.
run() {
    sh -c "${1:-:}"' && exec perl -I"$fixture/lib" -MINN::Config "$0" "$1"' \
        "$script" "$fixture/input" >"$fixture/output" 2>&1
    status=$?
}

# Run mod-active in the background with the active file read from a FIFO
# that the writer opens, so that mod-active receives SIGTERM after the pause.
# Should mod-active go on, diff is replaced so that it does not wait for ever
# on the FIFO, renamed to active.old.
run_fifo() {
    rm -f "$fixture/active"
    mkfifo "$fixture/active" || bail 'cannot create a FIFO'
    PATH="$fixture/nodiff:$PATH" perl -I"$fixture/lib" -MINN::Config \
        "$script" "$fixture/input" >"$fixture/output" 2>&1 &
    pid=$!
    if ! perl "$fixture/writer.pl" "$fixture/active" "$pid"; then
        kill -KILL "$pid" 2>/dev/null
    fi
    wait "$pid"
    status=$?
}

failed() {
    test "$status" -ne 0
}

gocount() {
    grep -c '^-s go ' "$fixture/ctl.calls"
}

resumed() {
    test ! -f "$fixture/paused" && test "$(gocount)" -eq 1
}

reset
run
ok 'update succeeds' test "$status" -eq 0
ok 'new group is in active' grep -q '^test.new ' "$fixture/active"
ok 'server is running after the update' test ! -f "$fixture/paused"
ok 'server is resumed once' test "$(gocount)" -eq 1

reset
rm "$fixture/active"
run
missing_status=$status
ok 'missing active: failure' failed
ok 'missing active: reported' grep -q "open $fixture/active:" \
    "$fixture/output"
ok 'missing active: server resumed' resumed

reset
rm "$fixture/active"
touch "$fixture/gofail"
run
ok 'go fails: same exit status' test "$status" -eq "$missing_status"
ok 'go fails: reported' grep -q '"go batch active update, ok" failed' \
    "$fixture/output"

reset
run 'mkdir "$fixture/active.new$$"'
ok 'new active not created: failure' failed
ok 'new active not created: reported' grep -q "open $fixture/active.new" \
    "$fixture/output"
ok 'new active not created: server resumed' resumed

reset
if [ -c /dev/full ]; then
    perl -e 'printf "test.%d 0000000001 0000000001 y\n", $_ for 1 .. 5000' \
        >"$fixture/active"
    run 'ln -s /dev/full "$fixture/active.new$$"'
    ok 'new active not written: failure' failed
    ok 'new active not written: reported' \
        grep -q "writing $fixture/active.new[0-9]* failed" "$fixture/output"
    ok 'new active not written: server resumed' resumed
else
    skip 'no /dev/full'
    skip 'no /dev/full'
    skip 'no /dev/full'
fi

reset
run_fifo
ok 'signal while rewriting: failure' failed
ok 'signal while rewriting: reported' grep -q 'caught SIGTERM' \
    "$fixture/output"
ok 'signal while rewriting: server resumed' resumed

reset
touch "$fixture/pausesig"
run
ok 'signal while pausing: failure' failed
ok 'signal while pausing: reported' grep -q 'caught SIGTERM' \
    "$fixture/output"
ok 'signal while pausing: server resumed' resumed

# A signal ignored when mod-active is started stays ignored.
reset
touch "$fixture/pausesig"
run 'trap "" TERM'
ok 'ignored signal: update succeeds' test "$status" -eq 0
ok 'ignored signal: server running' resumed

# A pause made by someone else is neither taken over nor resumed.
reset
echo 'other reason' >"$fixture/paused"
run
ok 'already paused: failure' failed
ok 'already paused: go not sent' test "$(gocount)" -eq 0
ok 'already paused: other pause kept' \
    test "$(cat "$fixture/paused")" = 'other reason'

# A failure to rename the current active file to active.old only gives a
# warning: the new active file still replaces it.
reset
mkdir "$fixture/active.old"
run
ok 'active.old not renamed: update succeeds' test "$status" -eq 0
ok 'active.old not renamed: warning' \
    grep -q "rename $fixture/active $fixture/active.old:" "$fixture/output"
ok 'active.old not renamed: new group is in active' \
    grep -q '^test.new ' "$fixture/active"
ok 'active.old not renamed: server resumed' resumed

# Once the new active file is in place, a failure leaves the server paused, as
# before (the server has not reloaded the active file).
reset
touch "$fixture/reloadfail"
run
ok 'reload failure: failure' failed
ok 'reload failure: go not sent' test "$(gocount)" -eq 0
ok 'reload failure: server still paused' test -f "$fixture/paused"

rm -rf "$fixture"
rmdir "$tmpdir" 2>/dev/null || :

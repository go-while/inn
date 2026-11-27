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

plan 19

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
# reason in the file paused.  Like innd, go only resumes a pause with the same
# reason.  The flag file reserved makes pause fail, and reloadfail makes
# reload fail.
cat >"$fixture/bin/ctlinnd" <<'EOF'
#! /bin/sh
echo "$*" >>"$fixture/ctl.calls"
test "$1" = -s && shift
command=$1
shift
case "$command" in
pause)
    if [ -f "$fixture/reserved" ]; then
        echo 'Reserved "fixture"'
        exit 1
    fi
    echo "$*" >"$fixture/paused"
    ;;
go)
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

echo 'ctlinnd newgroup test.new y' >"$fixture/input"

# Start every case from a fresh active file and a running server.
reset() {
    rm -rf "$fixture"/active* "$fixture/ctl.calls" "$fixture/paused" \
        "$fixture/reserved" "$fixture/reloadfail"
    printf 'test.old 0000000001 0000000001 y\n' >"$fixture/active"
}

# Run mod-active on the input file.  With an argument, the command first runs
# in a shell that keeps its PID for perl, so that it can prepare the temporary
# new active file named after that PID.
run() {
    if [ -n "$1" ]; then
        sh -c "$1"' && exec perl -I"$fixture/lib" -MINN::Config "$0" "$1"' \
            "$script" "$fixture/input" >"$fixture/output" 2>&1
    else
        perl -I"$fixture/lib" -MINN::Config "$script" "$fixture/input" \
            >"$fixture/output" 2>&1
    fi
    status=$?
}

failed() {
    test "$status" -ne 0
}

resumed() {
    test ! -f "$fixture/paused" && grep -q '^-s go ' "$fixture/ctl.calls"
}

reset
run
ok 'update succeeds' test "$status" -eq 0
ok 'new group is in active' grep -q '^test.new ' "$fixture/active"
ok 'server is running after the update' test ! -f "$fixture/paused"
ok 'server is resumed once' \
    test "$(grep -c '^-s go ' "$fixture/ctl.calls")" -eq 1

reset
rm "$fixture/active"
run
ok 'missing active: failure' failed
ok 'missing active: reported' grep -q "open $fixture/active:" \
    "$fixture/output"
ok 'missing active: server resumed' resumed

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
    ok 'new active not written: server resumed' resumed
else
    skip 'no /dev/full'
    skip 'no /dev/full'
fi

# Interrupt mod-active after the pause.  It reads the active file from a FIFO,
# so that it waits for the helper to open it.  The helper then sends SIGTERM
# to mod-active.
reset
rm "$fixture/active"
mkfifo "$fixture/active" || bail 'cannot create a FIFO'
perl -I"$fixture/lib" -MINN::Config "$script" "$fixture/input" \
    >"$fixture/output" 2>&1 &
pid=$!
if ! perl -e 'alarm 20; open(my $fh, ">", $ARGV[0]) or die "open: $!\n";
        kill("TERM", $ARGV[1]) or die "kill: $!\n"' "$fixture/active" "$pid"
then
    kill -KILL "$pid" 2>/dev/null
fi
wait "$pid"
status=$?
ok 'signal: failure' failed
ok 'signal: reported' grep -q 'caught SIGTERM' "$fixture/output"
ok 'signal: server resumed' resumed

reset
touch "$fixture/reserved"
run
ok 'pause refused: failure' failed
ok 'pause refused: go not sent' \
    test "$(grep -c '^-s go ' "$fixture/ctl.calls")" -eq 0

# Once the new active file is in place, a failure leaves the server paused, as
# before (the server has not reloaded the active file).
reset
touch "$fixture/reloadfail"
run
ok 'reload failure: failure' failed
ok 'reload failure: server still paused' test -f "$fixture/paused"

rm -rf "$fixture"
rmdir "$tmpdir" 2>/dev/null || :

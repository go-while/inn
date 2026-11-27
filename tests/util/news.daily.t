#! /bin/sh
#
# Test suite for the recovery of the server mode by news.daily after expire.
# news.daily runs in a private directory with stand-ins for ctlinnd, expire
# and shlock.

. "$C_TAP_SOURCE/tap/libtap.sh"

plan 21

tmpdir=$(test_tmpdir)
fixture=$tmpdir/news.daily-$$
rm -rf "$fixture"
mkdir -p "$fixture/bin" "$fixture/db" "$fixture/log" || bail 'cannot mkdir'
export fixture

cat >"$fixture/vars" <<'EOF'
MOST_LOGS=$fixture/log
PATHBIN=$fixture/bin
PATHDB=$fixture/db
PATHETC=$fixture
PATHSPOOL=$fixture
LOCKS=$fixture
HISTORY=$fixture/db/history
DO_DBZ_TAGGED_HASH=DONT
ACTIVE=$fixture/db/active
TMPDIR=$fixture
AWK=awk
SED=sed
SORT=sort
RNEWS=true
PATH=$fixture/bin:$PATH
export PATH
EOF

# The stand-in for ctlinnd logs its commands to ctl.calls and keeps the pause
# reason in the file paused and the reservation in the file reserved.  Like
# innd, a pause takes over a reservation with the same reason, a reservation
# needs a running server, and go needs the reason of the pause.  innd reopens
# the history on go and exits if it cannot:  the stand-in then records it in
# the file exited.
cat >"$fixture/bin/ctlinnd" <<'EOF'
#! /bin/sh
echo "$*" >>"$fixture/ctl.calls"
while :; do
    case "$1" in
    -s) shift ;;
    -t) shift 2 ;;
    -t*) shift ;;
    *) break ;;
    esac
done
command=$1
reason=$2
case "$command" in
mode)
    if [ -f "$fixture/paused" ]; then
        echo "Server paused $(cat "$fixture/paused")"
    else
        echo 'Server running'
    fi
    if [ -f "$fixture/reserved" ]; then
        echo "Reserved $(cat "$fixture/reserved")"
    else
        echo 'Not reserved'
    fi
    ;;
pause)
    test -f "$fixture/paused" && exit 1
    if [ -f "$fixture/reserved" ]; then
        test "$(cat "$fixture/reserved")" = "$reason" || exit 1
        rm -f "$fixture/reserved"
    fi
    echo "$reason" >"$fixture/paused"
    ;;
go)
    test -f "$fixture/paused" || exit 1
    if [ -n "$reason" ]; then
        test "$(cat "$fixture/paused")" = "$reason" || exit 1
    fi
    rm -f "$fixture/paused"
    for f in history history.dir history.index history.hash; do
        test -f "$fixture/db/$f" || touch "$fixture/exited"
    done
    ;;
reserve)
    test -f "$fixture/paused" && exit 1
    if [ -n "$reason" ]; then
        test -f "$fixture/reserved" && exit 1
        echo "$reason" >"$fixture/reserved"
    else
        test -f "$fixture/reserved" || exit 1
        rm -f "$fixture/reserved"
    fi
    ;;
esac
exit 0
EOF

# The stand-in for expire reserves the server, writes the new history files
# next to the old ones, pauses the server at the end of the history, and
# replaces the old files with the new ones in the order of hisv6_unlink and
# hisv6_rename, as expire does with the hisv6 history method.  With the flag
# file killexpire, it is killed while the server is paused, before the
# replacement.  With the flag file killswap, it is killed after the old index
# files have been removed.
cat >"$fixture/bin/expire" <<'EOF'
#! /bin/sh
echo $$ >"$fixture/expire.pid"
ctlinnd -s reserve "Expiring process $$" || exit 1
cd "$fixture/db" || exit 1
for f in history.n history.n.dir history.n.index history.n.hash; do
    echo new >"$f"
done
ctlinnd -s pause "Expiring process $$" || exit 1
test -f "$fixture/killexpire" && kill -KILL $$
rm -f history.index history.hash
test -f "$fixture/killswap" && kill -KILL $$
rm -f history.dir history
for f in index hash dir; do
    mv history.n.$f history.$f
done
mv history.n history
ctlinnd -s reserve ""
ctlinnd -s go "Expiring process $$"
EOF

cat >"$fixture/bin/shlock" <<'EOF'
#! /bin/sh
echo "$2" >"$4"
EOF
printf '#! /bin/sh\nexit 0\n' >"$fixture/bin/sleep"
printf '#! /bin/sh\nexit 0\n' >"$fixture/bin/innstat"
chmod +x "$fixture"/bin/*
touch "$fixture/db/active"

sed '2c\
. "$fixture/vars"
' "$C_TAP_SOURCE/../scripts/news.daily.in" >"$fixture/news.daily"

# Run news.daily with only expire and the recovery after it, starting with a
# complete history.  The first argument is the pause reason of the server
# before the run (empty if running); the other ones are passed to news.daily.
run() {
    rm -f "$fixture/ctl.calls" "$fixture/paused" "$fixture/reserved" \
        "$fixture/expire.pid" "$fixture/exited" "$fixture"/db/history*
    for f in history history.dir history.index history.hash; do
        echo old >"$fixture/db/$f"
    done
    test -n "$1" && echo "$1" >"$fixture/paused"
    shift
    sh "$fixture/news.daily" nologs noexpireover noexplog noprocbatch \
        norenumber norm nostat nomail "$@" >"$fixture/output" 2>&1
}

# Check that no go command has been sent to the server.
nogo() {
    grep -e ' go ' "$fixture/ctl.calls" >/dev/null && return 1
    return 0
}

# Check the pause reason of the server.
paused() {
    test -f "$fixture/paused" && test "$(cat "$fixture/paused")" = "$1"
}

# Check that the given line is in the file, without printing it.
hasline() {
    grep -e "$1" "$2" >/dev/null
}

# A normal expire resumes the server itself.
run ''
ok 'normal run: news.daily succeeds' test $? -eq 0
ok 'normal run: server running' test ! -f "$fixture/paused"
ok 'normal run: only expire sends go' \
    test "$(grep -c -e ' go ' "$fixture/ctl.calls")" -eq 1
ok 'normal run: new history installed' hasline new "$fixture/db/history"

# expire is killed while the server is paused, before it replaced the
# history files.  The old ones are complete, the new ones are left.
touch "$fixture/killexpire"
run ''
ok 'killed expire: news.daily succeeds' test $? -eq 0
ok 'killed expire: server resumed' test ! -f "$fixture/paused"
ok 'killed expire: innd did not exit' test ! -f "$fixture/exited"
ok 'killed expire: old history kept' hasline old "$fixture/db/history"
ok 'killed expire: go with the reason of expire' \
    hasline "^-s go Expiring process $(cat "$fixture/expire.pid")\$" \
    "$fixture/ctl.calls"
ok 'killed expire: recovery reported' \
    hasline 'paused Expiring process [0-9]* -- trying to recover' \
    "$fixture/output"
rm -f "$fixture/killexpire"

# expire is killed while it replaces the history files.
touch "$fixture/killswap"
run ''
ok 'killed during swap: news.daily succeeds' test $? -eq 0
ok 'killed during swap: pause kept' \
    paused "Expiring process $(cat "$fixture/expire.pid")"
ok 'killed during swap: no go' nogo
ok 'killed during swap: missing files reported' \
    hasline 'not resumed, missing: .*/history[.]index .*/history[.]hash$' \
    "$fixture/output"
ok 'killed during swap: go command given' \
    hasline "^then run: ctlinnd go 'Expiring process [0-9]*'\$" \
    "$fixture/output"
rm -f "$fixture/killswap"

# A pause left by an expire process which is gone is kept when news.daily
# does not run expire.
deadpid=$(sh -c 'echo $$')
run "Expiring process $deadpid" noexpire
ok 'noexpire: pause kept' paused "Expiring process $deadpid"
ok 'noexpire: no go' nogo

# A pause of an expire process which is still running is kept.  This test
# script stands for that expire process.
run "Expiring process $$"
ok 'running expire: pause kept' paused "Expiring process $$"
ok 'running expire: no go' nogo

# A pause by someone else is kept.
run 'Maintenance'
ok 'other reason: pause kept' paused 'Maintenance'
ok 'other reason: no go' nogo

rm -rf "$fixture"
rmdir "$tmpdir" 2>/dev/null

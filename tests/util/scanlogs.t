#! /bin/sh
# Exercise scanlogs in a private spool with a stand-in for ctlinnd.
. "$C_TAP_SOURCE/tap/libtap.sh"
plan 78
tmpdir=$(test_tmpdir)
fixture=$tmpdir/scanlogs-$$
mkdir -p "$fixture/bin" "$fixture/log" || exit 1
export fixture
cat >"$fixture/vars" <<'EOF'
MOST_LOGS=$fixture/log
PATHRUN=$fixture
PATHETC=$fixture
PATHBIN=$fixture/bin
LOCKS=$fixture
ACTIVE=$fixture/active
TMPDIR=$fixture
LOGCYCLES=2
LOG_COMPRESS=${TEST_DEFAULT-cat}
Z=''
AWK=awk
SED=sed
HISTORY=$fixture/history
PATHDB=$fixture
PATHSPOOL=$fixture
RNEWS=true
MAILCMD=$fixture/bin/mail
PATH=$fixture/bin:$PATH
export PATH
EOF
# The stand-in for ctlinnd logs its commands to ctl.calls and keeps the pause
# reason in the file paused.  Flag files in the fixture make commands fail or
# send SIGTERM to scanlogs while the server is paused.
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
shift
case "$command" in
    mode)
        test -e "$fixture/modefail" && exit 1
        if [ "${TEST_THROTTLE_FAILURE:-}" = yes ]; then
            echo 'Server paused by [innwatch: fixture]'
        elif [ -f "$fixture/paused" ]; then
            echo "Server paused $(cat "$fixture/paused")"
        else
            echo 'Server running'
        fi
        ;;
    pause)
        echo "$*" >"$fixture/paused"
        test -e "$fixture/pausesig" \
            && kill -TERM "$(cat "$fixture/LOCK.scanlogs")"
        ;;
    go)
        if [ -e "$fixture/gofail" ]; then
            echo 'go failed (simulated)'
            exit 1
        fi
        if [ ! -f "$fixture/paused" ] \
            || [ "$(cat "$fixture/paused")" != "$*" ]; then
            echo 'Not paused with this reason'
            exit 1
        fi
        rm -f "$fixture/paused"
        ;;
    flushlogs)
        test -e "$fixture/flushsig" \
            && kill -TERM "$(cat "$fixture/LOCK.scanlogs")"
        if [ -e "$fixture/flushfail" ]; then
            echo 'Cannot flush (simulated)'
            exit 1
        fi
        echo Ok
        ;;
    throttle)
        if [ "${TEST_THROTTLE_FAILURE:-}" = yes ]; then
            test -e "$fixture/takelock" \
                && echo 99999 >"$fixture/LOCK.news.daily"
            exit 1
        fi
        ;;
esac
exit 0
EOF
printf '#! /bin/sh\nfor lock do :; done\necho "$2" >"$lock"\n' >"$fixture/bin/shlock"
printf '#! /bin/sh\ncat >"$fixture/mail"\n' >"$fixture/bin/mail"
cat >"$fixture/bin/gzip" <<'EOF'
#! /bin/sh
case "$1" in
    -9) cat ;;
    -interrupt)
        if IFS= read -r line && [ "$line" = interruption ]; then
            echo partial
            test ! -f "$fixture/log/OLD/news.0.gz" &&
                touch "$fixture/archive-hidden"
            kill -TERM "$(cat "$fixture/LOCK.scanlogs")"
        fi
        ;;
    *) echo partial; exit 1 ;;
esac
EOF
printf '#! /bin/sh\necho analysis-complete\n' >"$fixture/bin/innreport"
chmod +x "$fixture"/bin/*
touch "$fixture/active"
sed '2c\
. "$fixture/vars"
' "$C_TAP_SOURCE/../scripts/scanlogs.in" >"$fixture/scanlogs"
run() {
    COMPRESSFORMAT="$1" sh "$fixture/scanlogs" >"$fixture/output" 2>&1
}
# Forget the ctlinnd calls and flags of the previous case.
reset_ctlinnd() {
    rm -f "$fixture/ctl.calls" "$fixture/paused" "$fixture/flushfail" \
        "$fixture/flushsig" "$fixture/pausesig" "$fixture/gofail" \
        "$fixture/modefail" "$fixture/takelock"
}
# Count the go commands sent to the server.
gocount() {
    grep -c '^-s go ' "$fixture/ctl.calls"
}
# Check that the scanlogs output follows the throttle diagnostic in the mail.
logs_after_throttle() {
    sed -n '/Cannot throttle while innwatch paused/,$p' "$fixture/mail" \
        | grep -q scanlogs-output
}
for word in first second third; do
    echo "$word" >"$fixture/$word"
done
old=$fixture/log/OLD
printf 'first\n' >"$fixture/log/news"
ok 'compress using runtime arguments' run 'gzip -9'
ok 'runtime suffix and contents' cmp "$old/news.1.gz" "$fixture/first"
printf 'second\n' >"$fixture/log/news"
ok 'switch to plain logs' run cat
ok 'old suffix advances with its generation' cmp "$old/news.2.gz" "$fixture/first"
ok 'new generation has plain suffix' cmp "$old/news.1" "$fixture/second"
printf 'third\n' >"$fixture/log/news"
run 'gzip -invalid'
ok 'compressor error is reported' test "$?" -ne 0
ok 'failed compression retains the complete plain log' cmp "$old/news.1" "$fixture/third"
ok 'oldest compressed generation expired' test ! -f "$old/news.2.gz"
ok 'preceding plain generation retained' cmp "$old/news.2" "$fixture/second"
for mode in missing unknown default; do
    echo "$mode" >"$fixture/log/news"
    echo "$mode" >"$fixture/expected"
    case $mode in
        missing) run "$fixture/missing/gzip" ;;
        unknown) run misspelled-compressor ;;
        default) (unset COMPRESSFORMAT; TEST_DEFAULT="$fixture/missing-default"
                 export TEST_DEFAULT; sh "$fixture/scanlogs") >"$fixture/output" 2>&1 ;;
    esac
    ok "$mode compressor returns failure" test "$?" -ne 0
    ok "$mode compressor keeps a plain archive" cmp "$old/news.1" "$fixture/expected"
    ok "$mode compressor still produces the report" grep -q analysis-complete "$fixture/output"
    ok "$mode compressor warns" grep -Eq 'unknown compressformat|compressor unavailable|compression failed' "$fixture/output"
done
cp "$fixture/scanlogs" "$fixture/bin/scanlogs"
chmod +x "$fixture/bin/scanlogs"
sed '2c\
. "$fixture/vars"
' "$C_TAP_SOURCE/../scripts/news.daily.in" >"$fixture/news.daily"
echo daily >"$fixture/log/news"
echo daily >"$fixture/expected"
COMPRESSFORMAT="$fixture/missing/gzip" sh "$fixture/news.daily" \
    noexpire noexpireover noexplog noprocbatch norenumber norm nostat
ok 'news.daily reports the rotation failure after mailing' test "$?" -ne 0
ok 'mail contains the analysis' grep -q analysis-complete "$fixture/mail"
ok 'mail contains the compressor warning' grep -q 'compressor unavailable' "$fixture/mail"
ok 'daily log rotated intact' cmp "$old/news.1" "$fixture/expected"

printf '#! /bin/sh\nexit 0\n' >"$fixture/bin/sleep"
printf '#! /bin/sh\necho expiry-status\n' >"$fixture/bin/innstat"
chmod +x "$fixture/bin/sleep" "$fixture/bin/innstat"
rm -f "$fixture/mail"
echo both-fail >"$fixture/log/news"
TEST_THROTTLE_FAILURE=yes COMPRESSFORMAT="$fixture/missing/gzip" \
    sh "$fixture/news.daily" noexpireover noexplog noprocbatch norenumber norm nostat
ok 'daily maintenance fails after scanlogs and throttle both fail' test "$?" -ne 0
ok 'throttle failure report still contains the analysis' grep -q analysis-complete "$fixture/mail"
ok 'throttle failure report includes the compressor warning' grep -q 'compressor unavailable' "$fixture/mail"
ok 'throttle failure report includes the throttle diagnostic' grep -q 'Cannot throttle while innwatch paused' "$fixture/mail"
ok 'throttle failure releases the daily lock' test ! -e "$fixture/LOCK.news.daily"
set -- "$fixture"/doex*
ok 'throttle failure removes the report buffer' test ! -e "$1"

echo interruption >"$fixture/log/news"
echo interruption >"$fixture/interrupted"
reset_ctlinnd
run 'gzip -interrupt'
ok 'interrupted rotation exits nonzero' test "$?" -eq 1
ok 'interrupted compression reports the interruption' \
    grep -q 'log rotation interrupted' "$fixture/output"
ok 'server not left paused by an interrupted compression' \
    test ! -f "$fixture/paused"
ok 'one go before the compression' test "$(gocount)" -eq 1
ok 'partial compressor output is hidden during the run' test -f "$fixture/archive-hidden"
ok 'interrupted rotation leaves no partial archive' test ! -e "$old/news.0.gz"
ok 'interrupted rotation preserves the uncompressed source' cmp "$old/news" "$fixture/interrupted"
ok 'interrupted rotation releases the scanlogs lock' test ! -e "$fixture/LOCK.scanlogs"
set -- "$old"/.scanlogs.*
ok 'interrupted rotation removes staged output' test ! -e "$1"

echo next-run >"$fixture/log/news"
echo next-run >"$fixture/expected"
ok 'rotation can restart after an interruption' run 'gzip -9'
ok 'next rotation archives the new log intact' cmp "$old/news.1.gz" "$fixture/expected"
set -- "$old"/recovered.*/news
ok 'next rotation saves the interrupted log intact' cmp "$1" "$fixture/interrupted"
ok 'next rotation reports the saved log path' grep -Fq "saved pending log to $1" "$fixture/output"

# Server pause and resume around the rotation.
echo resume >"$fixture/log/news"
reset_ctlinnd
run 'gzip -9'
ok 'normal rotation succeeds' test "$?" -eq 0
ok 'normal rotation pauses the server once' \
    test "$(grep -c '^-s pause ' "$fixture/ctl.calls")" -eq 1
ok 'normal rotation resumes the server once' test "$(gocount)" -eq 1
ok 'normal rotation leaves the server running' test ! -f "$fixture/paused"

echo flush-fails >"$fixture/log/news"
reset_ctlinnd
touch "$fixture/flushfail"
run 'gzip -9'
ok 'failed flushlogs exits 1' test "$?" -eq 1
ok 'failed flushlogs is reported' grep -q 'Cannot flush logs' "$fixture/output"
ok 'failed flushlogs resumes the server' test ! -f "$fixture/paused"
ok 'failed flushlogs sends one go' test "$(gocount)" -eq 1
ok 'failed flushlogs releases the lock' test ! -e "$fixture/LOCK.scanlogs"
ok 'failed flushlogs leaves the log in place' \
    grep -q flush-fails "$fixture/log/news"

echo signal-paused >"$fixture/log/news"
reset_ctlinnd
touch "$fixture/flushsig"
run 'gzip -9'
ok 'signal while paused exits 1' test "$?" -eq 1
ok 'signal while paused is reported' \
    grep -q 'log rotation interrupted' "$fixture/output"
ok 'signal while paused sends go' test "$(gocount)" -eq 1
ok 'signal while paused resumes the server' test ! -f "$fixture/paused"
ok 'signal while paused releases the lock' test ! -e "$fixture/LOCK.scanlogs"
set -- "$old"/.scanlogs.*
ok 'signal while paused leaves no staged output' test ! -e "$1"

echo signal-pausing >"$fixture/log/news"
reset_ctlinnd
touch "$fixture/pausesig"
run 'gzip -9'
ok 'signal during the pause command exits 1' test "$?" -eq 1
ok 'signal during the pause command resumes the server' \
    test ! -f "$fixture/paused"

echo go-fails >"$fixture/log/news"
echo go-fails >"$fixture/expected"
reset_ctlinnd
touch "$fixture/gofail"
run 'gzip -9'
ok 'failed go exits 1' test "$?" -eq 1
ok 'failed go prints the command to resume the server' \
    grep -q 'cannot resume innd, run: ctlinnd go "Flushing log and syslog files' \
    "$fixture/output"
ok 'failed go is not retried at exit' test "$(gocount)" -eq 1
ok 'failed go still rotates the log' cmp "$old/news.1.gz" "$fixture/expected"
ok 'failed go releases the lock' test ! -e "$fixture/LOCK.scanlogs"

# news.daily: report and exit status of an aborted run, and its lock.
printf '#! /bin/sh\necho scanlogs-output\nexit 3\n' >"$fixture/bin/scanlogs"
rm -f "$fixture/mail"
reset_ctlinnd
sh "$fixture/news.daily" noexpire noexpireover noexplog noprocbatch \
    norenumber norm nostat
ok 'news.daily exits with the status of scanlogs' test "$?" -eq 3
ok 'news.daily mails the scanlogs output' grep -q scanlogs-output "$fixture/mail"
sh "$fixture/news.daily" noexpire noexpireover noexplog noprocbatch \
    norenumber norm nostat nomail >"$fixture/output" 2>&1
ok 'news.daily without mail exits with the status of scanlogs' test "$?" -eq 3
ok 'news.daily without mail shows the scanlogs output' \
    grep -q scanlogs-output "$fixture/output"

rm -f "$fixture/mail"
reset_ctlinnd
TEST_THROTTLE_FAILURE=yes sh "$fixture/news.daily" noexpireover noexplog \
    noprocbatch norenumber norm nostat
ok 'throttle failure exits 1' test "$?" -eq 1
ok 'throttle failure mails the scanlogs output' \
    grep -q scanlogs-output "$fixture/mail"
ok 'scanlogs output comes after the throttle diagnostic' logs_after_throttle
set -- "$fixture"/doex*
ok 'throttle failure leaves no report buffer' test ! -e "$1"
ok 'throttle failure releases the daily lock of this run' \
    test ! -e "$fixture/LOCK.news.daily"

rm -f "$fixture/mail"
reset_ctlinnd
touch "$fixture/takelock"
TEST_THROTTLE_FAILURE=yes sh "$fixture/news.daily" noexpireover noexplog \
    noprocbatch norenumber norm nostat
ok 'throttle failure with a foreign lock exits 1' test "$?" -eq 1
ok 'throttle failure keeps a lock of another process' \
    grep -qx 99999 "$fixture/LOCK.news.daily"
rm -f "$fixture/LOCK.news.daily"

rm -rf "$fixture"
rmdir "$tmpdir" 2>/dev/null || :

#! /bin/sh
# Exercise scanlogs in a private spool with a stand-in for ctlinnd.
. "$C_TAP_SOURCE/tap/libtap.sh"
plan 41
fixture=$(test_tmpdir)/scanlogs-$$
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
cat >"$fixture/bin/ctlinnd" <<'EOF'
#! /bin/sh
if [ "${TEST_THROTTLE_FAILURE:-}" = yes ]; then
    case " $* " in
        *' mode '*) echo 'Server paused by [innwatch: fixture]'; exit 0 ;;
        *' throttle '*) exit 1 ;;
    esac
fi
echo Ok
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
run 'gzip -interrupt'
ok 'interrupted rotation exits nonzero' test "$?" -ne 0
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

rm -rf "$fixture"

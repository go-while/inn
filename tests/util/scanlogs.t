#! /bin/sh
# Exercise scanlogs in a private spool with a stand-in for ctlinnd.
. "$C_TAP_SOURCE/tap/libtap.sh"
plan 25
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
printf '#! /bin/sh\necho Ok\n' >"$fixture/bin/ctlinnd"
printf '#! /bin/sh\nfor lock do :; done\necho "$$" >"$lock"\n' >"$fixture/bin/shlock"
printf '#! /bin/sh\ncat >"$fixture/mail"\n' >"$fixture/bin/mail"
printf '#! /bin/sh\n[ "$1" = -9 ] || exit 1\ncat\n' >"$fixture/bin/gzip"
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
rm -rf "$fixture"

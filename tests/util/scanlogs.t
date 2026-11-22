#! /bin/sh
# Exercise scanlogs in a private spool with a stand-in for ctlinnd.
. "$C_TAP_SOURCE/tap/libtap.sh"
plan 9
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
LOG_COMPRESS=cat
Z=''
AWK=awk
PATH=$fixture/bin:$PATH
export PATH
EOF
printf '#! /bin/sh\necho Ok\n' >"$fixture/bin/ctlinnd"
printf '#! /bin/sh\nexit 0\n' >"$fixture/bin/shlock"
printf '#! /bin/sh\n[ "$1" = -9 ] || exit 1\ncat\n' >"$fixture/bin/gzip"
printf '#! /bin/sh\nexit 0\n' >"$fixture/bin/innreport"
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
rm -rf "$fixture"

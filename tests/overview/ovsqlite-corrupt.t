#! /bin/sh
#
# ovsqlite readers must skip a damaged compressed overview record and go on
# with the next ones.
#
# Fills a compressed WAL database through ovsqlite-server with the writer
# helper of ovsqlite-integ.t, stops the server, damages two records of one
# group with overview/ovsqlite-damage.t (a length prefix starting with
# 0xff, and a valid length prefix followed by data that is not a zlib
# stream), then reads the group with the direct reader and through a
# restarted server.
#
# The direct reader must return every other article of the group: a failed
# inflate must not leave the zlib stream in its error state.  The length
# prefix checks are also run for undefined shifts: in a build with
# -fsanitize=undefined, a runtime error reported in ovsqlite.c or
# ovsqlite-server.c fails the test (other builds always pass these checks).

count=1
printcount() {
    echo "$1 $count $2"
    count=$(expr $count + 1)
}

# runtests preserves its caller's working directory.  Normalize to the test
# build directory.
test_root=${C_TAP_BUILD:-}
if [ -z "$test_root" ]; then
    test_root=$(dirname "$0")
    test_root=$(cd "$test_root/.." && pwd)
fi
if ! cd "$test_root"; then
    echo "Bail out! cannot change to test directory $test_root"
    exit 1
fi

server="../storage/ovsqlite/ovsqlite-server"
writer="./overview/ovsqlite-write.t"
helper="./overview/ovsqlite-damage.t"

if ! grep -q '^#define HAVE_SQLITE3' ../include/config.h 2>/dev/null; then
    echo "1..0 # skip SQLite support not compiled"
    exit 0
fi
if ! grep -q '^#define HAVE_ZLIB' ../include/config.h 2>/dev/null; then
    echo "1..0 # skip zlib support not compiled"
    exit 0
fi
for prog in "$server" "$writer" "$helper"; do
    if [ ! -x "$prog" ]; then
        echo "1..0 # skip ovsqlite binaries not built"
        exit 0
    fi
done

tmpdir="$(pwd)/ov-tmp-corrupt"
rm -rf "$tmpdir"
mkdir -p "$tmpdir"

cat >"$tmpdir/inn.conf" <<EOF
domain:          news.example.com
pathhost:        inn.example.com
mta:             "/usr/sbin/sendmail -oi -oem %s"
hismethod:       hisv6
enableoverview:  true
ovmethod:        ovsqlite
wireformat:      true

pathnews:        .
patharchive:     archive
patharticles:    spool
pathoverview:    $tmpdir
pathrun:         $tmpdir
pathetc:         $tmpdir
pathdb:          $tmpdir
pathlog:         $tmpdir
pathtmp:         $tmpdir
EOF

cat >"$tmpdir/ovsqlite.conf" <<EOF
walmode: true
compress: true
cachesize: 1000
readercachesize: 1000
transrowlimit: 10000
transtimelimit: 30.0
EOF

INNCONF="$tmpdir/inn.conf"
export INNCONF

# Start ovsqlite-server in the foreground with its log in $1 and wait up to
# five seconds for its socket.  A socket left by an earlier server is removed
# first, so that only the new server's socket counts.  Sets server_pid.
start_server() {
    rm -f "$tmpdir/ovsqlite.sock"
    $server -d >"$1" 2>&1 &
    server_pid=$!
    waited=0
    while [ ! -S "$tmpdir/ovsqlite.sock" ] && [ $waited -lt 50 ]; do
        if ! kill -0 $server_pid 2>/dev/null; then
            break
        fi
        sleep 0.1
        waited=$(expr $waited + 1)
    done
    [ -S "$tmpdir/ovsqlite.sock" ]
}

stop_server() {
    kill $server_pid 2>/dev/null
    wait $server_pid 2>/dev/null
}

# Undefined shifts reported by -fsanitize=undefined in the ovsqlite code.
ubsan() {
    grep -E 'ovsqlite(-server)?\.c:[0-9]+:[0-9]+: runtime error' "$1"
}

echo 1..7

# Test 1: fill the database through the server.
if start_server "$tmpdir/server.log" \
    && $writer >"$tmpdir/writer.out" 2>&1; then
    printcount "ok" "# database filled through ovsqlite-server"
else
    printcount "not ok" "# database filled through ovsqlite-server"
    sed 's/^/# /' "$tmpdir/server.log" "$tmpdir/writer.out"
fi
stop_server

# Test 2: damage two records of the largest group.
info=$($helper damage 2>"$tmpdir/damage.err")
rc=$?
if [ $rc = 2 ]; then
    printcount "ok" "# skip database not compressed"
    printcount "ok" "# skip database not compressed"
    printcount "ok" "# skip database not compressed"
    printcount "ok" "# skip database not compressed"
    printcount "ok" "# skip database not compressed"
    printcount "ok" "# skip database not compressed"
    rm -rf "$tmpdir"
    exit 0
fi
set -- $info
if [ $rc = 0 ] && [ $# = 6 ]; then
    printcount "ok" "# damaged $1:$2 (length prefix) and $1:$3 (zlib data)"
    echo "# group $1 has $5 articles, $6 compressed ones after $1:$3"
else
    printcount "not ok" "# damaging records failed: $info"
    sed 's/^/# /' "$tmpdir/damage.err"
    rm -rf "$tmpdir"
    exit 1
fi
group=$1
bad1=$2
bad2=$3
high=$4
total=$5

# Tests 3-4: the direct reader skips both damaged records and returns all
# other articles of the group.
out=$($helper direct $group $bad1 $bad2 $high $total 2>"$tmpdir/direct.err")
rc=$?
echo "# $out"
if [ $rc = 0 ]; then
    printcount "ok" "# direct reader returns the articles after the damaged ones"
else
    printcount "not ok" "# direct reader lost articles after the damaged ones"
    sed 's/^/# /' "$tmpdir/direct.err" | head -20
fi
if ubsan "$tmpdir/direct.err" >/dev/null; then
    printcount "not ok" "# undefined behaviour in the direct reader"
    ubsan "$tmpdir/direct.err" | head -5 | sed 's/^/# /'
else
    printcount "ok" "# no undefined behaviour reported in the direct reader"
fi

# Tests 5-7: the same through a restarted server.
if start_server "$tmpdir/server2.log"; then
    printcount "ok" "# server restarted"
else
    printcount "not ok" "# server restarted"
    sed 's/^/# /' "$tmpdir/server2.log"
fi
out=$($helper server $group $bad1 $bad2 $high $total 2>"$tmpdir/server.err")
rc=$?
stop_server
echo "$out" | sed 's/^/# /'
if [ $rc = 0 ]; then
    printcount "ok" "# server returns the articles after the damaged ones"
else
    printcount "not ok" "# server lost articles after the damaged ones"
    sed 's/^/# /' "$tmpdir/server.err" | head -20
fi
if ubsan "$tmpdir/server2.log" >/dev/null; then
    printcount "not ok" "# undefined behaviour in ovsqlite-server"
    ubsan "$tmpdir/server2.log" | head -5 | sed 's/^/# /'
else
    printcount "ok" "# no undefined behaviour reported in ovsqlite-server"
fi

rm -rf "$tmpdir"

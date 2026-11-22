#! /bin/sh
# Exercise expire.ctl through expire and query the resulting history.

set -eu
cd "${C_TAP_BUILD:-.}"
root=$(cd .. && pwd)
data=$root/tests/data
tmp=$(mktemp -d "$(pwd)/remember-zero.XXXXXX")
trap 'rm -rf "$tmp"' 0
export LC_ALL=C
methods=hisv6
if grep -q '^#define HAVE_SQLITE3' "$root/include/config.h"; then
    methods="hisv6 hissqlite"
    echo 10
else
    echo 5
fi

n=0
check() {
    n=$((n + 1))
    if [ "$1" = "$2" ]; then
        echo "ok $n - $method $3"
    else
        echo "not ok $n - $method $3"
        echo "# expected '$2', got '$1'"
    fi
}

expire_with() {
    printf '/remember/:%s\n' "$1" >"$db/expire.ctl"
    shift
    "$root/expire/expire" -n "$@" "$db/expire.ctl"
}

missing() {
    "$root/expire/grephistory" -i <"$tmp/ids"
}

cat >"$tmp/ids" <<'EOF'
<old@example.test>
<future@example.test>
<arrival@example.test>
EOF
for method in $methods; do
    db=$tmp/$method
    mkdir -p "$db/spool"
    cp "$data/db/active" "$db/active"
    INNCONF=$db/inn.conf
    export INNCONF
    cat >"$INNCONF" <<EOF
domain: example.test
mta: "/bin/true %s"
hismethod: hisv6
enableoverview: false
expiretombstone: false
pathnews: "$db"
pathdb: "$db"
pathetc: "$data/etc"
patharticles: "$db/spool"
runasuser: "$(id -un)"
runasgroup: "$(id -gn)"
EOF
    token=$("$root/frontends/sm" -s <"$data/articles/1")
    perl -MDigest::MD5=md5_hex -e '
        ($token) = @ARGV;
        $past = time - 86400;
        $future = time + 86400;
        for (["old", "$past~-~$past"], ["future", "$past~-~$future"],
             ["arrival", "$future~-"], ["live", "$past~-~$past\t$token"]) {
            ($name, $times) = @$_;
            printf "[%s]\t%s\n", uc(md5_hex("<$name\@example.test>")), $times;
        }
    ' "$token" >"$db/history"
    "$root/expire/makedbz" -o -s 100
    if [ "$method" = hissqlite ]; then
        "$root/history/hissqlite/hissqlite-convert" "$db/history" "$db/history"
        sed 's/^hismethod: hisv6$/hismethod: hissqlite/' "$INNCONF" >"$db/new"
        mv "$db/new" "$INNCONF"
        # The first writer normally enables WAL after conversion.  Open the
        # fixture for writing before expire starts its read-only probe.
        "$root/expire/prunehistory" </dev/null
    fi

    expire_with never
    check "$(missing)" "" "never keeps all remembered entries"
    expire_with 0.000001
    check "$(missing)" "<old@example.test>" "a nonzero fraction uses a cutoff"
    expire_with 0 -t
    check "$(missing)" "<old@example.test>" "dry run preserves history"
    expire_with 0.0
    check "$(missing)" "$(cat "$tmp/ids")" "zero clears both future timestamps"
    check "$("$root/expire/grephistory" '<live@example.test>')" "$token" \
        "the stored article retains its token"
done

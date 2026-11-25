#! /bin/sh
#
# tradspool keeps the article number in a 32-bit token field.  Storing an
# article whose Xref number does not fit must fail instead of returning the
# token of another article.

count=1
printcount() {
    echo "$1 $count $2"
    count=$(expr $count + 1)
}

# Find the right directory.
dirs='../data data tests/data'
for dir in $dirs; do
    if [ -r "$dir/articles/1" ]; then
        cd $dir
        break
    fi
done
data=$(pwd)
sm="$data/../../frontends/sm"
if [ ! -x "$sm" ]; then
    echo "Could not find sm" >&2
    exit 1
fi

# 64-bit article numbers only exist where unsigned long is wider than 32 bits.
if [ "$(getconf LONG_BIT 2>/dev/null)" != 64 ]; then
    echo "1..0 # skip unsigned long is not 64 bits"
    exit 0
fi

echo 3

# A private spool, so that sm.t and repeated runs do not interfere.
tmp="$data/tradspool-artnum.tmp"
rm -rf "$tmp"
mkdir -p "$tmp/etc" "$tmp/spool" "$tmp/db"
cp etc/inn.conf etc/storage.conf "$tmp/etc/"
cp db/active "$tmp/db/"
cd "$tmp"
INNCONF=etc/inn.conf
export INNCONF

# Article 1 in example.test.
token1=$("$sm" -s <"$data/articles/1")
if [ $? = 0 ] && [ -r spool/example/test/1 ]; then
    printcount "ok" "# store example.test:1"
else
    printcount "not ok" "# store example.test:1"
fi

# The same group, article number 2^32 + 1.
sed -e 's/example\.test:1/example.test:4294967297/' \
    -e 's/<example-1@/<example-big@/' "$data/articles/1" >big
token2=$("$sm" -s <big 2>/dev/null)
if [ $? != 0 ]; then
    printcount "ok" "# store example.test:4294967297 refused"
else
    printcount "not ok" "# store example.test:4294967297 accepted: $token2 (example.test:1 is $token1)"
fi
if [ ! -e spool/example/test/4294967297 ]; then
    printcount "ok" "# no spool file for 4294967297"
else
    printcount "not ok" "# spool file for 4294967297 written"
fi

cd "$data"
rm -rf "$tmp"

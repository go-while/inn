#! /bin/sh
#
# tradspool stores a crossposted article once and links it under the other
# groups of its Xref header field.  When one of these links cannot be made,
# the store fails; the article file and the links already made must then be
# removed, so that the spool keeps no copy of an article that has no token
# and the same article can be stored again later.

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

echo 1..6

# A private spool, so that sm.t and repeated runs do not interfere.
tmp="$data/tradspool-xpost.tmp"
rm -rf "$tmp"
mkdir -p "$tmp/etc" "$tmp/spool" "$tmp/db"
cp etc/inn.conf etc/storage.conf "$tmp/etc/"
cp db/active "$tmp/db/"
cd "$tmp"
INNCONF=etc/inn.conf
export INNCONF

# Article 1, crossposted to three groups.
sed -e 's/^Xref: news\.example\.com example\.test:1$/Xref: news.example.com example.test:1 example.config:1 example.weird:1/' \
    -e 's/<example-1@/<example-xpost@/' "$data/articles/1" >xpost
if ! grep -q '^Xref: .* example.weird:1$' xpost; then
    echo "Bail out! cannot build the crossposted article"
    exit 1
fi

# A file already at the third group's path: link() and symlink() both fail
# there, so the store fails after the article file and the link in
# example.config were made.
mkdir -p spool/example/weird
echo blocker >spool/example/weird/1

if "$sm" -s <xpost >out 2>err; then
    printcount "not ok" "# store with a blocked link succeeded: $(cat out)"
else
    printcount "ok" "# store with a blocked link fails"
fi
sed 's/^/# sm: /' err
if [ ! -e spool/example/test/1 ]; then
    printcount "ok" "# article file removed after the failed store"
else
    printcount "not ok" "# article file spool/example/test/1 left behind"
fi
if [ ! -e spool/example/config/1 ]; then
    printcount "ok" "# link in example.config removed after the failed store"
else
    printcount "not ok" "# link spool/example/config/1 left behind"
fi
if [ "$(cat spool/example/weird/1 2>/dev/null)" = blocker ]; then
    printcount "ok" "# the file in the way is left alone"
else
    printcount "not ok" "# the file in the way was changed or removed"
fi

# Once the path is free, the same article can be stored.
rm -f spool/example/weird/1
if "$sm" -s <xpost >out 2>err; then
    printcount "ok" "# store again after the blocker is gone"
else
    printcount "not ok" "# store again after the blocker is gone fails"
    sed 's/^/# sm: /' err
fi
if [ -r spool/example/test/1 ] && [ -r spool/example/config/1 ] \
    && [ -r spool/example/weird/1 ]; then
    printcount "ok" "# article stored under all three groups"
else
    printcount "not ok" "# article not stored under all three groups"
fi

cd "$data"
rm -rf "$tmp"

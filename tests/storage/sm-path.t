#! /bin/sh
#
# Test suite for sm -s with the path key of storage.conf.

# The count starts at 1 and is updated each time ok is printed.  printcount
# takes "ok" or "not ok".
count=1
printcount() {
    echo "$1 $count $2"
    count=$(expr $count + 1)
}

# Store an article with a given Path header field body and article number (the
# spool file name), and check the storage class of its token (the second octet
# of the token, in hexadecimal).
store() {
    sed -e "s/^Path: .*/Path: $1/" \
        -e "s/^Xref: .*/Xref: news.example.com example.test:$3/" \
        ../articles/1 >article
    token=$($sm -s <article)
    if [ $? = 0 ]; then
        printcount "ok"
    else
        printcount "not ok"
    fi
    class=$(echo "$token" | cut -c4-5)
    if [ "$class" = "$2" ]; then
        printcount "ok"
    else
        echo "# Path $1: token $token, class $class, want $2"
        printcount "not ok"
    fi
}

# Find the right directory.
dirs='../data data tests/data'
for dir in $dirs; do
    if [ -r "$dir/articles/1" ]; then
        cd $dir
        break
    fi
done
sm="../../../frontends/sm"
tmp="sm-path.$$"
rm -rf "$tmp"
mkdir -p "$tmp/etc" "$tmp/db" "$tmp/spool"
cd "$tmp" || exit 1
if [ ! -x "$sm" ]; then
    echo "Could not find sm" >&2
    exit 1
fi
cp ../etc/inn.conf etc/inn.conf
cp ../db/active db/active
cat >etc/storage.conf <<'CONF'
method tradspool {
    newsgroups: *
    class: 1
    path: "*!spam.example!*"
}
method tradspool {
    newsgroups: *
    class: 3
    path: "*!origin.example!not-for-mail"
}
method tradspool {
    newsgroups: *
    class: 2
}
CONF

# Print out the count of tests.
echo 6

INNCONF=etc/inn.conf
export INNCONF

store 'news.example.com!spam.example!not-for-mail' 01 1
store 'news.example.com!origin.example!not-for-mail' 03 2
store 'news.example.com!not-for-mail' 02 3

# All done.  Clean up.
cd ..
rm -rf "$tmp"

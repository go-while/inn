#! /bin/sh
#
# Test suite for sm.

# The count starts at 1 and is updated each time ok is printed.  printcount
# takes "ok" or "not ok".
count=1
printcount() {
    echo "$1 $count $2"
    count=$(expr $count + 1)
}

# Store an article and make sure that sm succeeds.
store() {
    token=$($sm -s <$1)
    if [ $? = 0 ]; then
        printcount "ok"
    else
        printcount "not ok"
    fi
}

# Make sure that a file exists.
exists() {
    if [ -r "$1" ]; then
        printcount "ok"
    else
        printcount "not ok"
    fi
}

# Check that an article retrieved via sm is the same as the article stored in
# the article spool.  Takes the token and the path to the actual article.
retrieve() {
    "$sm" "$1" >spool/test
    if [ $? = 0 ] && diff "$2" spool/test; then
        printcount "ok"
    else
        printcount "not ok"
    fi
}

# Check the article information returned against the information in the actual
# article.
info() {
    real=$(grep ^Xref $2 | sed -e 's/.*\.example\.com //' -e 's/ .*//')
    real=$(echo "$real" | sed -e 's/:/: /')
    info=$($sm -i $1)
    if [ $? = 0 ] && [ "$info" = "$real" ]; then
        printcount "ok"
    else
        echo "Want: $real"
        echo "Have: $info"
        printcount "not ok"
    fi
}

# Given a token and a path in the spool, make sure that article removal works.
remove() {
    "$sm" -r "$1"
    if [ $? = 0 ] && [ ! -r "$2" ]; then
        printcount "ok"
    else
        printcount "not ok"
    fi
}

# Check retrieval of a raw article, given a token and a path in the spool.
raw() {
    "$sm" -R "$1" >spool/test
    if [ $? = 0 ] && diff "$2" spool/test; then
        printcount "ok"
    else
        printcount "not ok"
    fi
}

# Check retrieval of only the headers, given a token and a path to the real
# article.  (sed '/^$/Q' is unrecognized on some implementations.)
headers() {
    sed '/^$/q' "$2" | sed '/^$/d' >spool/real
    "$sm" -H "$1" >spool/test
    if [ $? = 0 ] && diff spool/real spool/test; then
        printcount "ok"
    else
        printcount "not ok"
    fi
}

# Check the rnews batch format.  Takes the token and the original article
# path.  Use Perl to get the size of the article, since I can't find a really
# good way to do this in shell.
rnews() {
    size=$(perl -e "print -s \"$2\"")
    (
        echo "#! rnews $size"
        cat "$2"
    ) >spool/real
    "$sm" -S "$1" >spool/test
    if [ $? = 0 ] && diff spool/real spool/test; then
        printcount "ok"
    else
        printcount "not ok"
    fi
}

# Check retrieval of multiple articles at once.  We do this both via passing
# multiple arguments to sm and passing multiple arguments on stdin.  Takes two
# tokens and two article paths.
multiple() {
    cat "$3" "$4" >spool/real
    "$sm" "$1" "$2" >spool/test
    if [ $? = 0 ] && diff spool/real spool/test; then
        printcount "ok"
    else
        printcount "not ok"
    fi
    (
        echo "$1"
        echo "$2"
    ) | "$sm" >spool/test
    if [ $? = 0 ] && diff spool/real spool/test; then
        printcount "ok"
    else
        printcount "not ok"
    fi
}

# Check the quieting of error messages.
quiet() {
    output=$($sm -q @BADTOKEN@ 2>&1)
    if [ $? = 1 ] && [ -z "$output" ]; then
        printcount "ok"
    else
        printcount "not ok"
    fi
}

# Check the messages of a retrieval that fails.  Takes the token and the
# message expected without -e, which is the historical one, and with -e.
failure() {
    output=$($sm "$1" 2>&1 >/dev/null)
    status=$?
    if [ $status = 1 ] && [ "$output" = "sm: could not retrieve $1" ]; then
        printcount "ok"
    else
        echo "Have: $status $output"
        printcount "not ok"
    fi
    output=$($sm -e "$1" 2>&1 >/dev/null)
    status=$?
    case "$output" in
    $2)
        if [ $status = 1 ]; then
            printcount "ok"
        else
            echo "Have: $status $output"
            printcount "not ok"
        fi
        ;;
    *)
        echo "Have: $status $output"
        printcount "not ok"
        ;;
    esac
}

# Find the right directory.
sm="../../frontends/sm"
dirs='../data data tests/data'
for dir in $dirs; do
    if [ -r "$dir/articles/1" ]; then
        cd $dir
        break
    fi
done
if [ ! -x "$sm" ]; then
    echo "Could not find sm" >&2
    exit 1
fi

# Print out the count of tests.
echo 29

# Point sm at the appropriate inn.conf file and create our required directory
# structure.
INNCONF=etc/inn.conf
export INNCONF
mkdir -p spool

# First, store the articles.
store articles/1
token1="$token"
store articles/2
token2="$token"
store articles/3
token3="$token"

# Make sure they were written properly.
exists spool/example/test/1
exists spool/example/test/2
exists spool/example/config/1
exists spool/example/config/2

# Make sure that the articles are correct.
retrieve "$token1" articles/1
retrieve "$token2" articles/2
retrieve "$token3" articles/3

# Check the article information.
info "$token1" articles/1
info "$token2" articles/2
info "$token3" articles/3

# Delete an article and make sure that this works.
remove "$token1" spool/example/test/1

# Check raw article retrieval.
raw "$token2" spool/example/config/1
raw "$token3" spool/example/config/2

# Check retrieval of only the headers.
headers "$token2" articles/2
headers "$token3" articles/3

# Check rnews format messages.
rnews "$token2" articles/2
rnews "$token3" articles/3

# Check retrieval of multiple articles.
multiple "$token2" "$token3" articles/2 articles/3

# Check silencing of error messages.
quiet

# Check that -e tells a missing article (token1 was removed above) from an
# article that cannot be read: make the directory of token2 a file, so that
# opening the article fails with ENOTDIR.
failure "$token1" "sm: article $token1 does not exist"
mv spool/example/config spool/example/config.saved
touch spool/example/config
failure "$token2" "sm: could not retrieve $token2: *"
rm -f spool/example/config
mv spool/example/config.saved spool/example/config

# The options that -e cannot be combined with.
output=$($sm -e -r "$token2" 2>&1)
if [ $? = 1 ] && [ "$output" = "sm: -e cannot be used with -c, -i, -r, -d, or -s" ]; then
    printcount "ok"
else
    echo "Have: $output"
    printcount "not ok"
fi
output=$($sm -e -s </dev/null 2>&1)
if [ $? = 1 ] && [ "$output" = "sm: -e cannot be used with -c, -i, -r, -d, or -s" ]; then
    printcount "ok"
else
    echo "Have: $output"
    printcount "not ok"
fi

# All done.  Clean up.
rm -rf spool

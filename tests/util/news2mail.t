#! /bin/sh
#
# Test suite for news2mail.
#
# news2mail reads "token lists" lines from innd and runs sm for each token to
# get the article, which it mails with the mta.  sm must be run as a program
# with the token as its own argument, not through a shell: the token is
# passed on unchanged, and the path to sm may contain a space.  The test runs
# backends/news2mail.in with a test INN::Config and with an sm and an mta
# that only record what they get.

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
news2mail="$data/../../backends/news2mail.in"
if [ ! -r "$news2mail" ]; then
    echo "Could not find news2mail.in" >&2
    exit 1
fi
perl=$(command -v perl)
if [ -z "$perl" ]; then
    echo "1..0 # skip perl not found"
    exit 0
fi

echo 1..7

tmp="$data/news2mail.tmp"
rm -rf "$tmp"
mkdir -p "$tmp/etc" "$tmp/bin" "$tmp/sm dir" "$tmp/lib1/INN" "$tmp/lib2/INN"

# A test INN::Config: pathbin is the second argument (where sm is).
writeconfig() {
    cat >"$1/INN/Config.pm" <<EOF
package INN::Config;
our \$pathetc = '$tmp/etc';
our \$pathbin = '$2';
our \$mta = '$tmp/bin/mta -oi -oem %s';
our \$newsuser = 'news';
our \$pathhost = 'news.example.com';
our \$syslog_facility = 'news';
1;
EOF
}
writeconfig "$tmp/lib1" "$tmp/bin"
writeconfig "$tmp/lib2" "$tmp/sm dir"

echo 'list-a a@example.test news@example.test' >"$tmp/etc/news2mail.cf"

# sm records its arguments, one per line, and prints an article.
cat >"$tmp/bin/sm" <<EOF
#! /bin/sh
for arg in "\$@"; do
    printf '%s\n' "\$arg"
done >"$tmp/sm.args"
cat <<'ART'
Path: news.example.com!not-for-mail
From: Test <test@example.com>
Newsgroups: example.test
Subject: news2mail test
Message-ID: <news2mail-test@example.com>
X-Test: dropped

Body line.
ART
EOF
chmod 755 "$tmp/bin/sm"
cp "$tmp/bin/sm" "$tmp/sm dir/sm"

# The mta records its arguments and the mail; nothing is sent.
cat >"$tmp/bin/mta" <<EOF
#! /bin/sh
printf '%s\n' "\$@" >"$tmp/mta.args"
cat >"$tmp/mail"
EOF
chmod 755 "$tmp/bin/mta"

# Run news2mail with the given INN::Config directory and input line.
run() {
    rm -f "$tmp/sm.args" "$tmp/mta.args" "$tmp/mail"
    echo "$2" | (cd "$tmp" && "$perl" -I"$1" -MINN::Config "$news2mail") \
        >"$tmp/out" 2>&1
}

# What sm got, on one line.
smargs() {
    tr '\n' ' ' 2>/dev/null <"$tmp/sm.args"
}

# An ordinary token.
if run "$tmp/lib1" '@0101@ list-a'; then
    printcount "ok" "# news2mail exits 0"
else
    printcount "not ok" "# news2mail fails: $(head -3 "$tmp/out" | tr '\n' ' ')"
fi
if [ "$(smargs)" = "-q @0101@ " ]; then
    printcount "ok" "# sm gets -q and the token"
else
    printcount "not ok" "# sm got: $(smargs)"
fi
if grep -q '^To: a@example.test$' "$tmp/mail" 2>/dev/null \
    && grep -q '^X-Newsgroups: example.test$' "$tmp/mail" \
    && grep -q '^Body line.$' "$tmp/mail" \
    && ! grep -q '^X-Test:' "$tmp/mail"; then
    printcount "ok" "# the article is mailed"
else
    printcount "not ok" "# mail: $(tr '\n' ' ' 2>/dev/null <"$tmp/mail")"
fi

# A token with shell syntax is one argument for sm, not a shell command.
token="@0102@;:>$tmp/shell-ran"
run "$tmp/lib1" "$token list-a"
if [ ! -e "$tmp/shell-ran" ]; then
    printcount "ok" "# no shell runs the token"
else
    printcount "not ok" "# a shell ran the token"
fi
if [ "$(smargs)" = "-q $token " ]; then
    printcount "ok" "# sm gets the token unchanged"
else
    printcount "not ok" "# sm got: $(smargs)"
fi

# The path to sm contains a space.
if run "$tmp/lib2" '@0103@ list-a'; then
    printcount "ok" "# news2mail exits 0 with a space in pathbin"
else
    printcount "not ok" "# news2mail fails with a space in pathbin: $(head -3 "$tmp/out" | tr '\n' ' ')"
fi
if grep -q '^Body line.$' "$tmp/mail" 2>/dev/null; then
    printcount "ok" "# the article is mailed with a space in pathbin"
else
    printcount "not ok" "# no article mailed with a space in pathbin"
fi

cd "$data" || exit 1
rm -rf "$tmp"

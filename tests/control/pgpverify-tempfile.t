#! /bin/sh
# Check the temporary files pgpverify writes for the signature verifier.

for dir in ../control ../../control control; do
    if test -r "$dir/pgpverify.in"; then
        pgpverify="$dir/pgpverify.in"
        break
    fi
done
if test -z "$pgpverify" || ! perl -e 1 2>/dev/null; then
    echo "1..0 # skip pgpverify or Perl not found"
    exit 0
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/pgpverify-tempfile.XXXXXX") || exit 1
trap 'rm -rf "$tmp"' 0 1 2 3 15
mkdir "$tmp/tmpdir" "$tmp/pathtmp"

# The fake verifier records its last two arguments (the signature file and
# the data file) and their modes, then reports a good signature.
cat >"$tmp/gpgv" <<EOF
#! /bin/sh
for f in "\$@"; do last2="\$last1"; last1="\$f"; done
printf '%s\n%s\n' "\$last2" "\$last1" >"$tmp/args"
ls -l "\$last2" "\$last1" >"$tmp/modes" 2>&1
echo '[GNUPG:] GOODSIG 0123456789ABCDEF signer@example.test'
exit 0
EOF
chmod +x "$tmp/gpgv"

# Write a copy of pgpverify with the fake verifier and $tmpdir set, plus
# the extra Perl given as $2.
mkscript() {
    config="\$gpgv = '$tmp/gpgv'; \$tmpdir = '$tmp/tmpdir';"
    config="$config \$syslog_method = ''; $2"
    sed "s|^# End of configuration section\.\$|$config|" "$pgpverify" >"$1"
}
mkscript "$tmp/pgpverify-pathtmp" "\$INN::Config::pathtmp = '$tmp/pathtmp';"
mkscript "$tmp/pgpverify" ""

sig=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
cat >"$tmp/article" <<EOF
Control: newgroup example.test
Subject: cmsg newgroup example.test
Message-ID: <tempfile@example.test>
Date: Mon, 23 Nov 2026 00:00:00 +0000
From: test@example.test
Sender: test@example.test
X-PGP-Sig: 2.6.2 Subject,Control,Message-ID,Date,From,Sender
	$sig
	AAAAAAAA==
	=AAAA

example.test	A test newsgroup.
EOF

echo 1..5

# 1: with INN::Config, both files are created in pathtmp.
output=$(perl "$tmp/pgpverify-pathtmp" <"$tmp/article" 2>&1)
status=$?
dirs=$(sed 's|/[^/]*$||' "$tmp/args" 2>/dev/null | sort -u)
if test "$status" = 0 && test "$dirs" = "$tmp/pathtmp"; then
    echo "ok 1 - verification files are created in pathtmp"
else
    echo "not ok 1 - verification files are created in pathtmp"
    echo "# status $status, files: $(cat "$tmp/args" 2>/dev/null)"
    echo "# output: $output"
fi

# 2: the signature file keeps the .asc suffix and both files are private.
sigfile=$(sed -n 1p "$tmp/args" 2>/dev/null)
modes=$(cut -c1-10 "$tmp/modes" 2>/dev/null | sort -u)
case "$sigfile" in
*.asc)
    if test "$modes" = "-rw-------"; then
        echo "ok 2 - signature file ends in .asc and both files are mode 0600"
    else
        echo "not ok 2 - signature file ends in .asc and both files are mode 0600"
        echo "# modes: $(cat "$tmp/modes" 2>/dev/null)"
    fi
    ;;
*)
    echo "not ok 2 - signature file ends in .asc and both files are mode 0600"
    echo "# signature file: $sigfile"
    ;;
esac

# 3: both files are removed after verification.
left=$(ls -A "$tmp/pathtmp" "$tmp/tmpdir")
if test -z "$(ls -A "$tmp/pathtmp")" && test -z "$(ls -A "$tmp/tmpdir")"; then
    echo "ok 3 - verification files are removed afterwards"
else
    echo "not ok 3 - verification files are removed afterwards"
    echo "# left: $left"
fi

# 4: a file already present at the time-and-PID name does not stop the
# verification.  The shell pre-creates such files for the next seconds,
# then execs pgpverify, which keeps the shell's PID.
rm -f "$tmp/args" "$tmp/modes"
output=$(sh -c '
    now=$(date +%s)
    t=$((now - 1))
    while test "$t" -le $((now + 30)); do
        : >"$1/pgp$t.$$.asc"
        t=$((t + 1))
    done
    exec perl "$2" <"$3"' sh "$tmp/tmpdir" "$tmp/pgpverify" "$tmp/article" 2>&1)
status=$?
if test "$status" = 0 && test -f "$tmp/args" \
    && printf '%s\n' "$output" | grep -F 'signer@example.test' >/dev/null; then
    echo "ok 4 - pre-created file at the time-and-PID name is not used"
else
    echo "not ok 4 - pre-created file at the time-and-PID name is not used"
    echo "# status $status, output: $output"
fi
rm -f "$tmp"/tmpdir/*

# 5: a missing temporary directory fails before the verifier runs.
rm -f "$tmp/args"
mkscript "$tmp/pgpverify-missing" "\$tmpdir = '$tmp/missing';"
output=$(perl "$tmp/pgpverify-missing" <"$tmp/article" 2>&1)
status=$?
if test "$status" = 255 && test ! -e "$tmp/args" \
    && printf '%s\n' "$output" | grep -i 'temp file' >/dev/null; then
    echo "ok 5 - missing temporary directory is reported"
else
    echo "not ok 5 - missing temporary directory is reported"
    echo "# status $status, output: $output"
fi

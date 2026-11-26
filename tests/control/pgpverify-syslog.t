#! /bin/sh
# Check the text pgpverify sends to syslog through Sys::Syslog.

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
if ! perl -MSys::Syslog -MSocket -e 1 2>/dev/null; then
    echo "1..0 # skip Sys::Syslog or Socket not available"
    exit 0
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/pgpverify-syslog.XXXXXX") || exit 1
trap 'rm -rf "$tmp"' 0 1 2 3 15
cat >"$tmp/gpgv" <<EOF
#! /bin/sh
exit 1
EOF
chmod +x "$tmp/gpgv"

# pgpverify keeps its default $syslog_method and so logs through
# Sys::Syslog; point Sys::Syslog at a local datagram socket.
config="\$gpgv = '$tmp/gpgv'; \$tmpdir = '$tmp';"
config="$config require Sys::Syslog;"
config="$config Sys::Syslog::setlogsock('unix', '$tmp/log');"
sed "s|^# End of configuration section\.\$|$config|" "$pgpverify" \
    >"$tmp/pgpverify"

# Bind the socket, run pgpverify on the article given as the first
# argument, then print every datagram received.
cat >"$tmp/capture" <<'EOF'
use strict;
use Socket;
my ($sock, $script, $input) = @ARGV;
socket(my $s, AF_UNIX, SOCK_DGRAM, 0) or die "socket: $!\n";
bind($s, pack_sockaddr_un($sock)) or die "bind: $!\n";
system('sh', '-c', 'exec perl "$1" <"$2" >/dev/null 2>&1', 'sh',
       $script, $input);
my $status = $? >> 8;
my $rin = '';
vec($rin, fileno($s), 1) = 1;
while (select(my $rout = $rin, undef, undef, 1) > 0) {
    my $buf;
    defined(recv($s, $buf, 65536, 0)) or last;
    $buf =~ s/\0//g;
    print $buf, ($buf =~ /\n\z/ ? '' : "\n");
}
unlink $sock;
exit $status;
EOF

# Run pgpverify on $tmp/article; set $status and $log.
run() {
    log=$(perl "$tmp/capture" "$tmp/log" "$tmp/pgpverify" "$tmp/article")
    status=$?
}

printf 'plainline\n' >"$tmp/article"
run
case "$log" in
*'non-header at line 1: plainline'*) ;;
*)
    echo "1..0 # skip cannot capture Sys::Syslog output (status $status)"
    exit 0
    ;;
esac

echo 1..3

if test "$status" = 255; then
    echo "ok 1 - plain error text is logged"
else
    echo "not ok 1 - plain error text is logged"
    echo "# status $status, log: $log"
fi

printf 'bad%%mline\n' >"$tmp/article"
run
case "$status $log" in
'255 '*'non-header at line 1: bad%mline'*)
    echo "ok 2 - %m in article text is logged literally"
    ;;
*)
    echo "not ok 2 - %m in article text is logged literally"
    echo "# status $status, log: $log"
    ;;
esac

cat >"$tmp/article" <<'EOF'
Message-ID: <id%m@example.test>
X-PGP-Sig: broken

body
EOF
run
case "$status $log" in
'255 '*'X-PGP-Sig not in expected format processing <id%m@example.test>'*)
    echo "ok 3 - Message-ID is logged verbatim"
    ;;
*)
    echo "not ok 3 - Message-ID is logged verbatim"
    echo "# status $status, log: $log"
    ;;
esac

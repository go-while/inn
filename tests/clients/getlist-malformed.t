#! /bin/sh
#
# Test that getlist moves on after a malformed LIST ACTIVE line.

# Find the right directory.
getlist="../../frontends/getlist"
dirs='. clients tests/clients'
for dir in $dirs; do
    if [ -r "$dir/getlist-malformed.t" ]; then
        cd $dir
        break
    fi
done
if [ ! -x "$getlist" ]; then
    echo 'Could not find getlist' >&2
    exit 1
fi

echo 3

INNCONF=../data/etc/inn.conf
export INNCONF

# One-shot server on 127.0.0.1:11139: a banner, then a LIST ACTIVE reply
# whose first line is malformed and whose second line is valid.
rm -f pid-malformed
perl -MIO::Socket::INET -e '
    my $s = IO::Socket::INET->new(LocalAddr => "127.0.0.1",
        LocalPort => 11139, Listen => 1, ReuseAddr => 1, Proto => "tcp")
        or die "listen: $!\n";
    my $pid = fork;
    die "fork: $!\n" unless defined $pid;
    if ($pid) {
        open(my $fh, ">", "pid-malformed") or die;
        print $fh "$pid\n";
        close $fh;
        exit 0;
    }
    alarm 30;
    my $c = $s->accept or exit 1;
    $c->autoflush(1);
    print $c "200 ready\r\n";
    while (<$c>) {
        if (/^LIST/i) {
            print $c "215 list follows\r\n", "malformed\r\n",
                "good.group 0000000010 0000000001 y\r\n", ".\r\n";
        } elsif (/^QUIT/i) {
            print $c "205 bye\r\n";
            last;
        } else {
            print $c "500 unknown\r\n";
        }
    }
    close $c;
'

# Bounded: a getlist that loops on the malformed line fills stderr; head
# stops reading after 4 KiB and the timeout ends a getlist that hangs.
rm -f seen rc err
( timeout 10 "$getlist" -h 127.0.0.1 -p 11139 active '*' y >seen
  echo $? >rc ) 2>&1 | head -c 4096 >err

if [ "$(cat rc)" = 0 ]; then
    echo "ok 1 - getlist exits 0"
else
    echo "not ok 1 - getlist exits 0 (rc=$(cat rc))"
fi
if [ "$(cat seen)" = "good.group 0000000010 0000000001 y" ]; then
    echo "ok 2 - valid line after the malformed one is printed"
else
    echo "not ok 2 - valid line after the malformed one is printed"
fi
n=$(grep -c 'malformed line from server' err)
if [ "$n" = 1 ]; then
    echo "ok 3 - one warning for the malformed line"
else
    echo "not ok 3 - one warning for the malformed line (saw $n)"
fi

if [ -f pid-malformed ]; then
    kill $(cat pid-malformed) 2>/dev/null
    rm -f pid-malformed
fi
rm -f seen rc err

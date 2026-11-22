#! /bin/sh
#
# Test suite for the URL handling of simpleftp.
#
# Net::FTP and HTTP::Tiny are replaced by fake modules which only record the
# calls simpleftp makes, so nothing is sent on the network.

# The count starts at 1 and is updated each time ok is printed.  printcount
# takes "ok" or "not ok".
count=1
printcount() {
    echo "$1 $count $2"
    count=$(expr $count + 1)
}

# Run simpleftp with the given URLs; its exit status goes to $status and the
# calls it made to $tmp/log.
run() {
    rm -f "$tmp/log"
    (cd "$tmp/work" && perl -I"$tmp/lib" "$simpleftp" "$@") 2>"$tmp/err"
    status=$?
    touch "$tmp/log"
}

# Check that the log contains a given line, the given number of times.
logged() {
    n=$(grep -c -x -F "$1" "$tmp/log")
    if [ "$n" = "$2" ]; then
        printcount "ok"
    else
        echo "# want \"$1\" $2 times, got $n; log:"
        sed 's/^/#   /' "$tmp/log"
        sed 's/^/#   stderr: /' "$tmp/err"
        printcount "not ok"
    fi
}

# Check the exit status of the last run.
exited() {
    if [ "$status" = "$1" ]; then
        printcount "ok"
    else
        echo "# exit status $status, want $1"
        printcount "not ok"
    fi
}

# Find simpleftp.
simpleftp=
for file in ../scripts/simpleftp ../../scripts/simpleftp scripts/simpleftp; do
    if [ -r "$file" ]; then
        simpleftp=$(pwd)/$file
        break
    fi
done
if [ -z "$simpleftp" ]; then
    echo "Could not find simpleftp" >&2
    exit 1
fi

tmp=$(pwd)/simpleftp-tmp.$$
rm -rf "$tmp"
mkdir -p "$tmp/lib/Net" "$tmp/lib/HTTP" "$tmp/work" || exit 1
FAKE_LOG="$tmp/log"
export FAKE_LOG

cat >"$tmp/lib/Net/FTP.pm" <<'PERL'
package Net::FTP;
use strict;
use warnings;
sub logcall {
    open(my $fh, '>>', $ENV{FAKE_LOG}) or die "$ENV{FAKE_LOG}: $!";
    print {$fh} "@_\n";
    close($fh);
}
sub new {
    my ($class, %args) = @_;
    logcall("ftp new $args{Host} $args{Port}");
    return bless({}, $class);
}
sub login  { logcall("ftp login $_[1]"); return 1 }
sub binary { logcall("ftp binary");      return 1 }
sub cwd    { logcall("ftp cwd $_[1]");   return 1 }
sub get    { logcall("ftp get $_[1]");   return $_[1] !~ /fail/ }
sub quit   { logcall("ftp quit");        return 1 }
1;
PERL

cat >"$tmp/lib/HTTP/Tiny.pm" <<'PERL'
package HTTP::Tiny;
use strict;
use warnings;
sub new     { return bless({}, shift) }
sub can_ssl { return 1 }
sub mirror {
    my ($self, $url, $file) = @_;
    open(my $fh, '>>', $ENV{FAKE_LOG}) or die "$ENV{FAKE_LOG}: $!";
    print {$fh} "http mirror $url $file\n";
    close($fh);
    return { success => 1 };
}
1;
PERL

# Print out the number of tests.
echo 23

# FTP from a host just used for an HTTP download: a new FTP session is needed.
run http://files.example/pub/a.txt ftp://files.example/pub/b.txt
exited 0
logged "ftp new files.example 21" 1
logged "ftp get b.txt" 1

# Two FTP downloads from the same host share one session.
run ftp://files.example/pub/a.txt ftp://files.example/pub/b.txt
exited 0
logged "ftp new files.example 21" 1
logged "ftp quit" 1

# FTP, HTTP and FTP again on one host: the FTP session is reused.
run ftp://files.example/pub/a.txt http://files.example/pub/b.txt \
    ftp://files.example/pub/c.txt
exited 0
logged "ftp new files.example 21" 1

# FTP, then HTTP to another host: the FTP session is closed once, at the end.
run ftp://files.example/pub/a.txt http://other.example/pub/b.txt
exited 0
logged "ftp quit" 1

# Another port or another login on the same host: a new session.
run ftp://files.example/pub/a.txt ftp://files.example:2121/pub/b.txt
logged "ftp new files.example 21" 1
logged "ftp new files.example 2121" 1
run ftp://one:secret@files.example/pub/a.txt \
    ftp://two:secret@files.example/pub/b.txt
logged "ftp login one" 1
logged "ftp login two" 1

# A failed transfer: the next URL for the same host gets a new session.
run ftp://files.example/pub/fail.txt ftp://files.example/pub/b.txt
exited 1
logged "ftp new files.example 21" 2

# Bracketed IPv6 addresses, without and with a port.
run 'ftp://[2001:db8::1]/pub/c.txt'
exited 0
logged "ftp new 2001:db8::1 21" 1
run 'ftp://[2001:db8::1]:2121/pub/d.txt'
logged "ftp new 2001:db8::1 2121" 1
run 'ftp://user:secret@[2001:db8::1]:2121/pub/e.txt'
logged "ftp login user" 1
logged "ftp new 2001:db8::1 2121" 1

# A host name with a port, and an unbracketed IPv6 address.
run ftp://files.example:2121/pub/f.txt
logged "ftp new files.example 2121" 1
run ftp://2001:db8::2/pub/g.txt
logged "ftp new 2001:db8::2 21" 1

# Clean up.
rm -rf "$tmp"

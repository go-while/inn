#! /bin/sh
exec perl -x "$0"
#!perl
use strict;
use warnings;
use File::Temp qw(tempdir);
use IO::Socket::INET;
use Test::More tests => 9;

my $dir = tempdir(CLEANUP => 1);
my $top = "$ENV{C_TAP_BUILD}/..";
$ENV{INNCONF} = "$dir/inn.conf";
$ENV{INN_TESTSUITE} = 1;
sub write_file {
    my ($path, $text) = @_;
    open(my $fh, '>', "$dir/$path") or die "$path: $!";
    print {$fh} $text;
    close($fh) or die "$path: $!";
}

write_file('inn.conf', qq{domain: example.test
pathnews: "$dir"
pathetc: "$dir"
patharticles: "$dir"
pathoverview: "$dir"
mta: "/usr/sbin/sendmail -oi -oem %s"
hismethod: hisv6
ovmethod: tradindexed
enableoverview: true
nnrpdloadlimit: 0
disablecapabilities: [ compress SASL StartTLS ]
});
write_file('storage.conf', "method tradspool {\n newsgroups: *\n class: 0\n}\n");
write_file('readers.conf', qq{auth "local" {
    hosts: "127.0.0.1"
    default: "<local>"
}
access "local" {
    users: "<local>"
    newsgroups: "*"
    access: R
}
});
system("$top/storage/tradindexed/tdx-util", '-c', '-n', 'example.test') == 0
    or BAIL_OUT('cannot create temporary overview');
my $listen = IO::Socket::INET->new(LocalAddr => '127.0.0.1', LocalPort => 0,
    Listen => 1, Proto => 'tcp') or BAIL_OUT("listen: $!");
my $client = IO::Socket::INET->new(PeerAddr => '127.0.0.1',
    PeerPort => $listen->sockport, Proto => 'tcp') or BAIL_OUT("connect: $!");
my $peer = $listen->accept or BAIL_OUT("accept: $!");
close $listen;
my $pid = fork();
defined $pid or BAIL_OUT("fork: $!");
if (!$pid) {
    close $client;
    open(STDIN, '<&', $peer) or die $!;
    open(STDOUT, '>&', $peer) or die $!;
    close $peer;
    exec "$top/nnrpd/nnrpd", '-n';
    die "exec nnrpd: $!";
}
close $peer;
local $SIG{ALRM} = sub { kill 'KILL', $pid; die "nnrpd timed out\n" };
alarm 15;
my $greeting = <$client>;
unless (like($greeting, qr/^20[01] /, 'reader greeting')) {
    close $client;
    waitpid($pid, 0);
    alarm 0;
    BAIL_OUT('reader did not accept the test connection');
}
print {$client} "CAPABILITIES\r\n";
like(scalar <$client>, qr/^101 /, 'CAPABILITIES');
my $caps = '';
while (my $line = <$client>) {
    last if $line eq ".\r\n";
    length($caps) < 8192 or BAIL_OUT('excess capability output');
    $caps .= $line;
}
like($caps, qr/^READER\r?$/m, 'unrelated capability still advertised');
my $hidden = unlike($caps,
    qr/^(?:COMPRESS|STARTTLS|SASL)\b|^AUTHINFO[^\r\n]*\bSASL\b/m,
    'disabled capabilities are absent');
SKIP: {
    skip 'reader still advertises a transport change', 5 unless $hidden;
    for my $test (['COMPRESS DEFLATE', 500], ['STARTTLS', 500],
                  ['AUTHINFO SASL EXTERNAL', 501], ['DATE', 111], ['QUIT', 205]) {
        print {$client} "$test->[0]\r\n";
        like(scalar <$client>, qr/^$test->[1] /, $test->[0]);
    }
}
close $client;
waitpid($pid, 0);
alarm 0;

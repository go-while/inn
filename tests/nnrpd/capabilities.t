#! /bin/sh
exec perl -x "$0"
#!perl
use strict;
use warnings;
use File::Temp qw(tempdir);
use IO::Socket::INET;
use Test::More;

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

my $config = qq{domain: example.test
pathnews: "$dir"
pathetc: "$dir"
patharticles: "$dir"
pathoverview: "$dir"
mta: "/usr/sbin/sendmail -oi -oem %s"
hismethod: hisv6
ovmethod: tradindexed
enableoverview: true
nnrpdloadlimit: 0
tlscafile: "$dir/cert.pem"
};
sub configure_reader {
    my ($disabled) = @_;
    write_file('inn.conf', $config . (defined $disabled
        ? "disablecapabilities: [ $disabled ]\n" : ''));
}
configure_reader(undef);
write_file('storage.conf', "method tradspool {\n newsgroups: *\n class: 0\n}\n");
write_file('readers.conf', qq{auth "local" {
    hosts: "127.0.0.1"
    auth: "ckpasswd"
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

open(my $header, '<', "$top/include/config.h") or die "config.h: $!";
my %built;
while (<$header>) {
    $built{$1} = 1 if /^#define HAVE_(SASL|OPENSSL|ZLIB) 1$/;
}
note('compiled features: ' . join(' ', sort keys %built));
my ($reader, $ssl_client);
local $SIG{ALRM} = sub {
    kill 'KILL', grep { defined } ($ssl_client, $reader);
    die "reader session timed out\n";
};

sub listen_reader {
    my (@flags) = @_;
    my $listen = IO::Socket::INET->new(LocalAddr => '127.0.0.1', LocalPort => 0,
        Listen => 1, Proto => 'tcp') or BAIL_OUT("listen: $!");
    my $port = $listen->sockport;
    $reader = fork();
    defined $reader or BAIL_OUT("fork: $!");
    if (!$reader) {
        my $peer = $listen->accept or die "accept: $!";
        close $listen;
        open(STDIN, '<&', $peer) or die $!;
        open(STDOUT, '>&', $peer) or die $!;
        close $peer;
        exec "$top/nnrpd/nnrpd", '-n', @flags;
        die "exec nnrpd: $!";
    }
    close $listen;
    alarm 20;
    return $port;
}

sub caps {
    my ($disabled, $requests) = @_;
    configure_reader($disabled);
    my $port = listen_reader();
    my $socket = IO::Socket::INET->new(PeerAddr => '127.0.0.1',
        PeerPort => $port, Proto => 'tcp') or die "connect: $!";
    my $greeting = <$socket> // '';
    $greeting =~ /^20[01] / or BAIL_OUT("reader greeting: $greeting");
    print {$socket} "CAPABILITIES\r\n";
    my $reply = <$socket> // '';
    $reply =~ /^101 / or BAIL_OUT("CAPABILITIES: $reply");
    my $caps = '';
    while (my $line = <$socket>) {
        last if $line eq ".\r\n";
        length($caps) < 8192 or BAIL_OUT('excess capability output');
        $caps .= $line;
    }
    for my $test (@$requests, ['QUIT', 205]) {
        print {$socket} "$test->[0]\r\n";
        like(scalar <$socket>, qr/^$test->[1] /, $test->[0]);
    }
    close $socket;
    waitpid($reader, 0);
    undef $reader;
    alarm 0;
    return $caps;
}

my $defaults = caps(undef, []);
my $empty = caps('', []);
is($empty, $defaults, 'empty list advertises the default capabilities');
like($empty, qr/^READER\r?$/m, 'reader remains available');
for my $cap (['ZLIB', 'COMPRESS'], ['OPENSSL', 'STARTTLS']) {
    is(($empty =~ /^$cap->[1]\b/m) ? 1 : 0, $built{$cap->[0]} // 0,
        "empty list advertises $cap->[1] when built");
}
my $has_sasl = $empty =~ /^SASL /m;
ok(!$built{SASL} || $has_sasl || $empty !~ /^AUTHINFO[^\r\n]*\bSASL\b/m,
   'AUTHINFO does not offer SASL without mechanisms');
note('default SASL mechanisms: ' . ($empty =~ /^SASL ([^\r\n]+)/m ? $1 : 'none'));
for my $name (qw(COMPRESS SASL STARTTLS)) {
    my $expected = $empty;
    $expected =~ s/^$name\b[^\r\n]*\r\n//m;
    $expected =~ s/^(AUTHINFO[^\r\n]*?) SASL(?=\s)/$1/m if $name eq 'SASL';
    is(caps($name, []), $expected, "disabling only $name preserves other capabilities");
}
my $all = caps('compress SASL StartTLS', [
    ['COMPRESS DEFLATE', 500], ['STARTTLS', 500],
    ['AUTHINFO SASL EXTERNAL', 501], ['DATE', 111],
]);
unlike($all, qr/^(?:COMPRESS|STARTTLS|SASL)\b|^AUTHINFO[^\r\n]*\bSASL\b/m,
    'all disabled capabilities absent');

SKIP: {
    skip 'reader built without OpenSSL', 4 unless $built{OPENSSL};
    my ($openssl) = grep { -x $_ && -f $_ }
        map { "$_/openssl" } split /:/, $ENV{PATH};
    skip 'openssl command unavailable', 4 unless $openssl;
    system($openssl, 'req', '-new', '-x509', '-newkey', 'rsa:2048', '-nodes',
        '-subj', '/CN=localhost', '-days', 1, '-out', "$dir/cert.pem",
        '-keyout', "$dir/key.pem") == 0 or BAIL_OUT('test TLS certificate');
    write_file('commands', "DATE\r\nQUIT\r\n");
    configure_reader('SASL');
    for my $mode ('STARTTLS', 'dedicated TLS') {
        my $upgrade = $mode eq 'STARTTLS';
        my $port = listen_reader($upgrade ? () : '-S');
        pipe(my $out, my $writer) or die "pipe: $!";
        $ssl_client = fork();
        defined $ssl_client or die "fork: $!";
        if (!$ssl_client) {
            close $out;
            open(STDOUT, '>&', $writer) or die $!;
            close $writer;
            open(STDIN, '<', "$dir/commands") or die $!;
            exec $openssl, 's_client', '-quiet', '-ign_eof', '-CAfile',
                "$dir/cert.pem", '-verify_return_error', '-connect', "127.0.0.1:$port",
                ($upgrade ? ('-starttls', 'nntp') : ());
            die "exec openssl: $!";
        }
        close $writer;
        my $reply = '';
        while (my $line = <$out>) {
            length($reply) < 8192 or BAIL_OUT('excess TLS output');
            $reply .= $line;
        }
        close $out;
        waitpid($ssl_client, 0);
        undef $ssl_client;
        waitpid($reader, 0);
        undef $reader;
        alarm 0;
        like($reply, qr/^111 /m, "$mode with only SASL disabled serves DATE over TLS");
        like($reply, qr/^205 /m, "$mode completes QUIT over TLS");
    }
}
done_testing();

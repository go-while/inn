#! /bin/sh
# Exercise the command-line limits with tinyleaf's inetd interface.
exec perl -x "$0"
#!perl
use strict;
use warnings;
use File::Temp qw(tempdir);
use IO::Select;
use IO::Socket::INET;
use IPC::Open2;
use POSIX ();
use Test::More;

my $binary = "$ENV{C_TAP_BUILD}/../innd/tinyleaf";
my $spool = tempdir(CLEANUP => 1);
my $article = "Subject: test\r\n\r\nbody\r\n.\r\n";

sub session {
    my ($input, @options) = @_;
    my $hold = ref($input) eq 'SCALAR';
    $input = $$input if $hold;
    my $pid = open2(my $out, my $in, $binary, @options, $spool);
    local $SIG{ALRM} = sub { kill 'KILL', $pid; die "tinyleaf timed out\n" };
    local $SIG{PIPE} = 'IGNORE';
    alarm 15;
    print {$in} $input if defined $input;
    close $in if defined $input && !$hold;
    my @replies = <$out>;
    close $in if !defined($input) || $hold;
    close $out;
    waitpid $pid, 0;
    my $status = $?;
    alarm 0;
    return ($status, join('', @replies));
}

my ($status, $reply) = session("QUIT\r\n");
is($status, 0, 'original invocation');
like($reply, qr/^205 /m, 'QUIT succeeds');

($status, $reply) = session("IHAVE <a\@example.test>\r\n$article"
        . "IHAVE <b\@example.test>\r\nx$article" . "QUIT\r\n",
    '-s', length($article));
like($reply, qr/^235 /m, 'article at the byte limit is stored');
like($reply, qr/^437 /m, 'article one byte over the limit is refused');
like($reply, qr/^205 /m, 'next command succeeds');
my @files = glob("$spool/*");
is(scalar @files, 1, 'only the accepted article was spooled');

($status, $reply) = session(undef, '-t', 1);
like($reply, qr/^201 /m, 'idle connection received greeting');
is($status, 0, 'read timeout closes the idle session');

($status) = session('', '-s', '0');
isnt($status, 0, 'zero size rejected');
($status) = session('', '-t', '2s');
isnt($status, 0, 'invalid timeout rejected');

($status) = session('', $spool, 'extra');
isnt($status, 0, 'reject more than two positional arguments');
my $partial = "IHAVE <partial\@example.test>\r\nSubject: partial\r\n";
($status, $reply) = session(\$partial, '-t', 1);
like($reply, qr/^335 /m, 'entered article transfer');
isnt($status, 0, 'timeout during article closes session');
is_deeply([glob("$spool/*")], \@files, 'partial article was discarded');

# Talk to tinyleaf over a real TCP connection on the loopback interface, as
# inetd would run it, and send more than the default 1 MiB input buffer.  The
# reply must reach the client before the disconnect: a close with unread
# input can send a reset instead of a normal shutdown.  Return the bytes
# received, how the connection ended (eof, reset, timeout or an error) and
# the exit status of tinyleaf.
sub socket_session {
    my ($input) = @_;
    my $listen = IO::Socket::INET->new(
        Listen => 1, LocalAddr => '127.0.0.1', LocalPort => 0,
        Proto => 'tcp', ReuseAddr => 1
    ) or BAIL_OUT("cannot listen: $!");
    my $pid = fork;
    BAIL_OUT("cannot fork: $!") unless defined $pid;
    if ($pid == 0) {
        my $connection = $listen->accept or POSIX::_exit(99);
        POSIX::dup2(fileno($connection), 0) or POSIX::_exit(98);
        POSIX::dup2(fileno($connection), 1) or POSIX::_exit(98);
        exec($binary, $spool) or POSIX::_exit(97);
    }
    my $port = $listen->sockport;
    my $client = IO::Socket::INET->new(
        PeerAddr => '127.0.0.1', PeerPort => $port, Proto => 'tcp'
    ) or BAIL_OUT("cannot connect: $!");
    close $listen;
    local $SIG{PIPE} = 'IGNORE';
    local $SIG{ALRM} = sub { kill 'KILL', $pid; die "tinyleaf timed out\n" };
    alarm 30;
    my $offset = 0;
    while ($offset < length $input) {
        my $written = syswrite($client, $input, 8192, $offset);
        last unless defined $written;    # reset or closed by the server
        $offset += $written;
    }
    my ($data, $end) = ('', 'timeout');
    my $select = IO::Select->new($client);
    while ($select->can_read(10)) {
        my $count = sysread($client, my $chunk, 8192);
        if (!defined $count) {
            $end = $!{ECONNRESET} ? 'reset' : "error: $!";
            last;
        }
        if ($count == 0) {
            $end = 'eof';
            last;
        }
        $data .= $chunk;
    }
    close $client;
    kill 'KILL', $pid if $end eq 'timeout';
    waitpid $pid, 0;
    my $status = $?;
    alarm 0;
    return ($data, $end, $status);
}

# One extra byte beyond the buffer size without a line terminator.
my $oversize = 1024 * 1024 + 1;
my $line = ('x' x 998) . "\r\n";
my $large_article = "Subject: large\r\n\r\n";
$large_article .= $line while length($large_article) < $oversize;
my %socket_cases = (
    'Command' => 'A' x $oversize,
    'Article' => "IHAVE <large\@example.test>\r\n$large_article",
);
for my $context ('Command', 'Article') {
    my ($data, $end, $code) = socket_session($socket_cases{$context});
    like($data, qr/^201 /m, "$context: greeting received");
    like($data, qr/^335 [^\n]*\n400 /m, "$context: IHAVE accepted, then 400")
        if $context eq 'Article';
    like($data, qr/^400 /m, "$context: 400 reached the client");
    unlike($data, qr/^(?:205|235|437) /m, "$context: no other reply after 400");
    ok($end eq 'eof' || $end eq 'reset', "$context: connection closed")
        or diag("connection ended with $end");
    diag("$context: 400 was followed by $end");
    is($code, 0, "$context: tinyleaf exited cleanly");
}
is_deeply([glob("$spool/*")], \@files, 'oversized input left no spooled article');

$binary = "$ENV{C_TAP_BUILD}/innd/tinyleaf-status";
for my $case (['LONG', 'Command'], ['IHAVE <full@example.test>', 'Article']) {
    my ($command, $context) = @$case;
    ($status, $reply) = session("$command\r\nQUIT\r\n");
    like($reply, qr/^400 $context /m, "$context buffer-full status gives 400 with default options");
    unlike($reply, qr/^205 /m, "$context buffer-full status ends the session");
    is($status, 0, "$context handler shut down cleanly");
}
is_deeply([glob("$spool/*")], \@files, 'buffer-full status left no spooled article');
done_testing();

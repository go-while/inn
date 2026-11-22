#! /bin/sh
# Exercise the command-line limits with tinyleaf's inetd interface.
exec perl -x "$0"
#!perl
use strict;
use warnings;
use File::Temp qw(tempdir);
use IPC::Open2;
use Test::More tests => 10;

my $binary = "$ENV{C_TAP_BUILD}/../innd/tinyleaf";
my $spool = tempdir(CLEANUP => 1);
my $article = "Subject: test\r\n\r\nbody\r\n.\r\n";

sub session {
    my ($input, @options) = @_;
    my $pid = open2(my $out, my $in, $binary, @options, $spool);
    local $SIG{ALRM} = sub { kill 'KILL', $pid; die "tinyleaf timed out\n" };
    local $SIG{PIPE} = 'IGNORE';
    alarm 15;
    print {$in} $input if defined $input;
    close $in if defined $input;
    my @replies = <$out>;
    close $in unless defined $input;
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

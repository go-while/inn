#! /usr/bin/env perl
#
# Test suite for pullnews (frontends/pullnews): which posts refused by the
# downstream server are reported as declined.
#
# pullnews runs in reader mode (-R) against two fake servers on the IPv4
# loopback address: an upstream server with four articles in fu.test, and a
# downstream server that refuses every POST with "441 435 <message-id>".  A
# refusal that names the Message-ID of the article is a plain duplicate and
# is not reported; a refusal that names another Message-ID is reported.  Two
# of the Message-IDs contain regular expression metacharacters.

use strict;
use warnings;

use File::Temp qw(tempdir);
use IO::Socket::INET ();
use POSIX ();

$| = 1;

if (!eval { require Net::NNTP; 1 }) {
    print "1..0 # skip Net::NNTP not available\n";
    exit 0;
}

my $pullnews;
for my $dir ('..', '../..', '.') {
    for my $file ('frontends/pullnews', 'frontends/pullnews.in') {
        if (-r "$dir/$file") {
            $pullnews = "$dir/$file";
            last;
        }
    }
    last if $pullnews;
}
die "cannot find frontends/pullnews\n" unless $pullnews;

my $tmp = tempdir(CLEANUP => 1);

# Article number => [Message-ID, Message-ID in the refusal of the downstream
# server].
my %articles = (
    1 => ['<fu+417.1@example.invalid>', '<fu+417.1@example.invalid>'],
    2 => ['<fu|417.2@example.invalid>', '<fu-other@example.invalid>'],
    3 => ['<fu417.3@example.invalid>', '<fu417.3@example.invalid>'],
    4 => ['<fu417.4@example.invalid>', '<fu-other@example.invalid>'],
);
my %refusal = map { $articles{$_}[0] => $articles{$_}[1] } keys %articles;

sub listener {
    my $sock = IO::Socket::INET->new(
        LocalAddr => '127.0.0.1',
        LocalPort => 0,
        Listen    => 5,
        Proto     => 'tcp',
        ReuseAddr => 1,
    ) or die "cannot listen on 127.0.0.1: $!\n";
    return $sock;
}

# Read one command line from a client; undef at end of file.
sub getcmd {
    my ($c) = @_;
    my $line = <$c>;
    return unless defined $line;
    $line =~ s/\r?\n\z//;
    return $line;
}

sub upstream {
    my ($c) = @_;
    print $c "200 fu upstream ready\r\n";
    while (defined(my $line = getcmd($c))) {
        if ($line =~ /^mode reader$/i) {
            print $c "200 posting allowed\r\n";
        } elsif ($line =~ /^group fu\.test$/i) {
            print $c "211 4 1 4 fu.test\r\n";
        } elsif ($line =~ /^article (\d+)$/i and $articles{$1}) {
            my ($n, $id) = ($1, $articles{$1}[0]);
            print $c "220 $n $id article\r\n";
            print $c "Path: upstream.example!not-for-mail\r\n";
            print $c "From: Tester <tester\@example.invalid>\r\n";
            print $c "Newsgroups: fu.test\r\n";
            print $c "Subject: pullnews test $n\r\n";
            print $c "Date: Wed, 25 Nov 2026 12:00:00 +0000\r\n";
            print $c "Message-ID: $id\r\n";
            print $c "\r\n";
            print $c "Body $n.\r\n";
            print $c ".\r\n";
        } elsif ($line =~ /^article /i) {
            print $c "423 no such article\r\n";
        } elsif ($line =~ /^quit$/i) {
            print $c "205 bye\r\n";
            return;
        } else {
            print $c "500 unknown command\r\n";
        }
    }
    return;
}

sub downstream {
    my ($c) = @_;
    print $c "200 fu downstream ready\r\n";
    while (defined(my $line = getcmd($c))) {
        if ($line =~ /^mode reader$/i) {
            print $c "200 posting allowed\r\n";
        } elsif ($line =~ /^post$/i) {
            print $c "340 send article\r\n";
            my $id = '';
            while (defined(my $art = getcmd($c))) {
                last if $art eq '.';
                $id = $1 if $art =~ /^Message-ID:\s*(\S+)/i;
            }
            open(my $fh, '>>', "$tmp/posted") or die "cannot write: $!\n";
            print $fh "$id\n";
            close($fh);
            my $other = $refusal{$id} || $id;
            print $c "441 435 $other\r\n";
        } elsif ($line =~ /^quit$/i) {
            print $c "205 bye\r\n";
            return;
        } else {
            print $c "500 unknown command\r\n";
        }
    }
    return;
}

# Serve connections one after the other in a child process.
my @children;
sub serve {
    my ($sock, $handler) = @_;
    my $pid = fork;
    die "cannot fork: $!\n" unless defined $pid;
    if ($pid == 0) {
        $SIG{PIPE} = 'IGNORE';
        while (my $c = $sock->accept) {
            $c->autoflush(1);
            $handler->($c);
            close($c);
        }
        POSIX::_exit(0);
    }
    close($sock);
    push(@children, $pid);
    return;
}

# Stop the fake servers; waitpid() sets $?, so keep the exit status of the
# test.
END {
    local $?;
    kill('TERM', @children) if @children;
    waitpid($_, 0) for @children;
}

my $up = listener();
my $down = listener();
my ($upport, $downport) = ($up->sockport, $down->sockport);
serve($up, \&upstream);
serve($down, \&downstream);

spew("$tmp/pullnews.marks", "127.0.0.1:$upport\n    fu.test 0 0\n");

# Run pullnews, killing it after 60 seconds; returns its exit status.
my $pid = fork;
die "cannot fork: $!\n" unless defined $pid;
if ($pid == 0) {
    open(STDOUT, '>', "$tmp/stdout") or POSIX::_exit(126);
    open(STDERR, '>', "$tmp/stderr") or POSIX::_exit(126);
    exec($^X, $pullnews, '-R', '-c', "$tmp/pullnews.marks",
        '-l', "$tmp/log", '-s', "127.0.0.1:$downport", '-N', '10')
      or POSIX::_exit(127);
}
my $status;
for (1 .. 600) {
    last if waitpid($pid, POSIX::WNOHANG()) == $pid and defined($status = $?);
    ## no critic (ProhibitSleepViaSelect)
    select(undef, undef, undef, 0.1);
    ## use critic
}
if (!defined $status) {
    kill('KILL', $pid);
    waitpid($pid, 0);
    $status = 'timeout';
}

sub spew {
    my ($file, $content) = @_;
    open(my $fh, '>', $file) or die "cannot write $file: $!\n";
    print $fh $content;
    close($fh) or die "cannot write $file: $!\n";
    return;
}

sub slurp {
    my ($file) = @_;
    open(my $fh, '<', $file) or return '';
    local $/;
    my $content = <$fh>;
    close($fh);
    return $content;
}

my $log = slurp("$tmp/log");
my %declined = map { $_ => 1 } $log =~ /Post (\d+) to server declined/g;
my $output = join("\n", 'log:', $log, 'stderr:', slurp("$tmp/stderr"));
my $reported = join("\n", 'reported:',
    $log =~ /^\s*(Post \d+ to server declined.*)$/mg);

my $n = 0;
sub check {
    my ($ok, $name, $diag) = @_;
    $n++;
    print(($ok ? 'ok' : 'not ok') . " $n - $name\n");
    if (!$ok && defined $diag) {
        print "# $_\n" for grep { /\S/ } split(/\n/, $diag);
    }
    return;
}

print "1..6\n";
check($status eq '0', 'pullnews exits with status 0',
    "status $status\n$output");
my @posted = split(/\n/, slurp("$tmp/posted"));
check(@posted == 4, 'all four articles offered with POST',
    join("\n", 'posted:', @posted));
check(!$declined{1}, 'refusal naming a Message-ID with + is not reported',
    $reported);
check($declined{2}, 'refusal naming another Message-ID than one with |'
      . ' is reported', $reported);
check(!$declined{3}, 'refusal naming a plain Message-ID is not reported',
    $reported);
check($declined{4}, 'refusal naming another Message-ID is reported',
    $reported);

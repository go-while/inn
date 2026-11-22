#! /bin/sh
exec perl -x "$0"
#!perl
use strict;
use warnings;
use File::Temp qw(tempdir);
use Test::More;

# Keep cycbuff paths within the legacy CNFS header's 63-byte limit.
my $dir = tempdir('inn-cnfs-XXXXXX', DIR => '/tmp', CLEANUP => 1);
my $sm = "$ENV{C_TAP_BUILD}/../frontends/sm";
$ENV{INNCONF} = "$dir/inn.conf";
$ENV{INN_TESTSUITE} = 1;
sub write_file {
    my ($name, $contents) = @_;
    open(my $out, '>', "$dir/$name") or die "$name: $!";
    print {$out} $contents;
    close $out or die "$name: $!";
}
write_file('inn.conf', qq{domain: example.test
mta: "/usr/sbin/sendmail -oi -oem %s"
pathnews: "$dir"
patharticles: "$dir"
pathetc: "$dir"
hismethod: hisv6
enableoverview: false
});
write_file('storage.conf', "method cnfs {\n newsgroups: *\n class: 0\n options: BOTH\n}\n");

my $number = 0;
sub store_batch {
    my (@blocks) = @_;
    open(my $in, '>', "$dir/articles") or die $!;
    for my $blocks (@blocks) {
        $number++;
        # Allow room in the final block for both the article and CNFS headers.
        my $body = 'x' x (($blocks - 1) * 4096 + 1);
        print {$in} "Newsgroups: example.test\r\nXref: news.example.test example.test:$number\r\n",
            "Message-ID: <$number\@example.test>\r\n\r\n$body\r\n.\r\n";
    }
    close $in or die $!;
    my $pid = fork();
    defined $pid or die $!;
    if (!$pid) {
        open(STDIN, '<', "$dir/articles") or die $!;
        open(STDOUT, '>', "$dir/tokens") or die $!;
        exec $sm, '-s', '-R';
        die "exec sm: $!";
    }
    local $SIG{ALRM} = sub { kill 'KILL', $pid; die "sm timed out\n" };
    alarm 30;
    waitpid $pid, 0;
    alarm 0;
    $? == 0 or BAIL_OUT('sm failed to store test articles');
    open(my $tokens, '<', "$dir/tokens") or die $!;
    my @placement;
    while (<$tokens>) {
        /^\@0300([0-9A-F]{16})[0-9A-F]{8}([0-9A-F]{8})\@$/i
            or BAIL_OUT('unexpected CNFS token');
        my ($name, $cycle) = ($1, $2);
        chomp;
        push @placement, [unpack('Z8', pack('H*', $name)), hex($cycle), $_];
    }
    return @placement;
}

# Capacity in 4 KiB blocks, excluding the 16 KiB disk header.
my %capacity = (A => 32, B => 64, C => 16);
for my $mode (qw(INTERLEAVE WEIGHTED)) {
    my $config = '';
    for my $name (sort keys %capacity) {
        my $kib = 16 + 4 * $capacity{$name};
        write_file($name, '');
        truncate("$dir/$name", $kib * 1024) or die $!;
        $config .= "cycbuff:$name:$dir/$name:$kib\n";
    }
    write_file('cycbuff.conf', $config . "metacycbuff:BOTH:A,B,C:$mode\n");
    my @initial = store_batch(1, 48);
    is_deeply([map { $_->[0] } @initial], [qw(A B)],
        "$mode stores the large article on the only member it fits");

    # B has advanced three quarters of a turn, A one block, and C none.
    my @blocks = ((1) x 6, (1, 2, 3, 1, 2, 1) x 70);
    my @next = store_batch(@blocks);
    is(scalar @next, scalar @blocks, "$mode stored every article after restart");
    my @first = map { $_->[0] } @next[0..3];
    is_deeply(\@first, $mode eq 'WEIGHTED' ? [qw(C A A C)] : [qw(A B C A)],
        "$mode first four placements after reopen");
    my (%count, %used, %last_cycle);
    $used{A} = 1;
    $used{B} = 48;
    my $total = 49;
    for my $i (0..$#next) {
        my ($member, $cycle) = @{$next[$i]};
        $count{$member}++;
        $used{$member} += $blocks[$i];
        $last_cycle{$member} = $cycle;
        $total += $blocks[$i];
    }
    for my $member (sort keys %capacity) {
        cmp_ok($last_cycle{$member}, '>', 1, "$mode observed $member rollover");
        if ($mode eq 'INTERLEAVE') {
            is($count{$member}, @next / 3, "equal article count on $member");
        } else {
            my $share = $capacity{$member} / 112;
            cmp_ok(abs($used{$member}/$total - $share), '<', 0.03,
                "padded bytes on $member track its share of total capacity");
        }
    }
    if ($mode eq 'WEIGHTED') {
        # Initialize D with the existing writer, leaving it outside BOTH.
        write_file('D', '');
        truncate("$dir/D", 20 * 1024) or die $!;
        $config .= "cycbuff:D:$dir/D:20\n";
        write_file('cycbuff.conf', $config . "metacycbuff:BOTH:A,B,C:WEIGHTED\n");
        my ($last) = store_batch(1);
        write_file('cycbuff.conf', $config . "metacycbuff:BOTH:A,B,C,D:WEIGHTED\n");
        open(my $read, '-|', $sm, '-H', $last->[2]) or die $!;
        my $headers = do { local $/; <$read> };
        ok(close($read), 'read-only storage opens with a member too small for writes');
        like($headers, qr/^Message-ID:/m, 'existing article remains readable');
    }
}
done_testing();

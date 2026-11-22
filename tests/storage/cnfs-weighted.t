#! /bin/sh
exec perl -x "$0"
#!perl
use strict;
use warnings;
use File::Temp qw(tempdir);
use Test::More tests => 7;

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
write_file('cycbuff.conf', "cycbuff:A:$dir/a:144\ncycbuff:B:$dir/b:272\nmetacycbuff:BOTH:A,B:WEIGHTED\n");
for my $buffer (['a', 144], ['b', 272]) {
    write_file($buffer->[0], '');
    truncate("$dir/$buffer->[0]", $buffer->[1] * 1024) or die $!;
}
my $number = 0;
sub store_batch {
    my $pid = fork();
    defined $pid or die $!;
    if (!$pid) {
        open(STDIN, '<', "$dir/articles") or die $!;
        open(STDOUT, '>', "$dir/tokens") or die $!;
        exec $sm, '-s', '-R';
        die "exec sm: $!";
    }
    local $SIG{ALRM} = sub { kill 'KILL', $pid; die "sm timed out\n" };
    alarm 15;
    waitpid $pid, 0;
    alarm 0;
    return () if $?;
    open(my $tokens, '<', "$dir/tokens") or die $!;
    return map { /^\@0300([0-9A-F]{16})/i ? unpack('Z8', pack('H*', $1)) : '?' } <$tokens>;
}
sub articles {
    my ($count) = @_;
    my $input = '';
    for (1 .. $count) {
        $number++;
        $input .= "Newsgroups: example.test\r\nXref: news.example.test example.test:$number\r\nMessage-ID: <$number\@example.test>\r\n\r\nbody\r\n.\r\n";
    }
    write_file('articles', $input);
}
# One block per article, with 128 and 256 KiB of usable space.
articles(60);
my @first = store_batch();
is(scalar @first, 60, 'all stores succeeded');
is(scalar(grep { $_ eq 'A' } @first), 20, 'small buffer takes one third');
is(scalar(grep { $_ eq 'B' } @first), 40, 'large buffer takes two thirds');
ok((grep { $first[$_] ne $first[$_-1] } 1 .. $#first) > 20, 'writes interleave');
# Reopen both disks and cross the first rollover.
articles(60);
my @second = store_batch();
is(scalar @second, 60, 'all stores after restart succeeded');
cmp_ok(abs(scalar(grep { $_ eq 'A' } @second) - 20), '<=', 1, 'small buffer stays in proportion');
cmp_ok(abs(scalar(grep { $_ eq 'B' } @second) - 40), '<=', 1, 'large buffer stays in proportion');

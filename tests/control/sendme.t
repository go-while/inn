#! /usr/bin/env perl
#
# Test suite for the sendme control message handler (control/modules/sendme.pl):
# which site names get a batch file in the batch directory.

use strict;
use warnings;

use File::Temp qw(tempdir);

my $module;
for my $dir ('..', '../..', '.') {
    if (-r "$dir/control/modules/sendme.pl") {
        $module = "$dir/control/modules/sendme.pl";
        last;
    }
}
die "cannot find control/modules/sendme.pl\n" unless $module;

my $tmp = tempdir(CLEANUP => 1);
for my $dir ('bin', 'batch', 'tmp') {
    mkdir("$tmp/$dir") or die "cannot create $tmp/$dir: $!\n";
}

# A grephistory that answers every Message-ID, so that the handler has
# something to write to the batch file.
open(my $fh, '>', "$tmp/bin/grephistory") or die "cannot write: $!\n";
print $fh "#!/bin/sh\ncat\n";
close($fh);
chmod(0755, "$tmp/bin/grephistory") or die "cannot chmod: $!\n";

# The parts of controlchan the handler uses.
{
    no warnings 'once';
    $INN::Config::newsbin = "$tmp/bin";
    $INN::Config::tmpdir = "$tmp/tmp";
    $INN::Config::batch = "$tmp/batch";
}
sub logdie { die "logdie: @_\n" }
sub logmsg { return }

package FakeArticle;
sub new { my ($class, $body) = @_; return bless({ body => $body }, $class) }
sub stringify_body { return $_[0]{body} }

package main;

require $module;

my $article
  = FakeArticle->new("<one\@example.invalid>\n<two\@example.invalid>\n");

# Run the handler with action doit for the given site; returns its error.
sub sendme {
    my ($site) = @_;
    my $ok = eval {
        control_sendme(undef, 'sender@example.invalid', undef, $site,
            'doit', undef, undef, $article);
        1;
    };
    return $ok ? '' : $@;
}

my $n = 0;
sub check {
    my ($ok, $name) = @_;
    $n++;
    print(($ok ? 'ok' : 'not ok') . " $n - $name\n");
    return;
}

print "1..4\n";

my $err = sendme('peer.example');
check(-s "$tmp/batch/peer.example.work" && !$err,
    'a plain site name is batched');

$err = sendme('news-peer_1.example');
check(-s "$tmp/batch/news-peer_1.example.work" && !$err,
    'a site name with - and _ is batched');

$err = sendme('../escape');
check(!-e "$tmp/escape.work", 'no batch file outside the batch directory');

$err = sendme('a:b');
check(!-e "$tmp/batch/a:b.work", 'a site name with : is not batched');

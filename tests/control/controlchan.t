#! /usr/bin/env perl
#
# Test suite for the ctlinnd() wrapper of controlchan (control/controlchan.in)
# and its callers in the newgroup and rmgroup handlers: when innd rejects one
# ctlinnd command, controlchan goes on with the next control message and does
# not change the newsgroups file for the rejected one.
#
# The real handlers (control/modules/newgroup.pl and rmgroup.pl) and the real
# ctlinnd() sub, taken from controlchan.in, run against a fake ctlinnd that
# answers like innd: it fails with exit status 1 for a newsgroup name with an
# illegal character, and for every command while a "throttled" file exists.
# Each scenario runs in a child process whose logdie() exits like the one of
# controlchan, so a child that stops after the first message shows up.

use strict;
use warnings;

use Encode;
use File::Temp qw(tempdir);
use POSIX ();

$| = 1;

my $top;
for my $dir ('..', '../..', '.') {
    if (-r "$dir/control/controlchan.in") {
        $top = $dir;
        last;
    }
}
die "cannot find control/controlchan.in\n" unless $top;

my $tmp = tempdir(CLEANUP => 1);
for my $dir ('bin', 'db', 'locks') {
    mkdir("$tmp/$dir") or die "cannot create $tmp/$dir: $!\n";
}

# Write a file with the given content.
sub spew {
    my ($file, $content) = @_;
    open(my $fh, '>', $file) or die "cannot write $file: $!\n";
    print $fh $content;
    close($fh) or die "cannot write $file: $!\n";
    return;
}

# Read a whole file; empty if it does not exist.
sub slurp {
    my ($file) = @_;
    open(my $fh, '<', $file) or return '';
    local $/;
    my $content = <$fh>;
    close($fh);
    return $content;
}

# The fake ctlinnd: innd refuses a newsgroup name with ':', '!' or '/'
# (innd/cc.c CCnewgroup) and every change while throttled.  Accepted
# commands are recorded.
spew("$tmp/bin/ctlinnd", <<"EOF");
#!/bin/sh
[ "\$1" = -s ] && shift
if [ -f '$tmp/throttled' ]; then
    echo 'ctlinnd: server throttled' >&2
    exit 1
fi
case "\$2" in
*[:!/]*)
    echo 'ctlinnd: Illegal character in newsgroup name' >&2
    exit 1
    ;;
esac
echo "\$*" >> '$tmp/ctlinnd.log'
exit 0
EOF
chmod(0755, "$tmp/bin/ctlinnd") or die "cannot chmod: $!\n";

{
    no warnings 'once';
    $INN::Config::newsbin = "$tmp/bin";
    $INN::Config::active = "$tmp/db/active";
    $INN::Config::newsgroups = "$tmp/db/newsgroups";
    $INN::Config::locks = "$tmp/locks";
    $INN::Config::pathdb = "$tmp/db";
}

# The parts of controlchan the handlers use.  logdie() exits like the one of
# controlchan (POSIX::_exit, so that the child does not run the END blocks of
# the parent); log lines go to a file the parent reads.
sub logmsg {
    my ($msg, $lvl) = @_;
    open(my $fh, '>>', "$tmp/errlog") or die "cannot write errlog: $!\n";
    print $fh (($lvl || 'notice') . ": $msg\n");
    close($fh);
    return;
}
sub logdie { logmsg($_[0], $_[1] || 'err'); POSIX::_exit(1) }
sub logger { return }
sub sendmail { die "sendmail called\n" }

{
    no warnings 'once';
    *INN::Utils::Shlock::lock = sub { return 1 };
    *INN::Utils::Shlock::unlock = sub { return 1 };
}

package FakeHead;
sub new { my ($class, $text) = @_; return bless({ text => $text }, $class) }
sub stringify { return $_[0]{text} }
sub mime_attr { return }
sub mime_type { return 'text/plain' }

package FakeArticle;
sub new {
    my ($class, $head, $body) = @_;
    return bless({ head => FakeHead->new($head), body => $body }, $class);
}
sub head { return $_[0]{head} }
sub stringify_body { return $_[0]{body} }
sub parts { return wantarray ? () : 0 }

package main;

# Load the real ctlinnd() sub from controlchan and the real handlers.
my $controlchan = slurp("$top/control/controlchan.in");
my ($ctlinnd) = $controlchan =~ /^(sub ctlinnd \{\n.*?^\}\n)/ms;
die "cannot find sub ctlinnd in controlchan.in\n" unless $ctlinnd;
## no critic (ProhibitStringyEval)
eval "$ctlinnd; 1" or die "cannot load ctlinnd(): $@";
## use critic
## no critic (RequireBarewordIncludes)
require "$top/control/modules/newgroup.pl";
require "$top/control/modules/rmgroup.pl";
## use critic

my $sender = 'admin@example.invalid';

sub newgroup_article {
    my ($group, $desc) = @_;
    return FakeArticle->new(
        "From: $sender\nControl: newgroup $group\nApproved: $sender\n",
        "Please create $group.\n\nFor your newsgroups file:\n$group\t$desc\n",
    );
}

sub rmgroup_article {
    my ($group) = @_;
    return FakeArticle->new(
        "From: $sender\nControl: rmgroup $group\nApproved: $sender\n",
        "Please remove $group.\n",
    );
}

# Run the given steps in a child process, as controlchan runs one control
# message after the other; returns the exit status of the child.
sub controlchan_run {
    my (@steps) = @_;
    my $pid = fork;
    die "cannot fork: $!\n" unless defined $pid;
    if ($pid == 0) {
        my $ok = eval { $_->() for @steps; 1 };
        logmsg("died: $@", 'crit') unless $ok;
        POSIX::_exit($ok ? 0 : 2);
    }
    waitpid($pid, 0);
    return $?;
}

sub reset_state {
    spew($INN::Config::active, <<'EOF');
fu.keep 0000000010 0000000001 y
fu.old 0000000010 0000000001 y
fu.gone 0000000010 0000000001 y
EOF
    spew($INN::Config::newsgroups, <<'EOF');
fu.keep			Kept group.
fu.old			Old group.
fu.gone			Gone group.
EOF
    unlink("$tmp/ctlinnd.log", "$tmp/errlog", "$tmp/throttled");
    return;
}

# Callers pass the result of a match in scalar context: a failed match in
# list context is an empty list and would shift the arguments.
my $n = 0;
sub check {
    my ($ok, $name, $diag) = @_;
    $n++;
    print(($ok ? 'ok' : 'not ok') . " $n - $name\n");
    if (!$ok && defined $diag) {
        print "# $_\n" for split(/\n/, $diag);
    }
    return;
}

print "1..8\n";

# rmgroup while innd is throttled, then rmgroup once it is not.
reset_state();
my $status = controlchan_run(
    sub {
        spew("$tmp/throttled", '');
        control_rmgroup(['fu.old'], $sender, $sender, '', 'doit', undef, 1,
            rmgroup_article('fu.old'));
    },
    sub {
        unlink("$tmp/throttled");
        control_rmgroup(['fu.gone'], $sender, $sender, '', 'doit', undef, 1,
            rmgroup_article('fu.gone'));
    },
);
my $errlog = slurp("$tmp/errlog");
check($status == 0, 'controlchan goes on after a rejected rmgroup',
    "child exit status $status\n$errlog");
check(scalar(slurp($INN::Config::newsgroups) =~ /^fu\.old\s/m),
    'the rejected rmgroup leaves newsgroups unchanged');
check(slurp("$tmp/ctlinnd.log") =~ /^rmgroup fu\.gone$/m
      && slurp($INN::Config::newsgroups) !~ /^fu\.gone\s/m,
    'the next rmgroup is carried out', "ctlinnd.log:\n"
      . slurp("$tmp/ctlinnd.log"));
check(scalar($errlog =~ /^err: ctlinnd returned status 1\b/m),
    'the log names the exit status of ctlinnd', $errlog);

# newgroup with a name innd refuses, then a valid newgroup.
reset_state();
$status = controlchan_run(
    sub {
        control_newgroup(['alt.fu/slash'], $sender, $sender, '', 'doit',
            undef, 1, newgroup_article('alt.fu/slash', 'Slash group.'),
            [], 'utf-8');
    },
    sub {
        control_newgroup(['fu.new'], $sender, $sender, '', 'doit', undef, 1,
            newgroup_article('fu.new', 'New group.'), [], 'utf-8');
    },
);
$errlog = slurp("$tmp/errlog");
check($status == 0, 'controlchan goes on after a rejected newgroup',
    "child exit status $status\n$errlog");
check(slurp($INN::Config::newsgroups) !~ /^alt\.fu\/slash\s/m,
    'no description for the group innd did not create',
    slurp($INN::Config::newsgroups));
my $created = slurp("$tmp/ctlinnd.log") =~ /^newgroup fu\.new y \Q$sender\E$/m;
check($created,
    'the next newgroup is carried out', "ctlinnd.log:\n"
      . slurp("$tmp/ctlinnd.log"));
my $described
  = slurp($INN::Config::newsgroups) =~ /^fu\.new\s+New group\.$/m;
check($described,
    'the next newgroup adds its description',
    slurp($INN::Config::newsgroups));

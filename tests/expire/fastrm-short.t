#! /bin/sh
#
# Test fastrm with an input line shorter than the directory prefix of the
# lines before it, where that short line ends exactly at the end of the
# QIO buffer (QIO_BUFFERSIZE, 32 KiB, include/inn/qio.h).
#
# process_line compares each new line with the directory of the current
# list.  Indexing line[dlen] for a line shorter than dlen reads past the
# line's terminating nul; at the end of the buffer that read is past the
# end of the allocation.  Only a build with AddressSanitizer reports it;
# the files fastrm removes are the same either way.

fastrm="../../expire/fastrm"
dirs='. expire tests/expire'
for dir in $dirs; do
    if [ -r "$dir/fastrm-short.t" ]; then
        cd $dir
        break
    fi
done
if [ ! -x "$fastrm" ]; then
    echo 'Could not find fastrm' >&2
    exit 1
fi

echo 6

base="$(pwd)/fastrm-short.$$"
rm -rf "$base"
mkdir -p "$base/dd"
: >"$base/dd/f1"
: >"$base/dd/keep"
: >"$base/x"

# The first line sets the directory "dd" (dlen 2).  Filler lines in the
# same directory (names that do not exist) bring the input to exactly
# 32 KiB, and the last line "x" (length 1) ends at the last byte of the
# buffer, so line[2] is the first byte after it.
perl -e '
    my $size = 32 * 1024;
    my $first = "dd/f1\n";
    my $last = "x\n";
    my $out = $first;
    my $room = $size - length($first) - length($last);
    my $i = 0;
    while ($room > 0) {
        my $n = $room >= 209 ? 200 : $room - 4;
        my $name = substr(sprintf("g%04d", $i++) . ("a" x 200), 0, $n);
        $out .= "dd/$name\n";
        $room -= $n + 4;
    }
    print $out, $last;
' >"$base.in"

size=$(wc -c <"$base.in" | tr -cd '0-9')
if [ "$size" = 32768 ] && [ "$(tail -c 2 "$base.in")" = x ]; then
    echo "ok 1 - input is 32768 bytes and ends with the short line"
else
    echo "not ok 1 - input is 32768 bytes and ends with the short line" \
        "(size=$size)"
fi

timeout 10 "$fastrm" "$base" <"$base.in" >"$base.out" 2>"$base.err"
rc=$?

if [ "$rc" = 0 ]; then
    echo "ok 2 - fastrm exits 0"
else
    echo "not ok 2 - fastrm exits 0 (rc=$rc)"
fi
if grep -q 'AddressSanitizer' "$base.err"; then
    echo "not ok 3 - no read past the end of the input buffer"
    grep -E 'ERROR: AddressSanitizer|#[0-9]+ .* in (process_line|main) ' \
        "$base.err" | head -4 | cut -c1-200 | sed 's/^/# /'
else
    echo "ok 3 - no read past the end of the input buffer"
fi
if [ ! -e "$base/dd/f1" ]; then
    echo "ok 4 - dd/f1 removed"
else
    echo "not ok 4 - dd/f1 removed"
fi
if [ ! -e "$base/x" ]; then
    echo "ok 5 - x (the short line) removed"
else
    echo "not ok 5 - x (the short line) removed"
fi
if [ -e "$base/dd/keep" ]; then
    echo "ok 6 - file not listed is kept"
else
    echo "not ok 6 - file not listed is kept"
fi

rm -rf "$base" "$base.in" "$base.out" "$base.err"

#! /bin/sh
#
# Test suite for ninpaths.

# The count starts at 1 and is updated each time ok is printed.  printcount
# takes "ok" or "not ok".
count=1
printcount() {
    echo "$1 $count $2"
    count=$(expr $count + 1)
}

# Print ok if the argument is a number between $before and $after (the time
# before and after ninpaths ran), not ok otherwise.
checktime() {
    case $1 in
    '' | *[!0-9]*)
        echo "  not a number: $1"
        printcount "not ok"
        ;;
    *)
        if [ "$1" -ge "$before" ] && [ "$1" -le "$after" ]; then
            printcount "ok"
        else
            echo "  $1 is not between $before and $after"
            printcount "not ok"
        fi
        ;;
    esac
}

# Print ok if the directory given as first argument contains exactly one
# file, whose name is the second argument followed by a number between
# $before and $after.
checkdumpname() {
    files=$(ls "$1")
    case $files in
    "$2"*)
        case $files in
        *"
"*)
            echo "  more than one file: $files"
            printcount "not ok"
            ;;
        *)
            checktime "${files#"$2"}"
            ;;
        esac
        ;;
    *)
        echo "  unexpected files: $files"
        printcount "not ok"
        ;;
    esac
}

# Run ninpaths with the dump file name given as argument on the input,
# and set $before and $after to the time before and after it ran.
runninpaths() {
    before=$(perl -e 'print time')
    $ninpaths -p -d "$1" <ninpaths-input 2>ninpaths-err
    status=$?
    after=$(perl -e 'print time')
}

# Find the right directory.
ninpaths="../../backends/ninpaths"
dirs='../data data tests/data'
for dir in $dirs; do
    if [ -r "$dir/articles/1" ]; then
        cd $dir
        break
    fi
done
if [ ! -x "$ninpaths" ]; then
    echo "Could not find ninpaths" >&2
    exit 1
fi

# Print out the count of tests.
echo 9

# Two Path header fields to build a dump file from.
rm -rf ninpaths-tmp ninpaths-input ninpaths-err ninpaths-out
mkdir ninpaths-tmp
cat >ninpaths-input <<'END'
Path: news.trigofacile.com!news.ecp.fr!usenet.stanford.edu!not-for-mail
Path: news.trigofacile.com!news.ecp.fr!newsgate.cistron.nl!bleachbot!not-for-mail
END

# %d is replaced with the current time.
runninpaths "ninpaths-tmp/inpaths.%d"
if [ $status = 0 ]; then
    printcount "ok"
else
    printcount "not ok"
fi
checkdumpname ninpaths-tmp "inpaths."
if head -n 1 ninpaths-tmp/inpaths.* | grep '^!!NINP ' >/dev/null; then
    printcount "ok"
else
    printcount "not ok"
fi

# %% is replaced with %, so that %%d is a literal %d.
rm -f ninpaths-tmp/*
runninpaths "ninpaths-tmp/literal-%%d"
if [ "$(ls ninpaths-tmp)" = "literal-%d" ]; then
    printcount "ok"
else
    echo "  $(ls ninpaths-tmp)"
    printcount "not ok"
fi

# %%%d is a % followed by the current time.
rm -f ninpaths-tmp/*
runninpaths "ninpaths-tmp/percent-%%%d"
checkdumpname ninpaths-tmp "percent-%"

# Any other % sequence is kept as is, and is not used as a format.
rm -f ninpaths-tmp/*
runninpaths "ninpaths-tmp/other-%s-%x-%n-%.3f-%.%d"
checkdumpname ninpaths-tmp "other-%s-%x-%n-%.3f-%."

# A name which does not fit is reported, and not written truncated.  Make a
# directory that is long enough so that the file name of the dump would be
# cut in the middle if it were truncated to the 1023 characters allowed.
rm -rf ninpaths-tmp
component=$(printf '%0200d' 0)
deep=ninpaths-tmp/$component/$component/$component/$component
mkdir -p $deep
name=$(printf '%0230d' 0)
runninpaths "$deep/$name.%d"
if grep 'too long' ninpaths-err >/dev/null; then
    printcount "ok"
else
    printcount "not ok"
fi
if [ -z "$(ls $deep)" ]; then
    printcount "ok"
else
    echo "  file created: $(ls $deep)"
    printcount "not ok"
fi

# A dash writes the dump to the standard output.
$ninpaths -p -d - <ninpaths-input >ninpaths-out
if head -n 1 ninpaths-out | grep '^!!NINP ' >/dev/null; then
    printcount "ok"
else
    printcount "not ok"
fi

# All done.  Clean up.
rm -rf ninpaths-tmp ninpaths-input ninpaths-err ninpaths-out

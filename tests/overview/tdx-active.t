#!/bin/sh
# Read-only audit of active water marks.

set -eu
source_dir=${C_TAP_SOURCE:-"$PWD"}
build_dir=${C_TAP_BUILD:-"$PWD"}
tdxutil=$build_dir/../storage/tradindexed/tdx-util
overchan=$build_dir/../backends/overchan

workdir=$(mktemp -d "${TMPDIR:-/tmp}/tdx-active.XXXXXX")
trap 'rm -rf "$workdir"' EXIT
cd "$workdir"
INNCONF=$workdir/inn.conf
INN_TESTSUITE=true
export INNCONF INN_TESTSUITE
cat "$source_dir/data/etc/inn-tdx.conf" > "$INNCONF"
printf '\npathdb: .\n' >> "$INNCONF"
mkdir ov-tmp

echo '1..7'
"$tdxutil" -c -n example.empty -a 4-10
cat > active <<'ACTIVE'
example.empty 0000000002 0000000003 y
example.alias 0000000000 0000000001 =example.empty
ACTIVE
cp active original
if "$tdxutil" -C 2> errors; then
    echo 'not ok 1 - audit returns failure for inconsistent marks'
else
    grep -q 'example.empty: active high 2 low 3; expected high at least 9 and low 10' errors \
        && echo 'ok 1 - audit reports both inconsistent marks' \
        || echo 'not ok 1 - audit reports both inconsistent marks'
fi
if cmp -s original active; then
    echo 'ok 2 - audit leaves active unchanged'
else
    echo 'not ok 2 - audit leaves active unchanged'
fi
cat > active <<'ACTIVE'
example.empty 0000000012 0000000010 y
example.alias 0000000000 0000000001 =example.empty
ACTIVE
if "$tdxutil" -C 2> errors && ! test -s errors; then
    echo 'ok 3 - audit allows a higher active mark and skips aliases'
else
    cat errors >&2
    echo 'not ok 3 - audit allows a higher active mark and skips aliases'
fi

"$tdxutil" -c -n example.test
sed -n '2s/^/@0502000005A4000000010000000000000000@ 1120456320 /p' \
    "$source_dir/data/overview/1-4" > overview-input
"$overchan" overview-input
cat > active <<'ACTIVE'
example.test 0000000000 0000000002 y
ACTIVE
if ! "$tdxutil" -C 2> errors \
    && grep -q 'example.test: active high 0 low 2; expected high at least 1 and low 1' errors; then
    echo 'ok 4 - populated group uses the overview marks'
else
    cat errors >&2
    echo 'not ok 4 - populated group uses the overview marks'
fi

cat > active <<'ACTIVE'
example.test 0000000001 0000000001 y
example.bad 0000000000 0000000001 z
example.alias 0000000000 0000000001 =
invalid
ACTIVE
if ! "$tdxutil" -C 2> errors \
    && grep -q 'active:2: invalid active status' errors \
    && grep -q 'active:3: invalid active status' errors \
    && grep -q 'active:4: invalid active entry' errors; then
    echo 'ok 5 - unknown flags, bare aliases and malformed rows fail'
else
    cat errors >&2
    echo 'not ok 5 - unknown flags, bare aliases and malformed rows fail'
fi

printf '%s\n' 'example.test 0000000001 0000000001 y' > active
if "$tdxutil" -C 2> errors && ! test -s errors; then
    echo 'ok 6 - an overview-only group is outside the check'
else
    cat errors >&2
    echo 'not ok 6 - an overview-only group is outside the check'
fi

printf '%s\n' 'example.absent 0000000000 0000000001 y' >> active
if ! "$tdxutil" -C 2> errors \
    && grep -q 'example.absent: no corresponding overview group' errors; then
    echo 'ok 7 - an active-only group is reported'
else
    cat errors >&2
    echo 'not ok 7 - an active-only group is reported'
fi

#!/bin/sh
# Read-only audit of active water marks.

set -eu
source_dir=${C_TAP_SOURCE:-"$PWD"}
build_dir=${C_TAP_BUILD:-"$PWD"}
tdxutil=$build_dir/../storage/tradindexed/tdx-util

workdir=$(mktemp -d "${TMPDIR:-/tmp}/tdx-active.XXXXXX")
trap 'rm -rf "$workdir"' EXIT
cd "$workdir"
INNCONF=$workdir/inn.conf
INN_TESTSUITE=true
export INNCONF INN_TESTSUITE
cat "$source_dir/data/etc/inn-tdx.conf" > "$INNCONF"
printf '\npathdb: .\n' >> "$INNCONF"
mkdir ov-tmp

echo '1..3'
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

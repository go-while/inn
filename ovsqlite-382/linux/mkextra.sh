#!/bin/bash
# usage: mkextra.sh 'PARAMS_JSON' TOTAL_TIMEOUT_S OUT
d=$(dirname "$0"); params=$1; tmo=$2; out=$3
{
cat <<EOF
#!/bin/bash
# ovsqlite-fsync measurement extra script (generated). Runs as root on the host.
set -u
exec 2>&1
PARAMS='$params'
TMO=$tmo
main() {
EOF
cat <<'EOF'
B=/var/tmp/inn-e2e-build
echo "== ovsqlite-fsync on $(hostname) $(date -u +%FT%TZ)"
. /etc/os-release; echo "$PRETTY_NAME; kernel $(uname -r); cpus $(nproc)"
free -m | head -2
findmnt -n -o SOURCE,FSTYPE,OPTIONS -T /var/tmp
command -v strace >/dev/null || DEBIAN_FRONTEND=noninteractive timeout 300 apt-get -qq install -y strace >/dev/null 2>&1
strace -V | head -1
dpkg-query -W -f '${Package} ${Version}\n' libsqlite3-0 2>/dev/null
python3 -V
test -x /opt/inn-e2e/bin/ovsqlite-server || { echo "no installed ovsqlite-server"; exit 2; }
pkill -9 -u news innd 2>/dev/null; pkill -9 -u news ovsqlite-server 2>/dev/null; pkill -9 -u news nnrpd 2>/dev/null
cp -p /opt/inn-e2e/bin/ovsqlite-server /opt/inn-e2e/bin/ovsqlite-server.up
echo "== build prototype ovsqlite-server (47472af18 diff applied to the upstream build tree)"
base64 -d > /var/tmp/ovf-proto.patch.gz <<'B64'
EOF
gzip -9c "$d/proto.patch" | base64
cat <<'EOF'
B64
gunzip -f /var/tmp/ovf-proto.patch.gz
( cd $B/src && patch -p1 < /var/tmp/ovf-proto.patch ) || { echo "patch failed"; exit 2; }
chown -R news "$B/src/storage"
su -s /bin/sh news -c "cd $B/src/storage && timeout 600 make >/var/tmp/ovf-proto-make.log 2>&1 && timeout 300 make install >>/var/tmp/ovf-proto-make.log 2>&1"
rc=$?
grep -E 'warning:|error:' /var/tmp/ovf-proto-make.log | head -10
tail -3 /var/tmp/ovf-proto-make.log
test $rc = 0 || { echo "proto build failed rc=$rc"; exit 2; }
cp -p /opt/inn-e2e/bin/ovsqlite-server /opt/inn-e2e/bin/ovsqlite-server.proto
cp -p /opt/inn-e2e/bin/ovsqlite-server.up /opt/inn-e2e/bin/ovsqlite-server
sha256sum /opt/inn-e2e/bin/ovsqlite-server.up /opt/inn-e2e/bin/ovsqlite-server.proto
cmp -s /opt/inn-e2e/bin/ovsqlite-server.up /opt/inn-e2e/bin/ovsqlite-server.proto && { echo "binaries identical?!"; exit 2; }
grep -c transaction_idle_limit $B/src/storage/ovsqlite/ovsqlite-server.c
base64 -d <<'B64' | gunzip > /var/tmp/ovf.py
EOF
gzip -9c "$d/ovf.py" | base64
cat <<'EOF'
B64
rm -rf /var/tmp/ovf; mkdir -p /var/tmp/ovf
echo "== run ovf.py $PARAMS (limit ${TMO}s)"
timeout -k 30 "$TMO" python3 -u /var/tmp/ovf.py "$PARAMS" 2>&1 | head -c 2000000
echo "ovf exit=${PIPESTATUS[0]}"
pkill -9 -u news innd 2>/dev/null; pkill -9 -u news ovsqlite-server 2>/dev/null; pkill -9 -u news nnrpd 2>/dev/null
pkill -9 strace 2>/dev/null
cp /var/tmp/ovf/results.jsonl /var/tmp/ovf-results-$(date +%s).jsonl 2>/dev/null
echo "== results.jsonl"
cat /var/tmp/ovf/results.jsonl 2>/dev/null | head -c 200000
df -h /var/tmp | tail -1
}
main < /dev/null
EOF
} > "$out"
chmod +x "$out"
bash -n "$out" && echo "$out"

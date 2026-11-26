#! /bin/sh
#
# Test suite for auth_krb5 against throwaway MIT Kerberos KDCs.
#
# Two KDCs for the same realm are started on 127.0.0.1 with scratch databases
# on non-default ports: the real KDC, and a second one with its own database
# that stands in for a spoofed KDC.  The tests check which logins auth_krb5
# accepts depending on the default keytab (KRB5_KTNAME) and on the Kerberos
# verify_ap_req_nofail setting.
#
# Needs auth_krb5 to be built (configure --with-krb5) and the MIT KDC tools
# krb5kdc, kdb5_util and kadmin.local; skips otherwise.  Uses only static test
# passwords that live in this file.

. "$C_TAP_SOURCE/tap/libtap.sh"

auth="$C_TAP_BUILD/../authprogs/auth_krb5"
test -x "$auth" || skip_all "auth_krb5 not built"
PATH="$PATH:/usr/sbin:/sbin"
for prog in krb5kdc kdb5_util kadmin.local; do
    command -v "$prog" >/dev/null 2>&1 || skip_all "$prog not found"
done
timeout=
command -v timeout >/dev/null 2>&1 && timeout="timeout 30"

realm=INNTEST.EXAMPLE
pass='inntest-user-pass-1'
evil='inntest-attacker-pass-2'
port_real=$(expr 21088 + $$ % 4000 \* 2)
port_evil=$(expr $port_real + 1)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/inn-krb5t.XXXXXX") || bail "mktemp failed"

cleanup() {
    for kdc in real evil; do
        if test -s "$tmp/$kdc/kdc.pid"; then
            kill "$(cat "$tmp/$kdc/kdc.pid")" 2>/dev/null
        fi
    done
    rm -rf "$tmp"
}
trap cleanup 0

# Write a client krb5.conf.  Takes the file, the KDC port and the value of
# verify_ap_req_nofail.
write_client_conf() {
    cat >"$1" <<EOF
[libdefaults]
    default_realm = $realm
    dns_lookup_kdc = false
    dns_lookup_realm = false
    dns_canonicalize_hostname = false
    rdns = false
    verify_ap_req_nofail = $3
[realms]
    $realm = {
        kdc = 127.0.0.1:$2
    }
EOF
}

# Set up and start one KDC.  Takes the name (real or evil), the port and the
# password of the user alice in that KDC's database.
start_kdc() {
    d="$tmp/$1"
    mkdir -p "$d"
    write_client_conf "$d/krb5.conf" "$2" false
    cat >"$d/kdc.conf" <<EOF
[kdcdefaults]
    kdc_ports = $2
    kdc_tcp_ports = $2
    kdc_listen = 127.0.0.1:$2
    kdc_tcp_listen = 127.0.0.1:$2
[realms]
    $realm = {
        database_name = $d/principal
        key_stash_file = $d/stash
        kdc_ports = $2
        kdc_tcp_ports = $2
        kdc_listen = 127.0.0.1:$2
        kdc_tcp_listen = 127.0.0.1:$2
    }
[logging]
    kdc = FILE:$d/kdc.log
    admin_server = FILE:$d/kadmin.log
    default = FILE:$d/krb5.log
EOF
    KRB5_CONFIG="$d/krb5.conf"
    KRB5_KDC_PROFILE="$d/kdc.conf"
    export KRB5_CONFIG KRB5_KDC_PROFILE
    kdb5_util -r $realm create -s -P inntest-master-pass >"$d/setup.log" 2>&1 \
        || bail "kdb5_util create failed for $1"
    kadm() {
        kadmin.local -r $realm -q "$1" >>"$d/setup.log" 2>&1
    }
    kadm "addprinc -pw $3 alice"
    kadm "addprinc -randkey host/inntest.example"
    kadm "addprinc -randkey nntp/inntest.example"
    $timeout krb5kdc -r $realm -P "$d/kdc.pid" >>"$d/setup.log" 2>&1 \
        || bail "krb5kdc did not start for $1"
    unset KRB5_CONFIG KRB5_KDC_PROFILE
}

# Make the keytabs from the real KDC.  stale.keytab gets the host key that
# the next ktadd replaces; host.keytab the current one.
make_keytabs() {
    d="$tmp/real"
    KRB5_CONFIG="$d/krb5.conf"
    KRB5_KDC_PROFILE="$d/kdc.conf"
    export KRB5_CONFIG KRB5_KDC_PROFILE
    for spec in stale:host host:host nntp:nntp; do
        kt=${spec%%:*}
        svc=${spec#*:}
        kadmin.local -r $realm \
            -q "ktadd -k $tmp/$kt.keytab $svc/inntest.example" \
            >>"$d/setup.log" 2>&1
        test -s "$tmp/$kt.keytab" || bail "ktadd failed for $kt.keytab"
    done
    cp "$tmp/host.keytab" "$tmp/unreadable.keytab"
    chmod 000 "$tmp/unreadable.keytab"
    unset KRB5_CONFIG KRB5_KDC_PROFILE
}

# Run auth_krb5 the way nnrpd does.  Takes the KDC port, the value of
# verify_ap_req_nofail, the keytab and the password.  Sets status and output.
run_auth() {
    write_client_conf "$tmp/client.conf" "$1" "$2"
    printf 'ClientAuthname: alice\r\nClientPassword: %s\r\n.\r\n' "$4" \
        | KRB5_CONFIG="$tmp/client.conf" KRB5_KTNAME="FILE:$3" \
            KRB5RCACHEDIR="$tmp" KRB5CCNAME="MEMORY:inntest" \
            $timeout "$auth" >"$tmp/auth.out" 2>&1
    status=$?
    output=$(tr -d '\r' <"$tmp/auth.out" | tr '\n' ' ' | sed 's/ $//')
}

# Check one login.  Takes the description, accept or reject, and the
# arguments of run_auth.
check() {
    desc=$1
    want=$2
    shift 2
    run_auth "$@"
    case "$want" in
    accept)
        test "$status" = 0 && test x"$output" = x"User:alice"
        ;;
    reject)
        test "$status" != 0 && ! echo "$output" | grep -q '^User:'
        ;;
    esac
    result=$?
    ok "$desc" test "$result" = 0
    test "$result" = 0 || diag "status $status, output: $output"
}

start_kdc real $port_real "$pass"
start_kdc evil $port_evil "$evil"
make_keytabs

# Wait until the real KDC answers.
tries=0
while :; do
    run_auth $port_real false "$tmp/none.keytab" "$pass"
    test "$status" = 0 && break
    tries=$(expr $tries + 1)
    if test $tries -ge 20; then
        diag "$output"
        bail "real KDC does not answer on port $port_real"
    fi
    sleep 1
done

plan 12
trap 'cleanup; finish' 0

none="$tmp/none.keytab"
host="$tmp/host.keytab"

check "real KDC, right password, no keytab" \
    accept $port_real false "$none" "$pass"
check "real KDC, wrong password, no keytab" \
    reject $port_real false "$none" "$evil"
check "real KDC, wrong password, host keytab" \
    reject $port_real false "$host" "$evil"
check "real KDC, right password, host keytab" \
    accept $port_real false "$host" "$pass"
check "real KDC, right password, host keytab, verify_ap_req_nofail" \
    accept $port_real true "$host" "$pass"
check "real KDC, right password, no keytab, verify_ap_req_nofail" \
    reject $port_real true "$none" "$pass"
check "real KDC, right password, keytab with only an nntp key" \
    accept $port_real false "$tmp/nntp.keytab" "$pass"
if test "$(id -u)" = 0; then
    skip "unreadable keytab test needs a non-root user"
else
    check "real KDC, right password, host keytab not readable" \
        accept $port_real false "$tmp/unreadable.keytab" "$pass"
fi
check "real KDC, right password, stale host keytab" \
    reject $port_real false "$tmp/stale.keytab" "$pass"
check "spoofed KDC, attacker password, host keytab" \
    reject $port_evil false "$host" "$evil"
check "spoofed KDC, attacker password, host keytab, verify_ap_req_nofail" \
    reject $port_evil true "$host" "$evil"
check "spoofed KDC, attacker password, no keytab" \
    accept $port_evil false "$none" "$evil"

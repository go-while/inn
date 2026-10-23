#!/bin/sh
##  Entry point of the INN image: prepares a site in /usr/local/news on the
##  first start (or on empty volumes) and runs innd in the foreground.
##
##  Environment (read at every start; files you mount in etc win):
##    INN_PATHHOST  pathhost and, after its first dot, domain (default: the
##                  container's host name, with ".invalid" when it has no dot)
##    INN_GROUPS    newsgroups to create, separated by commas or spaces
##    INN_READERS   hosts allowed to read, post and use NEWNEWS (default "*")
##    INN_PEERS     hosts allowed to feed with IHAVE and streaming (default none)

set -eu

P=/usr/local/news
ETC=$P/etc

# set_conf KEY VALUE: set a parameter in inn.conf.
set_conf() {
    if grep -q "^$1:" "$ETC/inn.conf"; then
        sed -i "s|^$1:.*|$1: $2|" "$ETC/inn.conf"
    else
        printf '%s: %s\n' "$1" "$2" >>"$ETC/inn.conf"
    fi
}

as_news() {
    su news -s /bin/sh -c "$*"
}

host=${INN_PATHHOST:-$(hostname)}
case $host in
*.*) ;;
*) host=$host.invalid ;;
esac
set_conf pathhost "$host"
set_conf domain "${host#*.}"
set_conf port 119
grep -q '^ovmethod: *[a-z]' "$ETC/inn.conf" || set_conf ovmethod tradindexed
grep -q '^hismethod: *[a-z]' "$ETC/inn.conf" || set_conf hismethod hisv6

if [ ! -e "$ETC/.docker-generated" ]; then
    printf 'auth "all" {\n    hosts: "%s"\n    default: "<all>"\n}\naccess "all" {\n    users: "<all>"\n    newsgroups: "*"\n    access: RPAN\n}\n' \
        "${INN_READERS:-*}" >"$ETC/readers.conf"
    : >"$ETC/incoming.conf"
    for peer in $(echo "${INN_PEERS:-}" | tr ',' ' '); do
        printf 'peer "%s" {\n    hostname: "%s"\n    streaming: true\n}\n' "$peer" "$peer" >>"$ETC/incoming.conf"
    done
    printf 'ME:*::\n' >"$ETC/newsfeeds"
    touch "$ETC/.docker-generated"
fi

# Empty volumes: the spool directories and the database.
for d in spool/articles spool/overview spool/incoming/bad spool/outgoing spool/archive \
    spool/innfeed log run tmp db; do
    mkdir -p "$P/$d"
done
chown -R news:news "$P/spool" "$P/log" "$P/run" "$P/tmp" "$P/db"
if [ ! -s "$P/db/active" ]; then
    printf 'control 0000000000 0000000001 n\ncontrol.cancel 0000000000 0000000001 n\njunk 0000000000 0000000001 n\n' >"$P/db/active"
    : >"$P/db/newsgroups"
    : >"$P/db/active.times"
    chown news:news "$P/db/active" "$P/db/newsgroups" "$P/db/active.times"
fi
for g in $(echo "${INN_GROUPS:-}" | tr ',' ' '); do
    if ! grep -q "^$g " "$P/db/active"; then
        printf '%s 0000000000 0000000001 y\n' "$g" >>"$P/db/active"
        printf '%s\t%s\n' "$g" "Group $g." >>"$P/db/newsgroups"
    fi
done
if [ ! -e "$P/db/history" ]; then
    as_news "cd $P/db && touch history && $P/bin/makedbz -i -o"
fi

# A container start means innd is not running: a pid file is left from the
# last run.
rm -f "$P/run/innd.pid"

exec "$P/bin/innd" -f

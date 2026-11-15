#!/usr/bin/env python3
# ovsqlite-server sync/commit cost at low feed rates: upstream vs prototype vs transtimelimit 1.
# Runs as root on a test host; INN programs run as news. Every wait is bounded.
import email.utils
import json
import os
import pwd
import re
import shutil
import signal
import socket
import statistics
import subprocess
import sys
import time

PREFIX = '/opt/inn-e2e'
BIN = PREFIX + '/bin'
ROOT = '/var/tmp/ovf'
SITE = ROOT + '/site'
PORT = 1119
news = pwd.getpwnam('news')
UID, GID = news.pw_uid, news.pw_gid
ENV = {'PATH': '/usr/local/bin:/usr/bin:/bin', 'INNCONF': SITE + '/etc/inn.conf',
       'HOME': SITE, 'LC_ALL': 'C'}
CONFIGS = {
    'up': ('ovsqlite-server.up', ''),
    'proto': ('ovsqlite-server.proto', ''),
    'ttl1': ('ovsqlite-server.up', 'transtimelimit: 1\n'),
}
GROUPS = ['ovf.a', 'ovf.b', 'ovf.c']
SYNCS = {'fsync', 'fdatasync', 'msync', 'sync_file_range', 'sync', 'syncfs'}
WRITES = {'pwrite64', 'write', 'pwritev', 'pwritev2', 'writev'}

P = dict(rates=[0.5, 1, 2, 3, 10], reps=3, warm=20, win=120, win_u=90, vis=5,
         untraced_rates=[1, 10], order=['up', 'proto', 'ttl1'])
if len(sys.argv) > 1:
    P.update(json.loads(sys.argv[1]))


def log(*a):
    print(time.strftime('%H:%M:%S'), *a, flush=True)


def run_news(args, timeout=60):
    try:
        p = subprocess.run(args, cwd=SITE, env=ENV, user=UID, group=GID, extra_groups=[],
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
        return p.returncode, p.stdout[-2000:].decode(errors='replace')
    except subprocess.TimeoutExpired:
        return -1, 'timeout'


def make_site(conf):
    if os.path.exists(SITE):
        shutil.rmtree(SITE)
    for d in ['etc', 'db', 'log', 'run', 'tmp', 'http', 'spool/articles', 'spool/overview',
              'spool/incoming/bad', 'spool/outgoing', 'spool/archive', 'spool/innfeed']:
        os.makedirs(os.path.join(SITE, d))
    for f in os.listdir(PREFIX + '/etc'):
        src = os.path.join(PREFIX, 'etc', f)
        if os.path.isfile(src):
            shutil.copy(src, os.path.join(SITE, 'etc', f))
    innconf = {
        'mta': '"/bin/true %s"', 'ovmethod': 'ovsqlite', 'hismethod': 'hisv6',
        'pathhost': 'ovf.test', 'domain': 'ovf.test', 'pathnews': SITE,
        'pathbin': BIN, 'pathtmp': SITE + '/tmp', 'port': str(PORT),
        'bindaddress': '127.0.0.1', 'server': '127.0.0.1', 'nnrpdloadlimit': '0',
    }

    def w(rel, text):
        with open(os.path.join(SITE, rel), 'w') as fh:
            fh.write(text)
    w('etc/inn.conf', ''.join('%s: %s\n' % kv for kv in sorted(innconf.items())))
    w('etc/readers.conf', 'auth "local" {\n    hosts: "127.0.0.1, ::1, localhost"\n'
      '    default: "<local>"\n}\naccess "local" {\n    users: "<local>"\n'
      '    newsgroups: "*"\n    access: RPAN\n}\n')
    w('etc/incoming.conf', 'peer ovf {\n    hostname: "127.0.0.2"\n    streaming: true\n}\n')
    w('etc/newsfeeds', 'ME:*::\n')
    w('etc/storage.conf', 'method tradspool {\n    newsgroups: *\n    class: 0\n}\n')
    w('etc/ovsqlite.conf', conf)
    act = ['control 0000000000 0000000001 n', 'control.cancel 0000000000 0000000001 n',
           'junk 0000000000 0000000001 n'] + ['%s 0000000000 0000000001 y' % g for g in GROUPS]
    w('db/active', '\n'.join(act) + '\n')
    w('db/active.times', '')
    w('db/newsgroups', ''.join('%s\tGroup %s.\n' % (g, g) for g in
                               ['control', 'control.cancel', 'junk'] + GROUPS))
    w('db/history', '')
    for dp, dns, fns in os.walk(SITE):
        for n in dns + fns:
            os.chown(os.path.join(dp, n), UID, GID)
    os.chown(SITE, UID, GID)
    rc, out = run_news([BIN + '/makedbz', '-i', '-o'])
    if rc != 0:
        raise RuntimeError('makedbz: ' + out)


def wait_for(fn, limit, what):
    end = time.monotonic() + limit
    while time.monotonic() < end:
        try:
            if fn():
                return
        except OSError:
            pass
        time.sleep(0.1)
    raise RuntimeError('timeout waiting for ' + what)


class Inn:
    def __init__(self, cfg):
        self.cfg = cfg
        self.srv = self.innd = self.srvout = self.inndout = None
        self.pid = 0

    def start(self):
        server, conf = CONFIGS[self.cfg]
        make_site(conf)
        self.srvout = open(SITE + '/log/ovsqlite-server.out', 'wb')
        self.srv = subprocess.Popen([BIN + '/' + server, '-d'], cwd=SITE, env=ENV, user=UID,
                                    group=GID, extra_groups=[], stdin=subprocess.DEVNULL,
                                    stdout=self.srvout, stderr=subprocess.STDOUT)
        sock = SITE + '/run/ovsqlite.sock'

        def sock_up():
            s = socket.socket(socket.AF_UNIX)
            try:
                s.connect(sock)
                return True
            finally:
                s.close()
        wait_for(sock_up, 30, 'ovsqlite-server socket')
        self.inndout = open(SITE + '/log/innd.out', 'wb')
        self.innd = subprocess.Popen([BIN + '/innd', '-f'], cwd=SITE, env=ENV, user=UID,
                                     group=GID, extra_groups=[], stdin=subprocess.DEVNULL,
                                     stdout=self.inndout, stderr=subprocess.STDOUT)

        def port_up():
            s = socket.create_connection(('127.0.0.1', PORT), timeout=2,
                                         source_address=('127.0.0.2', 0))
            s.close()
            return True
        wait_for(port_up, 30, 'innd port')
        self.pid = self.srv.pid

    def stop(self):
        if self.innd:
            rc, out = run_news([BIN + '/ctlinnd', '-t', '10', 'shutdown', 'ovf done'],
                               timeout=30)
            try:
                self.innd.wait(20)
            except subprocess.TimeoutExpired:
                log('innd did not stop, killing', out)
                self.innd.kill()
                self.innd.wait(10)
        if self.srv:
            self.srv.send_signal(signal.SIGTERM)
            try:
                self.srv.wait(30)
            except subprocess.TimeoutExpired:
                log('ovsqlite-server did not stop, killing')
                self.srv.kill()
                self.srv.wait(10)
        for f in (self.srvout, self.inndout):
            if f:
                f.close()
        subprocess.run(['pkill', '-9', '-u', 'news', 'nnrpd'], timeout=10)

    def tail_logs(self):
        for f in ['log/ovsqlite-server.out', 'log/innd.out', 'log/news.err', 'log/errlog']:
            p = os.path.join(SITE, f)
            if os.path.exists(p) and os.path.getsize(p):
                with open(p, 'rb') as fh:
                    fh.seek(max(0, os.path.getsize(p) - 1500))
                    log('--', f, fh.read().decode(errors='replace'))


class Conn:
    def __init__(self, src):
        self.s = socket.create_connection(('127.0.0.1', PORT), timeout=30,
                                          source_address=(src, 0))
        self.f = self.s.makefile('rb')
        self.greeting = self.line()

    def line(self):
        ln = self.f.readline(4096)
        if not ln:
            raise RuntimeError('connection closed')
        return ln.decode(errors='replace').rstrip('\r\n')

    def cmd(self, text):
        self.s.sendall(text.encode() + b'\r\n')
        return self.line()

    def close(self):
        try:
            self.cmd('QUIT')
        except Exception:
            pass
        self.s.close()


BODY = ''.join('line %03d %s\r\n' % (k, 'x' * 60) for k in range(40))


def article(tag, i, group=None):
    g = group or GROUPS[i % len(GROUPS)]
    mid = '<ovf-%s-%d@ovf.test>' % (tag, i)
    hdr = ['Path: ovf.peer!not-for-mail', 'From: ovf <ovf@ovf.test>', 'Newsgroups: ' + g,
           'Subject: ovf %s %d' % (tag, i), 'Message-ID: ' + mid,
           'Date: ' + email.utils.formatdate(usegmt=True), 'Organization: ovf measurement']
    return mid, ('\r\n'.join(hdr) + '\r\n\r\n' + BODY + '.\r\n').encode()


def ihave(c, tag, i, group=None):
    mid, art = article(tag, i, group)
    r = c.cmd('IHAVE ' + mid)
    if not r.startswith('335'):
        return r
    c.s.sendall(art)
    return c.line()


def snap(pid):
    with open('/proc/%d/stat' % pid) as fh:
        f = fh.read().rsplit(')', 1)[1].split()
    tck = os.sysconf('SC_CLK_TCK')
    d = {'cpu_ms': (int(f[11]) + int(f[12])) * 1000.0 / tck}
    with open('/proc/%d/schedstat' % pid) as fh:
        d['sched_ms'] = int(fh.read().split()[0]) / 1e6
    with open('/proc/%d/io' % pid) as fh:
        for ln in fh:
            k, v = ln.split(':')
            d[k.strip()] = int(v)
    for suf in ['', '-wal']:
        p = SITE + '/spool/overview/ovsqlite.db' + suf
        d['size' + suf] = os.path.getsize(p) if os.path.exists(p) else 0
    return d


LINE = re.compile(r'^(?:\[pid\s+\d+\]\s+|\d+\s+)?(\w+)\((\d+)<([^>]*)>(.*)\)\s+=\s+(-?\d+)'
                  r'(?:[^<]*<([\d.]+)>)?\s*$')


def parse_trace(path, sample=0):
    r = {'sync': {}, 'sync_s': 0.0, 'wal_frames': 0, 'commits': 0, 'wal_hdr': 0,
         'wal_bytes': 0, 'db_writes': 0, 'db_bytes': 0, 'wal_trunc': 0, 'wal_max': 0,
         'other_writes': 0, 'unparsed': 0}
    raw = []
    with open(path, errors='replace') as fh:
        for ln in fh:
            m = LINE.match(ln)
            if not m:
                if 'attached' not in ln and 'detached' not in ln:
                    r['unparsed'] += 1
                continue
            sc, fd, fpath, rest, ret, dur = m.groups()
            ret = int(ret)
            if '\\x' in fpath:
                try:
                    fpath = bytes.fromhex(fpath.replace('\\x', '')).decode(errors='replace')
                except ValueError:
                    pass
            if r.setdefault('_dbg', 0) < 3 and sample:
                r['_dbg'] += 1
                raw.append(ln.rstrip()[:200])
            base = os.path.basename(fpath)
            if sc in SYNCS:
                k = '%s(%s)' % (sc, base)
                r['sync'][k] = r['sync'].get(k, 0) + 1
                r['sync_s'] += float(dur or 0)
                if len(raw) < sample:
                    raw.append(ln.rstrip())
            elif sc in WRITES:
                if base.endswith('.db-wal'):
                    r['wal_bytes'] += max(ret, 0)
                    a = rest.split(', ')
                    if sc == 'pwrite64' and len(a) >= 3:
                        try:
                            r['wal_max'] = max(r['wal_max'], int(a[-1]) + max(ret, 0))
                        except ValueError:
                            pass
                    if ret == 24:
                        r['wal_frames'] += 1
                        bs = re.findall(r'\\x([0-9a-f]{2})', rest[:60])[:8]
                        if len(bs) == 8 and any(int(b, 16) for b in bs[4:8]):
                            r['commits'] += 1
                    elif ret == 32:
                        r['wal_hdr'] += 1
                elif base.endswith('.db'):
                    r['db_writes'] += 1
                    r['db_bytes'] += max(ret, 0)
                else:
                    r['other_writes'] += 1
            elif sc == 'ftruncate' and base.endswith('.db-wal'):
                r['wal_trunc'] += 1
                if len(raw) < sample:
                    raw.append(ln.rstrip())
    r.pop('_dbg', None)
    r['sync_n'] = sum(r['sync'].values())
    return r, raw


def tracer_attached(pid):
    with open('/proc/%d/status' % pid) as fh:
        for ln in fh:
            if ln.startswith('TracerPid:'):
                return int(ln.split()[1]) != 0
    return False


def run_rate(cfg, rate, rep, traced=True, sample=0):
    tag = '%s-%s-%d-%s' % (cfg, rate, rep, 't' if traced else 'u')
    inn = Inn(cfg)
    res = {'cfg': cfg, 'rate': rate, 'rep': rep, 'traced': traced}
    trace = ROOT + '/trace-' + tag
    st = None
    win = P['win'] if traced else P['win_u']
    try:
        inn.start()
        c = Conn('127.0.0.2')
        t0 = time.monotonic()
        i = 0
        acc = rej = 0
        started = False
        while True:
            now = time.monotonic()
            if not started and now >= t0 + P['warm']:
                if traced:
                    st = subprocess.Popen(
                        ['strace', '-f', '-p', str(inn.pid), '-y', '-T', '-s', '8', '-xx',
                         '-e', 'trace=' + ','.join(sorted(SYNCS | WRITES)) + ',ftruncate',
                         '-o', trace], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                        stderr=subprocess.DEVNULL)
                    wait_for(lambda: tracer_attached(inn.pid), 10, 'strace attach')
                s0 = snap(inn.pid)
                tw0 = time.monotonic()
                started = True
                continue
            if started and now >= tw0 + win:
                s1 = snap(inn.pid)
                tw1 = time.monotonic()
                break
            next_art = t0 + i / rate
            next_evt = (t0 + P['warm']) if not started else (tw0 + win)
            if next_art <= next_evt:
                if next_art > now:
                    time.sleep(next_art - now)
                r = ihave(c, tag, i)
                if started:
                    if r.startswith('235'):
                        acc += 1
                    else:
                        rej += 1
                        if rej <= 3:
                            log('reply', r)
                i += 1
            else:
                time.sleep(max(0, next_evt - now))
        if st:
            st.send_signal(signal.SIGINT)
            st.wait(20)
        c.close()
        res.update(win_s=round(tw1 - tw0, 2), accepted=acc, rejected=rej)
        for k in ['cpu_ms', 'sched_ms', 'write_bytes', 'wchar', 'syscw', 'cancelled_write_bytes']:
            res[k] = round(s1[k] - s0[k], 2)
        res['wal_size_end'] = s1['size-wal']
        res['db_size_end'] = s1['size']
        if traced:
            tr, raw = parse_trace(trace, sample)
            res.update(tr)
            for ln in raw:
                log('  sample', ln)
            os.unlink(trace)
    except Exception as e:
        log('RUN ERROR', tag, repr(e))
        inn.tail_logs()
        res['error'] = repr(e)
        if st and st.poll() is None:
            st.kill()
    finally:
        inn.stop()
    log('RESULT', json.dumps(res, sort_keys=True))
    with open(ROOT + '/results.jsonl', 'a') as fh:
        fh.write(json.dumps(res, sort_keys=True) + '\n')
    return res


def group_count(r, g):
    ln = r.cmd('GROUP ' + g)
    if not ln.startswith('211'):
        raise RuntimeError('GROUP: ' + ln)
    return int(ln.split()[1])


def visibility(cfg, n):
    inn = Inn(cfg)
    out = []
    try:
        inn.start()
        c = Conn('127.0.0.2')
        rd = Conn('127.0.0.1')
        if not rd.greeting.startswith('20'):
            raise RuntimeError('reader greeting ' + rd.greeting)
        # warm-up: one article, wait until visible (creates the group's rows)
        ihave(c, 'vis' + cfg, 0, 'ovf.a')
        wait_for(lambda: group_count(rd, 'ovf.a') >= 1, 20, 'first article visible')
        for k in range(1, n + 1):
            time.sleep(2)
            before = group_count(rd, 'ovf.a')
            r = ihave(c, 'vis' + cfg, k, 'ovf.a')
            tacc = time.monotonic()
            if not r.startswith('235'):
                raise RuntimeError('IHAVE ' + r)
            seen = None
            polls = 0
            while time.monotonic() - tacc < 15:
                polls += 1
                if group_count(rd, 'ovf.a') > before:
                    seen = time.monotonic() - tacc
                    break
                time.sleep(0.02)
            out.append(round(seen, 3) if seen is not None else None)
            log('VIS', cfg, k, out[-1], 'polls', polls)
        rd.close()
        c.close()
    except Exception as e:
        log('VIS ERROR', cfg, repr(e))
        inn.tail_logs()
    finally:
        inn.stop()
    log('VISRESULT', json.dumps({'cfg': cfg, 'samples': out}))
    return out


def med(xs):
    xs = [x for x in xs if x is not None]
    if not xs:
        return '-'
    m = statistics.median(xs)
    f = (lambda v: '%.0f' % v) if max(abs(x) for x in xs) >= 100 else (lambda v: '%.3g' % v)
    return '%s (%s-%s)' % (f(m), f(min(xs)), f(max(xs)))


def summary(results):
    keys = ['accepted', 'commits', 'wal_frames', 'sync_n', 'wal_trunc', 'wal_hdr', 'sync_s',
            'cpu_ms', 'sched_ms', 'write_bytes', 'wal_bytes', 'db_bytes', 'wal_max']
    print('\n## SUMMARY traced runs: median (min-max)')
    print('| rate | cfg | n | ' + ' | '.join(keys) + ' |')
    print('|' + '---|' * (len(keys) + 3))
    for rate in P['rates']:
        for cfg in P['order']:
            rs = [r for r in results if r['cfg'] == cfg and r['rate'] == rate and r['traced']
                  and 'error' not in r]
            if rs:
                print('| %s | %s | %d | ' % (rate, cfg, len(rs)) +
                      ' | '.join(med([r.get(k) for r in rs]) for k in keys) + ' |')
    print('\n## sync calls by file (all traced runs, summed per rate x cfg)')
    for rate in P['rates']:
        for cfg in P['order']:
            tot = {}
            for r in results:
                if r['cfg'] == cfg and r['rate'] == rate and r['traced'] and 'sync' in r:
                    for k, v in r['sync'].items():
                        tot[k] = tot.get(k, 0) + v
            print(rate, cfg, json.dumps(tot, sort_keys=True))
    print('\n## untraced vs traced (cpu_ms, sched_ms, write_bytes)')
    for rate in P['untraced_rates']:
        for cfg in P['order']:
            for tr in (True, False):
                rs = [r for r in results if r['cfg'] == cfg and r['rate'] == rate and
                      r['traced'] == tr and 'error' not in r]
                if rs:
                    print(rate, cfg, 'traced' if tr else 'untraced', len(rs),
                          med([r['cpu_ms'] for r in rs]), med([r['sched_ms'] for r in rs]),
                          med([r['write_bytes'] for r in rs]))
    sys.stdout.flush()


def main():
    os.makedirs(ROOT, exist_ok=True)
    log('params', json.dumps(P))
    vis = {}
    for cfg in P['order']:
        vis[cfg] = visibility(cfg, P['vis'])
    results = []
    order = list(P['order'])
    for rep in range(1, P['reps'] + 1):
        for rate in P['rates']:
            for cfg in order:
                results.append(run_rate(cfg, rate, rep, True,
                                        sample=40 if rep == 1 and rate in (3, 10) else 0))
            if rate in P['untraced_rates']:
                for cfg in order:
                    results.append(run_rate(cfg, rate, rep, False))
            order = order[1:] + order[:1]
    print('\n## VISIBILITY (s after 235)')
    for cfg in P['order']:
        print(cfg, vis[cfg], 'median', med(vis[cfg]))
    summary(results)


if __name__ == '__main__':
    main()

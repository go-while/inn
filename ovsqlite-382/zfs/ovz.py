#!/usr/local/bin/python3.12
# ovsqlite on ZFS: bytes written per article at low feed rates, today vs transtimelimit 1 vs
# the idle-commit prototype, for several recordsize / SQLite page size settings (#382).
# FreeBSD, runs as root; INN runs as news. Overview on a fresh ZFS dataset per run, seeded from
# a prebuilt database; articles go to the trash storage method (no spool I/O).
import email.utils
import json
import os
import pwd
import random
import re
import shutil
import signal
import socket
import subprocess
import sys
import threading
import time

PREFIX = '/opt/inn-ovz'
BIN = PREFIX + '/bin'
ROOT = '/var/tmp/ovz'
SITE = ROOT + '/site'
POOL = 'ovz'
DS = POOL + '/run'
DEV = 'da1'
PORT = 1119
news = pwd.getpwnam('news')
UID, GID = news.pw_uid, news.pw_gid
ENV = {'PATH': '/usr/local/bin:/usr/bin:/bin:/sbin:/usr/sbin', 'INNCONF': SITE + '/etc/inn.conf',
       'HOME': SITE, 'LC_ALL': 'C'}
CONFIGS = {
    'up': ('ovsqlite-server.up', ''),
    'proto': ('ovsqlite-server.proto', ''),
    'ttl1': ('ovsqlite-server.up', 'transtimelimit: 1\n'),
}
NGROUPS = 200
GROUPS = ['ovz.g%03d' % k for k in range(NGROUPS)]

P = dict(rates=[0.5, 2, 10], reps=2, warm=20, win=90, seed=300000, seedconns=4,
         order=['up', 'ttl1', 'proto'],
         settings=[{'name': 'rs128k-p4k', 'rs': '128K', 'page': 4096},
                   {'name': 'rs16k-p4k', 'rs': '16K', 'page': 4096},
                   {'name': 'rs16k-p16k', 'rs': '16K', 'page': 16384}])
if len(sys.argv) > 1:
    P.update(json.loads(sys.argv[1]))


def log(*a):
    print(time.strftime('%H:%M:%S'), *a, flush=True)


def sh(args, timeout=120, check=True):
    p = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    out = p.stdout.decode(errors='replace')
    if check and p.returncode != 0:
        raise RuntimeError('%s: rc %d: %s' % (' '.join(args), p.returncode, out[-800:]))
    return out


def run_news(args, timeout=60):
    try:
        p = subprocess.run(args, cwd=SITE, env=ENV, user=UID, group=GID, extra_groups=[],
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
        return p.returncode, p.stdout[-2000:].decode(errors='replace')
    except subprocess.TimeoutExpired:
        return -1, 'timeout'


def chown_tree(top):
    os.chown(top, UID, GID)
    for dp, dns, fns in os.walk(top):
        for n in dns + fns:
            os.chown(os.path.join(dp, n), UID, GID)


def make_site(site, ovdir, conf):
    if os.path.exists(site):
        shutil.rmtree(site)
    for d in ['etc', 'db', 'log', 'run', 'tmp', 'http', 'spool/articles',
              'spool/incoming/bad', 'spool/outgoing', 'spool/archive', 'spool/innfeed']:
        os.makedirs(os.path.join(site, d))
    for f in os.listdir(PREFIX + '/etc'):
        src = os.path.join(PREFIX, 'etc', f)
        if os.path.isfile(src):
            shutil.copy(src, os.path.join(site, 'etc', f))
    innconf = {
        'mta': '"/usr/bin/true %s"', 'ovmethod': 'ovsqlite', 'hismethod': 'hisv6',
        'pathhost': 'ovz.test', 'domain': 'ovz.test', 'pathnews': site,
        'pathbin': BIN, 'pathtmp': site + '/tmp', 'pathoverview': ovdir, 'port': str(PORT),
        'bindaddress': '127.0.0.1', 'server': '127.0.0.1', 'nnrpdloadlimit': '0',
    }

    def w(rel, text):
        with open(os.path.join(site, rel), 'w') as fh:
            fh.write(text)
    w('etc/inn.conf', ''.join('%s: %s\n' % kv for kv in sorted(innconf.items())))
    w('etc/readers.conf', 'auth "local" {\n    hosts: "127.0.0.1, ::1, localhost"\n'
      '    default: "<local>"\n}\naccess "local" {\n    users: "<local>"\n'
      '    newsgroups: "*"\n    access: RPAN\n}\n')
    w('etc/incoming.conf', 'peer ovz {\n    hostname: "127.0.0.2"\n    streaming: true\n}\n')
    w('etc/newsfeeds', 'ME:*::\n')
    w('etc/storage.conf', 'method trash {\n    newsgroups: *\n    class: 0\n}\n')
    w('etc/ovsqlite.conf', conf)
    act = ['control 0000000000 0000000001 n', 'control.cancel 0000000000 0000000001 n',
           'junk 0000000000 0000000001 n'] + ['%s 0000000000 0000000001 y' % g for g in GROUPS]
    w('db/active', '\n'.join(act) + '\n')
    w('db/active.times', '')
    w('db/newsgroups', ''.join('%s\tGroup %s.\n' % (g, g) for g in
                               ['control', 'control.cancel', 'junk'] + GROUPS))
    w('db/history', '')
    chown_tree(site)
    p = subprocess.run([BIN + '/makedbz', '-i', '-o'], cwd=site,
                       env=dict(ENV, INNCONF=site + '/etc/inn.conf', HOME=site),
                       user=UID, group=GID, extra_groups=[], stdout=subprocess.PIPE,
                       stderr=subprocess.STDOUT, timeout=60)
    if p.returncode != 0:
        raise RuntimeError('makedbz: ' + p.stdout.decode(errors='replace'))


def set_ovconf(site, conf):
    with open(site + '/etc/ovsqlite.conf', 'w') as fh:
        fh.write(conf)
    os.chown(site + '/etc/ovsqlite.conf', UID, GID)


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
    def __init__(self, server, ovdir):
        self.server = server
        self.ovdir = ovdir
        self.srv = self.innd = self.srvout = self.inndout = None
        self.pid = 0

    def start(self):
        self.srvout = open(SITE + '/log/ovsqlite-server.out', 'wb')
        self.srv = subprocess.Popen([BIN + '/' + self.server, '-d'], cwd=SITE, env=ENV, user=UID,
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
        wait_for(sock_up, 60, 'ovsqlite-server socket')
        self.inndout = open(SITE + '/log/innd.out', 'wb')
        self.innd = subprocess.Popen([BIN + '/innd', '-f'], cwd=SITE, env=ENV, user=UID,
                                     group=GID, extra_groups=[], stdin=subprocess.DEVNULL,
                                     stdout=self.inndout, stderr=subprocess.STDOUT)

        def port_up():
            s = socket.create_connection(('127.0.0.1', PORT), timeout=2,
                                         source_address=('127.0.0.2', 0))
            s.close()
            return True
        wait_for(port_up, 60, 'innd port')
        self.pid = self.srv.pid

    def stop(self):
        if self.innd:
            rc, out = run_news([BIN + '/ctlinnd', '-t', '10', 'shutdown', 'ovz done'], timeout=30)
            try:
                self.innd.wait(30)
            except subprocess.TimeoutExpired:
                log('innd did not stop, killing', out)
                self.innd.kill()
                self.innd.wait(10)
        if self.srv:
            self.srv.send_signal(signal.SIGTERM)
            try:
                self.srv.wait(60)
            except subprocess.TimeoutExpired:
                log('ovsqlite-server did not stop, killing')
                self.srv.kill()
                self.srv.wait(10)
        for f in (self.srvout, self.inndout):
            if f:
                f.close()

    def tail_logs(self):
        for f in ['log/ovsqlite-server.out', 'log/innd.out', 'log/news.err', 'log/errlog']:
            p = os.path.join(SITE, f)
            if os.path.exists(p) and os.path.getsize(p):
                with open(p, 'rb') as fh:
                    fh.seek(max(0, os.path.getsize(p) - 1500))
                    log('--', f, fh.read().decode(errors='replace'))


class Conn:
    def __init__(self, src):
        self.s = socket.create_connection(('127.0.0.1', PORT), timeout=60,
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


def ihave(c, tag, i, group):
    mid = '<ovz-%s-%d@ovz.test>' % (tag, i)
    hdr = ['Path: ovz.peer!not-for-mail', 'From: ovz <ovz@ovz.test>', 'Newsgroups: ' + group,
           'Subject: ovz %s %d' % (tag, i), 'Message-ID: ' + mid,
           'Date: ' + email.utils.formatdate(usegmt=True), 'Organization: ovz measurement']
    r = c.cmd('IHAVE ' + mid)
    if not r.startswith('335'):
        return r
    c.s.sendall(('\r\n'.join(hdr) + '\r\n\r\n' + BODY + '.\r\n').encode())
    return c.line()


def build_seed(page):
    """Seed site + overview database with P['seed'] articles spread over NGROUPS groups."""
    sdir = '%s/seed-p%d' % (ROOT, page)
    if os.path.exists(sdir + '/done'):
        log('seed', sdir, 'exists')
        return sdir
    if os.path.exists(sdir):
        shutil.rmtree(sdir)
    os.makedirs(sdir + '/ov')
    os.chown(sdir + '/ov', UID, GID)
    conf = 'walmode: true\npagesize: %d\n' % page
    make_site(SITE, sdir + '/ov', conf)
    inn = Inn('ovsqlite-server.up', sdir + '/ov')
    t0 = time.monotonic()
    try:
        inn.start()
        n = P['seed']
        k = P['seedconns']
        errs = []

        def worker(w):
            rnd = random.Random(1000 + w)
            try:
                c = Conn('127.0.0.2')
                for i in range(w, n, k):
                    r = ihave(c, 'seed', i, GROUPS[rnd.randrange(NGROUPS)])
                    if not r.startswith('235'):
                        errs.append(r)
                        if len(errs) > 20:
                            return
                c.close()
            except Exception as e:
                errs.append(repr(e))
        ts = [threading.Thread(target=worker, args=(w,)) for w in range(k)]
        for t in ts:
            t.start()
        for t in ts:
            t.join()
        if errs:
            raise RuntimeError('seed errors: %s' % errs[:5])
    except Exception:
        inn.tail_logs()
        raise
    finally:
        inn.stop()
    db = sdir + '/ov/ovsqlite.db'
    sh(['su', '-m', 'news', '-c',
        '/usr/local/bin/sqlite3 %s "PRAGMA wal_checkpoint(TRUNCATE);" ' % db], timeout=300)
    for suf in ('-wal', '-shm'):
        if os.path.exists(db + suf) and os.path.getsize(db + suf) == 0:
            os.unlink(db + suf)
    pg = sh(['su', '-m', 'news', '-c', '/usr/local/bin/sqlite3 %s "PRAGMA page_size; '
             'PRAGMA page_count; PRAGMA journal_mode;"' % db]).split()
    shutil.copytree(SITE, sdir + '/site', symlinks=True,
                    ignore=shutil.ignore_patterns('*.sock', '*.pid'))
    open(sdir + '/done', 'w').close()
    log('SEED', json.dumps({'page': page, 'articles': P['seed'], 's': round(time.monotonic() - t0),
                            'db_bytes': os.path.getsize(db), 'pragma': pg,
                            'files': sorted(os.listdir(sdir + '/ov'))}))
    return sdir


def ds_kstat(name):
    out = sh(['sysctl', 'kstat.zfs.%s.dataset' % POOL], check=False)
    objs = {}
    for ln in out.splitlines():
        m = re.match(r'kstat\.zfs\.[^.]+\.dataset\.(objset-0x[0-9a-f]+)\.(\w+): (.*)$', ln)
        if m:
            objs.setdefault(m.group(1), {})[m.group(2)] = m.group(3).strip()
    for o in objs.values():
        if o.get('dataset_name') == name:
            return {k: int(v) for k, v in o.items() if v.lstrip('-').isdigit()}
    return {}


def dev_kw():
    out = sh(['iostat', '-I', '-x', '-d', DEV])
    for ln in out.splitlines():
        f = ln.split()
        if f and f[0] == DEV:
            return float(f[4]), float(f[2])   # total KB written, total write ops
    raise RuntimeError('iostat: ' + out)


def cpu_us(pid):
    out = sh(['procstat', '-r', str(pid)], check=False)
    tot = 0.0
    for ln in out.splitlines():
        m = re.search(r'(user|system) time\s+(\d+):(\d+):([\d.]+)', ln)
        if m:
            tot += int(m.group(2)) * 3600 + int(m.group(3)) * 60 + float(m.group(4))
    return tot * 1e6


def txgs():
    # Pool transaction-group history (vfs.zfs.txg.history): txg, state, ndirty, nwritten.
    out = sh(['sysctl', '-n', 'kstat.zfs.%s.txgs' % POOL], check=False)
    rows = []
    for ln in out.splitlines():
        f = ln.split()
        if len(f) >= 6 and f[0].isdigit():
            rows.append((int(f[0]), f[2], int(f[3]), int(f[5])))
    return rows


def snap(pid):
    sh(['zpool', 'sync', POOL])
    kw, wops = dev_kw()
    ks = ds_kstat(DS)
    d = {'dev_kw': kw, 'dev_wops': wops, 'cpu_us': cpu_us(pid)}
    for k in ['nwritten', 'writes', 'zil_commit_count', 'zil_itx_count',
              'zil_itx_metaslab_normal_bytes', 'zil_itx_metaslab_normal_write',
              'zil_itx_copied_bytes', 'zil_itx_indirect_bytes', 'zil_itx_needcopy_bytes']:
        d[k] = ks.get(k, 0)
    d['txg_span'] = max((t for t, st, _, _ in txgs() if st == 'C'), default=0)
    return d


# Running counters for the server's syscalls; the harness prints them with a marker
# (getppid() from this process) right before each snapshot, so the DTrace window is the
# same as the disk/kstat window.  The server is single-threaded, so plain globals are safe.
DTRACE = ('BEGIN { fs = 0; fds = 0; pwb = (int64_t)0; pwn = 0; wb = (int64_t)0; wn = 0; vn = 0; '
          'printf("READY\\n"); } '
          'syscall::fsync:entry /pid == %(srv)d/ { fs++; } '
          'syscall::fdatasync:entry /pid == %(srv)d/ { fds++; } '
          'syscall::pwrite:entry /pid == %(srv)d/ { pwb += arg2; pwn++; } '
          'syscall::write:entry /pid == %(srv)d/ { wb += arg2; wn++; } '
          'syscall::pwritev:entry,syscall::writev:entry /pid == %(srv)d/ { vn++; } '
          'syscall::getppid:entry /pid == %(me)d/ '
          '{ printf("M %%d %%d %%d %%d %%d %%d %%d\\n", fs, fds, pwb, pwn, wb, wn, vn); }')
DTKEYS = ['dt_S_fsync', 'dt_S_fdatasync', 'dt_W_pwrite', 'dt_N_pwrite', 'dt_W_write', 'dt_N_write',
          'dt_N_vec']


def run_rate(setting, sdir, cfg, rate, rep):
    tag = '%s-%s-%s-%d' % (setting['name'], cfg, rate, rep)
    server, extra = CONFIGS[cfg]
    res = {'setting': setting['name'], 'rs': setting['rs'], 'page': setting['page'], 'cfg': cfg,
           'rate': rate, 'rep': rep}
    inn = None
    dt = None
    try:
        sh(['zfs', 'destroy', '-r', DS], check=False)
        sh(['zfs', 'create', '-o', 'recordsize=' + setting['rs'], DS])
        ovdir = '/' + DS
        os.chown(ovdir, UID, GID)
        shutil.copy(sdir + '/ov/ovsqlite.db', ovdir + '/ovsqlite.db')
        os.chown(ovdir + '/ovsqlite.db', UID, GID)
        if os.path.exists(SITE):
            shutil.rmtree(SITE)
        shutil.copytree(sdir + '/site', SITE, symlinks=True)
        for f in os.listdir(SITE + '/run'):
            os.unlink(os.path.join(SITE, 'run', f))
        chown_tree(SITE)
        with open(SITE + '/etc/inn.conf') as fh:
            ic = fh.read()
        ic = re.sub(r'(?m)^pathoverview: .*$', 'pathoverview: ' + ovdir, ic)
        with open(SITE + '/etc/inn.conf', 'w') as fh:
            fh.write(ic)
        set_ovconf(SITE, 'walmode: true\npagesize: %d\n%s' % (setting['page'], extra))
        sh(['zpool', 'sync', POOL])
        inn = Inn(server, ovdir)
        inn.start()
        c = Conn('127.0.0.2')
        rnd = random.Random('%s-%s-%d' % (setting['name'], rate, rep))
        # DTrace attaches before the feed starts, so its start-up (5-6 s of system time on
        # this VM) cannot hold articles back and release them as a burst in the window.
        dtout = open(ROOT + '/dtrace.out', 'w')
        dt = subprocess.Popen(['dtrace', '-q', '-x', 'switchrate=20hz', '-n',
                               DTRACE % {'srv': inn.pid, 'me': os.getpid()}],
                              stdin=subprocess.DEVNULL, stdout=dtout,
                              stderr=subprocess.STDOUT)
        for _ in range(600):                   # wait until dtrace is attached (max 60 s)
            time.sleep(0.1)
            if dt.poll() is not None:
                break
            with open(ROOT + '/dtrace.out') as fh:
                if 'READY' in fh.read().splitlines():
                    break
        if dt.poll() is not None or not os.path.getsize(ROOT + '/dtrace.out'):
            with open(ROOT + '/dtrace.out') as fh:
                raise RuntimeError('dtrace did not start: ' + fh.read()[-400:])
        t0 = time.monotonic()
        i = acc = rej = lag = 0
        started = False
        while True:
            now = time.monotonic()
            if not started and now >= t0 + P['warm']:
                os.getppid()                   # marker: counters at the window start
                s0 = snap(inn.pid)
                tw0 = time.monotonic()
                started = True
                # The snapshot (zpool sync) takes time; restart the schedule from now so
                # the articles due meanwhile are spread out, not sent at once.
                if rate:
                    late = tw0 - (t0 + i / rate)
                    res['start_late_s'] = round(max(0.0, late), 3)
                    if late > 0:
                        t0 += late
                continue
            if started and now >= tw0 + P['win']:
                os.getppid()                   # marker: counters at the window end
                s1 = snap(inn.pid)
                tw1 = time.monotonic()
                break
            next_art = (t0 + i / rate) if rate else float('inf')
            next_evt = (t0 + P['warm']) if not started else (tw0 + P['win'])
            if next_art <= next_evt:
                if next_art > now:
                    time.sleep(next_art - now)
                if started and time.monotonic() - next_art > 0.5:
                    lag += 1                   # sent more than 0.5 s behind its slot
                r = ihave(c, tag, i, GROUPS[rnd.randrange(NGROUPS)])
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
        dt.send_signal(signal.SIGINT)
        dt.wait(30)
        dtout.close()
        c.close()
        res.update(win_s=round(tw1 - tw0, 2), accepted=acc, rejected=rej, late_articles=lag)
        for k in s0:
            res[k] = round(s1[k] - s0[k], 1)
        # Transaction groups synced in the window (after the start snapshot's zpool sync,
        # up to and including the end snapshot's): count, groups with data, bytes each.
        t0x, t1x = s0['txg_span'], s1['txg_span']
        rows = [r for r in txgs() if t0x < r[0] <= t1x and r[1] == 'C']
        res['txg_n'] = len(rows)
        res['txg_n_data'] = sum(1 for r in rows if r[3] > 0)
        res['txg_nwritten'] = sum(r[3] for r in rows)
        res['txg_w'] = [r[3] for r in rows if r[3] > 0]
        res['txg_hist_ok'] = len(rows) == t1x - t0x
        with open(ROOT + '/dtrace.out') as fh:
            marks = [ln.split()[1:] for ln in fh if ln.startswith('M ')]
        if len(marks) != 2:
            raise RuntimeError('expected 2 dtrace markers, got %d' % len(marks))
        for k, a, b in zip(DTKEYS, marks[0], marks[1]):
            res[k] = int(b) - int(a)
        for suf in ('', '-wal'):
            p = ovdir + '/ovsqlite.db' + suf
            res['size' + suf] = os.path.getsize(p) if os.path.exists(p) else 0
    except Exception as e:
        log('RUN ERROR', tag, repr(e))
        if inn:
            inn.tail_logs()
        res['error'] = repr(e)
    finally:
        if dt and dt.poll() is None:
            dt.kill()
        if inn:
            inn.stop()
    log('RESULT', json.dumps(res, sort_keys=True))
    with open(ROOT + '/results.jsonl', 'a') as fh:
        fh.write(json.dumps(res, sort_keys=True) + '\n')
    return res


def main():
    os.makedirs(ROOT, exist_ok=True)
    log('params', json.dumps(P))
    sh(['kldload', '-n', 'dtraceall'])
    sh(['sysctl', 'vfs.zfs.txg.history=2000'])   # keep every group of a 40-minute window
    if '127.0.0.2' not in sh(['ifconfig', 'lo0']):
        sh(['ifconfig', 'lo0', 'alias', '127.0.0.2/32'])
    seeds = {}
    for page in sorted({s['page'] for s in P['settings']}):
        seeds[page] = build_seed(page)
    order = list(P['order'])
    for rep in range(1, P['reps'] + 1):
        for setting in P['settings']:
            if P.get('idle', True):
                for cfg in P.get('idle_cfgs', ['up']):
                    run_rate(setting, seeds[setting['page']], cfg, 0, rep)
            for rate in P['rates']:
                for cfg in order:
                    run_rate(setting, seeds[setting['page']], cfg, rate, rep)
                order = order[1:] + order[:1]
    sh(['zfs', 'destroy', '-r', DS], check=False)
    log('DONE')


if __name__ == '__main__':
    main()

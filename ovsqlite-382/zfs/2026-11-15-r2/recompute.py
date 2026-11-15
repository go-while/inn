# Parse-only: reads ovz.py result jsonl files, prints tables. No project code, no I/O besides reading.
import json
import os
import sys

OLD = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', '2026-11-14', 'results.jsonl')
NEW = os.path.dirname(os.path.abspath(__file__))
CFGS = ['up', 'ttl1', 'proto']
NAME = {'up': 'today', 'ttl1': 'transtimelimit 1', 'proto': 'prototype'}


def load(path, src):
    out = []
    if not os.path.exists(path):
        return out
    for n, ln in enumerate(open(path), 1):
        if not ln.strip():
            continue
        r = json.loads(ln)
        r['_src'] = '%s:%d' % (src, n)
        out.append(r)
    return out


old = [r for r in load(OLD, '1114') if r['setting'] == 'rs128k-p4k']
short = []
for j in ['job2a', 'job2b', 'job3']:
    short += load('%s/results-%s.jsonl' % (NEW, j), j)
long_ = []
for j in ['job4', 'job5', 'job6', 'job7']:
    long_ += load('%s/results-%s.jsonl' % (NEW, j), j)

allnew = short + long_
print('== errors / anomalies')
for r in allnew:
    flags = []
    if 'error' in r:
        flags.append('ERROR ' + r['error'])
    if r.get('rejected'):
        flags.append('rejected=%d' % r['rejected'])
    if r.get('rate') and 'dt_W_pwrite' not in r:
        flags.append('no dtrace pwrite')
    if r.get('cpu_us', 0) > 1e6:
        flags.append('cpu_us=%.0f' % r['cpu_us'])
    print(r['_src'], r['cfg'], r['rate'], r['rep'], 'acc', r.get('accepted'), 'win', r.get('win_s'),
          'cpu_s %.3f' % (r.get('cpu_us', 0) / 1e6), ' '.join(flags) or 'ok')
print('old rs128k runs with cpu > 1 s:', [r['_src'] for r in old if r.get('cpu_us', 0) > 1e6])


def m_kibs(r):
    return r['dev_kw'] / r['win_s']


def m_pw(r):
    return r['dt_W_pwrite'] / 1024 / r['accepted']


def m_dk(r):
    return r['dev_kw'] / r['accepted']


def rng(vals, nd=1):
    if not vals:
        return '-'
    f = '%%.%df' % nd
    lo, hi = min(vals), max(vals)
    s = f % lo if f % lo == f % hi else (f % lo) + '–' + (f % hi)
    return '%s (mean %s, n=%d)' % (s, f % (sum(vals) / len(vals)), len(vals))


def cell(rows, rate, cfg, fn, need_acc=True):
    v = []
    for r in rows:
        if r['rate'] == rate and r['cfg'] == cfg and 'error' not in r:
            if need_acc and not r.get('accepted'):
                continue
            v.append(fn(r))
    return v


def table(title, fn, rates, nd=1, need_acc=True):
    print('\n== ' + title)
    print('| Feed | Config | new (job 2/3) | 2026-11-14 | combined |')
    print('|---|---|---|---|---|')
    for rate in rates:
        cfgs = ['up'] if rate == 0 else CFGS
        for cfg in cfgs:
            a = cell(short, rate, cfg, fn, need_acc)
            b = cell(old, rate, cfg, fn, need_acc)
            print('| %s | %s | %s | %s | %s |' % ('idle' if rate == 0 else '%g/s' % rate, NAME[cfg],
                                                rng(a, nd), rng(b, nd), rng(a + b, nd)))


table('disk KiB/s = dev_kw / win_s', m_kibs, [0, 0.5, 2, 10], 1, need_acc=False)
table('SQLite pwrite KiB/article = dt_W_pwrite / 1024 / accepted', m_pw, [0.5, 2, 10], 1)
table('disk KiB/article = dev_kw / accepted', m_dk, [0.5, 2, 10], 1)


def mean(v):
    return sum(v) / len(v)


print('\n== derived (combined means)')
for rate in [0.5, 2, 10]:
    rows = short + old
    pw = {c: mean(cell(rows, rate, c, m_pw)) for c in CFGS}
    ks = {c: mean(cell(rows, rate, c, m_kibs)) for c in CFGS}
    print('%g/s pwrite/article: ' % rate + ', '.join('%s %.2f' % (c, pw[c]) for c in CFGS),
          '| alt vs today: ttl1 %+.0f %%, proto %+.0f %%' % (100 * (pw['ttl1'] / pw['up'] - 1),
                                                        100 * (pw['proto'] / pw['up'] - 1)))
    print('%g/s disk KiB/s means: ' % rate + ', '.join('%s %.1f' % (c, ks[c]) for c in CFGS),
          '| extra: ttl1 %+.1f, proto %+.1f KiB/s (%+.0f %%, %+.0f %%)' % (
              ks['ttl1'] - ks['up'], ks['proto'] - ks['up'],
              100 * (ks['ttl1'] / ks['up'] - 1), 100 * (ks['proto'] / ks['up'] - 1)))
    # per-run KiB per transaction group with data: today one per 10 s, alternatives one per 5 s
    if rate in (0.5, 2):
        t = [m_kibs(r) * 10 for r in rows if r['rate'] == rate and r['cfg'] == 'up']
        a = [m_kibs(r) * 5 for r in rows if r['rate'] == rate and r['cfg'] != 'up']
        print('   KiB per txg with data (today x10 s, alternatives x5 s): today %.0f–%.0f, alt %.0f–%.0f'
              % (min(t), max(t), min(a), max(a)))

print('\n== long runs (per run)')
keys = ['accepted', 'win_s', 'dev_kw', 'dev_wops', 'dt_W_pwrite', 'dt_N_pwrite', 'writes', 'nwritten',
        'dt_S_fdatasync', 'dt_S_fsync', 'zil_commit_count', 'zil_itx_count', 'size-wal', 'cpu_us']
for r in long_:
    if 'error' in r:
        print(r['_src'], 'ERROR', r['error'])
        continue
    acc = r['accepted']
    print('%s %s %g/s win %s: disk %.2f KiB/s, %.1f KiB/art; pwrite %.2f KiB/art; pwrite calls/art %.2f; '
          'frames/art (bytes/4120) %.2f; dev write ops/art %.2f; kstat writes/art %.2f'
          % (r['_src'], r['cfg'], r['rate'], r['win_s'], m_kibs(r), m_dk(r), m_pw(r),
             r['dt_N_pwrite'] / acc, r['dt_W_pwrite'] / 4120 / acc, r['dev_wops'] / acc, r['writes'] / acc))
    print('   ' + ', '.join('%s=%s' % (k, r.get(k, '-')) for k in keys))

print('\n== per-article structure at 0.5/s and 2/s 90-s runs (combined), for comparison')
for rate in [0.5, 2, 10]:
    for c in CFGS:
        rows = [r for r in short + old if r['rate'] == rate and r['cfg'] == c and r.get('accepted')]
        print('%g/s %s: pwrite calls/art %s, frames/art %s, dev ops/s %s' % (
            rate, c, rng([r['dt_N_pwrite'] / r['accepted'] for r in rows], 2),
            rng([r['dt_W_pwrite'] / 4120 / r['accepted'] for r in rows], 2),
            rng([r['dev_wops'] / r['win_s'] for r in rows], 2)))


def decomp(r):
    # WAL frame = two pwrites (24-B header + 4096-B page) = 4120 B; checkpoint = one 4096-B pwrite per db page
    c, b = r['dt_N_pwrite'], r['dt_W_pwrite']
    fr = (4096 * c - b) / 4072
    return fr, c - 2 * fr


print('\n== long runs table')
print('| Feed, window | Config | accepted | disk KiB/s | disk KiB/article | pwrite KiB/article | WAL frames/article | checkpoint db pages | fdatasync | ZIL commits | WAL at end (B) | server CPU s |')
print('|---|---|---|---|---|---|---|---|---|---|---|---|')
for r in long_:
    fr, dbp = decomp(r)
    print('| %g/s, %d s | %s | %d | %.1f | %.0f | %.1f | %.2f | %.0f | %d | %d | %d | %.2f |' % (
        r['rate'], round(r['win_s']), NAME[r['cfg']], r['accepted'], m_kibs(r), m_dk(r), m_pw(r),
        fr / r['accepted'], dbp, r.get('dt_S_fdatasync', 0), r['zil_commit_count'], r['size-wal'],
        r['cpu_us'] / 1e6))

print('\n== WAL frames/article and checkpoints in 90-s runs (combined)')
for rate in [0.5, 2, 10]:
    for c in CFGS:
        rows = [r for r in short + old if r['rate'] == rate and r['cfg'] == c and r.get('accepted')]
        print('%g/s %s: frames/art %s; checkpoint pages %s; fdatasync %s' % (
            rate, c, rng([decomp(r)[0] / r['accepted'] for r in rows], 2),
            rng([decomp(r)[1] for r in rows], 0), rng([r.get('dt_S_fdatasync', 0) for r in rows], 0)))


def r2(vals, nd):
    f = '%%.%df' % nd
    lo, hi = f % min(vals), f % max(vals)
    return lo if lo == hi else lo + '–' + hi


def table2(title, fn, rates, nd, need_acc=True):
    print('\n### ' + title)
    print('| Feed | Config | new, job 2/3 (n=3) | combined with 2026-11-14 (n=5) | combined mean |')
    print('|---|---|---|---|---|')
    for rate in rates:
        for cfg in (['up'] if rate == 0 else CFGS):
            a = cell(short, rate, cfg, fn, need_acc)
            b = cell(old, rate, cfg, fn, need_acc)
            assert len(a) == 3 and len(b) == 2, (rate, cfg, len(a), len(b))
            print('| %s | %s | %s | %s | %s |' % ('idle' if rate == 0 else '%g/s' % rate, NAME[cfg],
                                                 r2(a, nd), r2(a + b, nd), ('%%.%df' % nd) % mean(a + b)))


print('\n== MARKDOWN')
table2('1. Disk KiB/s', m_kibs, [0, 0.5, 2, 10], 1, need_acc=False)
table2('2. SQLite pwrite KiB per article', m_pw, [0.5, 2, 10], 1)
table2('3. Disk KiB per article', m_dk, [0.5, 2, 10], 1)

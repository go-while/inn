# Parse-only: reads ovz.py result jsonl files (r3, 2026-11-15) and prints tables.
# No project code, no I/O besides reading. Run with: python3 -I recompute.py
import json
import os

D = os.path.dirname(os.path.abspath(__file__))
OLD = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', '2026-11-14', 'results.jsonl')
CFGS = ['up', 'ttl1', 'proto']
NAME = {'up': 'today', 'ttl1': 'ttl1', 'proto': 'proto'}
JOBS = ['0', '0b', '1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '11', '12']
RS = ['16K', '32K', '64K', '128K']


def load(path, src):
    out = []
    if not os.path.exists(path):
        return out
    for n, ln in enumerate(open(path), 1):
        if ln.strip():
            r = json.loads(ln)
            r['_src'] = '%s:%d' % (src, n)
            out.append(r)
    return out


runs = []
for j in JOBS:
    runs += load('%s/results-job%s.jsonl' % (D, j), 'job' + j)
old = load(OLD, '1114')
# Job 5 first attempt (queue 38): up and proto failed with 'dtrace did not start'; its ttl1 run is valid.
# Kept apart (not in the main tables) and used as a second sample of 32K 2/s ttl1.
EXTRA = ['5-dtfail']
extra = []
for j in EXTRA:
    extra += load('%s/results-job%s.jsonl' % (D, j), 'job' + j)


def ok(r):
    return 'error' not in r


def kibs(r):          # disk KiB/s
    return r['dev_kw'] / r['win_s']


def dka(r):           # disk KiB per article
    return r['dev_kw'] / r['accepted']


def pwa(r):           # pwrite KiB per article
    return r['dt_W_pwrite'] / 1024 / r['accepted']


def nwa(r):           # ZFS dataset nwritten KiB per article
    return r['nwritten'] / 1024 / r['accepted']


def decomp3(r):
    # WAL frame = 2 pwrites (24-B frame header + page); checkpoint page = 1 page-sized pwrite;
    # WAL header rewrite after a WAL reset = 1 pwrite of 32 B (H).
    #   c = 2F + K + H,  b = F*(page+24) + K*page + 32*H
    #   =>  page*c - b = F*(page-24) + H*(page-32)
    # Smallest H >= 0 that gives integer F and K >= 0. None if no exact solution (other write sizes).
    # (The 2-term version without H was off by about one frame and one page per WAL reset:
    #  F came out as n - 0.002*H, under the old 0.01 integer check.)
    p, c, b = r['page'], r['dt_N_pwrite'], r['dt_W_pwrite']
    return _d3(p, c, b)


def decomp_ds(r):
    # same decomposition from the dataset kstats (ZFS 'writes' calls, 'nwritten' bytes) instead of DTrace;
    # used when the DTrace counters have no exact solution (16 KiB pages: pwrites outside ovz/run)
    return _d3(r['page'], r['writes'], r['nwritten'])


def _d3(p, c, b):
    x = p * c - b
    for h in range(0, 200):
        y = x - h * (p - 32)
        if y < 0:
            break
        if y % (p - 24) == 0:
            f = y // (p - 24)
            k = c - 2 * f - h
            if k >= 0:
                return f, k, h
    return None


def decomp(r):
    # (frames, checkpoint pages); falls back to the 2-term float estimate when decomp3 has no exact solution
    d = decomp3(r) or decomp_ds(r)
    if d:
        return d[0], d[1]
    p, c, b = r['page'], r['dt_N_pwrite'], r['dt_W_pwrite']
    f = (p * c - b) / (p - 24)
    return f, c - 2 * f


def hms(s):
    h, m, x = (int(v) for v in s.split(':'))
    return h * 3600 + m * 60 + x


def jobinfo(j):
    """Queue id, queue rc, RESULT/RUN ERROR counts and harness wall time from jobN.out
    (job 3: queue job 31 was cut off locally; its harness lines are in job3.recover.out)."""
    import re
    out = os.path.join(D, 'job%s.out' % j)
    main = open(out).read()
    txt = main
    rec = os.path.join(D, 'job%s.recover.out' % j)
    if os.path.exists(rec):
        txt += open(rec).read()
    qid = re.search(r'^jobq: job (\d+) on ', main, re.M)
    rc = re.findall(r'queue_rc=(\d+)', main)
    res = re.findall(r'^RESULTS=(\d+) RUNERRORS=(\d+)', txt, re.M)
    t0 = re.search(r'^(\d\d:\d\d:\d\d) params ', txt, re.M)
    t1 = re.search(r'^(\d\d:\d\d:\d\d) DONE', txt, re.M)
    wall = (hms(t1.group(1)) - hms(t0.group(1))) / 60 if t0 and t1 else None
    return (qid.group(1) if qid else '?', rc[0] if rc else 'none (local run killed)',
            res[-1] if res else ('?', '?'), wall)


print('== jobs (queue id, queue rc, RESULT lines, RUN ERROR lines, harness wall min, result lines/errors in jsonl)')
print('| job | queue id | rc | RESULTs | RUN ERRORs | wall min | jsonl lines | jsonl errors |')
print('|---|---|---|---|---|---|---|---|')
for j in JOBS[:6] + EXTRA + JOBS[6:]:
    if not os.path.exists(os.path.join(D, 'job%s.out' % j)):
        continue
    q, rc, (nr, ne), wall = jobinfo(j)
    rr = [r for r in runs + extra if r['_src'].split(':')[0] == 'job' + j]
    print('| %s | %s | %s | %s | %s | %s | %d | %d |' % (j, q, rc, nr, ne, '%.1f' % wall if wall else '?',
                                                       len(rr), sum(1 for r in rr if not ok(r))))

print('\n== all runs (src, setting, cfg, rate, rep, accepted, win_s, cpu_s, flags)')
for r in runs + extra:
    fl = []
    if not ok(r):
        fl.append('ERROR ' + r['error'])
    if r.get('rejected'):
        fl.append('rejected=%d' % r['rejected'])
    if r.get('rate') and ok(r) and 'dt_W_pwrite' not in r:
        fl.append('no dtrace')
    if r.get('cpu_us', 0) > 1e6:
        fl.append('CPU>1s')
    if ok(r) and r.get('dt_N_pwrite') is not None:
        d = decomp3(r)
        if d:
            fl.append('F=%d K=%d H=%d%s' % (d + ('' if d[2] * 3 == r.get('dt_S_fdatasync', -1) else ' (H*3!=fdatasync)',)))
        else:
            dd = decomp_ds(r)
            fl.append('DTrace counters not exact; dataset kstats: %s; pwrites outside ovz/run: %d calls, %d B' % (
                'F=%d K=%d H=%d' % dd if dd else 'not exact either',
                r['dt_N_pwrite'] - r['writes'], r['dt_W_pwrite'] - r['nwritten']))
        fl.append('pw-nw=%d' % (r['dt_W_pwrite'] - r['nwritten']))
        fl.append('Npw-writes=%d' % (r['dt_N_pwrite'] - r['writes']))
    print(r['_src'], r['setting'], r['cfg'], r['rate'], r['rep'], r.get('accepted'), r.get('win_s'),
          '%.3f' % (r.get('cpu_us', 0) / 1e6), ' '.join(fl))


def row(r):
    f, k = decomp(r)
    d = decomp3(r)
    t = ''
    if not d:
        d = decomp_ds(r)
        t = 'ds:' if d else '~'   # ds: = from dataset kstats (writes/nwritten); ~ = 2-term estimate
    return ('%d | %.1f | %.1f | %.2f | %.2f | %s%.2f | %s%.0f | %s | %d | %d | %d | %.2f' % (
        round(r['win_s']), kibs(r), dka(r), pwa(r), nwa(r), t, f / r['accepted'], t, k,
        d[2] if d else '?', r.get('dt_S_fdatasync', 0), r['accepted'], r.get('size-wal', 0), r['cpu_us'] / 1e6))


HDR = ('| rs | rate | cfg | win s | disk KiB/s | disk KiB/art | pwrite KiB/art | nwritten KiB/art | '
       'WAL frames/art | ckpt pages | WAL hdr writes | fdatasync | accepted | WAL end B | CPU s |\n'
       '|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|')


def pick(rows, setting, rate, cfg):
    return [r for r in rows if ok(r) and r['setting'] == setting and r['rate'] == rate and r['cfg'] == cfg]


print('\n== smoke (job 0b): pwrite vs nwritten')
for r in pick(runs, 'rs128k-p4k', 0.5, 'up'):
    if r['win_s'] < 61:
        print(r['_src'], 'dt_W_pwrite', r['dt_W_pwrite'], 'nwritten', r['nwritten'],
              'ratio %.4f' % (r['dt_W_pwrite'] / r['nwritten']), 'dt_N_pwrite', r['dt_N_pwrite'],
              'writes', r['writes'])

print('\n== MAIN: 600-s runs, page 4096')
print(HDR)
long4 = [r for r in runs if ok(r) and r['page'] == 4096 and r['win_s'] > 500]
for rate in [0.5, 2]:
    for rs in RS:
        for cfg in CFGS:
            for r in pick(long4, 'rs%sk-p4k' % rs[:-1].lower(), rate, cfg):
                print('| %s | %g | %s | %s |' % (rs, rate, NAME[cfg], row(r).replace(' | ', ' | ')))

print('\n== 128K, page 16384, 600-s runs')
print(HDR)
for rate in [0.5, 2]:
    for cfg in CFGS:
        for r in pick(runs, 'rs128k-p16k', rate, cfg):
            print('| 128K p16k | %g | %s | %s |' % (rate, NAME[cfg], row(r)))

print('\n== 10/s, 90-s runs (r3 32K/64K new DTrace window; 2026-11-14 16K/128K old window, pwrite may include ~1.5 s extra)')
print(HDR.replace('| rs |', '| src | rs |').replace('|---|', '|---|---|', 1))
for setting, rows in [('rs16k-p4k', old), ('rs32k-p4k', runs), ('rs64k-p4k', runs), ('rs128k-p4k', old)]:
    for cfg in CFGS:
        for r in pick(rows, setting, 10, cfg):
            print('| %s | %s | 10 | %s | %s |' % (r['_src'], r['rs'], NAME[cfg], row(r)))

print('\n== idle (job 1)')
for r in runs:
    if ok(r) and r['rate'] == 0:
        print(r['_src'], r['setting'], r['cfg'], 'win', r['win_s'], 'dev_kw', r['dev_kw'], 'dev_wops', r['dev_wops'],
              'nwritten', r['nwritten'], 'pwrite', r.get('dt_W_pwrite'), 'Npwrite', r.get('dt_N_pwrite'),
              'fdatasync', r.get('dt_S_fdatasync'), 'cpu_s %.3f' % (r['cpu_us'] / 1e6), 'size-wal', r.get('size-wal'))


def one(rows, setting, rate, cfg):
    # the single 600-s run of a cell (the 60-s smoke run job0b shares the 128K 0.5/s today cell)
    v = [r for r in pick(rows, setting, rate, cfg) if r['win_s'] > 500]
    return v[0] if len(v) == 1 else None


print('\n== DERIVED 1: alternatives vs today (600-s runs, one run each)')
print('| page | rs | rate | alt | disk ratio | disk extra KiB/s | pwrite ratio | nwritten ratio | ckpt pages today/alt |')
print('|---|---|---|---|---|---|---|---|---|')
for page, rss in [(4096, RS), (16384, ['128K'])]:
    for rate in [0.5, 2]:
        for rs in rss:
            st = 'rs%sk-p%dk' % (rs[:-1].lower(), page // 1024)
            u = one(runs, st, rate, 'up')
            if not u:
                continue
            for a in ['ttl1', 'proto']:
                x = one(runs, st, rate, a)
                if not x:
                    continue
                print('| %d | %s | %g | %s | %.2f | %+.1f | %.2f | %.2f | %.0f/%.0f |' % (
                    page, rs, rate, a, kibs(x) / kibs(u), kibs(x) - kibs(u), pwa(x) / pwa(u),
                    nwa(x) / nwa(u), decomp(u)[1], decomp(x)[1]))

print('\n== DERIVED 2: disk KiB/s by recordsize at fixed rate and config (page 4096, 600 s), and ratio to 16K')
print('| rate | cfg | 16K | 32K | 64K | 128K | 32K/16K | 64K/16K | 128K/16K | ckpt pages 16/32/64/128 |')
print('|---|---|---|---|---|---|---|---|---|---|')
for rate in [0.5, 2]:
    for cfg in CFGS:
        rr = [one(runs, 'rs%sk-p4k' % rs[:-1].lower(), rate, cfg) for rs in RS]
        if not all(rr):
            continue
        v = [kibs(r) for r in rr]
        print('| %g | %s | %s | %s | %s |' % (rate, NAME[cfg], ' | '.join('%.1f' % x for x in v),
                                             ' | '.join('%.2f' % (x / v[0]) for x in v[1:]),
                                             '/'.join('%.0f' % decomp(r)[1] for r in rr)))

print('\n== DERIVED 3: disk KiB per txg-with-data, no-checkpoint runs only (today: 1 commit per 10 s; alt: data in every 5-s txg)')
for rate in [0.5, 2]:
    for rs in RS:
        st = 'rs%sk-p4k' % rs[:-1].lower()
        parts = []
        for cfg in CFGS:
            r = one(runs, st, rate, cfg)
            if r and decomp(r)[1] < 0.5:
                period = 10 if cfg == 'up' else 5
                parts.append('%s %.0f KiB/txg (%.1f KiB disk per KiB pwrite)' % (
                    NAME[cfg], kibs(r) * period, r['dev_kw'] / (r['dt_W_pwrite'] / 1024)))
            elif r:
                parts.append('%s has ckpt' % NAME[cfg])
        print('%g/s %s: %s' % (rate, rs, '; '.join(parts)))

print('\n== DERIVED 4: checkpoint cost estimate, per recordsize')
# For each (rs, rate, alt config) run with checkpoint pages, compare with the other alternative at the same
# rs and rate when that one has none (ttl1 and proto commit at about the same rate): extra disk KiB per
# checkpoint page and per fdatasync. One run per cell: indicative only.
for page, rss in [(4096, RS), (16384, ['128K'])]:
    for rs in rss:
        st = 'rs%sk-p%dk' % (rs[:-1].lower(), page // 1024)
        for rate in [0.5, 2]:
            a, b = one(runs, st, rate, 'ttl1'), one(runs, st, rate, 'proto')
            if not (a and b):
                continue
            ka, kb = decomp(a)[1], decomp(b)[1]
            if (ka > 0.5) == (kb > 0.5):
                print('%s %g/s: ttl1 ckpt %.0f, proto ckpt %.0f -> no with/without pair' % (st, rate, ka, kb))
                continue
            w, wo = (a, b) if ka > 0.5 else (b, a)
            kw = decomp(w)[1]
            extra = w['dev_kw'] - wo['dev_kw']
            # normalise the no-checkpoint run to the same pwrite volume of WAL frames
            print('%s %g/s: with (%s) %.0f KiB, without (%s) %.0f KiB, extra %.0f KiB = %.1f MiB for %.0f ckpt pages '
                  '(%.1f MiB of pages), %d fdatasync; disk extra / ckpt bytes %.1f' % (
                      st, rate, NAME[w['cfg']], w['dev_kw'], NAME[wo['cfg']], wo['dev_kw'], extra, extra / 1024, kw,
                      kw * page / 1048576, w.get('dt_S_fdatasync', 0), extra / (kw * page / 1024)))

print('\n== REPLICATE: same cell measured twice (job 5 first attempt vs rerun)')
print(HDR.replace('| rs |', '| src | rs |').replace('|---|', '|---|---|', 1))
for x in extra:
    if not ok(x):
        continue
    for r in [x] + pick(runs, x['setting'], x['rate'], x['cfg']):
        print('| %s | %s | %g | %s | %s |' % (r['_src'], r['rs'], r['rate'], NAME[r['cfg']], row(r)))

print('\n== DERIVED 1b: per run, pwrite/nwritten and disk/nwritten (ZFS physical bytes per logical byte), 600-s runs')
print('| page | rs | rate | cfg | pwrite/nwritten | disk/nwritten | disk/pwrite |')
print('|---|---|---|---|---|---|---|')
for page, rss in [(4096, RS), (16384, ['128K'])]:
    for rate in [0.5, 2]:
        for rs in rss:
            st = 'rs%sk-p%dk' % (rs[:-1].lower(), page // 1024)
            for cfg in CFGS:
                r = one(runs, st, rate, cfg)
                if r:
                    print('| %d | %s | %g | %s | %.4f | %.2f | %.2f |' % (
                        page, rs, rate, NAME[cfg], r['dt_W_pwrite'] / r['nwritten'],
                        r['dev_kw'] * 1024 / r['nwritten'], r['dev_kw'] * 1024 / r['dt_W_pwrite']))


def solve(a, b):
    # least squares via normal equations, Gaussian elimination; a: rows of regressors
    n = len(a[0])
    m = [[sum(r[i] * r[j] for r in a) for j in range(n)] + [sum(r[i] * y for r, y in zip(a, b))] for i in range(n)]
    for i in range(n):
        piv = max(range(i, n), key=lambda k: abs(m[k][i]))
        m[i], m[piv] = m[piv], m[i]
        if abs(m[i][i]) < 1e-12:
            return None
        for k in range(n):
            if k != i:
                f = m[k][i] / m[i][i]
                m[k] = [x - f * y for x, y in zip(m[k], m[i])]
    return [m[i][n] / m[i][i] for i in range(n)]


print('\n== DERIVED 4b: checkpoint cost, model fit per recordsize (6 runs: 3 configs x 2 rates, ONE run per cell)')
print('# Model: disk KiB = m * txgs_with_data + w * WAL-frame KiB + c * checkpoints')
print('#   txgs_with_data assumed = win/10 for today (commit 10 s after the first write) and win/5 for ttl1/proto')
print('#   (commits about every 1 s or 250 ms, so data in every 5-s txg; vfs.zfs.txg.timeout = 5 read on the VM, zfsprops.out).')
print('#   checkpoints = WAL header rewrites H (plus 1 when fdatasync shows a checkpoint whose restart fell outside the')
print('#   window, fdatasync > 3H). 3 parameters from 6 points: indicative only, no error bars possible.')
print('| page | rs | m KiB/txg | w disk KiB per WAL KiB | c KiB per checkpoint | c MiB | ckpt pages/ckpt | c per ckpt page KiB | max abs resid % |')
print('|---|---|---|---|---|---|---|---|---|')
for page, rss in [(4096, RS), (16384, ['128K'])]:
    for rs in rss:
        st = 'rs%sk-p%dk' % (rs[:-1].lower(), page // 1024)
        rr = [one(runs, st, rate, cfg) for rate in [0.5, 2] for cfg in CFGS]
        rr = [r for r in rr if r]
        if len(rr) < 6 or not all(decomp3(r) for r in rr):
            print('| %d | %s | not enough exact runs (%d) |' % (page, rs, len(rr)))
            continue
        a, b, ck, kp = [], [], 0, 0
        for r in rr:
            f, k, h = decomp3(r)
            nck = h + (1 if r.get('dt_S_fdatasync', 0) > 3 * h else 0)
            ntxg = r['win_s'] / (10 if r['cfg'] == 'up' else 5)
            a.append([ntxg, f * (page + 24) / 1024, nck])
            b.append(r['dev_kw'])
            ck += nck
            kp += k
        x = solve(a, b)
        if not x:
            print('| %d | %s | singular |' % (page, rs))
            continue
        res = max(abs(sum(c * v for c, v in zip(x, row_)) - y) / y for row_, y in zip(a, b))
        ppc = kp / ck if ck else 0
        print('| %d | %s | %.0f | %.2f | %.0f | %.1f | %.0f | %.1f | %.1f |' % (
            page, rs, x[0], x[1], x[2], x[2] / 1024, ppc, x[2] / ppc if ppc else 0, 100 * res))

print('\n== DERIVED 4c: checkpoint cost, 2-parameter fit per recordsize: disk KiB = m * txgs_with_data + c * checkpoints')
print('#   same txg and checkpoint counting as 4b; the WAL-volume term of 4b is dropped (it came out negative: collinear).')
print('#   ONE run per cell; the job-5 replicate shows ~13% run-to-run disk spread, so c is uncertain by several MiB.')
print('| page | rs | m KiB/txg | c KiB/ckpt | c MiB | resid % (0.5 today/ttl1/proto, 2 today/ttl1/proto) |')
print('|---|---|---|---|---|---|')
for page, rss in [(4096, RS), (16384, ['128K'])]:
    for rs in rss:
        st = 'rs%sk-p%dk' % (rs[:-1].lower(), page // 1024)
        rr = [one(runs, st, rate, cfg) for rate in [0.5, 2] for cfg in CFGS]
        if not all(rr):
            print('| %d | %s | missing runs |' % (page, rs))
            continue
        a, b = [], []
        for r in rr:
            d = decomp3(r) or decomp_ds(r)
            fds = r.get('dt_S_fdatasync', 0)
            # no exact decomposition (other write sizes): checkpoints = ceil(fdatasync / 3)
            nck = (d[2] + (1 if fds > 3 * d[2] else 0)) if d else -(-fds // 3)
            a.append([r['win_s'] / (10 if r['cfg'] == 'up' else 5), nck])
            b.append(r['dev_kw'])
        x = solve(a, b)
        res = [sum(c * v for c, v in zip(x, row_)) / y - 1 for row_, y in zip(a, b)]
        print('| %d | %s | %.0f | %.0f | %.1f | %s |' % (page, rs, x[0], x[1], x[1] / 1024,
                                                     ' '.join('%+.0f' % (100 * q) for q in res)))

print('\n== 10/s SUMMARY: mean of the 2 reps per cell (90-s windows; 1 or 2 checkpoints per window, so phase matters)')
print('| rs | src | cfg | disk KiB/s rep1, rep2 | mean | mean disk KiB/art | ckpt pages rep1, rep2 | mean / today |')
print('|---|---|---|---|---|---|---|---|')
for setting, rows, src in [('rs16k-p4k', old, '2026-11-14'), ('rs32k-p4k', runs, 'r3 job12'),
                           ('rs64k-p4k', runs, 'r3 job12'), ('rs128k-p4k', old, '2026-11-14')]:
    base = None
    for cfg in CFGS:
        v = pick(rows, setting, 10, cfg)
        if not v:
            continue
        mk = sum(kibs(r) for r in v) / len(v)
        if cfg == 'up':
            base = mk
        print('| %s | %s | %s | %s | %.1f | %.1f | %s | %s |' % (
            v[0]['rs'], src, NAME[cfg], ', '.join('%.1f' % kibs(r) for r in v), mk,
            sum(dka(r) for r in v) / len(v), ', '.join('%.0f' % decomp(r)[1] for r in v),
            '%.2f' % (mk / base) if base else '-'))

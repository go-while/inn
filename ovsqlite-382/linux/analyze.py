#!/usr/bin/env python3
# usage: analyze.py LOG... : markdown tables from the RESULT lines of ovsqlite-fsync logs
import json
import re
import statistics
import sys

ORDER = ['up', 'ttl1', 'proto']
NAME = {'up': 'upstream', 'ttl1': 'upstream, transtimelimit 1', 'proto': 'prototype'}


def mr(xs, fmt='%.0f'):
    xs = [x for x in xs if x is not None]
    if not xs:
        return '-'
    m = statistics.median(xs)
    if min(xs) == max(xs):
        return fmt % m
    return '%s (%s–%s)' % (fmt % m, fmt % min(xs), fmt % max(xs))


for path in sys.argv[1:]:
    res, vis = [], {}
    host = path
    for ln in open(path, errors='replace'):
        m = re.search(r'RESULT (\{.*\})$', ln)
        if m and 'VISRESULT' not in ln:
            res.append(json.loads(m.group(1)))
        m = re.search(r'VISRESULT (\{.*\})$', ln)
        if m:
            d = json.loads(m.group(1))
            vis[d['cfg']] = d['samples']
        m = re.match(r'== (Ubuntu.*)', ln)
        if m and host == path:
            host = m.group(1)
    rates = sorted({r['rate'] for r in res})
    print('\n### %s (%s)\n' % (host, path))
    errs = [r for r in res if 'error' in r]
    if errs:
        print('errors:', len(errs), [(r['cfg'], r['rate'], r['rep'], r['error']) for r in errs])
    print('Traced runs (strace attached), per %ss window, median (range) of %s runs:\n' % (
        res[0].get('win_s', '?') if res else '?', 'n'))
    print('| rate/s | config | articles | commits | WAL frames | frames/article | sync calls | '
          'sync calls by file | checkpoints | WAL bytes | db bytes | max WAL | est. checkpoints/h | est. fdatasync/h |')
    print('|---|---|---|---|---|---|---|---|---|---|---|---|---|---|')
    for rate in rates:
        for cfg in ORDER:
            rs = [r for r in res if r['cfg'] == cfg and r['rate'] == rate and r['traced']
                  and 'error' not in r]
            if not rs:
                continue
            byf = {}
            for r in rs:
                for k, v in r['sync'].items():
                    byf.setdefault(k, []).append(v)
            byf_s = ', '.join('%s %s' % (k.replace('fdatasync', 'fds'), mr(v + [0] * (len(rs) - len(v))))
                              for k, v in sorted(byf.items())) or 'none'
            print('| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |' % (
                rate, NAME[cfg], mr([r['accepted'] for r in rs]), mr([r['commits'] for r in rs]),
                mr([r['wal_frames'] for r in rs]),
                mr([r['wal_frames'] / max(r['accepted'], 1) for r in rs], '%.2f'),
                mr([r['sync_n'] for r in rs]), byf_s, mr([r['wal_trunc'] for r in rs]),
                mr([r['wal_bytes'] / 1024 for r in rs], '%.0fK'),
                mr([r['db_bytes'] / 1024 for r in rs], '%.0fK'),
                mr([r['wal_max'] / 1024 for r in rs], '%.0fK'),
                mr([r['wal_frames'] * 3.6 / r['win_s'] for r in rs], '%.1f'),
                mr([3 * r['wal_frames'] * 3.6 / r['win_s'] for r in rs], '%.0f')))
    print('\nUntraced runs (no strace), per window, median (range):\n')
    print('| rate/s | config | articles | CPU ms (schedstat) | CPU ms (utime+stime) | '
          'write_bytes | wchar | WAL size at end |')
    print('|---|---|---|---|---|---|---|---|')
    for rate in rates:
        for cfg in ORDER:
            rs = [r for r in res if r['cfg'] == cfg and r['rate'] == rate and not r['traced']
                  and 'error' not in r]
            if not rs:
                continue
            print('| %s | %s | %s | %s | %s | %s | %s | %s |' % (
                rate, NAME[cfg], mr([r['accepted'] for r in rs]),
                mr([r['sched_ms'] for r in rs], '%.1f'), mr([r['cpu_ms'] for r in rs]),
                mr([r['write_bytes'] / 1024 for r in rs], '%.0fK'),
                mr([r['wchar'] / 1024 for r in rs], '%.0fK'),
                mr([r['wal_size_end'] / 1024 for r in rs], '%.0fK')))
    print('\nTracer overhead (CPU ms schedstat, traced vs untraced, median):\n')
    for rate in rates:
        row = []
        for cfg in ORDER:
            t = [r['sched_ms'] / r['win_s'] for r in res if r['cfg'] == cfg and r['rate'] == rate and r['traced'] and 'error' not in r]
            u = [r['sched_ms'] / r['win_s'] for r in res if r['cfg'] == cfg and r['rate'] == rate and not r['traced'] and 'error' not in r]
            if t and u:
                row.append('%s %.2f vs %.2f ms/s' % (cfg, statistics.median(t), statistics.median(u)))
        print('- %s/s: %s' % (rate, '; '.join(row)))
    print('\nVisibility (s from 235 to GROUP count change):\n')
    for cfg in ORDER:
        if cfg in vis:
            print('- %s: %s' % (NAME[cfg], ', '.join(str(x) for x in vis[cfg])))

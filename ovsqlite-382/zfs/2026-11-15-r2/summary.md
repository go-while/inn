> **Superseded.** This is the working summary written right after round r2 (2026-11-15), kept as it was.
> Its pwrite figures use the older DTrace window, and its checkpoint estimate ("17–18 MiB") came from comparing two single runs;
> the later round r3 gives roughly 7–8 MiB per checkpoint from a fit. Use `../../RESULTS.md` instead.

# ovsqlite on ZFS, repeats and long runs (VM 120, 2026-11-15)

Caveat (ovz.py before the 2026-11-15 fix, lines 394-399 and 423): the DTrace window opens 1.5 s before the s0 snapshot and closes after s1, so pwrite KiB/article can include one extra commit, mostly for today. Disk KiB/s (iostat) is not affected.

Formulas: disk KiB/s = dev_kw / win_s; pwrite KiB/article = dt_W_pwrite / 1024 / accepted; disk KiB/article = dev_kw / accepted.
"combined" = 3 new runs + the 2 rs128k-p4k runs of 2026-11-14 (n=5); ranges are min–max of per-run values.

Host check: FreeBSD 15.1-RELEASE-p6, hw.ncpu 4, hw.physmem 3,180,818,432 B, vfs.zfs.txg.timeout 5; pool ovz ONLINE on da1, recordsize 128K, compression on, atime on; binaries .up 78155ea2…caefbf, .proto 0022b210…e412fcc; seeds present; booted ~11:44 UTC (uptime). ovz.py was in /root, copied to /var/tmp/ovz.

| Feed | Config | disk KiB/s new (n=3) / combined (n=5), mean | pwrite KiB/article new / combined, mean | disk KiB/article new / combined, mean |
|---|---|---|---|---|
| idle | today | 0 / 0 | – | – |
| 0.5/s | today | 29.1–33.6 / 27.3–33.6, 30.3 | 9.0–10.9 / 9.0–11.2, 10.2 | 57.1–67.3 / 54.6–67.3, 60.4 |
| 0.5/s | ttl1 | 71.5–75.4 / 70.2–83.6, 74.7 | 12.8–13.5 / 12.8–13.5, 13.1 | 139.4–147.7 / 139.4–167.9, 147.6 |
| 0.5/s | proto | 72.5–74.1 / 70.4–82.1, 74.5 | 12.6–13.6 / 12.4–13.6, 12.9 | 145.8–150.6 / 141.2–163.7, 149.6 |
| 2/s | today | 37.1–39.5 / 35.1–40.4, 37.9 | 8.6–9.3 / 8.6–9.4, 9.0 | 18.3–19.5 / 17.2–19.8, 18.6 |
| 2/s | ttl1 | 82.7–85.0 / 78.6–90.7, 84.3 | 10.5–12.0 / 10.5–12.0, 11.4 | 40.7–42.3 / 39.1–45.7, 41.8 |
| 2/s | proto | 79.9–86.5 / 79.9–89.1, 84.1 | 12.6–13.6 / 12.6–13.6, 13.3 | 39.1–43.1 / 39.1–43.9, 41.6 |
| 10/s | today | 220.2–228.3 / 213.9–228.3, 221.0 | 13.7–14.1 / 13.7–14.1, 13.9 | 21.5–22.4 / 21.1–22.4, 21.6 |
| 10/s | ttl1 | 243.2–253.3 / 238.4–253.3, 246.6 | 15.0–15.3 / 14.8–15.3, 15.1 | 24.0–25.0 / 23.3–25.2, 24.4 |
| 10/s | proto | 225.3–226.2 / 220.9–233.4, 226.2 | 14.0–14.7 / 14.0–14.7, 14.3 | 21.9–22.2 / 21.5–23.6, 22.2 |

Derived (combined means): pwrite alternatives vs today 0.5/s +29/+27 %, 2/s +27/+48 %, 10/s +9/+3 %. Disk extra 0.5/s +44.4/+44.2 KiB/s, 2/s +46.4/+46.2, 10/s +25.6 (+12 %) / +5.2 (+2 %).
Idle: only `up` is measured (ovz.py:467 runs rate 0 with up only).

WAL frames per article (90-s runs): 0.5/s today 2.24–2.78, ttl1 3.19–3.35, proto 3.09–3.38; 2/s 2.14–2.33 / 2.62–2.99 / 3.13–3.38; 10/s 2.02–2.06 / 2.42–2.52 / 2.05–2.27.
Checkpoint pages: 0 in every 0.5 and 2/s 90-s run; 1156–1369 per window at 10/s (5–6 fdatasync).

Long runs (one each):

| Run | accepted | disk KiB/s | disk KiB/article | pwrite KiB/article | frames/article | ckpt pages | fdatasync | WAL end (B) | CPU s |
|---|---|---|---|---|---|---|---|---|---|
| 0.04/s up | 72 | 17.2 | 431 | 13.4 | 3.32 | 0 | 0 | 992952 | 0.06 |
| 0.04/s ttl1 | 72 | 17.7 | 443 | 13.2 | 3.28 | 0 | 0 | 1001192 | 0.06 |
| 0.04/s proto | 72 | 18.1 | 454 | 13.2 | 3.28 | 0 | 0 | 1001192 | 0.06 |
| 0.5/s 600 s up | 301 | 39.7 | 79 | 10.1 | 2.50 | 0 | 0 | 3201272 | 0.11 |
| 0.5/s 600 s ttl1 | 300 | 103.6 | 207 | 19.2 | 3.23 | 468 | 2 | 0 | 3.85 |
| 0.5/s 600 s proto | 300 | 105.5 | 211 | 19.4 | 3.26 | 472 | 3 | 8272 | 0.19 |

The 0.04/s feeder is strictly periodic (one article every 25 s, ovz.py:407): no bursts in that data.
One checkpoint adds roughly 17–18 MiB on disk on 128K records (estimate from the 600-s runs).
Odd: CPU > 1 s in 2 of 33 runs (job 2b run 5 proto 2.42 s; job 7 ttl1 3.85 s), bytes normal.

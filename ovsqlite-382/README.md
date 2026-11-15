# ovsqlite write measurements for InterNetNews/inn#382

Scripts behind the numbers in InterNetNews/inn#382.
They compare three ways of running ovsqlite-server:

| Name | What |
|---|---|
| `up` | stock server, `transtimelimit` 10 s (default) |
| `ttl1` | stock server with `transtimelimit: 1` in ovsqlite.conf |
| `proto` | the prototype on this branch (commit 47472af18): also commits after 250 ms without another write |

They are measurement scripts, not part of the test suite, and not meant to be merged.
Both run as root on a disposable test host, start INN as user `news`, and bound every wait.
Don't run them on a machine you care about.

## zfs/: bytes on ZFS with a filled database (FreeBSD)

`ovz.py` (Python 3.12, FreeBSD 15.1).
For each run it creates a fresh ZFS dataset, copies in a seeded overview database (300,000 articles in 200 groups), starts innd with the `trash` storage method (so only the overview writes), feeds articles at a fixed rate, and measures:

- disk bytes: `iostat -I` on the pool's disk;
- bytes ovsqlite-server writes: DTrace on `pwrite`;
- ZFS dataset and ZIL kstats.

It needs:

- INN installed in `/opt/inn-ovz`, with `bin/ovsqlite-server.up` (stock build) and `bin/ovsqlite-server.proto` (built from this branch);
- a ZFS pool `ovz` on its own disk `da1` (edit `POOL` and `DEV` at the top);
- DTrace (`kldload dtraceall`), and a loopback alias 127.0.0.2, which it adds itself.

Parameters are one JSON argument that overrides the `P` dict at the top, for example:

```
python3.12 ovz.py '{"rates":[0.5,2],"reps":3,"win":90,"settings":[{"name":"rs128k-p4k","rs":"128K","page":4096}]}'
```

Each run appends one JSON line to `/var/tmp/ovz/results.jsonl`.
Formulas: disk KiB/s = `dev_kw / win_s`; ovsqlite-server KiB per article = `dt_W_pwrite / 1024 / accepted`.
`results.jsonl` here holds the 60 runs of 2026-11-14: 3 settings (recordsize 128K or 16K, page size 4 or 16 KiB), rates 0.5, 2 and 10 articles/s, the three configurations, 2 repeats, 90 s windows, plus idle runs.

## linux/: commits, syncs and bytes on an empty database (Linux)

`ovf.py` measures commits, WAL frames, fsync/fdatasync calls (under strace) and `wchar` / `write_bytes` from `/proc/<pid>/io` at low feed rates, plus how long a new article takes to show in GROUP.
`mkextra.sh 'PARAMS_JSON' TIMEOUT_S OUT` wraps it into one shell script for a host that already has INN built in `/var/tmp/inn-e2e-build/src` and installed in `/opt/inn-e2e`.
The script builds the prototype server from `proto.patch` (the server change of 47472af18) next to the stock one, then runs `ovf.py`.
`analyze.py LOG...` turns the `RESULT` lines of such logs into tables.

The full-speed runs mentioned in #382 (about 14,000 articles/s) used a different harness, which depends on one particular server and its article archive; it isn't included.

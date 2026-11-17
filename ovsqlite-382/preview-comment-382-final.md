Byte counts, measured on FreeBSD 15.1-RELEASE-p6 in a VM (4 vCPUs, 3 GiB) with SQLite 3.54.1 and `walmode: true`, with the overview alone on a ZFS pool (OpenZFS 2.4.2, lz4, ashift 12, atime on, sync standard, no separate log device) on a 5 GiB virtual disk.
That disk is a zvol (16K volblocksize, lz4) on the host's ZFS pool of 3-way HDD mirrors; the disk figures below are what the guest wrote to it, not what the host wrote below it.
The overview held 300,000 articles (57 MB file, 4 KiB SQLite pages).
Every measurement below is in, or follows from, [RESULTS.md](https://github.com/go-while/inn/blob/c1b2388b861c3595d03d404d5a25916cf3adadc2/ovsqlite-382/RESULTS.md), generated from the raw files next to it, with the runs it comes from and the caveats.
"Today" is the stock server, which commits 10 s after a transaction starts; the prototype (commit 47472af18 on our fork) also commits once 250 ms pass without a further write.

Short answer:

- WAL growth: a checkpoint runs once the WAL holds 1000 frames (the default `walcheckpointthreshold`) and no transaction is open, and truncates it, so as long as no reader blocks it the WAL stays below about 4.1 MB (decimal, 1000 frames of 4 KiB pages) plus one transaction in all three configurations; the alternatives fill it more often.
  No reader was connected; the largest WAL seen at a window end was 4.3 MB, at 10 articles/s.
  With 16 KiB pages (a separate set of runs) the bound is about 16.4 MB plus one transaction; the largest seen was 14.0 MB (Today at 2/s).
- B-tree: all three use the same SQL insert/update path per article.
  Splits and other B-tree work are included in the page counts but were not counted separately, so these measurements do not establish that their rebalancing work is equal; expiry (deletes, which merge and redistribute pages) was not run.
  An article committed alone wrote 3.2–3.3 WAL frames (each frame is one changed page); batched, Today wrote 2.26–2.61 per article, as articles share pages.
- What SQLite writes: over whole checkpoint cycles the alternatives write about 1.2 times Today's bytes at 0.5 articles/s (1.19–1.20 with Today's checkpoint size measured in a 30-minute window) and 1.2 (`transtimelimit: 1`) and 1.35 (prototype) times at 2 articles/s (estimate).
  These bytes come from SQLite, so they should not depend on the filesystem (measured on ZFS only); only the disk figures below are specific to ZFS.
- What reaches the disk on ZFS: 2.6–2.7 times Today's bytes at 0.5 articles/s and 1.5–2.1 times at 2 articles/s in the 10-minute windows (which caught 0/1/1 and 2/3/4 checkpoints).
  In windows that held about the checkpoints due (Today 30 minutes, the alternatives 10 minutes) it was 2.3–2.4 times at 0.5/s (four alternative runs on one day, all against the same 30-minute Today run).
  At 2/s the 1.5–2.1 is for the 10-minute windows, not adjusted for checkpoints.
  At 0.04 articles/s, where all three commit every article alone, the alternatives wrote 0.89–0.93 times Today's disk bytes in one round and 1.03–1.05 in another (30-minute runs); the same configuration differed by 15–32 % between the rounds, so these runs cannot tell the three apart.
  At 10 articles/s, evenly spaced, in 90-second windows, the prototype wrote 0.96–1.02 times Today's disk bytes per second in the 7 of 8 pairs with the same number of checkpoints (recordsize 16K to 128K); `transtimelimit: 1` wrote 1.15–1.23 times in the 3 pairs with the same number (16K, 64K) and 1.65–1.77 times in the 5 where it had one checkpoint more (the prototype's one such pair: 1.52).
  The prototype had data in as few transaction groups as Today (8–9 of 20–21), `transtimelimit: 1` in 18–19.

**What SQLite writes** (bytes ovsqlite-server passes to `pwrite`, per accepted article; 600-second windows, one run per ZFS recordsize 16K–128K, ranges are min–max over those four):

| | Today | `transtimelimit: 1` | Prototype |
|---|---|---|---|
| 0.5 articles/s, measured | 10.1–10.5 KiB | 19.1–19.6 KiB | 19.0–19.7 KiB |
| 0.5 articles/s, over whole checkpoint cycles (estimate) | 16.1–16.5 KiB | 19.0–19.6 KiB | 18.9–19.6 KiB |
| 2 articles/s, measured | 13.0–13.2 KiB | 16.4–16.9 KiB | 19.4–19.8 KiB |
| 2 articles/s, over whole checkpoint cycles (estimate) | 14.4–14.8 KiB | 17.1–17.7 KiB | 19.2–19.8 KiB |
| WAL frames per article, 0.5 articles/s | 2.50–2.61 | 3.23–3.33 | 3.22–3.33 |
| WAL frames per article, 2 articles/s | 2.26–2.30 | 2.81–2.88 | 3.26–3.33 |

- The windows have a fixed length, which favours Today: at 0.5/s only the alternatives reached a checkpoint within a 10-minute window.
  The estimate rows spread each checkpoint over the 1000 frames that trigger it (details in RESULTS.md).

**What reached the disk on ZFS** (bytes written to the pool's virtual disk; same 600-second runs, idle: 90 seconds):

| | Today | `transtimelimit: 1` | Prototype |
|---|---|---|---|
| idle (90 s, 128K) | 0 | 0 | 0 |
| 0.5 articles/s, KiB/s | 32–43 | 86–112 | 86–114 |
| 2 articles/s, KiB/s | 60–72 | 110–132 | 105–135 |
| 0.5 articles/s, KiB per article | 63–86 | 171–223 | 171–225 |
| 2 articles/s, KiB per article | 30–36 | 55–65 | 53–68 |

- In the 10-minute windows up to 4 (0.5/s) and 10 (2/s) articles were sent at once at the window start, held back while the tracing attached; repeating the 128K cells without that changed the disk bytes by at most 4 % against a run on the same day.
- Between days the same 128K cells moved by up to 19 % (0.5/s) and 8 % (2/s); the ratio to Today moved from 2.63–2.69 to 2.58–2.66 at 0.5/s and from 1.83–1.88 to 1.68–1.76 at 2/s.
- On this pool, 90-second runs without any checkpoint gave the alternatives 2.5 times Today's disk bytes at 0.5/s and 2.2 times at 2/s (recordsize 16K and 128K), so most of the disk gap is not from checkpoints.
  Counted directly (recordsize 128K), runs without a checkpoint averaged 0.49–0.56 MiB per 5-second transaction group that contained a write, and that was all their disk bytes; the alternatives wrote in 98–100 % of the groups at 0.5 and 2/s, Today in 41–48 %.
  At 0.04/s every article had a group of its own: 500–570 KiB on disk for about 13 KiB written by SQLite (431–454 KiB in an earlier round without group counts).
  The pool held only the overview; on a pool with other writes, part of that cost would be shared (inferred, not measured).

Rates between 0.04 and 0.5 and between 2 and 10 articles/s, and bursty arrival, were not measured.
The earlier comment's figures came from an empty database on Linux and counted SQLite's bytes.
Its "at most about 45 KB written … per second" is of the same order as SQLite's bytes here (33–40 KiB/s for the alternatives at 2/s), but its checkpoint rates were too low for Today ("1–4 checkpoints/hour") and slightly for `transtimelimit: 1` ("4–19"): at 2/s the WAL frames come to about 16 checkpoints an hour for Today (the 10-minute windows caught 2) and about 20–21 and 24 for the alternatives (estimate).
The disk table shows what ZFS adds.
On ZFS at 0.5–2 articles/s the alternatives' write cost is no longer neutral, so our earlier "cost doesn't decide between the options" doesn't hold as stated; whether it decides for a real, quiet and bursty feed these measurements can't say.

Not covered: other filesystems (UFS, ext4), readers (which can delay checkpoints), expiry, crossposts (one group per article here), real overview data (probably less compressible than these synthetic rows; not measured), larger databases.

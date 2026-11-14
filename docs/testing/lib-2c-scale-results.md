# LIB-2c scale qualification: synthetic run

**Result: every catalogue route meets PERF-05 (p95 ≤ 1 s) at idle, during a
full rescan and during a probe turn; SCALE-01's repeat scan probes nothing.**
This is the synthetic half of LIB-2c Task 8. The run on a real library of the
same size (Task 8's first bullet) is still to do; its numbers go below this
section.

## Setup

- **Harness:** `server/internal/library/scale_test.go` (build tag `scale`, not
  part of `make check`):

  ```sh
  go -C server test -tags scale -run TestCatalogueAtScale -v -timeout 40m ./internal/library/
  SCALE_EXPLAIN=1 go -C server test -tags scale ...   # also times and EXPLAINs the slow queries
  ```

- **Catalogue:** 5,000 movies (1,000 with two versions, 200 with three) and
  1,000 shows (999 with 3 seasons of 10 episodes, one with 30 seasons):
  39,297 items, 36,470 files. The files are 10-byte placeholders, and probes
  come from a fake prober (`benchProber`), so scan and probe times measure the
  catalogue's own work, not ffprobe or disk reads.
- **Timing:** server time, from calling the catalogue handler directly (no
  network or browser), with the principal an admin granted all libraries.
  Idle: 30 rounds of every route. Under load: every route, round after round,
  while the load runs; a round still running when the load ends is dropped,
  and the load is repeated until every route has at least 20 samples taken
  under it (full rescan: 7 runs, 20 samples; probe turn: 2 runs, 21 samples).
  p50 and p95 are nearest-rank (the ⌈q·n⌉-th smallest sample), so with n ≥ 20
  p95 is not just the maximum.
- **Machine:** a 12-core x86-64 Linux container, SQLite in WAL mode on local
  disk, from `main` at `0fc22e5`. Other builds were running on the same machine
  during this run, so idle numbers are noisier than under load in places.
- **Facts:** `benchProber` takes a file's resolution from its path length and
  its audio language from a hash of the path, so every resolution comes with
  every language. The combined filters (resolution + audio) use a pair that
  some file has, and the harness fails if either returns no items.
- **The TV resolution filter is a worst case.** All synthetic episode paths have
  the same length, so every episode is 1080p. The `720p` TV filter matches
  nothing and has to walk every show's episodes.

## SCALE-01

| Root | First scan | First probe | Repeat scan | Repeat probe |
|---|---|---|---|---|
| Movies (6,200 files) | 1.7 s, 6,200 added, 0 unplaced | 2.1 s, 6,200 stored, 0 failed | 0.50 s, 0 changed | 0 queued |
| TV (30,270 files) | 19.2 s, 30,270 added, 0 unplaced | 12.4 s, 30,270 stored, 0 failed | 2.6 s, 0 changed | 0 queued |

Peak queue isn't measured here: the harness calls `ScanRoot` and `ProbeDue`
directly, without the `Scheduler`.

## SCALE-02 / PERF-05: server time (p50 / p95)

| Route | Idle (n = 30) | Full rescan (4.9 s, n = 20) | Probe turn (14 s, every file, n = 21) |
|---|---|---|---|
| `listLibraries` | 0.19 / 0.28 ms | 0.17 / 0.30 ms | 0.17 / 0.66 ms |
| facets, movies | 77 / 108 ms | 82 / 114 ms | 75 / 91 ms |
| facets, TV | **493 / 820 ms** | **448 / 655 ms** | **423 / 499 ms** |
| items by title (first page) | 2.9 / 8.9 ms | 3.9 / 6.3 ms | 4.2 / 5.5 ms |
| items `from=M` | 2.5 / 11 ms | 2.4 / 5.0 ms | 2.2 / 3.4 ms |
| items, last page (paged by 200) | 5.2 / 13 ms | 5.8 / 9.0 ms | 4.6 / 6.0 ms |
| items by added / by year | 2.9 / 9.5; 12 / 43 ms | 3.3 / 11; 14 / 24 ms | 2.6 / 3.4; 11 / 16 ms |
| items genre / year range | 3.6 / 10; 6.7 / 17 ms | 4.1 / 9.4; 7.6 / 14 ms | 3.2 / 4.4; 6.1 / 8.0 ms |
| items resolution / hdr / sdr (movies) | 45–46 / 77–95 ms | 44–47 / 57–61 ms | 43–45 / 53–56 ms |
| items audio (movies) | 69 / 120 ms | 64 / 122 ms | 64 / 74 ms |
| items resolution + audio (movies, 4k + jpn) | 60 / 85 ms | 54 / 68 ms | 55 / 66 ms |
| items TV by title | 1.5 / 2.2 ms | 2.3 / 3.4 ms | 2.1 / 2.8 ms |
| items TV resolution (720p, matches nothing) | **473 / 656 ms** | **383 / 501 ms** | **348 / 379 ms** |
| items TV resolution + audio (1080p + fra) | 92 / 132 ms | 94 / 130 ms | 90 / 124 ms |
| seasons of a 30-season show | 1.2 / 1.7 ms | 1.9 / 3.5 ms | 1.7 / 2.3 ms |
| episodes of a season | 0.83 / 1.2 ms | 0.72 / 1.2 ms | 0.73 / 1.2 ms |
| `getItem`, 3-version movie | 0.60 / 1.2 ms | 0.52 / 0.65 ms | 0.49 / 0.63 ms |
| `getItem`, 30-season show | 0.29 / 0.47 ms | 0.26 / 0.49 ms | 0.24 / 0.32 ms |
| `Resolver.Resolve` (#131 note 1) | 0.11 / 0.14 ms | 0.09 / 0.17 ms | 0.09 / 0.10 ms |

## Routes over 100 ms: query plans

Every part uses an index. The cost is volume: each query visits all ~30,000
episodes once.

- **TV facets:** audio 269 ms and resolution 248 ms in this run (201–220 and
  128–133 ms in a quieter earlier run); genres and years are under 4 ms. Both slow queries are `SEARCH i USING INDEX
  sqlite_autoindex_library_items_2 (library_id=?)`, then `v` by `item_id`,
  `f` by `id` (and `a` by `file_id`), with a temp B-tree for DISTINCT.
- **TV file filters:** the first-page COUNT takes 192 ms and the page
  216 ms (153–158 and 151 ms in the earlier run): `SEARCH i USING INDEX library_items_added/_browse (library_id=?)`,
  plus a correlated subquery per show over `descendantFiles` (a `UNION ALL` of
  both levels through `library_items_children`). That's #168's "show file
  filters walk every show's episodes in both the page query and the COUNT".

**Not changed:** all of these are under target. A measured option if a real
library on slower hardware misses it: read a file's library from its root
(`library_roots.library_id`) instead of joining through `library_items`. That
took TV facet audio from 201 to 117 ms and resolution from 128 to 59 ms (earlier run). It
scans `library_files` (it grows with every library), and it has to keep
excluding unplaced files (`EXISTS` on `library_versions`).

## Shared-database reads (#168) and the M1 fallback (#170)

- **No queueing behind writes.** Catalogue reads open with `ReadOnly: true`,
  which modernc turns into a plain deferred `BEGIN` (the `_txlock=immediate`
  setting applies only to write transactions), and WAL lets them read while
  the scanner writes. No read under either load was slower than the same
  route's idle p95 by more than noise, and none came near the 5 s
  `busy_timeout`. The worst reads were the two TV routes that walk every
  episode (TV facets 655 ms and TV resolution 501 ms p95 during the rescan);
  every other route stayed under 200 ms.
- **The M1 fallback never ran.** All 41 `Resolve` calls under load (and 30
  idle) succeeded,
  so the catalogue's DB error path never fired. That matches decision 11.

## Not covered here

- A real library of this size (Task 8's first bullet).
- Resolve's share of a 4k range request (#131 note 1) needs real media. On its
  own, Resolve takes about 0.1 ms.
- Browser time (after UI-2 PR C).

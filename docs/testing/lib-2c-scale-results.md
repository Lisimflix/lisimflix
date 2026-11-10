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
  while the load runs (at least 5 rounds).
- **Machine:** a 12-core x86-64 Linux container, SQLite in WAL mode on local
  disk, from `main` at `f085e10`.
- **TV filters are a worst case.** All synthetic episode paths have the same
  length, so `benchProber` gives every episode the same resolution and
  language. A TV filter for any other value matches nothing and has to walk
  every show's episodes.

## SCALE-01

| Root | First scan | First probe | Repeat scan | Repeat probe |
|---|---|---|---|---|
| Movies (6,200 files) | 1.5 s, 6,200 added, 0 unplaced | 2.0 s, 6,200 stored, 0 failed | 0.48 s, 0 changed | 0 queued |
| TV (30,270 files) | 14.5 s, 30,270 added, 0 unplaced | 11.7 s, 30,270 stored, 0 failed | 2.2 s, 0 changed | 0 queued |

Peak queue isn't measured here: the harness calls `ScanRoot` and `ProbeDue`
directly, without the `Scheduler`.

## SCALE-02 / PERF-05: server time (p50 / p95)

| Route | Idle | Full rescan (6.9 s) | Probe turn (16 s, every file) |
|---|---|---|---|
| `listLibraries` | 0.13 / 0.18 ms | 0.16 / 0.17 ms | 0.15 / 0.29 ms |
| facets, movies | 55 / 66 ms | 56 / 73 ms | 78 / 115 ms |
| facets, TV | **347 / 385 ms** | **346 / 432 ms** | **413 / 866 ms** |
| items by title (first page) | 2.1 / 2.9 ms | 3.8 / 4.2 ms | 4.7 / 5.7 ms |
| items `from=M` | 1.7 / 2.5 ms | 2.7 / 3.0 ms | 2.3 / 3.2 ms |
| items, last page (paged by 200) | 3.6 / 4.4 ms | 4.6 / 5.5 ms | 5.0 / 6.0 ms |
| items by added / by year | 2.0 / 2.5; 8.8 / 10 ms | 3.2 / 3.3; 13 / 15 ms | 2.7 / 3.7; 13 / 17 ms |
| items genre / year range | 2.5 / 3.6; 4.4 / 5.3 ms | 4.1 / 5.6; 6.5 / 7.7 ms | 3.7 / 5.2; 7.5 / 18 ms |
| items resolution / hdr / sdr (movies) | 34 / 37 ms (each about the same) | 37–40 / 45–50 ms | 46–52 / 78–93 ms |
| items audio (movies) | 46 / 48 ms | 50 / 65 ms | 72 / 107 ms |
| items resolution + audio (movies) | 61 / 67 ms | 71 / 99 ms | 97 / 189 ms |
| items TV by title | 1.2 / 1.7 ms | 1.3 / 2.6 ms | 2.1 / 2.7 ms |
| items TV resolution | **312 / 355 ms** | **345 / 370 ms** | **382 / 515 ms** |
| items TV resolution + audio | **316 / 350 ms** | **324 / 379 ms** | **409 / 498 ms** |
| seasons of a 30-season show | 0.88 / 1.1 ms | 1.1 / 2.6 ms | 2.4 / 3.9 ms |
| episodes of a season | 0.54 / 0.81 ms | 0.79 / 1.0 ms | 0.82 / 1.3 ms |
| `getItem`, 3-version movie | 0.42 / 0.60 ms | 0.49 / 0.62 ms | 0.52 / 0.96 ms |
| `getItem`, 30-season show | 0.21 / 0.54 ms | 0.22 / 0.30 ms | 0.24 / 0.89 ms |
| `Resolver.Resolve` (#131 note 1) | 0.07 / 0.12 ms | 0.08 / 0.11 ms | 0.10 / 0.14 ms |

## Routes over 100 ms: query plans

Every part uses an index. The cost is volume: each query visits all ~30,000
episodes once.

- **TV facets:** audio 201–220 ms and resolution 128–133 ms; genres and years
  are under 4 ms. Both slow queries are `SEARCH i USING INDEX
  sqlite_autoindex_library_items_2 (library_id=?)`, then `v` by `item_id`,
  `f` by `id` (and `a` by `file_id`), with a temp B-tree for DISTINCT.
- **TV file filters:** the first-page COUNT takes 153–158 ms and the page
  151 ms: `SEARCH i USING INDEX library_items_added/_browse (library_id=?)`,
  plus a correlated subquery per show over `descendantFiles` (a `UNION ALL` of
  both levels through `library_items_children`). That's #168's "show file
  filters walk every show's episodes in both the page query and the COUNT".

**Not changed:** all of these are under target. A measured option if a real
library on slower hardware misses it: read a file's library from its root
(`library_roots.library_id`) instead of joining through `library_items`. That
took TV facet audio from 201 to 117 ms and resolution from 128 to 59 ms. It
scans `library_files` (it grows with every library), and it has to keep
excluding unplaced files (`EXISTS` on `library_versions`).

## Shared-database reads (#168) and the M1 fallback (#170)

- **No queueing behind writes.** Catalogue reads open with `ReadOnly: true`,
  which modernc turns into a plain deferred `BEGIN` (the `_txlock=immediate`
  setting applies only to write transactions), and WAL lets them read while
  the scanner writes. Under both loads reads slowed by at most about 2.3×
  (CPU shared with the writer), and none came near the 5 s `busy_timeout`. The
  worst reads were the three TV routes (498–866 ms p95 during the probe turn);
  every other route stayed under 200 ms.
- **The M1 fallback never ran.** All 14 `Resolve` calls under load (and 30
  idle) succeeded,
  so the catalogue's DB error path never fired. That matches decision 11.

## Not covered here

- A real library of this size (Task 8's first bullet).
- Resolve's share of a 4k range request (#131 note 1) needs real media. On its
  own, Resolve takes about 0.1 ms.
- Browser time (after UI-2 PR C).

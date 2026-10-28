# LIB-2b Watcher, Schedule and `serve` Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the LIB-2a catalogue in step with the disk while the server runs. An inotify watcher feeds folder-scoped scans, a periodic whole-root scan is the safety net, offline roots are retried once they are readable again, and probes run in bounded turns. All of it is wired into `serve`, with a small `library` command to add libraries until the admin screens exist.

**Architecture:**
- **`Scanner.Scan`:** LIB-2a's scan, generalised to a scope (a folder or a file inside the root). A watcher event rescans only what changed, never the whole root. Only whole-root runs change a root's state, count towards retirement or flag duplicate pairs.
- **`watcher`:** fsnotify (inotify) over every folder the scan would walk. Events are hints; they become scopes.
- **`Scheduler`:** two lanes. The scan lane runs one scan at a time across all roots; the probe lane runs one `ProbeDue` turn at a time with LIB-1's 2 workers. Watcher hints, the 6-hour scan, rechecks, restart reconciliation and Scan now all feed one coalescing queue per root.
- **`serve`:** registers the catalogue migrations, runs the scheduler in the background and stops it before the media root and database close.

**Tech Stack:** Go 1.27.3, `modernc.org/sqlite` through OPS-2's `storage`, LIB-1's `library` and `probe` packages, and one new dependency: `github.com/fsnotify/fsnotify` v1.10.1 (BSD-3-Clause; it only needs `golang.org/x/sys`, already in `go.sum`).

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md):
- §3.1: "inotify watch plus a periodic full scan (default 6h) as a safety net".
- §3.6: watcher events are hints; overflow, remount and restart trigger reconciliation; periodic scans cover missed events and network mounts; duplicate work coalesces; cancel keeps completed work and stops owned probes; Scan now; status with reasons.
- §10.2: background jobs persist intent/status, idempotent retry, crashes reconcile on restart.
- §11.2: queues have capacity; 2 probes; operators can pause maintenance.
- §13: SCAN-01, SCALE-01. §9.4: the LIB-2 row.

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **LIB-2** (resilient catalogue, P0 / G1), slice **2b** of 3: watcher, schedule, status and `serve` wiring. 2a ([#90](https://github.com/Lisimflix/lisimflix/pull/90)) is the store and scan engine; 2c is the read API on CTR-2's shapes, the admin actions and the actual-scale SCALE-02/PERF-05 runs |
| Issue | #77 |
| Owner / reviewers | Lammy (claimant) / Pete (LIB-1 owner) and Ondo |
| Acceptance | **SCAN-01** with the scheduler: an offline root is retried only once it can be read, then reconciled; nothing goes missing meanwhile; a dropped watch (unmount) is noticed and re-armed; interrupted runs are recovered at startup. **§3.6**: watched changes found by scoped scans; hints coalesce; a busy folder is still scanned; Scan now, Cancel, Pause/Resume; status with queued, scanning, probing, waiting-for-source and failed states. **SCALE-01** (module part): scans and probe turns are logged with counters and durations; run history is bounded. Actual scale is 2c |
| File scope | **New:** `server/internal/library/{watch,scheduler,status}.go` + tests, `server/internal/library/scope_test.go`, `server/internal/library/migrations/20261028120000_library_scan_scope.sql`, `server/cmd/lisimflix/library.go` + test. **Changed:** `server/internal/library/{scan,duplicates,probes,retire,store}.go` (2a code, listed in Decision 8), `server/cmd/lisimflix/{main,serve,main_test}.go`, `server/go.mod`, `server/go.sum`, `README.md`, `deploy/README.md`. No contract, OpenAPI or CI change |
| Consumes | 2a: `Store`, `NewStore`, `Migrations`, `Scanner{Store, Media, Discover}`, `ScanRoot`, `ProbeDue`, `MarkInterrupted`, `CreateLibrary`, `Library`, the run/root constants and the `settle`/`finish`/`applyBatch` internals. LIB-1: `Discover`, `skipDir`, `isVideo`, `Prober`, `DefaultQuiet`, `DefaultWorkers`, `probe.Runner`, and the image's `/usr/lib/jellyfin-ffmpeg/ffprobe`. OPS-2: `storage.Source`, `MigrateWithBackup`. OPS-5: the JSON logger |
| Produces | `Scanner.Scan`, `Cause*` constants, `Run.Scope`/`Run.Cause`; `ProbeDue`'s `limit`; `Store.Libraries`; `Scheduler`, `NewScheduler`, `SchedulerOptions`, `Run`, `Reload`, `ScanNow`, `Cancel`, `Pause`, `Resume`, `Status`, `Hint` (for LIB-4); `RootStatus`, `RunInfo`, `FileCounts`, `Activity*`, `Watch*`; the `lisimflix library add|list` command; log events `library.scan`, `library.probe`, `library.watch`, `library.recover` |
| Start dependency | **LIB-1 code (#92) and LIB-2a code merged.** Every task builds on both. The 2a plan (#90) must be approved first; 2a's code PR follows it |
| Standalone test | `go -C server test -race -count=1 ./internal/library/... ./cmd/...`, then `make check` |
| Module proof | Temporary SQLite and media trees, a real inotify watcher on them, scripted probers. Mutation-checked (see Self-review) |
| Integration proof | Task 6: the real image (ffprobe, read-only media, non-root, read-only rootfs) on zraid with libraries added through the CLI, a file added and a season deleted on the host, and a root unplugged and returned. Record "integrated" on #77 with the log lines. Actual-scale qualification (Pete's library, a second installation) is 2c |

## Global Constraints

- Go 1.27.3. Module `github.com/Lisimflix/lisimflix/server`. `CGO_ENABLED=0`. Run `make check` before pushing.
- **Read-only media (SAFE-01):** nothing writes under the media root. The watcher only adds inotify watches. Tests write only in `t.TempDir()`.
- Every scan reads through `os.Root`. Watcher paths are hints: they are mapped to a slash path inside a root and checked with `fs.ValidPath`; an event outside every root is dropped.
- Migrations: one new file, `20261028120000_library_scan_scope.sql`, module `library`. Never edit a merged migration. **Timestamps are global across modules** (OPS-2 refuses two migrations with the same timestamp), so a test's fake migration must not reuse a real one's.
- Write transactions stay short (spec §10.2): scans keep 2a's 500-file batches.
- Code without a production caller yet carries `// Planned: <Feature ID> (<caller>).`; remove the tag when the caller lands (#60).
- New dependency versions must be at least 14 days old (Dependabot cooldown, #78). fsnotify v1.10.1 is from 2026-05-04.
- Workflow:
  - Branch `feat/lib-2b-scheduler`. PR title `feat(library): watcher, scheduled scans and serve wiring (LIB-2b)`. Labels `area:library`.
  - Reviewers lisim and ondona. The author squash-merges after approval.

## Decisions taken in this plan (please review)

1. **Watcher events become scoped scans, not root walks.**
   - Each event's path is the scope: the new file, the new or moved-in folder, or the removed name.
   - A scope that exists is walked (a folder) or recorded (a video file). A scope that's gone, inside a folder that exists, has its files marked missing **without a walk**: the existing parent proves the root is still there. If the parent is gone too, the highest gone folder is the scope. So renaming a top-level folder never walks the whole root.
   - The root-empty check (SCAN-01) runs for scoped scans too: an empty root is offline whatever the scope.
   - **Only whole-root scans** change the root's state and `last_ok_at`, count towards retirement (`scope IS NULL`), and **flag** duplicate pairs. A scoped scan only **folds** a pair whose old item has no present file: after the first scoped scan of a renamed folder, the old folder may simply not have been rescanned yet. A live pair stays pending until the next whole-root scan (6 h at most). The prototype found this: without it, every rename seen by the watcher was flagged as two folders.
   - LIB-4's import notifications (#98) queue scopes through the same path: `Scheduler.Hint(rootID, scope, cause)` with cause `import`, so a notification and a watcher event for the same folder coalesce into one scan.
2. **Two lanes, one scan at a time.**
   - Scans of one root must never overlap: a whole-root scan marks every file it didn't see missing, so a concurrent scoped run that touched a file would make it look unseen. The simplest safe rule is one scan at a time across all roots; the database has one writer anyway.
   - Probes run in their own lane, one `ProbeDue` turn at a time with LIB-1's 2 workers (spec §11.2), so a long probe backlog never delays a scan. 2a's compare-and-set makes probes safe alongside scans.
   - A probe turn takes at most 200 files, oldest change first (`ProbeDue` gains a `limit`), then the next root with work gets a turn (round robin).
3. **fsnotify, not hand-written inotify.** fsnotify is the standard Go wrapper (used by Hugo, Viper, Kubernetes) and reports overflow as `ErrEventOverflow`. It isn't recursive, so the watcher adds one watch per folder, skipping the folders `Discover` skips. Hand-written inotify would save one small dependency at the cost of more code to review.
4. **Unmounts are found by checking, not by events.** fsnotify drops a watch on `IN_UNMOUNT`/`IN_IGNORED` **without sending any event** (found while prototyping). Every recheck (1 min) checks that each root's own folder is still watched; if not, the root gets a whole-root scan, which re-arms the watch if the root is readable, or records it offline if not. A moved or deleted root folder does send an event, which queues the same scan.
5. **Offline roots are retried only once readable.** A recheck queues a scan of an offline root only when it can be opened and isn't empty, so an outage adds one failed run, not one a minute. A degraded root (a walk failed part-way) waits for the next periodic scan, unless a scoped scan works on it: then the root is back, and a whole-root scan follows at once.
6. **Defaults** (`SchedulerOptions`, all overridable in code; not in the config file yet):

   | Option | Default | Why |
   |---|---|---|
   | `FullScanEvery` | 6 h | §3.1; negative turns it off |
   | `Debounce` | 10 s | a burst of events (a season pack) becomes one scan |
   | `MaxDelay` | 2 min | a folder that keeps changing (a file written in place) is still scanned |
   | `Recheck` | 1 min | offline roots, lost watches and new libraries |
   | `Quiet` / `Workers` | 30 s / 2 | LIB-1's defaults |
   | `ProbeBatch` | 200 | files per probe turn |
   | `MaxScopes` | 64 | more pending folders for one root become one whole-root scan |
   | kept runs | 100 whole-root + 100 scoped per root | bounded history; scoped runs can't push out the whole-root runs retirement uses |

7. **Restart reconciliation is a whole-root scan of every root** (§3.6: "restart trigger[s] reconciliation"). `MarkInterrupted` runs first. On an unchanged library this is a stat-only walk; its cost at Pete's scale is measured in 2c.
8. **Changes to 2a code** (small and listed, so the 2a review stays valid):
   - `ScanRoot` becomes `Scan(ctx, rootID, ".", CauseManual)`; the body moves into `Scan`.
   - `finish` gains `setRoot bool`; `settle` gains `flag bool`; the run insert moves into `startRun`.
   - Retirement's candidate rule adds `r.scope IS NULL`.
   - `ProbeDue` gains `limit int` and orders by `mtime_ns, path`.
   - `Store.Libraries` is added for the CLI. The `Planned: LIB-2b` tags on `Migrations`, `NewStore`, `MarkInterrupted`, `ScanRoot` and `ProbeDue` are removed, as are the `Planned: LIB-2c` tags on `CreateLibrary` and `Library` (the CLI calls them now).
9. **The `library` command until 2c.** `lisimflix library add --name N --type movies|tv|anime --folder F [--folder G]` and `lisimflix library list` work on the database next to `--config`, like `backup-now`. A running server picks up a new library at its next recheck (within a minute). Renaming and removing libraries come with 2c's admin screens.
10. **ffprobe** is the image's `/usr/lib/jellyfin-ffmpeg/ffprobe` (LIB-1). `LISIMFLIX_FFPROBE` overrides it for runs outside the image. No config file change.
11. **Pause is in memory.** It survives until Resume or a restart; persisting it is 2c's admin setting if wanted. Watcher hints keep queuing while paused, so nothing is lost.
12. **Log events** (OPS-5 style; the run ID is the job correlation ID OPS-5 left for LIB-2):

    | Event (`msg`) | Level | Fields | When |
    |---|---|---|---|
    | `library.scan` | INFO; WARN when failed; ERROR on a database error | `job_id` (run ID), `root_id`, `scope`, `cause`, `status`, `reason`, `duration_ms`, `seen`, `added`, `changed`, `returned`, `missing`, `unplaced` | Each scan ends |
    | `library.probe` | INFO; WARN on an error | `root_id`, `queued`, `stored`, `stale`, `deferred`, `failed`, `gone`, `duration_ms` | Each probe turn that had work |
    | `library.watch` | WARN | `root_id`, `state` (`off`, `limited`, `overflow`, `error`), `reason`/`err` | A watch is lost or limited, events overflowed, or the watcher failed |
    | `library.recover` | INFO | `interrupted_runs` | Startup ended runs a crash left running |

13. **Not in this slice:** HTTP endpoints and admin screens, retirement and duplicate decisions in a UI (2c); PERF-05/SCALE-02 measurement (2c); the Sonarr/Radarr endpoint (LIB-4; this slice only adds `Hint` and the `import` cause for it); playback-aware throttling of probes (probes stay at 2 workers; revisit with OPS-3 if G0 measurements show contention); NFO files (META-1).

## Review Focus

The five conditions most likely to bite a real installation, each pinned by a test:

1. **The inotify watch limit.** A big library needs one watch per folder; `fs.inotify.max_user_watches` can be lower. The root is reported `limited`, logged once, and relies on the periodic scan; it doesn't loop on rechecks → `TestSchedulerWatchLimitIsReported`. The deploy README says how to raise the limit.
2. **Unmount without an event** (a USB disk, a NAS share dropping) → `TestSchedulerRearmsLostWatch`; the outage itself → `TestSchedulerOfflineRootReturns`.
3. **A file written in place for minutes** (a download that isn't moved in) keeps the debounce from ever expiring → `TestSchedulerBusyFolderStillScanned`; the probe still waits for the quiet period (LIB-1).
4. **A folder renamed while the server runs** (Radarr adding `{tmdb-…}`): one scoped scan sees the new folder before the old one is gone → `TestScopedScansFoldARename`, `TestScopedScanLeavesLivePairPending`.
5. **Hidden and partial files** (`.grab/`, `.incoming/`, `x.mkv.part`, `.nfo`) cause no scans → `TestWatcherHints`, `TestScopeMustStayInsideTheRoot`.

Network and FUSE mounts (SMB, NFS, Unraid's `/mnt/user`) often don't deliver inotify events for changes made elsewhere. The periodic scan covers them (§3.6), and the deploy README says so.

## File Structure

```
server/internal/library/
  migrations/20261028120000_library_scan_scope.sql   scope + cause on scan runs
  scan.go            Scan (scoped), resolve, rootEmpty, skippedPath, startRun; finish(setRoot)
  duplicates.go      settle(flag): scoped scans only fold
  retire.go          candidates count whole-root runs only
  probes.go          ProbeDue(limit)
  store.go           Libraries
  scope_test.go      scoped scans
  status.go          RootStatus, FileCounts, run history and pruning, nextProbeAt
  status_test.go
  watch.go           watcher: fsnotify, folder tree, hints, lost roots
  watch_test.go
  scheduler.go       Scheduler: queue, lanes, rechecks, Pause, Status
  scheduler_test.go
server/cmd/lisimflix/
  library.go         library add|list; newProber; libraryOptions
  library_test.go
  serve.go           migrations, scheduler start, shutdown order
  main.go            the library command
  main_test.go       the upgrade test's fake migration gets its own timestamp
README.md, deploy/README.md
```

---

### Task 1: Scoped scans

**Files:**
- Create: `server/internal/library/migrations/20261028120000_library_scan_scope.sql`
- Modify: `server/internal/library/scan.go`, `server/internal/library/duplicates.go`, `server/internal/library/retire.go`
- Test: `server/internal/library/scope_test.go`

**Interfaces:**
- Consumes: 2a's `Scanner`, `Store.root`, `applyBatch`, `settle`, `finish`, `setRoot`, `ErrInvalid`; LIB-1's `Discover`, `skipDir`, `isVideo`, `RevisionOf`. Test helpers `newStore`, `newMedia`, `media.write`, `media.scanner`, `scan`, `setupMovies`, `rootState`, `Store.fileStates` (2a).
- Produces: `func (sc *Scanner) Scan(ctx context.Context, rootID, scope, cause string) (Run, error)`; `Run.Scope`, `Run.Cause` (`"."` for the whole root); `CauseStartup`, `CauseSchedule`, `CauseWatch`, `CauseRecheck`, `CauseManual`, `CauseAdded`, `CauseImport` (LIB-4); `func skippedPath(p string) bool`; `func rootEmpty(dir *os.Root) (bool, error)`. Test helpers `scanScope(t, sc, rootID, scope) Run`, `setupTV(t, files...)`.

- [ ] **Step 1: Write the failing test**

**Create `server/internal/library/scope_test.go`:**

```go
package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func scanScope(t *testing.T, sc *Scanner, rootID, scope string) Run {
	t.Helper()
	run, err := sc.Scan(context.Background(), rootID, scope, CauseWatch)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func setupTV(t *testing.T, files ...string) (*Store, *clock, *media, *Scanner, string) {
	t.Helper()
	s, c := newStore(t)
	m := newMedia(t)
	for _, f := range files {
		m.write("TV/"+f, 10)
	}
	lib, err := s.CreateLibrary(context.Background(), "TV", "tv", []string{"TV"})
	if err != nil {
		t.Fatal(err)
	}
	sc := m.scanner(s)
	scan(t, sc, lib.Roots[0].ID)
	return s, c, m, sc, lib.Roots[0].ID
}

// A scoped scan reconciles only its folder: files elsewhere that have gone
// stay present until a scan covers them.
func TestScopedScanTouchesOnlyItsScope(t *testing.T) {
	s, _, m, sc, root := setupTV(t,
		"Show (2019)/Season 01/Show - S01E01.mkv",
		"Show (2019)/Season 02/Show - S02E01.mkv",
		"Show (2019) 2/Season 01/Show (2019) 2 - S01E01.mkv", // sorts just before "Show (2019)/"
		"Show (2019)0/Season 01/Show (2019)0 - S01E01.mkv",   // sorts just after
		"Other (2020)/Season 01/Other - S01E01.mkv")
	os.RemoveAll(filepath.Join(m.dir, "TV/Show (2019) 2"))
	os.RemoveAll(filepath.Join(m.dir, "TV/Show (2019)0"))
	os.Remove(filepath.Join(m.dir, "TV/Other (2020)/Season 01/Other - S01E01.mkv"))
	m.write("TV/Show (2019)/Season 01/Show - S01E02.mkv", 10)

	run := scanScope(t, sc, root, "Show (2019)/Season 01")
	if run.Status != RunCompleted || run.Scope != "Show (2019)/Season 01" || run.Seen != 2 || run.Added != 1 || run.Missing != 0 {
		t.Fatalf("scoped run %+v", run)
	}
	for p, st := range s.fileStates(t, root) {
		if st != "present" {
			t.Fatalf("%s is %s after a scan of another folder", p, st)
		}
	}
}

// A scope that no longer exists has its files marked missing, without a walk
// of its parent: renaming a top-level folder mustn't walk the whole root.
func TestScopedScanOfDeletedPath(t *testing.T) {
	s, _, m, sc, root := setupTV(t,
		"Show (2019)/Season 01/Show - S01E01.mkv",
		"Show (2019)/Season 02/Show - S02E01.mkv",
		"Other (2020)/Season 01/Other - S01E01.mkv")
	os.RemoveAll(filepath.Join(m.dir, "TV/Show (2019)/Season 02"))
	run := scanScope(t, sc, root, "Show (2019)/Season 02/Show - S02E01.mkv")
	if run.Status != RunCompleted || run.Scope != "Show (2019)/Season 02" || run.Seen != 0 || run.Missing != 1 {
		t.Fatalf("run %+v", run)
	}
	if st := s.fileStates(t, root)["Show (2019)/Season 02/Show - S02E01.mkv"]; st != "missing" {
		t.Fatalf("deleted episode is %s", st)
	}

	// A whole show gone: the highest gone folder is the scope.
	os.RemoveAll(filepath.Join(m.dir, "TV/Other (2020)"))
	if run := scanScope(t, sc, root, "Other (2020)/Season 01"); run.Scope != "Other (2020)" || run.Missing != 1 {
		t.Fatalf("show folder gone: %+v", run)
	}
	if got := s.fileStates(t, root)["Show (2019)/Season 01/Show - S01E01.mkv"]; got != "present" {
		t.Fatalf("other show's episode is %s", got)
	}
}

// A single file can be the scope: a loose file in the root doesn't need a
// walk of the whole root.
func TestScopedScanOfOneFile(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	m.write("Movies/Loose (2020).mkv", 10)
	m.write("Movies/notes.txt", 10)
	if run := scanScope(t, sc, root, "Loose (2020).mkv"); run.Seen != 1 || run.Added != 1 {
		t.Fatalf("file scope %+v", run)
	}
	if run := scanScope(t, sc, root, "notes.txt"); run.Seen != 0 || run.Status != RunCompleted {
		t.Fatalf("non-video scope %+v", run)
	}
	os.Remove(filepath.Join(m.dir, "Movies/Loose (2020).mkv"))
	if run := scanScope(t, sc, root, "Loose (2020).mkv"); run.Scope != "Loose (2020).mkv" || run.Missing != 1 {
		t.Fatalf("deleted file %+v", run)
	}
	if got := s.fileStates(t, root); got["A (2001)/A (2001).mkv"] != "present" {
		t.Fatalf("states %v", got)
	}
}

// SCAN-01 for scoped scans: an emptied or unreachable root still never marks
// anything missing, and sets the root offline.
func TestScopedScanOfflineRoot(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv")
	os.Rename(filepath.Join(m.dir, "Movies"), filepath.Join(m.dir, "away"))
	if run := scanScope(t, sc, root, "A (2001)"); run.Status != RunFailed || run.Reason != ReasonRootUnavailable {
		t.Fatalf("unreachable %+v", run)
	}
	if st, _ := rootState(t, s, root); st != RootOffline {
		t.Fatalf("root %s", st)
	}
	os.Mkdir(filepath.Join(m.dir, "Movies"), 0o755)
	if run := scanScope(t, sc, root, "A (2001)"); run.Status != RunFailed || run.Reason != ReasonRootEmpty {
		t.Fatalf("empty %+v", run)
	}
	for p, st := range s.fileStates(t, root) {
		if st != "present" {
			t.Fatalf("%s is %s", p, st)
		}
	}
}

// Only whole-root scans change the root's state or count towards retirement.
func TestScopedScanLeavesRootStateAndRetirementAlone(t *testing.T) {
	s, c, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv")
	ctx := context.Background()
	if _, err := s.db.Exec(`UPDATE library_roots SET state = 'degraded', reason = 'enumeration_failed' WHERE id = ?`, root); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(m.dir, "Movies/B (2002)/B (2002).mkv"))
	if run := scanScope(t, sc, root, "B (2002)"); run.Missing != 1 {
		t.Fatalf("run %+v", run)
	}
	if st, why := rootState(t, s, root); st != RootDegraded || why != ReasonEnumerationFailed {
		t.Fatalf("root %s %s after a scoped scan", st, why)
	}
	c.t = c.t.Add(25 * time.Hour)
	scanScope(t, sc, root, "B (2002)")
	if cs, _ := s.RetirementCandidates(ctx, root); len(cs) != 0 {
		t.Fatalf("scoped scans made candidates %+v", cs)
	}
	scan(t, sc, root)
	if cs, _ := s.RetirementCandidates(ctx, root); len(cs) != 1 {
		t.Fatalf("whole-root scan: candidates %+v", cs)
	}
	if st, _ := rootState(t, s, root); st != RootAvailable {
		t.Fatalf("root %s after a whole-root scan", st)
	}
}

func TestScopeMustStayInsideTheRoot(t *testing.T) {
	_, _, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	for _, scope := range []string{"../etc", "/etc", ".grab/x.mkv", "A (2001)/@eaDir/x.mkv", "A (2001)/.hidden.mkv"} {
		if _, err := sc.Scan(context.Background(), root, scope, CauseWatch); !errors.Is(err, ErrInvalid) {
			t.Errorf("scope %q: %v, want ErrInvalid", scope, err)
		}
	}
}

// A folder rename seen by the watcher (new folder, then old one gone) is
// folded like a rename found by a whole-root scan: the item keeps its ID.
func TestScopedScansFoldARename(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "Heat (1995)/Heat (1995).mkv")
	var before string
	s.db.QueryRow(`SELECT id FROM library_items WHERE kind = 'movie'`).Scan(&before)
	os.Rename(filepath.Join(m.dir, "Movies/Heat (1995)"), filepath.Join(m.dir, "Movies/Heat (1995) {tmdb-949}"))
	scanScope(t, sc, root, "Heat (1995) {tmdb-949}")
	scanScope(t, sc, root, "Heat (1995)")
	var ids []string
	rows, _ := s.db.Query(`SELECT id FROM library_items WHERE kind = 'movie'`)
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 1 || ids[0] != before {
		t.Fatalf("movies after rename %v, want [%s]", ids, before)
	}
}

// Two folders that both exist are flagged only by a whole-root scan.
func TestScopedScanLeavesLivePairPending(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "Heat (1995)/Heat (1995).mkv")
	m.write("Movies/Heat.1995.2160p/Heat.1995.2160p.mkv", 10)
	scanScope(t, sc, root, "Heat.1995.2160p")
	state := func() string {
		var st string
		s.db.QueryRow(`SELECT state FROM library_duplicates`).Scan(&st)
		return st
	}
	if got := state(); got != "pending" {
		t.Fatalf("after scoped scan: %s", got)
	}
	scan(t, sc, root)
	if got := state(); got != "flagged" {
		t.Fatalf("after whole-root scan: %s", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 ./internal/library/...
```

Expected: FAIL to build: `sc.Scan undefined`, `undefined: CauseWatch`, `run.Scope undefined`.

- [ ] **Step 3: Add the migration**

**Create `server/internal/library/migrations/20261028120000_library_scan_scope.sql`:**

```sql
-- Scoped scans (LIB-2b): a watcher event rescans one folder, not the whole
-- root. scope is the folder or file the run covered, relative to the root;
-- NULL means the whole root. Only whole-root runs count towards retirement.
-- cause says what started the run, for the status view and logs.
ALTER TABLE library_scan_runs ADD COLUMN scope TEXT;
ALTER TABLE library_scan_runs ADD COLUMN cause TEXT NOT NULL DEFAULT 'manual'
    CHECK (cause IN ('startup', 'schedule', 'watch', 'recheck', 'manual', 'added', 'import'));
```

- [ ] **Step 4: Implement `Scan` in `scan.go`**

- `Run` gains `Scope, Cause string`. Add the six `Cause*` constants, spelled as the migration's `CHECK`.
- `ScanRoot(ctx, rootID)` becomes `return sc.Scan(ctx, rootID, ".", CauseManual)`.
- `Scan`:
  1. `scope = path.Clean(scope)`; refuse with `ErrInvalid` unless `fs.ValidPath(scope) && !skippedPath(scope)`.
  2. Open the root as 2a does. A root that can't be opened (or isn't a folder) is `errRootGone`. Otherwise `scope, target, err = resolve(dir, scope)`.
  3. `startRun(ctx, root, scope, cause)` inserts the run (`scope` NULL for `"."`) and, for `"."` only, sets the root `scanning`.
  4. `full := scope == "."`. `errRootGone` → `finish(…, setRoot=true, failed, root_unavailable, offline)`. Another resolve error → `finish(…, full, failed, enumeration_failed, degraded)`.
  5. Walk: `target == nil` (gone) walks nothing. A folder: `Discover` on `dir` for the whole root, or on `dir.OpenRoot(scope)` with each `File.Path` prefixed by `scope + "/"`. A regular file whose name `isVideo`: one `File{scope, RevisionOf(target)}`. Anything else: nothing. Batches as in 2a.
  6. Cancelled and failed walks finish as in 2a, with `setRoot = full`.
  7. `Seen == 0`: if `rootEmpty(dir)` and the root has present files → `finish(…, setRoot=true, failed, root_empty, offline)`.
  8. Mark missing as in 2a, restricted for a scope to `AND (path = ? OR (path >= ? AND path < ?))` with `scope, scope+"/", scope+"0"` (`'0'` is the byte after `'/'`, so the range is exactly the folder's contents, and a sibling like `Show (2019) 2` is outside it).
  9. `settle(bg, rootID, full)`, then `finish(…, full, completed, "", available)`.
- `resolve(dir *os.Root, scope string) (string, fs.FileInfo, error)`: returns the scope and its info if it exists. Otherwise it climbs: the highest gone folder whose parent exists is returned with nil info. If the climb reaches `"."` and that fails, `errRootGone`; any error other than not-exist is returned as is.
- `rootEmpty`: `dir.Open(".")` then `Readdirnames(1)`; `io.EOF` means empty.
- `skippedPath(p)`: true when a folder on the path is `skipDir`, or the last name starts with `.`.
- `finish(ctx, root, run, setRoot bool, status, reason, rootState)` writes the run, and the root only when `setRoot`.

- [ ] **Step 5: Scoped scans only fold (`duplicates.go`), and only whole-root runs retire (`retire.go`)**

- `settle(ctx, rootID string, flag bool)`: a live pair is flagged only when `flag`; with `flag` false it stays pending. A dead pair is folded either way.
- `candidateCond` adds `AND r.scope IS NULL` to the run it requires.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
go -C server test -race -count=1 ./internal/library/...
```

Expected: PASS, including all of 2a's tests unchanged.

- [ ] **Step 7: Commit**

```bash
git add server/internal/library
git commit -m "feat(library): scoped scans for watched changes (LIB-2b)"
```

---

### Task 2: Probe turns and status queries

**Files:**
- Modify: `server/internal/library/probes.go`, `server/internal/library/probes_test.go`
- Create: `server/internal/library/status.go`, `server/internal/library/status_test.go`

**Interfaces:**
- Consumes: Task 1's `Run.Scope`/`Cause` and the `scope`/`cause` columns; 2a's `Store.root`, `scanStrings`, `ms`.
- Produces:
  - `func (sc *Scanner) ProbeDue(ctx context.Context, rootID string, p Prober, workers int, quiet time.Duration, limit int) (ProbeRun, error)`; `limit` 0 means all.
  - `type RootStatus struct { Root; Activity, Watch string; LastRun *RunInfo; Files FileCounts }`, `type RunInfo struct { Run; Started, Finished time.Time }`, `type FileCounts struct { Present, Missing, Unplaced, ProbesDue, ProbesWaiting, ProbeErrors int }`.
  - `ActivityIdle = "idle"`, `ActivityQueued = "queued"`, `ActivityScanning = "scanning"`, `ActivityProbing = "probing"`, `ActivityPaused = "paused"`.
  - Unexported: `(*Store).roots(ctx) ([]Root, error)`, `lastRun(ctx, rootID) (*RunInfo, error)`, `fileCounts(ctx, rootID) (FileCounts, error)`, `nextProbeAt(ctx, rootID) (time.Time, bool, error)`, `pruneRuns(ctx, rootID) error`, `const keepRuns = 100`.

- [ ] **Step 1: Write the failing tests**

In `probes_test.go`, the `probeDue` helper passes `0` as the new last argument (`sc.ProbeDue(context.Background(), root, p, 2, quiet, 0)`), and this test is appended:

```go
// A limit takes the oldest changes first and leaves the rest for later calls.
func TestProbeDueLimit(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv", "C (2003)/C (2003).mkv")
	old := time.Now().Add(-3 * time.Hour)
	os.Chtimes(filepath.Join(m.dir, "Movies/C (2003)/C (2003).mkv"), old, old)
	scan(t, sc, root)
	p := &scriptedProber{}
	run, err := sc.ProbeDue(context.Background(), root, p, 2, quiet, 1)
	if err != nil || run.Queued != 1 || run.Stored != 1 || len(p.calls) != 1 || p.calls[0] != "C (2003)/C (2003).mkv" {
		t.Fatalf("limit 1: %+v %v calls %v", run, err, p.calls)
	}
	if run := probeDue(t, sc, root, p); run.Queued != 2 {
		t.Fatalf("rest: %+v", run)
	}
	_ = s
}
```

**Create `server/internal/library/status_test.go`:**

```go
package library

import (
	"context"
	"testing"
	"time"
)

// Run history is bounded per kind, so scoped runs never push out the
// whole-root runs retirement relies on.
func TestPruneRunsKeepsEachKind(t *testing.T) {
	s, c, _, sc, root := setupMovies(t, "A (2001)/A (2001).mkv")
	for range keepRuns + 20 {
		c.t = c.t.Add(time.Second)
		scanScope(t, sc, root, "A (2001)")
	}
	if err := s.pruneRuns(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	var full, scoped int
	s.db.QueryRow(`SELECT COUNT(*) FROM library_scan_runs WHERE scope IS NULL`).Scan(&full)
	s.db.QueryRow(`SELECT COUNT(*) FROM library_scan_runs WHERE scope IS NOT NULL`).Scan(&scoped)
	if full != 1 || scoped != keepRuns {
		t.Fatalf("kept %d whole-root and %d scoped runs", full, scoped)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -race -count=1 ./internal/library/...
```

Expected: FAIL to build: `too many arguments in call to sc.ProbeDue` (then `undefined: keepRuns`, `s.pruneRuns undefined`).

- [ ] **Step 3: Implement**

- `ProbeDue`: the due-files query orders by `mtime_ns, path` and ends `LIMIT ?` with `cmp.Or(limit, -1)` (SQLite: -1 is no limit). Its comment says the limit lets a large backlog take turns with other roots. Remove its `Planned: LIB-2b` tag.
- `status.go`:
  - `roots`: every root, by path, through `Store.root`.
  - `lastRun`: the newest run (`ORDER BY started_at DESC, rowid DESC LIMIT 1`); NULL scope reads as `"."`; nil when none.
  - `fileCounts`: one query over the root's files. **Due** = present, probe revision ≠ file revision, `probe_after` NULL or past. **Waiting** = the same but `probe_after` in the future. **Errors** = present with `probe_error` for the current revision.
  - `nextProbeAt`: `MIN(probe_after)` over present, stale-probe files with `probe_after` in the future.
  - `pruneRuns`: deletes finished runs beyond `keepRuns` per root and per kind, newest kept:
    ```sql
    DELETE FROM library_scan_runs WHERE id IN (
      SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY scope IS NULL ORDER BY started_at DESC, rowid DESC) AS n
        FROM library_scan_runs WHERE root_id = ? AND status != 'running') WHERE n > ?)
    ```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go -C server test -race -count=1 ./internal/library/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/library
git commit -m "feat(library): bounded probe turns and scan status queries (LIB-2b)"
```

---

### Task 3: The watcher

**Files:**
- Modify: `server/go.mod`, `server/go.sum`
- Create: `server/internal/library/watch.go`, `server/internal/library/watch_test.go`

**Interfaces:**
- Consumes: Task 1's `skippedPath`; LIB-1's `skipDir`, `isVideo`.
- Produces (unexported; the scheduler is the only user):
  - `WatchOff = "off"`, `WatchOn = "on"`, `WatchLimited = "limited"`.
  - `type watchHooks struct { hint func(rootID, scope string); lost func(rootID string); limited func(rootID string, err error); overflow func(); failed func(error) }`.
  - `func newWatcher(h watchHooks) (*watcher, error)`; `(w *watcher) watch(rootID, abs string) (string, error)` returns `WatchOn`, or `WatchLimited` with the error; `unwatch(rootID string)`; `watching() map[string]bool`; `run(ctx context.Context)`.

- [ ] **Step 1: Add the dependency**

```bash
go -C server get github.com/fsnotify/fsnotify@v1.10.1
```

- [ ] **Step 2: Write the failing test**

**Create `server/internal/library/watch_test.go`:**

```go
package library

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

type watchLog struct {
	hints chan [2]string
	lost  chan string
}

func startWatcher(t *testing.T) (*watcher, *watchLog) {
	t.Helper()
	l := &watchLog{hints: make(chan [2]string, 100), lost: make(chan string, 10)}
	w, err := newWatcher(watchHooks{
		hint:     func(root, scope string) { l.hints <- [2]string{root, scope} },
		lost:     func(root string) { l.lost <- root },
		limited:  func(root string, err error) { t.Errorf("limited %s: %v", root, err) },
		overflow: func() { t.Error("overflow") },
		failed:   func(err error) { t.Error(err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return w, l
}

// expect collects hint scopes for root until want are all seen, failing on
// any scope outside allowed (want plus extra).
func (l *watchLog) expect(t *testing.T, root string, want []string, extra ...string) {
	t.Helper()
	seen := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for len(seen) < len(want) {
		select {
		case h := <-l.hints:
			if h[0] != root || !slices.Contains(want, h[1]) && !slices.Contains(extra, h[1]) {
				t.Fatalf("unexpected hint %v", h)
			}
			if slices.Contains(want, h[1]) {
				seen[h[1]] = true
			}
		case <-deadline:
			t.Fatalf("hints seen %v, want %v", seen, want)
		}
	}
}

func (l *watchLog) quiet(t *testing.T) {
	t.Helper()
	select {
	case h := <-l.hints:
		t.Fatalf("unexpected hint %v", h)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWatcherHints(t *testing.T) {
	m := newMedia(t)
	m.write("TV/Show (2019)/Season 01/Show - S01E01.mkv", 10)
	os.MkdirAll(filepath.Join(m.dir, "TV/.grab"), 0o755)
	w, l := startWatcher(t)
	abs := filepath.Join(m.dir, "TV")
	if st, err := w.watch("root-1", abs); st != WatchOn || err != nil {
		t.Fatalf("watch = %s, %v", st, err)
	}
	if !w.watching()["root-1"] {
		t.Fatal("root not watched")
	}

	// A new episode: hinted by its own path.
	m.write("TV/Show (2019)/Season 01/Show - S01E02.mkv", 10)
	l.expect(t, "root-1", []string{"Show (2019)/Season 01/Show - S01E02.mkv"})
	l.drain()

	// Files that don't matter: no hints.
	m.write("TV/Show (2019)/Season 01/Show - S01E02.nfo", 10)
	m.write("TV/.grab/Other - S01E01.mkv", 10)
	m.write("TV/Show (2019)/Season 01/Show - S01E03.mkv.part", 10)
	os.Mkdir(filepath.Join(abs, ".incoming"), 0o755) // a new hidden folder isn't watched or hinted
	l.quiet(t)
	m.write("TV/.incoming/Show - S01E04.mkv", 10)
	l.quiet(t)

	// A folder moved in, already full: the folder is hinted, and later
	// changes inside it are watched.
	stage := filepath.Join(t.TempDir(), "New (2020)")
	os.MkdirAll(filepath.Join(stage, "Season 01"), 0o755)
	os.WriteFile(filepath.Join(stage, "Season 01/New - S01E01.mkv"), nil, 0o644)
	if err := os.Rename(stage, filepath.Join(abs, "New (2020)")); err != nil {
		t.Skip("staging folder is on another filesystem:", err)
	}
	l.expect(t, "root-1", []string{"New (2020)"})
	m.write("TV/New (2020)/Season 01/New - S01E02.mkv", 10)
	l.expect(t, "root-1", []string{"New (2020)/Season 01/New - S01E02.mkv"})
	l.drain()

	// A season deleted: hinted whatever the names.
	os.RemoveAll(filepath.Join(abs, "Show (2019)/Season 01"))
	l.expect(t, "root-1", []string{"Show (2019)/Season 01"}, "Show (2019)/Season 01/Show - S01E01.mkv",
		"Show (2019)/Season 01/Show - S01E02.mkv", "Show (2019)/Season 01/Show - S01E02.nfo",
		"Show (2019)/Season 01/Show - S01E03.mkv.part")
	l.drain()

	// The root itself moved away: lost, and no longer watched.
	os.Rename(abs, filepath.Join(m.dir, "away"))
	select {
	case id := <-l.lost:
		if id != "root-1" {
			t.Fatalf("lost %s", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("root move not reported")
	}

	// unwatch stops hints.
	os.Rename(filepath.Join(m.dir, "away"), abs)
	w.unwatch("root-1")
	m.write("TV/New (2020)/Season 01/New - S01E03.mkv", 10)
	l.quiet(t)
}

func (l *watchLog) drain() {
	for {
		select {
		case <-l.hints:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

```bash
go -C server test -race -count=1 -run TestWatcherHints ./internal/library/
```

Expected: FAIL to build: `undefined: watcher`, `undefined: newWatcher`, `undefined: watchHooks`.

- [ ] **Step 4: Implement `watch.go`**

- `watcher` holds the `*fsnotify.Watcher`, the hooks, and `roots map[string]string` (root ID → absolute folder) under a mutex.
- `watch`: `unwatch` first, record the root, then `addTree(abs)`. An error from `addTree` → `WatchLimited`, err.
- `addTree(dir)`: `filepath.WalkDir`; an error on `dir` itself is returned, others skipped; folders with `skipDir` names below `dir` are skipped; each folder gets `fw.Add`, whose error stops the walk (usually `ENOSPC`, the watch limit). `WalkDir` doesn't follow symlinked folders, like `Discover`.
- `unwatch`: forget the root, and `fw.Remove` every watched path that is `abs` or under it.
- `watching`: which roots still have a watch on their own folder (from `fw.WatchList()`).
- `locate(name)`: the root whose folder contains `name`, and the slash path inside it (`filepath.Rel`, `ToSlash`, `fs.ValidPath`).
- `run`: loop over `Events` and `Errors` until `ctx` ends; `errors.Is(err, fsnotify.ErrEventOverflow)` → `overflow()`, other errors → `failed(err)`. `defer fw.Close()`.
- `event(ev)`, in this order:
  1. Unknown path → drop.
  2. `rel == "."`: a `Remove` or `Rename` → `lost(rootID)`; anything else is dropped.
  3. `skippedPath(rel)` → drop.
  4. A `Create` whose `os.Lstat` is a folder → `addTree` it (an error → `limited(rootID, err)`), then `hint(rootID, rel)`: files may have landed before its watch existed.
  5. `Remove` or `Rename` → `hint` whatever the name: it may have been a folder.
  6. `Create` or `Write` of a name that `isVideo` → `hint`. Everything else (`Chmod`, `.nfo`, `.part`) is dropped.

- [ ] **Step 5: Run the test to verify it passes**

```bash
go -C server test -race -count=1 -run TestWatcherHints ./internal/library/
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/go.mod server/go.sum server/internal/library
git commit -m "feat(library): inotify watcher for library folders (LIB-2b)"
```

---

### Task 4: The scheduler

**Files:**
- Create: `server/internal/library/scheduler.go`, `server/internal/library/scheduler_test.go`

**Interfaces:**
- Consumes: Task 1's `Scan` and causes; Task 2's `ProbeDue(limit)`, status queries and `pruneRuns`; Task 3's watcher; 2a's `MarkInterrupted`; LIB-1's `DefaultQuiet`, `DefaultWorkers`, `Prober`.
- Produces:
  - `type SchedulerOptions struct { FullScanEvery, Debounce, MaxDelay, Recheck, Quiet time.Duration; Workers, ProbeBatch, MaxScopes int; NoWatch bool }` with the Decision 6 defaults for zero values.
  - `func NewScheduler(sc *Scanner, p Prober, log *slog.Logger, opt SchedulerOptions) *Scheduler` (nil log discards).
  - `func (s *Scheduler) Run(ctx context.Context) error`, `Reload(ctx) error`, `ScanNow(libraryID string) error`, `Cancel(libraryID string) error` (both `ErrNotFound` for an unknown library), `Pause()`, `Resume()`, `Status(ctx) ([]RootStatus, error)`, and `Hint(rootID, scope, cause string) error` (`ErrNotFound` for an unknown root, `ErrInvalid` for a scope `Scan` would refuse).
  - Unexported, used by tests: `hooks() watchHooks`, `mu`, `w`, `probing`.
  - `func (sc *Scanner) reachable(rootPath string) bool`: the root opens and isn't empty.

- [ ] **Step 1: Write the failing test**

**Create `server/internal/library/scheduler_test.go`:**

```go
package library

import (
	"context"
	"errors"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fast options for tests; the clock for DB times stays the test clock.
var fastOpts = SchedulerOptions{Debounce: 30 * time.Millisecond, MaxDelay: 200 * time.Millisecond,
	Recheck: 50 * time.Millisecond, FullScanEvery: time.Hour}

type schedFixture struct {
	s    *Store
	m    *media
	sc   *Scanner
	sch  *Scheduler
	lib  Library
	root string
	stop func()
}

func startScheduler(t *testing.T, libType string, opts SchedulerOptions, p Prober, files ...string) *schedFixture {
	t.Helper()
	s, _ := newStore(t)
	s.now = time.Now // real time: the scheduler compares probe_after with it
	m := newMedia(t)
	dir := map[string]string{"movies": "Movies", "tv": "TV"}[libType]
	for _, f := range files {
		m.write(dir+"/"+f, 10)
	}
	lib, err := s.CreateLibrary(context.Background(), dir, libType, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	sc := m.scanner(s)
	sch := NewScheduler(sc, p, nil, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sch.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Run didn't return after cancel")
			}
		})
	}
	t.Cleanup(stop)
	return &schedFixture{s: s, m: m, sc: sc, sch: sch, lib: lib, root: lib.Roots[0].ID, stop: stop}
}

func (f *schedFixture) status(t *testing.T) RootStatus {
	t.Helper()
	st, err := f.sch.Status(context.Background())
	if err != nil || len(st) != 1 {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	return st[0]
}

func (f *schedFixture) runs(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM library_scan_runs WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *schedFixture) probed(t *testing.T, rel string) bool {
	t.Helper()
	var n int
	f.s.db.QueryRow(`SELECT COUNT(*) FROM library_files WHERE path = ? AND state = 'present' AND probe_json IS NOT NULL
		AND probe_size = size AND probe_mtime_ns = mtime_ns`, rel).Scan(&n)
	return n == 1
}

// Startup reconciles every root, probes what it found and watches the root;
// a later change is found through the watcher with a scoped scan.
func TestSchedulerStartupWatchAndProbe(t *testing.T) {
	f := startScheduler(t, "tv", fastOpts, &scriptedProber{}, "Show (2019)/Season 01/Show - S01E01.mkv")
	eventually(t, "startup scan and probe", func() bool { return f.probed(t, "Show (2019)/Season 01/Show - S01E01.mkv") })
	st := f.status(t)
	if st.LastRun == nil || st.LastRun.Cause != CauseStartup || st.LastRun.Status != RunCompleted || st.Watch != WatchOn {
		t.Fatalf("status after startup %+v %+v", st, st.LastRun)
	}

	f.m.write("TV/Show (2019)/Season 01/Show - S01E02.mkv", 10)
	eventually(t, "watched file probed", func() bool { return f.probed(t, "Show (2019)/Season 01/Show - S01E02.mkv") })
	if n := f.runs(t, `cause = 'watch' AND scope IS NOT NULL`); n == 0 {
		t.Fatal("no scoped watch run")
	}
	if n := f.runs(t, `scope IS NULL`); n != 1 {
		t.Fatalf("%d whole-root runs; a watched change must not walk the root", n)
	}

	os.RemoveAll(filepath.Join(f.m.dir, "TV/Show (2019)/Season 01"))
	eventually(t, "deleted season missing", func() bool {
		return f.s.fileStates(t, f.root)["Show (2019)/Season 01/Show - S01E01.mkv"] == "missing"
	})
	if st := f.status(t); st.Files.Missing != 2 || st.Files.Present != 0 {
		t.Fatalf("counts %+v", st.Files)
	}
}

// Hints coalesce: a scope inside another pending scope adds nothing, and too
// many scopes become one whole-root scan.
func TestSchedulerCoalescesHints(t *testing.T) {
	opts := fastOpts
	opts.NoWatch, opts.MaxScopes, opts.Debounce = true, 3, 300*time.Millisecond
	f := startScheduler(t, "movies", opts, &scriptedProber{}, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv")
	eventually(t, "startup scan", func() bool { return f.runs(t, `status = 'completed'`) == 1 })
	h := f.sch.hooks()
	h.hint(f.root, "A (2001)/A (2001).mkv")
	h.hint(f.root, "A (2001)")
	h.hint(f.root, "A (2001)/A (2001).mkv")
	h.hint(f.root, "B (2002)")
	eventually(t, "scoped runs", func() bool { return f.runs(t, `cause = 'watch'`) == 2 })
	time.Sleep(400 * time.Millisecond)
	if n := f.runs(t, `cause = 'watch'`); n != 2 {
		t.Fatalf("%d watch runs, want 2 (A and B)", n)
	}
	if n := f.runs(t, `scope = 'A (2001)'`) + f.runs(t, `scope = 'B (2002)'`); n != 2 {
		t.Fatalf("scopes %d", n)
	}

	for _, p := range []string{"a", "b", "c", "d"} {
		h.hint(f.root, p)
	}
	eventually(t, "whole-root scan for many hints", func() bool { return f.runs(t, `cause = 'watch' AND scope IS NULL`) == 1 })
}

// SCAN-01 with the scheduler: an offline root is retried only once it can
// be read again, then reconciled; nothing goes missing meanwhile.
func TestSchedulerOfflineRootReturns(t *testing.T) {
	f := startScheduler(t, "movies", fastOpts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool { return f.runs(t, `status = 'completed'`) == 1 })
	away := filepath.Join(f.m.dir, "away")
	os.Rename(filepath.Join(f.m.dir, "Movies"), away)
	if err := f.sch.ScanNow(f.lib.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.sch.ScanNow("lib-nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ScanNow(unknown) = %v", err)
	}
	eventually(t, "root offline", func() bool { return f.status(t).State == RootOffline })
	time.Sleep(300 * time.Millisecond) // several rechecks
	failed := f.runs(t, `status = 'failed'`)
	if failed > 2 {
		t.Fatalf("%d failed runs while offline: rechecks must wait for the root", failed)
	}
	if got := f.s.fileStates(t, f.root)["A (2001)/A (2001).mkv"]; got != "present" {
		t.Fatalf("file is %s while offline", got)
	}
	os.Rename(away, filepath.Join(f.m.dir, "Movies"))
	eventually(t, "root back", func() bool {
		st := f.status(t)
		return st.State == RootAvailable && st.Watch == WatchOn
	})
	if n := f.runs(t, `cause = 'recheck' AND status = 'completed'`); n != 1 {
		t.Fatalf("%d recheck runs", n)
	}
}

// An unmount drops inotify watches without an event; the recheck notices,
// re-arms and rescans the root.
func TestSchedulerRearmsLostWatch(t *testing.T) {
	f := startScheduler(t, "movies", fastOpts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "watching", func() bool { return f.status(t).Watch == WatchOn })
	f.sch.mu.Lock()
	w := f.sch.w
	f.sch.mu.Unlock()
	for _, p := range w.fw.WatchList() {
		w.fw.Remove(p) // as an unmount would
	}
	eventually(t, "recheck scan", func() bool { return f.runs(t, `cause = 'recheck' AND status = 'completed'`) == 1 })
	eventually(t, "re-armed", func() bool { return f.status(t).Watch == WatchOn })
	f.m.write("Movies/B (2002)/B (2002).mkv", 10)
	eventually(t, "watched again", func() bool { return f.probed(t, "B (2002)/B (2002).mkv") })
}

// blockingProber holds every probe until released.
type blockingProber struct {
	started chan string
	release chan struct{}
}

func (b *blockingProber) Probe(ctx context.Context, r *os.Root, rel string) (probe.Result, error) {
	select {
	case b.started <- rel:
	default:
	}
	select {
	case <-b.release:
		return (&scriptedProber{}).Probe(ctx, r, rel)
	case <-ctx.Done():
		return probe.Result{}, ctx.Err()
	}
}

// Pause stops running probes and holds new work; Resume finishes it.
func TestSchedulerPauseAndResume(t *testing.T) {
	bp := &blockingProber{started: make(chan string, 10), release: make(chan struct{})}
	f := startScheduler(t, "movies", fastOpts, bp, "A (2001)/A (2001).mkv")
	<-bp.started
	f.sch.Pause()
	eventually(t, "probe stopped", func() bool {
		f.sch.mu.Lock()
		defer f.sch.mu.Unlock()
		return f.sch.probing == nil
	})
	if st := f.status(t); st.Activity != ActivityPaused || st.Files.ProbesDue != 1 {
		t.Fatalf("paused status %+v %+v", st.Activity, st.Files)
	}
	f.m.write("Movies/B (2002)/B (2002).mkv", 10)
	time.Sleep(300 * time.Millisecond)
	if n := f.runs(t, `cause = 'watch'`); n != 0 {
		t.Fatalf("%d scans while paused", n)
	}
	close(bp.release)
	f.sch.Resume()
	eventually(t, "both probed", func() bool { return f.probed(t, "A (2001)/A (2001).mkv") && f.probed(t, "B (2002)/B (2002).mkv") })
}

// Shutdown stops a running scan promptly; it is recorded as cancelled.
func TestSchedulerShutdownCancelsScan(t *testing.T) {
	s, _ := newStore(t)
	m := newMedia(t)
	m.write("Movies/A (2001)/A (2001).mkv", 10)
	lib, _ := s.CreateLibrary(context.Background(), "Movies", "movies", []string{"Movies"})
	sc := m.scanner(s)
	inWalk := make(chan struct{})
	sc.Discover = func(ctx context.Context, r *os.Root, found func(File) error) error {
		close(inWalk)
		<-ctx.Done()
		return ctx.Err()
	}
	sch := NewScheduler(sc, &scriptedProber{}, nil, fastOpts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sch.Run(ctx) }()
	<-inWalk
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't stop")
	}
	var status string
	s.db.QueryRow(`SELECT status FROM library_scan_runs WHERE root_id = ?`, lib.Roots[0].ID).Scan(&status)
	if status != RunCancelled {
		t.Fatalf("run %s", status)
	}
}

// The periodic whole-root scan repeats.
func TestSchedulerPeriodicScan(t *testing.T) {
	opts := fastOpts
	opts.FullScanEvery, opts.NoWatch = 100*time.Millisecond, true
	f := startScheduler(t, "movies", opts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "scheduled scans", func() bool { return f.runs(t, `cause = 'schedule' AND scope IS NULL`) >= 2 })
}

// A scoped scan that works on a root whose last whole-root scan didn't
// means the root is back: the whole root is reconciled.
func TestSchedulerScopedSuccessOnDegradedRootRechecks(t *testing.T) {
	f := startScheduler(t, "movies", fastOpts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool {
		st := f.status(t)
		return st.Watch == WatchOn && st.LastRun != nil && st.LastRun.Status == RunCompleted
	})
	if _, err := f.s.db.Exec(`UPDATE library_roots SET state = 'degraded', reason = 'enumeration_failed'`); err != nil {
		t.Fatal(err)
	}
	f.m.write("Movies/B (2002)/B (2002).mkv", 10)
	eventually(t, "recheck", func() bool { return f.runs(t, `cause = 'recheck' AND scope IS NULL AND status = 'completed'`) == 1 })
	if st := f.status(t); st.State != RootAvailable {
		t.Fatalf("root %s", st.State)
	}
}

// A folder that never stops changing (a file written in place) is still
// scanned every MaxDelay, not postponed forever by the debounce.
func TestSchedulerBusyFolderStillScanned(t *testing.T) {
	opts := fastOpts
	opts.NoWatch, opts.Debounce, opts.MaxDelay = true, 100*time.Millisecond, 150*time.Millisecond
	f := startScheduler(t, "movies", opts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool { return f.runs(t, `status = 'completed'`) == 1 })
	h := f.sch.hooks()
	for end := time.Now().Add(700 * time.Millisecond); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		h.hint(f.root, "A (2001)")
	}
	if n := f.runs(t, `cause = 'watch'`); n < 2 {
		t.Fatalf("%d scans of a busy folder in 700 ms, want at least 2 (MaxDelay 150 ms)", n)
	}
}

// A root over the inotify watch limit is reported, and relies on periodic
// scans instead of a recheck loop.
func TestSchedulerWatchLimitIsReported(t *testing.T) {
	f := startScheduler(t, "movies", fastOpts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool { return f.status(t).LastRun != nil && f.status(t).LastRun.Status == RunCompleted })
	f.sch.hooks().limited(f.root, errors.New("no space left on device"))
	if st := f.status(t); st.Watch != WatchLimited {
		t.Fatalf("watch %s", st.Watch)
	}
	time.Sleep(300 * time.Millisecond) // several rechecks
	if n := f.runs(t, `cause = 'recheck'`); n != 0 {
		t.Fatalf("%d recheck scans for a limited watch", n)
	}
}

// Hint queues a scoped scan with the caller's cause, through the same
// coalescing queue as watcher events (LIB-4's import notifications).
func TestSchedulerHint(t *testing.T) {
	opts := fastOpts
	opts.NoWatch = true
	f := startScheduler(t, "movies", opts, &scriptedProber{}, "A (2001)/A (2001).mkv")
	eventually(t, "startup scan", func() bool { return f.runs(t, `status = 'completed'`) == 1 })
	if err := f.sch.Hint("root-nope", "A (2001)", CauseImport); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown root: %v", err)
	}
	for _, bad := range []string{"../x", "/abs", ".grab/x.mkv"} {
		if err := f.sch.Hint(f.root, bad, CauseImport); !errors.Is(err, ErrInvalid) {
			t.Fatalf("scope %q: %v", bad, err)
		}
	}
	f.m.write("Movies/B (2002)/B (2002).mkv", 10)
	if err := f.sch.Hint(f.root, "B (2002)/B (2002).mkv", CauseImport); err != nil {
		t.Fatal(err)
	}
	f.sch.Hint(f.root, "B (2002)", CauseImport) // the folder replaces the file
	eventually(t, "import scan", func() bool {
		return f.runs(t, `cause = 'import' AND scope = 'B (2002)' AND status = 'completed'`) == 1
	})
	if n := f.runs(t, `cause = 'import'`); n != 1 {
		t.Fatalf("%d import runs, want 1", n)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go -C server test -race -count=1 -run Scheduler ./internal/library/
```

Expected: FAIL to build: `undefined: SchedulerOptions`, `undefined: Scheduler`, `undefined: NewScheduler`.

- [ ] **Step 3: Implement `scheduler.go`**

State, under one mutex: per root a `rootSched` (the `Root`, its absolute folder, the pending whole-root cause or a set of scopes, `first`/`due` times, `nextFull`, the watch state, and `probe`/`probeAt`), plus `paused`, the running scan job and probe job (each with its `cancel` and a `requeue` flag), the watcher, and two wake channels.

- **`Run`:** `MarkInterrupted` (log `library.recover` when > 0); start the watcher unless `NoWatch` (if inotify can't start, log `library.watch` state `off` and carry on); `reload(CauseStartup)`; start the probe loop; run the scan loop; wait for both after `ctx` ends.
- **`reload(cause)`:** new roots get a whole-root scan due now with `cause`, and `nextFull` = now + `FullScanEvery`; removed roots are unwatched, and their running jobs cancelled. `Reload` uses `CauseAdded`.
- **Queueing:**
  - `queueFull(rs, cause, due)` replaces pending scopes. A manual cause overrides another pending cause. `due` only moves earlier.
  - A watcher hint does nothing if a whole-root scan is pending. Otherwise: `first` is set if unset; `due = min(now + Debounce, first + MaxDelay)`. A scope inside a pending scope is dropped, and pending scopes inside the new one are removed (`within(p, q)`: `q == "."`, `p == q`, or `p` starts with `q + "/"`). More than `MaxScopes` → a whole-root scan with `CauseWatch`.
  - `lost` → watch `off` and a recheck scan due now. `limited` → watch `limited`. `overflow` → a whole-root `CauseWatch` scan for every watched root. Each logs `library.watch`.
- **Scan loop:** `nextScan` takes the root whose due time is earliest and past, unless paused or a scan is running. It first queues a `CauseSchedule` scan for any root past `nextFull`. Taking a whole-root job sets `nextFull` = now + `FullScanEvery`, and clears the root's pending work. With nothing due, the loop waits for a wake, the earliest due time, or the `Recheck` ticker.
- **`execute`:** before a whole-root scan, if there's a watcher, the root isn't watched (`on`) and it's `reachable`, call `watch` first, so a change during the walk is hinted. Then run the job's scans with a cancellable context, logging each as `library.scan` (WARN when failed) and calling `after`. On cancellation with `requeue` (Pause), put the unfinished work back.
- **`after(run)`:**
  - `pruneRuns`; refresh the root from the store.
  - Completed → `probe = true` and wake the probe lane. If it was scoped and the root isn't `available`, queue a `CauseRecheck` whole-root scan.
  - Failed with `root_unavailable` or `root_empty` → unwatch, watch `off`.
- **`recheck` (every `Recheck`):**
  1. `reload(CauseAdded)`.
  2. Roots whose watch is `on` but missing from `watching()` become `off`.
  3. For each root with nothing pending or running: if it's `offline`, queue a `CauseRecheck` scan only if `reachable`. If there's a watcher and its watch is `off`, queue one either way.
- **Probe loop:** `nextProbe` round-robins over root IDs from the one after the last turn. A root whose `probeAt` has passed becomes `probe = true`, and an offline root is skipped. A turn runs `ProbeDue(…, Workers, Quiet, ProbeBatch)` and logs `library.probe` when `Queued > 0`. After the turn, `probe` stays true when the batch was full (or the turn was paused). `probeAt` is set from `nextProbeAt`, so files deferred by the quiet period are retried when due, not polled. The loop sleeps until a wake or the earliest `probeAt`.
- **`Hint`:** clean and check the scope as `Scan` does, then queue it exactly as a watcher hint, but with the caller's cause. Pending scopes are a map scope → cause, the job carries each scope's cause, and too many scopes become a whole-root scan with that cause. Tag it `// Planned: LIB-4 (the Sonarr/Radarr webhook queues imported paths).`
- **`ScanNow`:** a `CauseManual` whole-root scan, due now, of each of the library's roots.
- **`Cancel`:** clear the library's pending work and probe flags, and cancel its running jobs (no requeue).
- **`Pause`:** set paused, mark running jobs `requeue`, cancel them. **`Resume`:** unpause and wake both lanes.
- **`Status`:** per root, the stored `Root`, `lastRun` and `fileCounts`, plus the in-memory watch state and activity: `scanning` (its scan runs), else `paused` (paused with work), else `queued` (work pending), else `probing` (its probe turn runs), else `idle`.
- Timers: a stopped timer when there is nothing to wait for, never a busy loop. Remove each timer once its wait ends; no `defer` inside a loop.
- Tag `Reload`, `ScanNow`, `Cancel`, `Pause`, `Resume` and `Status` with `// Planned: LIB-2c (…).`

- [ ] **Step 4: Run the tests to verify they pass, several times**

```bash
go -C server test -race -count=5 ./internal/library/...
```

Expected: PASS on every run.

- [ ] **Step 5: Commit**

```bash
git add server/internal/library
git commit -m "feat(library): scan scheduler with watcher, rechecks and probe turns (LIB-2b)"
```

---

### Task 5: `serve` wiring and the `library` command

**Files:**
- Create: `server/cmd/lisimflix/library.go`, `server/cmd/lisimflix/library_test.go`
- Modify: `server/cmd/lisimflix/serve.go`, `server/cmd/lisimflix/main.go`, `server/cmd/lisimflix/main_test.go`, `server/internal/library/store.go`, `README.md`, `deploy/README.md`

**Interfaces:**
- Consumes: Task 4's `Scheduler`; 2a's `CreateLibrary`, `Library`; `probe.Runner`; `openStorage`, `storagePaths`, `m1config.Load`.
- Produces: `func (s *Store) Libraries(ctx context.Context) ([]Library, error)` (by name); `cmdLibrary(args []string, stdout io.Writer) error`; package vars `newProber func() library.Prober` and `libraryOptions library.SchedulerOptions` (tests replace them); `server.scans *library.Scheduler`, `server.libraryDone chan struct{}`.

- [ ] **Step 1: Write the failing tests**

**Create `server/cmd/lisimflix/library_test.go`:**

```go
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// fakeProber stands in for ffprobe, which unit tests don't have.
type fakeProber struct{}

func (fakeProber) Probe(context.Context, *os.Root, string) (probe.Result, error) {
	return probe.Result{Container: "mov,mp4,m4a,3gp,3g2,mj2", DurationMs: 12500}, nil
}

func fastLibrary(t *testing.T) {
	t.Helper()
	oldP, oldO := newProber, libraryOptions
	newProber = func() library.Prober { return fakeProber{} }
	libraryOptions = library.SchedulerOptions{Debounce: 20 * time.Millisecond, Recheck: 50 * time.Millisecond}
	t.Cleanup(func() { newProber, libraryOptions = oldP, oldO })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// LIB-2b wiring: a library added with the CLI while serve runs is picked up,
// scanned and probed; serve's shutdown stops the scheduler.
func TestServeScansLibraries(t *testing.T) {
	fastLibrary(t)
	f := newFixture(t)
	media := filepath.Join(f.dir, "media")
	os.MkdirAll(filepath.Join(media, "Films", "Heat (1995)"), 0o755)
	os.WriteFile(filepath.Join(media, "Films", "Heat (1995)", "Heat (1995).mkv"), []byte("x"), 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(media, "Films", "Heat (1995)", "Heat (1995).mkv"), old, old)

	var out bytes.Buffer
	if err := run([]string{"library", "add", "--config", f.cfgPath, "--name", "Films", "--type", "movies", "--folder", "Films"},
		nil, &out, &out); err != 0 {
		t.Fatalf("library add: %s", out.String())
	}
	waitFor(t, "scan and probe", func() bool {
		st, err := f.s.scans.Status(context.Background())
		return err == nil && len(st) == 1 && st[0].LastRun != nil && st[0].LastRun.Status == library.RunCompleted &&
			st[0].Files.Present == 1 && st[0].Files.ProbesDue == 0
	})

	out.Reset()
	if code := run([]string{"library", "list", "--config", f.cfgPath}, nil, &out, &out); code != 0 ||
		!strings.Contains(out.String(), "Films") || !strings.Contains(out.String(), "available") {
		t.Fatalf("library list (%d):\n%s", code, out.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.ts.Close()
	if err := f.s.shutdown(ctx, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.s.libraryDone:
	default:
		t.Fatal("scheduler still running after shutdown")
	}
}

func TestLibraryAddChecksInput(t *testing.T) {
	f := newFixture(t)
	for name, args := range map[string][]string{
		"missing folder": {"--name", "X", "--type", "movies", "--folder", "Nope"},
		"escape":         {"--name", "X", "--type", "movies", "--folder", "../config"},
		"bad type":       {"--name", "X", "--type", "music", "--folder", "Movies"},
		"no folder":      {"--name", "X", "--type", "movies"},
	} {
		var out bytes.Buffer
		if code := run(append([]string{"library", "add", "--config", f.cfgPath}, args...), nil, &out, &out); code == 0 {
			t.Errorf("%s: accepted\n%s", name, out.String())
		}
	}
}
```

In `main_test.go`'s `TestUpgradeTakesPreUpgradeSnapshot`, the fake newer migration is renamed `20261231000000_itest_marker.sql` (it shared 2a's timestamp, which OPS-2 refuses now that `serve` registers the catalogue), and the schema assertion no longer assumes a single core migration:

```go
	if len(m.Schema) == 0 || slices.Contains(m.Schema, "20261231000000_itest_marker") {
		t.Fatalf("pre-upgrade snapshot schema = %v, want the old one", m.Schema)
	}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -race -count=1 ./cmd/...
```

Expected: FAIL to build: `undefined: newProber`, `undefined: libraryOptions`.

- [ ] **Step 3: Implement**

- `store.go`: `Libraries` lists library IDs by `name, id` and returns `Library` for each. Remove the `Planned` tags listed in Decision 8.
- `library.go`:
  - `defaultFFprobe = "/usr/lib/jellyfin-ffmpeg/ffprobe"`; `newProber` returns `probe.Runner{Path: $LISIMFLIX_FFPROBE or defaultFFprobe}`; `libraryOptions` is the zero `SchedulerOptions`.
  - `cmdLibrary`: subcommand `add` or `list`. Flags `--config` (default `/config/m1.json`), `--db`, `--backups`, `--name`, `--type`, repeated `--folder`. It loads the config, opens storage with `openStorage` (so migrations and the pre-upgrade snapshot apply as in `serve`), and builds a `library.Store`.
  - `add` first checks each folder is a folder inside the config's media root (through `os.OpenRoot`), then calls `CreateLibrary` and prints `added <id> (<name>)`.
  - `list` prints a `tabwriter` table: `LIBRARY NAME TYPE FOLDER STATE LAST SCAN OK`, one row per root, with the state's reason in brackets and `never` before a successful scan.
- `main.go`: the `library` command, and `library` added to the usage line and the package comment.
- `serve.go`:
  - `openStorage` registers `library.Migrations()` after `storage.Core()`.
  - When storage started, `compose` builds `&library.Scanner{Store: library.NewStore(db), Media: root}` and `library.NewScheduler(sc, newProber(), log, libraryOptions)`, then runs it on its own context in a goroutine that closes `libraryDone`. Otherwise `libraryDone` is closed at once. Readiness doesn't depend on scanning.
  - `shutdown` order: playback, HTTP server, **stop the scheduler and wait for it (bounded by ctx)**, media root, nightly job, database. The scheduler reads through the media root, and its cancelled runs write their outcome to the database, so it must stop before both close.
- `README.md`: after the Status paragraph, add: "Library scanning (LIB-2) is being built: `lisimflix library add` registers a folder of movies, TV or anime, and the server keeps it scanned and probed. Nothing browses libraries yet (LIB-2c, UI-2)."
- `deploy/README.md`, a new section **5. Libraries (preview)**, before the run notes:
  - Adding libraries: `docker compose -f deploy/compose.m1.yml exec lisimflix lisimflix library add --name Movies --type movies --folder Movies` (folders are relative to the media root; repeat `--folder` for more). `library list` shows each folder's state and last good scan. A running server starts scanning a new library within a minute.
  - The server watches library folders for changes, rescans every folder every 6 hours, and probes new files with ffprobe once they've been unchanged for 30 seconds. It never writes to the media.
  - **Watch limit:** each library folder and subfolder needs one inotify watch. If the log shows `library.watch` with `state":"limited"`, raise `fs.inotify.max_user_watches` on the host (e.g. `sysctl fs.inotify.max_user_watches=524288`, made permanent in `/etc/sysctl.d/`). The 6-hourly scan still finds changes meanwhile.
  - **Network and pooled mounts:** changes made on another machine to an SMB or NFS share, or below Unraid's `/mnt/user`, may not raise events. They are picked up by the 6-hourly scan.
  - **Offline disks:** an unmounted or empty library folder is shown as `offline` and nothing in it is marked missing; it is rescanned within a minute of coming back.
  - Logs: `library.scan`, `library.probe` and `library.watch` lines (the event table in Decision 12).

- [ ] **Step 4: Run the full check**

```bash
go -C server test -race -count=3 ./cmd/... ./internal/library/...
make check
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server README.md deploy/README.md
git commit -m "feat(library): run the scan scheduler in serve; library command (LIB-2b)"
```

---

### Task 6: Integration run on the image

**Files:** none. The evidence goes on #77.

- [ ] **Step 1: Build the image and provision a throwaway media folder**

```bash
make image   # lisimflix:<version>, with LIB-1's ffprobe
```

Use a scratch folder outside the repo: `media/Movies/Tears of Steel (2012)/Tears of Steel (2012).mp4` and `media/TV/Show (2019)/Season 01/Show - S01E01.mp4` (copies of the 60 s Tears of Steel smoke clip), plus `Show - S01E02.mkv` of 100 kB random bytes. `init` the config as `deploy/README.md` §3 says, with the clip's probe record. Run `serve` with media read-only, `--read-only --tmpfs /tmp`, as the image's non-root user.

- [ ] **Step 2: Exercise it, checking the logs after each step**

| Action (on the host) | Expected log |
|---|---|
| `library add` Movies and TV through `docker exec` | within 1 min: `library.scan` `cause":"added"` completed for both; `library.probe` stored 1 (Movies) and stored 1 + failed 1 (TV: the random file) |
| copy a new `Show - S01E03.mp4` into Season 01 | ~10 s later a `library.scan` with `cause":"watch"` and that file as `scope`, added 1; `library.probe` deferred 1; ~30 s after the copy `library.probe` stored 1 |
| `rm -r` Season 01 | a scoped `library.scan` of `Show (2019)/Season 01`, missing 3; no whole-root scan |
| `mv media/TV media/TV.away`, wait 70 s | `library.watch` `root_moved`; exactly one `library.scan` failed `root_unavailable`; `library list` shows TV `offline`; nothing missing |
| `mv` it back | within 1 min `library.scan` `cause":"recheck"` completed; `library list` shows `available` |
| `docker stop` | `shutting down` and `stopped` within a second |

- [ ] **Step 3: Record**

Post the log lines and `library list` output on #77 as LIB-2 "integrated" (module and image level; actual scale is 2c). Remove the container and the scratch folder.

---

## Self-review

**Spec coverage:**

| Requirement | Where |
|---|---|
| §3.1 inotify watch plus a 6 h periodic full scan | Tasks 3–4 (`watcher`, `FullScanEvery`), `TestSchedulerStartupWatchAndProbe`, `TestSchedulerPeriodicScan` |
| §3.6 watcher events are hints | Task 3: hints become scopes; every read through `os.Root` (Task 1) |
| §3.6 overflow, remount and restart trigger reconciliation | Task 4: overflow → whole-root scans; lost watch → recheck (`TestSchedulerRearmsLostWatch`); startup → whole-root scans after `MarkInterrupted` |
| §3.6 periodic scans cover missed events and network mounts | Task 4 schedule; deploy README |
| §3.6 duplicate work coalesces | Task 4 (`TestSchedulerCoalescesHints`) |
| §3.6 scans resumable/idempotent; cancel keeps completed work and stops owned probes | 2a's batches; Task 4 Cancel/Pause/shutdown (`TestSchedulerPauseAndResume`, `TestSchedulerShutdownCancelsScan`) |
| §3.6 Scan now; status with queued, waiting for source, scanning, completed, failed and reasons | Task 4 `ScanNow`, `Status` (`Activity`, `FileCounts.ProbesWaiting`, `LastRun` reason); the HTTP side is 2c |
| §3.6 an offline root must not remove its catalogue entries; missing only after enumeration of an online root | Task 1 (`TestScopedScanOfflineRoot`), Task 4 (`TestSchedulerOfflineRootReturns`) |
| §3.6 moves preserve identity; ambiguous cases go to review | Task 1 (`TestScopedScansFoldARename`, `TestScopedScanLeavesLivePairPending`) |
| §10.2 background jobs persist status, reconcile on restart | runs are rows with status/scope/cause; `MarkInterrupted` at startup |
| §11.2 bounded queues, 2 probes, pause maintenance | `MaxScopes`, `ProbeBatch`, `Workers`, `Pause` |
| SCAN-01 (module, with the scheduler) | Tasks 1 and 4 |
| SCALE-01 (module): observable, bounded | Decision 12 logs; `pruneRuns`; probe turns |
| SCALE-01/02, PERF-05 at actual scale | 2c |

**Verified while writing this plan:**
- **Prototype:** all of it ran on the LIB-2a prototype (LIB-1's plan code plus 2a). `make check` passed. The library and command tests passed 5 times in a row under `-race`.
- **Rebuild:** each task was rebuilt in order on the 2a prototype. Each failed with its Step 2 messages before its implementation and passed after, and the result is identical to the prototype.
- **Integration:** Task 6 was run on a prototype image (LIB-1's Dockerfile with ffprobe; read-only, non-root) on zraid. Every row of its table matched: new libraries scanned within the minute; the corrupt file listed as a probe failure; a copied episode scanned about 10 s later and probed after the quiet period; a deleted season marked missing by a scoped scan; an unplugged root gave one failed run in 70 s and was reconciled when it came back; shutdown took 11 ms.
- **Mutation check:** each of these 25 made a test fail:
  - scoped missing-marking not limited to the scope
  - retirement counting scoped runs
  - scoped scans flagging live duplicate pairs
  - scoped scans changing the root's state
  - no root-empty check
  - a gone scope widened to its parent's walk
  - no hint coalescing
  - no `MaxScopes` cap
  - offline rechecks not waiting for a readable root
  - lost watches not noticed
  - Pause not cancelling running work
  - paused probes not requeued
  - the watch armed after the walk instead of before
  - pruning not per kind
  - probes not oldest first
  - new folders not watched
  - removed folders not hinted
  - hidden folders hinted
  - Scan now accepting an unknown library
  - a scoped success on a degraded root not rechecking it
  - no periodic scan
  - no `MaxDelay` for a busy folder
  - a watch-limit report not kept
  - `Hint` not validating its scope
  - `Hint` dropping its cause (both added for LIB-4)
- **Found while prototyping:**
  - fsnotify drops a watch on unmount with no event (Decision 4).
  - The first scoped scan of a renamed folder flagged it as a duplicate (Decision 1).
  - 2a's migration shared a timestamp with a test's fake migration (Task 5).
  - The degraded-root test first raced the startup scan; it now waits for it.
- **Placeholders:** none.
- **Names and types** match between tasks: Task 4's tests use Task 1's `scanScope` and `setupTV`, and 2a's `setupMovies`; Task 5 uses Task 4's `Status` fields.

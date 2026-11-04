# DATA-1a Personal Progress Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record each profile's progress per movie or episode so that resume, watched state and Continue Watching are right after crashes, duplicates, out-of-order events, two devices and explicit user actions. An explicit watched mark is never undone by a late event, and unknown values stay null.

**Architecture:** A new `userdata` package (spec §2.2) on OPS-2's storage, with no HTTP and no dependency on `library`:
- **Rules (pure):** unique watched intervals, the 90% watched rule and the 30-second played rule from spec §3.8.
- **Store:** CTR-2's `UserItemState` per profile and item, an append-only event history, and the merged watched intervals for the current version. `revision` counts explicit changes (watched marks and hide), and those writes are compare-and-set on it. Progress events never touch it.
- **Events:** `Apply` takes one `ProgressEvent` plus the facts the playback session fixed, and answers with CTR-2's `ProgressResult`.

HTTP routes, serve wiring and the playback hookup are **DATA-1b** (Decision 1).

**Tech Stack:** Go 1.27.3 standard library (`database/sql`), `modernc.org/sqlite` through OPS-2's `storage`, CTR-2's `contracts` types. No new dependencies.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md):
- §3.8: progress, the watched rule, Continue Watching, event ordering.
- §10.3 and MIGRATE-01: units, nullability, provenance.
- §0.3 and §10.2: bounded work, short transactions.
- §9.4: the DATA-1 row. §13: PERF-06, DATA-01, MIGRATE-01.

CTR-2 plan ([`2026-10-27-ctr-2-catalogue-contracts.md`](2026-10-27-ctr-2-catalogue-contracts.md)), Decisions 4–7 and R3–R4: revision, `sequence`, duplicate/stale, manual watched, unwatched as reset.

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **DATA-1** (personal progress, P0 / G1), slice **1a** of 2. 1a: rules, store, events, watched and hide writes, Continue Watching, MIGRATE-01 fixtures, all against a temporary database. **1b:** the four HTTP operations (CTR-2's three plus `hideContinueWatching`, Decision 14), serve wiring, and the playback hookup (Decision 1) |
| Issue | #152 |
| Owner | Pete |
| Acceptance | **PERF-06:** resume within 15 s of the last accepted event (positions are stored on every accepted event); an explicit watched mark survives stale and replayed events. **DATA-01:** duplicate and out-of-order events and two sessions never erase newer work. **MIGRATE-01 (module part):** identity, units, timestamps, unknown values and manual-override semantics round-trip; neutral fixtures show namespaced external IDs mapping to distinct local item IDs and an import-origin state surviving later local progress. The "import never overwrites newer local progress" half waits for the importer (DATA-3, G4, #164; Task 5). **§3.8:** watched = 90% unique coverage, Continue Watching = at least 30 s played and not watched |
| File scope | **New only:** `server/internal/userdata/{rules,store,progress,watched}.go`, their tests, `env_test.go` and `migrate01_test.go`, `server/internal/userdata/migrations/20261102120000_userdata_progress.sql`. No change to existing code, `go.mod`, contracts or CI |
| Consumes | OPS-2: `storage.DB`, `storage.Source`, `Core`, `Migrate`, `Open`. CTR-2: `contracts.UserItemState`, `Resume`, `Watched`, `ProgressEvent`, `ProgressResult` and the `Watched*` and `Progress*` constants (`contracts/userdata.go`). Facts the playback session already fixes (`playback.Source`: item, version, source revision), passed in as a `Session` |
| Produces | `userdata.Migrations()`, `Store` (`NewStore`, `Get`, `Apply`, `SetWatched`, `Hide`, `ContinueWatching`), `Session`, `ErrRevisionConflict`, `ErrInvalid`. Rules: `Span`, `Merge`, `Covered`, `Advance`, `IsWatched`, `IsPlayed`, and the constants `WatchedPercent`, `PlayedMinMs`, `MaxTickAdvanceMs` |
| Start dependency | None. OPS-2 and CTR-2 are merged. Nothing here reads LIB-2's tables |
| Standalone test | `go -C server test -race -count=1 ./internal/userdata/`, then `make check` |
| Module proof | A real temporary database and a controlled clock; the harness plays the part of the playback session |
| Integration proof (not in this slice) | 1b: a real playback session reports progress over HTTP, the player resumes from it, and Continue Watching lists it (UI-2's integration proof, #113). Record "module accepted" on #152 when 1a merges |

## Global Constraints

- Go 1.27.3. Module `github.com/Lisimflix/lisimflix/server`. `CGO_ENABLED=0`. Run `make check` before pushing.
- **Units:** positions and durations are milliseconds (`...Ms` fields); instants are stored as UTC Unix milliseconds. Unknown stays `NULL`, never `0` or `false` (§10.3).
- **Client clocks are not authoritative (§3.8):** ordering uses `sequence` per session and the server's receipt time. Event times never come from the client.
- **Short transactions (§10.2):** every operation is one transaction and no operation calls out of the process.
- **Bounded work (§0.3):** `ContinueWatching` takes a limit (default 50, maximum 200, the same bounds as the plan map's lists).
- **Progress outlives the catalogue:** no foreign keys to `library_items`, so a rescan or removal never deletes a user's history (§10.3). `profile_id` is a plain string; there is no profiles table yet.
- Migrations: one file, module `userdata`, `STRICT` tables, timestamp after `20261029120000`.
- Code without a production caller carries `// Planned: <Feature ID> (<caller>).` (#60).
- Workflow: branch `feat/data-1a-progress-store`, PR title `feat(userdata): personal progress store with watched rules (DATA-1a)`, label `area:userdata`. Reviewers come from CODEOWNERS; the PR does not name them.

## Review Focus

Inputs the spec implies but no obvious test covers, most likely first. Each has its test in the task that owns it.

1. **Two devices play the same item.** The latest accepted event wins resume, both histories are kept, and neither session's sequence numbers collide. (Task 3)
2. **A client replays a batch after reconnecting.** Duplicates and out-of-order sequences, in any mix, change nothing and report `duplicate` or `stale`. (Task 3)
3. **A late heartbeat after the user marks watched, unwatched or hides.** It cannot undo a manual mark, a session already open before an unwatched reset cannot mark the item watched again, and a session already open before a hide cannot bring the item back into Continue Watching. (Task 4)
4. **Unknown or odd durations and positions.** A duration of 0 is unknown: never watched, no divide by zero. A negative position is invalid and not stored; a position past the duration is clamped to the duration (Decision 13). (Task 3)
5. **The file is replaced (new source revision) mid-history.** Coverage and resume from the old cut never count toward the new one. (Task 3)

## Decisions taken in this plan (please review)

1. **DATA-1 is split in two slices**, like LIB-2 and META-1.
   - **1a (this plan):** store, rules, events, watched and hide, Continue Watching, fixtures.
   - **1b:** `reportProgress`, `setWatched`, `listContinueWatching` (CTR-2's operations), a new `hideContinueWatching` operation for `Hide` (Decision 14), serve wiring, the playback session supplying `Session`, and LIB-2c's `watched` parameter going live.
2. **The spec's rule, not the archived draft's.** Watched means unique, server-acknowledged media time covers **90%** of the duration (`WatchedPercent`). The "50% plus a natural advance into a verified credits marker" clause is **not built**: no feature produces credits markers before G4, so `IsWatched(coveredMs, durationMs)` has no credits input yet. The 90% and the 30-second minimum are the spec's provisional defaults (§3.8; the spec points at D11, which doesn't mention them, so I'll raise a spec issue instead of editing it).
3. **What counts as covered.** A `tick` or `pause` adds the span from the session's last accepted position to the new position only when it moves forward by at most `MaxTickAdvanceMs` = 30,000 (three times the 10 s heartbeat). A larger forward move adds nothing and is not an error: the position still updates. A `seek` never adds coverage, and the first event of a session adds none. A `stop` counts like a `pause`, so the last stretch before stopping isn't lost. The "last accepted position" includes a seek's target. Playback speed does not matter because only media positions are used.
4. **A tick that moves backwards within a session** (no `seek`) is answered `stale` and changes nothing (§3.8: "backwards progress requires a seek event").
5. **Progress events do not bump `revision` (Pete's call, 2 Nov).** Only `SetWatched` and `Hide` bump it. Otherwise a player reporting every 10 s would make any mark-watched sent from another screen fail with a 409. Automatic watched (from progress) doesn't bump it either; a mark sent with the revision the user last saw still works, which is the right outcome because the user's intent is explicit. **This departs from CTR-2 R3** ("revisions go up by 1 on every committed change") and from the `UserItemState.revision` description in `docs/contracts/openapi.json`. The plan doesn't edit another member's plan or the contract; the PR gets a follow-up issue to reword all three places that say revision counts every committed change or "0 = nothing recorded yet": CTR-2 R3, `server/internal/contracts/userdata.go:30-32` and `docs/contracts/openapi.json:488` (a row with progress only has `revision` 0).
6. **Play count:** `PlayCount` increments each time the item becomes watched automatically (null to watched). Finishing an already-watched item again doesn't count. A manual mark neither counts nor resets it. `LastPlayedAt` is the server time of the last accepted event, and never moves backwards (`max(existing, now)`). Watched, automatic or manual, leaves `resume` as it is; Continue Watching already excludes watched items.
7. **Marking unwatched is CTR-2's reset:** `watched` and `resume` become `null`, coverage is cleared, and the state records `reset_event_id`, the highest event id at that moment (0 if none). A session is *late* for a mark when its lowest recorded event id (accepted or rejected rows; a session belongs to one item) is **at or below** the mark's event id; a session with no rows yet is new. A late session gets `stale` for the rest of the session. Ids, not times, so two things in the same millisecond are still ordered.
8. **Resume is one value per item** (CTR-2 shape), written by the latest accepted event of any session, with that session's version and source revision. Resume for another version isn't copied (§3.8: needs confirmation). Coverage is kept per (version, source revision): an event only adds to its own source's coverage and automatic watched uses that source's total, so a different version or a replaced file starts from empty coverage and two sessions on different sources never wipe each other's. `covered_ms` in the state mirrors the source of the latest accepted event (it drives Continue Watching). Coverage for old sources stays until a later pruning slice.
9. **Events are history, kept in full** (§3.8: "retain both histories"), one row per accepted or rejected event. Pruning is a later slice (Next slices).
10. **Continue Watching** = resume present, `covered_ms` at least `PlayedMinMs` = 30,000, `watched` not true, not hidden. Newest `LastPlayedAt` first. `Hide` records `hide_event_id` the same way a reset does. An accepted event from a session that is not late for the hide clears `hidden`; a late session's events don't (§3.8: a late heartbeat cannot undo an explicit user action). The list has a limit and no cursor in 1a (the CTR-2 operation has a cursor; 1b adds it).
11. **The session's duration comes from the caller** (`Session.DurationMs`), because `ProgressEvent` carries none and the playback session knows the file. 1b wires it from the probe data. **A duration of 0 means unknown.** `Resume.durationMs` has a minimum of 1 in the contract, so with an unknown duration the event is still recorded and `covered_ms` and `LastPlayedAt` still update, but `resume` is not written and nothing is marked watched. Pete agreed this is fine: an unknown duration only happens for a file the probe couldn't read, which already isn't playable.
12. **Multi-episode files:** the caller sets `Session.NoAutoWatched` when it has no per-episode boundaries (§3.8). Coverage and resume are still recorded; automatic watched is not.
13. **Sequences start at 0 or 1:** the contract allows a minimum of 0, so the store accepts `sequence >= 0`. **Positions past the duration** (probe durations are rounded) are clamped to the duration, not rejected.
14. **`Hide` is DATA-1b's fourth operation, and the contract grows by one.** CTR-2 has no hide operation and UI-2 left "Remove from Continue Watching" to DATA-1 (`2026-10-28-ui-2-browse-to-play.md:106`). 1b opens a contract PR first that adds `hideContinueWatching`: `PUT /api/v1/items/{itemId}/continue-watching-hidden` with `{expectedRevision}`, answering `UserItemState`, `409` on a stale revision. 1a builds only the store method; it is tagged `// Planned: DATA-1b (hideContinueWatching)`. #155 lists `Hide` as the second write that bumps `revision`, next to `setWatched`.

## File Structure

| File | Responsibility |
|---|---|
| `rules.go` | Pure: `Span`, `Merge`, `Covered`, `Advance`, `IsWatched`, `IsPlayed`, constants |
| `store.go` | `Store`, `NewStore`, `Migrations()`, `Session`, errors, `Get`, row mapping, the revision bump (explicit writes only) |
| `progress.go` | `Apply`: ordering, coverage, resume, automatic watched |
| `watched.go` | `SetWatched`, `Hide`, `ContinueWatching` |
| `migrations/20261102120000_userdata_progress.sql` | `userdata_state`, `userdata_intervals`, `userdata_events` |
| `env_test.go` | `newEnv(t)`: temporary database, controlled clock, `play` helper |

---

### Task 1: Rules

**Files:**
- Create: `server/internal/userdata/rules.go`
- Test: `server/internal/userdata/rules_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `const WatchedPercent = 90`, `const PlayedMinMs int64 = 30_000`, `const MaxTickAdvanceMs int64 = 30_000`
  - `type Span struct{ StartMs, EndMs int64 }`
  - `func Merge(spans []Span) []Span` (sorted, non-overlapping, touching spans joined, empty spans dropped)
  - `func Covered(spans []Span) int64` (total length of merged spans)
  - `func Advance(prevMs, posMs int64) (Span, bool)`
  - `func IsWatched(coveredMs, durationMs int64) bool`
  - `func IsPlayed(coveredMs int64) bool`

- [ ] **Step 1: Write the failing tests** in `rules_test.go`:
  - `TestMerge`: `[{0,10},{5,20},{20,30},{40,50}]` becomes `[{0,30},{40,50}]`; an unsorted input gives the same; `{5,5}` is dropped.
  - `TestAdvance`: `(1000, 11000)` gives `{1000,11000}, true`; `(1000, 31000)` (exactly 30,000) is true; `(1000, 31001)` is false; `(1000, 1000)` is false; `(1000, 500)` is false.
  - `TestIsWatched`: at duration 100,000, covered 89,999 is false and 90,000 is true; duration 0 is false for any covered; negative duration is false.
  - `TestIsPlayed`: 29,999 is false, 30,000 is true.
  - `TestReplayCountsOnce`: `Covered(Merge(append(spans, spans...)))` equals `Covered(spans)`.
- [ ] **Step 2: Run to verify it fails.** Run: `go -C server test ./internal/userdata/ -run 'TestMerge|TestAdvance|TestIsWatched|TestIsPlayed|TestReplayCountsOnce'`. Expected: FAIL, undefined names.
- [ ] **Step 3: Implement** the signatures above in `rules.go`. `IsWatched` uses integer math (`coveredMs*100 >= durationMs*WatchedPercent`, `durationMs > 0`).
- [ ] **Step 4: Run to verify it passes.** Same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat(userdata): watched and played rules`.

---

### Task 2: Migration and store

**Files:**
- Create: `server/internal/userdata/store.go`, `migrations/20261102120000_userdata_progress.sql`, `env_test.go`
- Test: `server/internal/userdata/store_test.go`

**Interfaces:**
- Consumes: Task 1's `Span`. `storage.Source`, `storage.Open`, `(*storage.DB).Migrate`.
- Produces:
  - `func Migrations() storage.Source` (module `userdata`, same shape as `metadata.Migrations()`)
  - `type Store struct{ db *storage.DB; now func() time.Time }`, `func NewStore(db *storage.DB) *Store`
  - `type Session struct{ ID, ItemID, VersionID, SourceRevision string; DurationMs int64; NoAutoWatched bool }`
  - `var ErrRevisionConflict`, `var ErrInvalid`
  - `func (s *Store) Get(ctx context.Context, profileID, itemID string) (contracts.UserItemState, error)`
  - Harness (tests only): `newEnv(t *testing.T) *env` with `e.st *Store`, `e.clock time.Time` (injected as `st.now`), `e.advance(d time.Duration)`.

- [ ] **Step 1: Write the failing tests** in `store_test.go`:
  - `TestGetUnknownItem`: `Get` for a never-seen profile and item returns `Revision == 0`, `Resume == nil`, `Watched == nil`, `PlayCount == nil`, `LastPlayedAt == nil`, `HiddenFromContinueWatching == false`, and no error.
  - `TestMigrateIsIdempotent`: running `Migrate` twice on the same database leaves the three tables.
  - `TestProfilesAreSeparate`: after the Task 3 helper writes state for `p1`, `Get` for `p2` on the same item is the zero state. (Written with Task 3's helper; mark it `t.Skip` until Task 3, then remove the skip.)
- [ ] **Step 2: Run to verify it fails.** Expected: FAIL, undefined `newEnv`, `NewStore`.
- [ ] **Step 3: Write the migration.** Three `STRICT` tables with explicit column types, no foreign keys, times in UTC Unix ms:
  - `userdata_state(profile_id TEXT, item_id TEXT, revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0), resume_position_ms INTEGER, resume_duration_ms INTEGER, resume_version_id TEXT, resume_source_revision TEXT, resume_updated_at INTEGER, watched_value INTEGER, watched_origin TEXT, watched_at INTEGER, play_count INTEGER, last_played_at INTEGER, hidden INTEGER NOT NULL DEFAULT 0, hide_event_id INTEGER, reset_event_id INTEGER, covered_ms INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (profile_id, item_id))`. Resume, watched, count and time columns are nullable. A state row exists after the first change of any kind; a row with progress only has `revision` 0, the same as no row.
  - `userdata_intervals(profile_id TEXT, item_id TEXT, version_id TEXT, source_revision TEXT, start_ms INTEGER, end_ms INTEGER, PRIMARY KEY (profile_id, item_id, version_id, source_revision, start_ms))`: the merged coverage per source.
  - `userdata_events(id INTEGER PRIMARY KEY AUTOINCREMENT, profile_id TEXT, item_id TEXT, session_id TEXT, sequence INTEGER, kind TEXT, position_ms INTEGER, from_position_ms INTEGER, version_id TEXT, source_revision TEXT, accepted INTEGER NOT NULL, reason TEXT, received_at INTEGER)`, plus `CREATE UNIQUE INDEX ... ON userdata_events(profile_id, session_id, sequence) WHERE accepted = 1`. The index is partial on purpose: a replayed event is stored as a rejected row with the same key, so only accepted rows may be unique.
  - Also an index on `userdata_state(profile_id, last_played_at DESC)` for Continue Watching, and `CREATE INDEX ... ON userdata_events(profile_id, session_id, id)` so the lateness lookup (a session's lowest event id, Decision 7) is one index seek however long the history grows (§0.3). The partial unique index can't serve it because it only covers accepted rows.
- [ ] **Step 4: Implement** `Migrations`, `Store`, `NewStore` (`now: time.Now`), `Session`, the two errors, `Get` (maps the row, or the zero state), and an unexported `bump(tx, profileID, itemID string, expected int64) (int64, error)`. It runs `UPDATE ... SET revision = revision + 1 WHERE ... AND revision = ?` first; if no row matched and `expected == 0`, it runs `INSERT ... ON CONFLICT DO NOTHING` at revision 1. Zero rows changed in the end returns `ErrRevisionConflict`, so a stale `expected` is a conflict, not a key error. Only `SetWatched` and `Hide` call it. `Apply` writes state through its own upsert that leaves `revision` as it is (a new row starts at 0). `newEnv` opens `storage.Open` on `t.TempDir()`, migrates `storage.Core()` and `Migrations()`.
- [ ] **Step 5: Run to verify it passes.** Run: `go -C server test -race -count=1 ./internal/userdata/`. Expected: PASS (the skipped test reported as skipped).
- [ ] **Step 6: Commit** `feat(userdata): progress tables and store`.

---

### Task 3: Progress events

**Files:**
- Create: `server/internal/userdata/progress.go`
- Test: `server/internal/userdata/progress_test.go` (add a `play(t, session, kind, seq, posMs)` helper to `env_test.go`)

**Interfaces:**
- Consumes: Tasks 1–2.
- Produces: `func (s *Store) Apply(ctx context.Context, profileID string, sess Session, e contracts.ProgressEvent) (contracts.ProgressResult, error)`

Behaviour, in order, in one transaction:
1. **Validate** (returns `ErrInvalid`, writes nothing): `sess.ID`, `ItemID`, `VersionID` or `SourceRevision` empty; `e.Kind` not one of the four constants; `e.Sequence < 0`; `PositionMs < 0`; `sess.DurationMs < 0`; `FromPositionMs < 0`; `Kind == seek` without `FromPositionMs`, or `FromPositionMs` on any other kind. A `PositionMs` past a known `sess.DurationMs` is clamped to it (Decision 13), not rejected; the stored position, the event row and `Advance` all use the clamped value.
2. **Order** by `(session, sequence)`:
   - **duplicate:** an *accepted* row already has this sequence.
   - **stale:** otherwise, the sequence is at or below the highest accepted sequence of the session; or a `tick`, `pause` or `stop` whose position is below the session's last accepted position (Decision 4); or the session is late for the item's `reset_event_id` (Decision 7).
   - Rejected events are recorded with `accepted = 0` and the reason (with the session's version and source revision), and answered `Accepted: false` with the unchanged state and no revision bump.
3. **Coverage scope:** read and write only the coverage rows for the session's `(version_id, source_revision)` (Decision 8). A reset deletes all coverage rows for the item.
4. **Coverage:** for `tick`, `pause` and `stop`, add `Advance(last accepted position in this session, e.PositionMs)` when it is ok; the first event of a session has no previous position and adds nothing; `seek` adds nothing. Set the state's `covered_ms` to that source's merged total.
5. **Write:** `last_played_at = max(existing, now)`; clear `hidden` only when the session began after `hide_event_id` (Decision 10). `revision` is left unchanged (Decision 5). When `sess.DurationMs > 0`, also write resume (position, `sess.DurationMs`, version, source revision, server time); when it is 0, leave resume untouched (Decision 11).
6. **Automatic watched:** only if `!sess.NoAutoWatched`, `sess.DurationMs > 0`, `IsWatched(covered_ms, sess.DurationMs)` and `watched` is null: set `{true, auto, now}` and set `play_count = COALESCE(play_count, 0) + 1`. A state whose `watched` is not null (manual or already auto) is never changed (Decisions 6 and 12; CTR-2 Decision 7).

- [ ] **Step 1: Write the failing tests:**
  - `TestFirstTickRecordsResume`: one `tick` at 10,000 ms gives `Accepted`, `State.Revision == 0` (Decision 5), `Resume.PositionMs == 10000`, `Resume.VersionID` and `SourceRevision` equal to the session's, `Watched == nil`.
  - `TestSequenceStartsAtZero`: an event with `Sequence == 0` is accepted.
  - `TestDuplicateAndStale` (**Review Focus 2**): send sequences 1 and 3, then 2: `Accepted == false`, `Reason == "stale"` (never seen, below the highest). Replaying 3 and replaying 1 are both `"duplicate"`. The revision and resume are unchanged after each rejection.
  - `TestReplayedBatchInAnyOrder`: sequences `[3,1,2,2,3,1]` give reasons `[accepted, stale, stale, stale, duplicate, stale]`; the final resume is sequence 3's; `userdata_events` holds six rows, one with `accepted = 1`.
  - `TestBackwardsTickIsStale`: tick 20,000 then tick 5,000 in one session is `stale`; a `seek` with `FromPositionMs = 20000` to 5,000 is accepted and moves resume.
  - `TestSeekOverIntervalDoesNotCount`: ticks at 0 and 10,000, then a seek to 80,000 and a tick at 90,000 give `covered_ms == 20000` (10,000 for 0 to 10,000, 10,000 for 80,000 to 90,000), so `Watched == nil` for a 100,000 ms duration.
  - `TestStopCountsLikePause`: ticks at 0 and 10,000, then a `stop` at 18,000 give `covered_ms == 18000`.
  - `TestWatchedAt90PercentCoverage`: ticks every 10,000 ms from 0 to 90,000 (duration 100,000) set `Watched == {true, "auto", now}` and `PlayCount == 1`; one more tick does not increment `PlayCount`.
  - `TestReplayDoesNotDoubleCount`: playing 0 to 50,000 twice in two sessions leaves `covered_ms == 50000`, so not watched.
  - `TestImplausibleJumpAddsNothing`: a tick that jumps 60,000 ms forward adds no coverage but moves resume.
  - `TestUnknownDuration` (**Review Focus 4**): with `DurationMs == 0`, ticks are accepted, `Watched == nil`, `Resume == nil`, `covered_ms` and `LastPlayedAt` update, no panic.
  - `TestPositionPastDurationIsClamped` (**Review Focus 4**): duration 100,000 and a tick at 100,400 stores resume at 100,000.
  - `TestInvalidEvents`: table over the validation rules in step 1 (including a negative position and a negative sequence), each returning `ErrInvalid` and writing no event row.
  - `TestTwoSessionsLatestWins` (**Review Focus 1**): session A ticks to 30,000, then session B (sequence restarting at 1) ticks to 5,000 then 10,000; resume is B's 10,000, `LastPlayedAt` is B's time, and `userdata_events` holds both sessions' rows.
  - `TestSourceChangeStartsEmptyCoverage` (**Review Focus 5**): on revision `r1` a session covers 0 to 80,000 (duration 100,000, not yet watched). A new session on `r2` then plays 80,000 to 100,000. Its coverage started empty, so `covered_ms == 20000`, `Watched == nil` and `Resume.SourceRevision == "r2"` (carried-over coverage would have marked it watched).
  - `TestTwoSourcesKeepOwnCoverage` (**Review Focus 5**): sessions A (`r1`) and B (`r2`) alternate ticks every 10,000 ms, each covering its own 0 to 50,000. Neither loses coverage (`covered_ms` follows the latest event's source), `Watched == nil`; when A then continues to 90,000 of 100,000 the item becomes watched once.
  - `TestNoAutoWatchedWithoutBoundaries`: with `NoAutoWatched` set, playing 0 to 100,000 records coverage and resume but leaves `Watched == nil`.
  - Manual-watched and reset interactions are tested in Task 4, where those writes exist.
- [ ] **Step 2: Run to verify it fails.** Run: `go -C server test ./internal/userdata/ -run 'TestFirstTick|TestSequenceStarts|TestDuplicate|TestReplayed|TestBackwards|TestSeek|TestStop|TestWatchedAt|TestReplayDoes|TestImplausible|TestUnknown|TestPositionPast|TestInvalid|TestTwoSessions|TestSourceChange|TestTwoSources|TestNoAutoWatched'`. Expected: FAIL, `Apply` undefined.
- [ ] **Step 3: Implement `Apply`** per the six behaviours above in `progress.go` (`session began after` a mark means the session's lowest event id is above the mark's event id; a session with no recorded events yet counts as having begun now). Coverage is read and merged in the same transaction: load rows, `Merge` with the new span, replace the rows (delete then insert); the merged set stays small because adjacent spans join.
- [ ] **Step 4: Run to verify it passes.** Run: `go -C server test -race -count=1 ./internal/userdata/`. Expected: PASS. Remove the skip from `TestProfilesAreSeparate`.
- [ ] **Step 5: Commit** `feat(userdata): ordered progress events`.

---

### Task 4: Watched marks, hide, Continue Watching

**Files:**
- Create: `server/internal/userdata/watched.go`
- Test: `server/internal/userdata/watched_test.go`

**Interfaces:**
- Consumes: Tasks 2–3.
- Produces:
  - `func (s *Store) SetWatched(ctx context.Context, profileID, itemID string, watched bool, expectedRevision int64) (contracts.UserItemState, error)`
  - `func (s *Store) Hide(ctx context.Context, profileID, itemID string, expectedRevision int64) (contracts.UserItemState, error)`
  - `func (s *Store) ContinueWatching(ctx context.Context, profileID string, limit int) ([]contracts.UserItemState, error)`

Behaviour: `SetWatched(true)` writes `{true, manual, now}`. `SetWatched(false)` is the reset (Decision 7): `watched` and `resume` become null, coverage rows are deleted, `covered_ms = 0`, `reset_event_id` = the highest `userdata_events.id` (0 if none). Both bump the revision, and a stale `expectedRevision` returns `ErrRevisionConflict` and changes nothing. `Hide` sets `hidden`, sets `hide_event_id` the same way, and bumps. All three writes are plain state changes: none of them write an event row. `ContinueWatching` follows Decision 10; `limit <= 0` means 50 and anything above 200 is 200.

- [ ] **Step 1: Write the failing tests:**
  - `TestManualWatchedSurvivesLateEvents` (**Review Focus 3**): a session is mid-play (duration 100,000, covered 40,000), then `SetWatched(true)` marks it manually. The clock advances, then the same session, still open, plays on to cover well past 90% (so automatic watched would fire were it not guarded) and replays one already-accepted event. Assert `Watched` is still `{true, "manual"}` with `Origin == "manual"` and the original `At`, `PlayCount` is unchanged, and the replay is `duplicate`.
  - `TestUnwatchedIsAReset`: watch to 90% automatically, `SetWatched(false)` leaves `Watched == nil`, `Resume == nil`, `PlayCount` unchanged and a *new session* that then plays the whole item marks it watched again with origin `auto` (CTR-2 Decision 7); the old session's events are stale.
  - `TestLateSessionAfterResetIsStale` (**Review Focus 3**): session A is mid-play, `SetWatched(false)` runs, then A's next tick is `stale`; a new session B afterwards is accepted.
  - `TestProgressDoesNotBumpRevision` (Decision 5): with a duration of 40,000, five accepted ticks from 0 to 40,000 (which also make it automatically watched) leave `Revision == 0`; a `SetWatched(true)` sent afterwards with `expectedRevision == 0` succeeds and returns `Revision == 1`.
  - `TestRevisionConflict`: `SetWatched` with `expectedRevision` one behind returns `ErrRevisionConflict` and `Get` is unchanged; `expectedRevision == 0` on a never-seen item succeeds and the result has `Revision == 1`.
  - `TestContinueWatching`: items A (40 s covered), B (10 s covered), C (60 s covered, then marked watched), D (60 s covered, then hidden) and E (60 s covered, newer than A) with resume on each; the list is `[E, A]`, so B, C and D are out for their own reason.
  - `TestContinueWatchingLimit`: with 201 qualifying items, `limit` 1000 returns 200, `limit` 0 and `limit` -1 return 50, `limit` 3 returns the 3 newest.
  - `TestHideThenPlayAgainRestores`: after `Hide`, the item is out of the list; a *new* session's first accepted event restores it (Decision 10).
  - `TestLateTickDoesNotUnhide` (**Review Focus 3**): session A is mid-play, `Hide` runs, then A's next tick is accepted but the item stays hidden and out of the list.
  - `TestMarkOrderingUsesEventIDsNotTime` (**Review Focus 3**): with the clock frozen (every write has the same millisecond), a tick from A then `SetWatched(false)` then another tick from A leaves the second tick `stale`, and a new session B right after is accepted.
  - `TestHideKeepsHistory`: after `Hide`, the event rows are all still present.
- [ ] **Step 2: Run to verify it fails.** Run: `go -C server test ./internal/userdata/ -run 'TestManual|TestUnwatched|TestLate|TestMark|TestProgressDoesNot|TestRevision|TestContinue|TestHide'`. Expected: FAIL, undefined methods.
- [ ] **Step 3: Implement** the three methods in `watched.go` using `bump` from Task 2.
- [ ] **Step 4: Run to verify it passes.** Run: `go -C server test -race -count=1 ./internal/userdata/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(userdata): watched marks and continue watching`.

---

### Task 5: MIGRATE-01 fixtures and the check

**Files:**
- Test: `server/internal/userdata/migrate01_test.go`

**Interfaces:**
- Consumes: Tasks 2–4.
- Produces: nothing new. This task proves spec §10.3.

- [ ] **Step 1: Write the tests:**
  - `TestRoundTripKeepsMeaning`: a state with resume, an auto watched, a play count and a hidden flag read back through `Get` with the same units (ms), UTC instants, `Watched.Origin`, and `nil` where nothing was recorded (a state with only `hidden` set has `Resume == nil` and `Watched == nil`, not zero values).
  - `TestLastPlayedNeverMovesBackwards`: after an accepted event at time T, an accepted event applied while the injected clock is an hour earlier leaves `LastPlayedAt` at T (Decision 6).
  - `TestImportFixturesKeepIdentityAndOrigin` (spec §10.3 MIGRATE-01, no importer): a demonstration fixture (not a behaviour check): a test-only map from neutral namespaced source keys (`plex:srvA:1001`, `plex:srvB:1001`, same external ID on two servers) to two distinct local item IDs. Seed each item's state straight into `userdata_state` through SQL (the store has no import path, on purpose): an import-origin watched `{true, "import", at}` with a `last_played_at` an hour before the injected clock, and null resume. Assert (a) the two items stay separate, so the same external ID on two instances doesn't collide; (b) `Get` returns `Origin == "import"` with unknown values still `nil`; (c) a local tick applied afterwards moves `LastPlayedAt` and `Resume` forward to local time, leaves `Watched` as the import value, and doesn't change `PlayCount`. **Deferred to the importer (DATA-3, G4, #164, spec §10.3 "Time and provenance" and the MIGRATE-01 paragraph):** that an import arriving later never makes old progress newer than local activity. 1a has no import path, so a test of it would only test its own SQL.
  - `TestExpectedRevisionZeroAfterExplicitWrite`: after one `SetWatched`, a second `SetWatched` with `expectedRevision == 0` returns `ErrRevisionConflict` and changes nothing.
- [ ] **Step 2: Run.** Run: `go -C server test -race -count=1 ./internal/userdata/`. Expected: PASS if Tasks 2–4 hold; any failure is a bug in them, fixed there.
- [ ] **Step 3: Run the repo check.** Run: `make check`. Expected: PASS (includes `-race` over all packages).
- [ ] **Step 4: Mutation-check** and note it in the PR: change `IsWatched` to `>` instead of `>=`, remove the `watched is null` condition in `Apply` step 6 (must fail `TestManualWatchedSurvivesLateEvents`), remove the late-session check against `reset_event_id`, and remove the `hide_event_id` check, one at a time; each must fail at least one named test. Revert each.
- [ ] **Step 5: Commit** `test(userdata): MIGRATE-01 round-trip fixtures`.

## Next slices

- **DATA-1b:** `reportProgress` (`POST /api/v1/playback/sessions/{sessionId}/progress`), `setWatched` (`PUT /api/v1/items/{itemId}/watched`) and `listContinueWatching` (`GET /api/v1/continue-watching`) per CTR-2 (`docs/contracts/openapi.json`), registering `Migrations()` in serve, mapping `source_changed` 409, supplying `Session` (including `DurationMs`) from the playback session, and flipping LIB-2c's `watched` parameter from `400 invalid_request` to live. UI-2's integration proof (#113) runs here. Also: the contract PR and handler for `hideContinueWatching` (Decision 14), the cursor on `listContinueWatching`, and an issue to reword CTR-2 R3 and the `UserItemState.revision` description (Decision 5). **Extras (LIB-3 Decision 4, #161):** an extra plays under its own playback ID, and the hookup must not call `Apply` for it, so a finished trailer can't mark its parent movie watched or overwrite its resume. 1a needs no change (`Apply` only sees what the hookup passes it).
- **Event history pruning,** once real sizes are known (about 720 rows for a two-hour film at 10 s).
- **Credits clause** (50% plus a natural advance into a verified credits marker) when a feature produces credits markers (G4).
- **Next Up** and the explicit multi-episode-file rules (§3.8) with the episode order they need.

## Self-review

- **Spec coverage:** §3.8 watched rule (Tasks 1, 3; credits clause deferred, Decision 2), seeks and implausible jumps (Task 3), crash resume (positions stored on every accepted event), concurrent sessions and retained history (Task 3), explicit overrides (Task 4), Continue Watching (Task 4); §10.3 and MIGRATE-01 (Task 5; the "never overwrite newer local progress" half is deferred to DATA-3, #164, see Raised); PERF-06 and DATA-01 (Tasks 3–4). Gap: "Next Up", "explicit per-episode actions for multi-episode files" and track precedence belong to later features.
- **Step scan:** every step names a command and its expected result, or a signature and its tests. The plan has no function bodies; the `Apply` order is stated as behaviour.
- **Type consistency:** `Session`, `Span`, the errors and the method signatures are defined once in Tasks 1–2 and used unchanged in 3–5.
- **Review Focus:** the five lines map to `TestTwoSessionsLatestWins`, `TestDuplicateAndStale` and `TestReplayedBatchInAnyOrder`, `TestManualWatchedSurvivesLateEvents`, `TestLateSessionAfterResetIsStale`, `TestLateTickDoesNotUnhide` and `TestMarkOrderingUsesEventIDsNotTime`, `TestUnknownDuration`, `TestPositionPastDurationIsClamped` and `TestInvalidEvents`, `TestSourceChangeStartsEmptyCoverage` and `TestTwoSourcesKeepOwnCoverage`.
- **Checked against `main`:** `metadata.Migrations()` is a function (Task 2 matches it); OPS-2's `userdata` migration (`2026-10-26-ops-2-durable-storage.md:245`) is a test fixture in `migrate_test.go` and doesn't clash with the real tables.
- **Review round 1 (fresh reviewer):** fixed the partial unique index for rejected events, the duplicate/stale definitions and their tests, the "same final state" test, sequence 0, position clamping, hide-vs-late-heartbeat, event ids instead of times for reset and hide, `NoAutoWatched`, `ON CONFLICT` in `bump`, column types, event provenance columns, `covered_ms`, and the tests that could not fail.
- **Raised, not fixed here:** the MIGRATE-01 "import never overwrites newer local progress" proof moves from G1 to the importer (DATA-3, G4, #164) because 1a has no import path, a departure from spec §10.3 and §13; `Resume.durationMs` can't express "unknown" (Decision 11: skipped, agreed with Pete); the contract's Continue Watching summary leaves out the 30 s rule; Decision 5 departs from CTR-2 R3 and two other places. The last two go in issues after this plan merges.
- **Review round 2:** fixed the lateness comparison (at or below), per-source coverage so two sessions can't wipe each other's, the limit and Continue Watching tests, `COALESCE`, negative-value validation, `AUTOINCREMENT`, and the test run patterns. No third review round was run.
- **Not verified:** that `contracts.Resume`'s fields match `docs/contracts/openapi.json` exactly (the Go type was read, not the schema); whether the `sequence` check in `reportProgress` (1b) should also consult the playback session's heartbeat counter.

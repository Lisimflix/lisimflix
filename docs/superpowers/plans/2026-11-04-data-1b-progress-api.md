# DATA-1b Progress API and Playback Hookup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put DATA-1a's progress store behind CTR-2's HTTP operations, so a real playback session reports progress that is stored per profile, resume and watched state can be read back, and Continue Watching lists it.

**Architecture:** `userdata` gets an HTTP layer (`api.go`) that depends on small interfaces it defines (session lookup, item facts, item summaries), so it imports neither `playback` nor `library`; `serve` adapts them. `playback` exposes a read-only session lookup. Two PRs: **A = Tasks 1–5** (needs nothing from LIB-2c, works with the M1 movie), **B = Tasks 6–7** (after #169 merges, Decision 4).

**Tech Stack:** Go 1.27.3 standard library (`net/http`, `database/sql`), CTR-2's `contracts`, OPS-2's `storage`. No new dependencies.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md): §3.8 (progress, watched rule, Continue Watching), §4.9 (session lifecycle, heartbeat), §10.2 (ownership), §12 (User data row), §13 (PERF-06, DATA-01). Builds on the [DATA-1a plan](2026-11-02-data-1a-progress-store.md) (merged, #167) and its "Next slices".

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **DATA-1** (P0 / G1), slice **1b**, in two PRs (A: Tasks 1–5, B: Tasks 6–7). The `watched` list filter is a separate follow-up (Decision 5) |
| Issue | #152; notes from #166, #172 and #155 folded in as listed per task |
| Owner | Pete (also the integration owner for the `app.New`, `serve.go`, `main_test.go` and `openapi.json` overlap with #169, CONTRIBUTING §integration) |
| Acceptance | **PERF-06:** progress posted every 10 s is stored, and a restart keeps resume within 15 s of the last accepted event (read back over HTTP). An explicit watched mark survives stale and replayed events. **DATA-01:** duplicates, out-of-order events and two sessions never erase newer work. Profile B never reads or changes profile A's state |
| File scope | **PR A:** `server/internal/userdata/{api.go, store.go, progress.go, watched.go}` and tests; `server/internal/playback/{sessions.go, http.go}` and tests; `server/internal/contracts/userdata.go` and test; `docs/contracts/openapi.json`; `testdata/contracts/catalogue/` (fixture and `README.md`); `web/contract-tests/fixtures.test.mjs`; `web/src/api/schema.d.ts` (generated); `server/internal/app/app.go` and `app_test.go`; `server/cmd/lisimflix/{serve.go, main_test.go}`; `README.md` (progress status line). **PR B:** `server/cmd/lisimflix/`, `server/internal/library/` (the facts read; the `userState` hook in `getItem` and `listItems`; the summaries-by-ID read, Task 7), `server/internal/app/app.go` (the continue-watching route), `server/internal/userdata/api.go` and tests, `docs/contracts/openapi.json` (`listContinueWatching` to `implemented`) |
| Consumes | DATA-1a (`Store`, `Session`, `ErrInvalid`, `ErrRevisionConflict`), PLY-2 (`playback.Server`), `m1config` (`cfg.Movie()`, `DurationMS`), CTR-2 error codes, `auth.Require`, `contracts.PrincipalFrom` |
| Produces | `userdata.Store.States` (PR A), the `library` user-state hook (PR B), the four operations `reportProgress`, `setWatched`, `hideContinueWatching` (new), `listContinueWatching`; `playback.Server.Session`; `userdata.NewAPI` |
| Start dependency | PR A: none (DATA-1a is on `main`). PR B: LIB-2c code (#169) merged |
| Standalone test | `go -C server test -race -count=1 ./internal/userdata/ ./internal/playback/ ./internal/app/ ./internal/contracts/ ./cmd/lisimflix/`, then `make check` (it runs `test:contracts`) |
| Integration proof | PR A: store state round-trips over HTTP with the M1 movie (Task 5): progress, restart, then `setWatched` returns the stored resume. The M1 movie has no list or detail shape that carries user state, so the full UI-2 proof (#113: reload resumes, Continue Watching lists the item) is Task 7, after Task 6 fills `userState` |

## Global Constraints

- Go 1.27.3, Node 24.22.1, `CGO_ENABLED=0`; no new dependency. Run `make check` before pushing.
- IDs are opaque strings with no length rule of their own (the contract only requires `minLength: 1`); an unknown or over-long ID is `404` through the session lookup or `ItemFacts` `ok=false`. A path never appears in a public shape. JSON is camelCase; a Go nil slice encodes as `[]`.
- Write routes need the `X-CSRF-Token` header (`auth.CSRF` already enforces it before `auth.Require`, so an unauthenticated write without a token is `403`, with a token `401`; handlers add no check). The profile always comes from `contracts.PrincipalFrom`, never from the request.
- Errors go through `contracts.WriteError` with CTR-2's codes (`invalid_request` 400, `not_found` 404, `source_changed` 409, `revision_conflict` 409, `playback_expired` 410, `internal_error` 500). Messages are fixed text of at most 256 bytes, never a store or SQL error.
- A handler makes one store call per request, except the bounded refill in Task 7 (at most 3 pages). Handlers hold no lock.
- Code without a production caller carries `// Planned: <Feature ID> (<caller>).`; PR A removes every `Planned: DATA-1b` tag it makes live, checked in Task 5.
- Workflow: branches `feat/data-1b-progress-api` (A) and `feat/data-1b-catalogue-facts` (B); PR titles `feat(userdata): progress, watched and hide API (DATA-1b)` and `feat(userdata): continue watching and catalogue facts (DATA-1b)`; label `area:userdata`; `Refs #152` and tick boxes on #166, #172, #155. Reviewers come from CODEOWNERS.

## Review Focus

Most likely first; each has its test in the owning task.

1. **The client retries a progress POST after a timeout.** The same body twice gives `200` both times, the second `accepted:false, reason:"duplicate"`, and nothing changes. (Task 4)
2. **Profile B guesses profile A's session or item ID.** A session owned by someone else is `404`; `setWatched` and `hide` touch only the caller's rows. (Task 4)
3. **Progress arrives for a session that ended or whose file changed or vanished.** Ended is `410 playback_expired`, a replaced or missing file `409 source_changed`; neither writes anything. (Tasks 2, 4)
4. **Two tabs mark watched at once.** The second `expectedRevision` is stale: `409 revision_conflict`, nothing changes, and a retry after reloading succeeds. (Task 4)
5. **Storage failed at start-up.** The server still serves; the progress routes answer `404` instead of panicking. (Task 5)
6. **The user hides an item and plays it again.** It reappears in Continue Watching on the first accepted event of a session started *after* the hide (DATA-1a's `isLate` rule, `progress.go:88`); the same session carrying on does not bring it back. (Tasks 4, 7)
7. **Reload the detail page after progress, then toggle watched.** `getItem` carries the stored resume and revision, so the web sends the current `expectedRevision` and gets `200`, not `409`. (Task 6)

## Decisions (3 and 5 decided by Pete on 2026-11-04; the rest proposed, please review)

1. **Hide stays a fourth operation** (DATA-1a Decision 14): `PUT /api/v1/items/{itemId}/continue-watching-hidden`, body `{"expectedRevision": n}` (`HideRequest`), answers `UserItemState`, errors as `setWatched`. Un-hiding is playing again (§3.8).
2. **`userdata` defines the interfaces it needs** (`SessionLookup`, `ItemFacts`, `Summaries`) and its own session sentinels; `serve` adapts `playback` and `library` and translates the errors. This keeps §10.2 ownership: `userdata` imports neither.
3. **A progress report for an item that is not a catalogue item** (an extra, LIB-3 Decision 4, or an unknown ID) is `404 not_found`, nothing stored. **DECIDED (Pete, 4 Nov).** Rejected alternative: `200 accepted:false reason:"stale"` (nearest CTR-2 reason, but mislabels an extra and invents an empty state). The web ignores a 404 on progress (`PlayerPage.tsx:58`), so `404` needs no contract change. `setWatched` and `hide` give the same `404` for an item `ItemFacts` doesn't know.
4. **Ordering around LIB-2c.** PR A uses the M1 movie: `serve` passes an `ItemFacts` closure returning the configured movie's `DurationMs`, `noAutoWatched=false`, `ok = itemID == movie ID`. PR B **chains** the facts the way #169 chains the resolver (`playback.Chain(library.NewResolver(...), single)`): catalogue facts first, then the M1 closure when `itemID == movie.ID`, so the M1 movie, which still plays, keeps its progress, watched and hide, and Task 5's `TestProgressOverHTTPSurvivesRestart` stays green in PR B. When the M1 movie is retired (#77), the PR that removes `playback.Single` removes the M1 closure with it; the rows stored for that ID stay in the database, unreachable, until a pruning slice removes them, and that PR moves Task 5's restart test to a catalogue movie. If #169 merges before PR B, catalogue items play but their progress answers `404` until PR B.
5. **The `watched` filter on `listItems` stays `400 invalid_request`.** Flipping it needs a per-profile join across `library` and `userdata` with keyset paging and touches LIB-2c's query path. **DECIDED (Pete, 4 Nov): separate follow-up slice.** DATA-1 becomes three slices, and the 1a plan's "flip it in 1b" promise moves to it. Either way Task 1 rewords the `listItems` contract text "until DATA-1 is live" to name the filter slice, since DATA-1 is live after PR A.
6. **Missing fields in a progress body are `400`.** Sequence, kind, positionMs and fromPositionMs must all be present (fromPositionMs as `null` for non-seeks), as the contract requires; the handler checks key presence before decoding.
7. **A missing source file is `409 source_changed`** (the same text as a replaced one); the contract lists no `503` for `reportProgress`.
8. **Not folded in, with reasons:** #166 note 2 (`lastAccepted` cost) waits for a pruning slice; #172 note 2 goes to #164 (the importer owns `value:false`); CTR-2 R3's wording lives in Lammy's plan, so Task 1 leaves a comment on #155 for him instead of editing it (CLAUDE.md). #166 note 5's tags: Task 5 clears the ones that become live; `IsPlayed` keeps its tag unless Task 7's handler calls it, and `ContinueWatching` and `Cursor` keep theirs until Task 7. `Get` loses its production callers when Task 3 moves the read into the transaction: Task 3 keeps it with a `Planned:` tag naming the next slice that will call it, or deletes it and points its tests at the in-transaction helper.
9. **`userState` is filled in PR B** (Lammy's review of #174). The contract says `ItemSummary.userState` is the current profile's state for movies and episodes (null for shows and seasons), and the web reads resume and revision only from it (`Detail.tsx:57,65,79`, `Home.tsx:65`). #169 returns null (LIB-2c Decision 5). PR A adds the batch read `userdata.Store.States` (Task 3); PR B adds an optional hook type in `library` (so there is no second read path and no import cycle) that `serve` adapts to `userdata`, and fills `userState` in `getItem` and `listItems` (Task 6); Continue Watching sets it from the state rows `Store.ContinueWatching` already returned (Task 7), with no hook and no extra query. A movie or episode with no stored row gets the zero state (revision 0, nulls, `hidden: false`), so a detail page's first `setWatched` sends the right `expectedRevision`. Rejected: deferring it (reload-resume and Continue Watching resume cannot work, and watched or hide from a freshly loaded detail page gets `409 revision_conflict` once an item reaches revision 1).

## File Structure

| File | Responsibility |
|---|---|
| `userdata/api.go` | `API`, `NewAPI`, `Handler()`, the handlers, cursor encoding, error mapping, session sentinels |
| `userdata/store.go`, `progress.go`, `watched.go` | Task 3 changes only |
| `playback/sessions.go`, `http.go` | `Server.Session` lookup |
| `app/app.go` | `New` takes an optional userdata handler; three mounts |
| `cmd/lisimflix/serve.go` | `userdata.Migrations()`, store, API and adapters; only when storage opened |
| `contracts/userdata.go`, `openapi.json`, `testdata/contracts/catalogue/` | `HideRequest` and the hide operation |

---

## PR A

### Task 1: Contract — `hideContinueWatching`

**Files:**
- Modify: `server/internal/contracts/userdata.go`, `server/internal/contracts/userdata_test.go`, `docs/contracts/openapi.json`, `web/contract-tests/fixtures.test.mjs` (the fixture-to-schema map at ~`:98-106`), `web/src/api/schema.d.ts` (generated)
- Create: `testdata/contracts/catalogue/hide-request.json` (`{"expectedRevision": 3}`; this is the directory `loadCatalogueFixture` reads, and `watched-request.json` lives there); one row in `testdata/contracts/catalogue/README.md`

**Interfaces:**
- Produces: `type HideRequest struct{ ExpectedRevision int64 \`json:"expectedRevision"\` }`; named component schema `HideRequest` (required `expectedRevision`, integer, minimum 0, `additionalProperties:false`); operation `hideContinueWatching` (`PUT /api/v1/items/{itemId}/continue-watching-hidden`, `itemId` path parameter and `X-CSRF-Token` as in `setWatched`; responses 200 `UserItemState`, 400, 401, 403, 404, 409, 500; `x-lisimflix` owner `DATA-1`, status `planned`).

- [ ] **Step 1: Write failing tests:** add `hide-request.json` to `userDataFixtures` as `&HideRequest{}` (`TestUserDataFixturesRoundTrip` fails without the type); map it to the `HideRequest` schema in `fixtures.test.mjs` with invalid variants (negative revision, extra field, missing field) that must fail validation.
- [ ] **Step 2: Run** `go -C server test ./internal/contracts -run TestUserData -v` and `npm --prefix web run test:contracts`. Expected: FAIL.
- [ ] **Step 3: Implement** the type, the schema and the operation. In the same edit, fix the #155 boxes that are ours: reword the `UserItemState.revision` description in `openapi.json` and the doc comment at `userdata.go:30-32` to say progress events do not bump the revision; correct the `listContinueWatching` summary (`openapi.json:248`) to state the 30-second played rule; reword `listItems`' `watched` text per Decision 5. Run `npm --prefix web run generate:api`. Comment on #155 asking Lammy to reword CTR-2 R3 in his plan.
- [ ] **Step 4: Run** the two commands from Step 2. Expected: PASS.
- [ ] **Step 5: Commit** `feat(contracts): hideContinueWatching operation (DATA-1b)`.

---

### Task 2: Playback — read-only session lookup

**Files:**
- Modify: `server/internal/playback/sessions.go`, `server/internal/playback/http.go` (a `testHookStat func()` seam in the style of `testHookAfterAlive` (`http.go:278`), called just before the stat; `owned` takes an id instead of the request; keep `idPattern`; `heartbeat`, `stop` and `content` pass `r.PathValue("id")`)
- Test: `server/internal/playback/session_lookup_test.go`

**Interfaces:**
- Produces:
  ```go
  type SessionRef struct{ ID, ItemID, VersionID, SourceRevision string }
  var ErrSessionNotFound, ErrSessionEnded, ErrSourceChanged error // exported sentinels
  // Session returns the caller's live session and checks its file is still the one the session started on.
  // It never extends the deadline: only the heartbeat keeps a session alive.
  func (s *Server) Session(p contracts.Principal, id string) (SessionRef, error)
  ```

- [ ] **Step 1: Write failing tests** (the package's `env` helpers; `alice` and a second principal `bob`): `TestSessionLookupReturnsOwnedLiveSession` (fields equal the started session's); `TestSessionLookupRefusesOtherPrincipal` (`bob` and an unknown id both give `ErrSessionNotFound`); `TestSessionLookupEnded` (after `DELETE`, `ErrSessionEnded`); `TestSessionLookupSourceChanged` (touch the file after start → `ErrSourceChanged`) and `TestSessionLookupSourceMissing` (remove it → `ErrSourceChanged`); `TestSessionLookupKeepsDeadline` (deadline unchanged after a lookup); `TestSessionLookupDoesNotStatUnderLock` (`testHookStat` blocks on a channel; `heartbeat` on another session still completes).
- [ ] **Step 2: Run** `go -C server test ./internal/playback -run TestSessionLookup -v`. Expected: FAIL.
- [ ] **Step 3: Implement** `Session`: under `s.mu` do the `owned` and `liveLocked` checks and copy `sess.src`; release with `s.unlock()`; then `statSource` the copy outside the lock (the pattern `start` uses at `http.go:101`). Map the stat's `errSourceChanged` and a missing file to `ErrSourceChanged`; use the same `os.SameFile` check against `sess.info` that content delivery uses (`source.go:112`).
- [ ] **Step 4: Run** the same command plus `go -C server test -race ./internal/playback`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(playback): read-only session lookup for progress (DATA-1b)`.

---

### Task 3: Store — HTTP-ready changes (folds in #166 notes 1, 3, 4, #172 notes 1, 4)

**Files:**
- Modify: `server/internal/userdata/{store.go, progress.go, watched.go}`, `server/internal/userdata/{progress_test.go, watched_test.go, env_test.go, migrate01_test.go}` (callers at `watched_test.go:52,180,184`; `"r1"` at `migrate01_test.go:13,20,55,94`), `docs/superpowers/plans/2026-11-02-data-1a-progress-store.md` (one "Next slices" line)

**Interfaces:**
- Produces:
  ```go
  type Cursor struct{ LastPlayedAt time.Time; ItemID string }
  // ContinueWatching pages: after == nil starts at the newest; next is nil on the last page.
  func (s *Store) ContinueWatching(ctx context.Context, profileID string, limit int, after *Cursor) (items []contracts.UserItemState, next *Cursor, err error)
  ```
  `SetWatched` and `Hide` return `ErrInvalid` for an empty `profileID` or `itemID`; `Apply` for an empty `profileID` (it already refuses an empty session or item ID).
  ```go
  // States returns one state per requested item for the profile, the zero state (revision 0, nulls, hidden false) when there is no row. One query for the whole batch.
  func (s *Store) States(ctx context.Context, profileID string, itemIDs []string) (map[string]contracts.UserItemState, error)
  ```
  `States` returns `ErrInvalid` for an empty `profileID`. Its only caller is PR B's hook, so it carries `// Planned: DATA-1b (library UserStates hook, Task 6)`; `Get` keeps its own tag per Decision 8.
  A test seam `Store.afterCommit func()` called between commit and any later read, nil in production.

- [ ] **Step 1: Write failing tests:**
  - `TestEmptyProfileIDIsInvalid`: `Apply`, `SetWatched`, `Hide`, `States` with an empty `profileID`, and the last two with an empty `itemID`, give `errors.Is(err, ErrInvalid)` and write no row.
  - `TestMarkReturnsStateFromItsTransaction` (#166-1): set `afterCommit` to close the database; `SetWatched` and `Apply` return no error and the state just written.
  - `TestStatesBatch`: three items, one with progress and one watched → the stored states, and the zero state (carrying its `itemId`) for the third; another profile's rows are never returned; an empty ID list gives an empty map and no query; 200 IDs (the largest page, `maxLimit`) work in one query; inputs are page-bounded, so no chunking.
  - `TestContinueWatchingPages`: 120 items, some with equal `lastPlayedAt`; pages of 50 cover each exactly once, newest first, ties by `ItemID`, `next` nil on the last page, and an item updated between pages is never duplicated.
  - `TestShortForwardSeekDoesNotCount` (#172-1; a regression test that passes on current code, since only the seek rule at `progress.go:78` excludes the 15,000 ms jump): ticks to 10,000, a `seek` from 10,000 to 25,000, a tick at 30,000 → coverage excludes the 10,000–25,000 jump (fails if `progress.go`'s seek rule is removed).
  - Fixtures use `contracts.SourceRevision(...)` 64-hex values instead of `"r1"`/`"r2"` (#172-4), so the existing tests keep passing with them.
- [ ] **Step 2: Run** `go -C server test ./internal/userdata -run 'TestEmptyProfileID|TestMarkReturns|TestStatesBatch|TestContinueWatchingPages|TestShortForwardSeek' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** First add the `afterCommit` seam at the current post-commit `Get` calls (`progress.go:117`, `watched.go:72`) so `TestMarkReturnsStateFromItsTransaction` fails on behaviour, then read the returned state inside the writing transaction (`Apply`, `mark`); keyset-page on `(last_played_at DESC, item_id ASC)`; wrap every `rows.Err()` as `fmt.Errorf("userdata: ...: %w", err)` (#166-4, a review item, no test). Add to the DATA-1a plan's Next slices: "A pruning slice must keep each session's first event row (lateness depends on it)" (#172-3).
- [ ] **Step 4: Run** the same command plus `go -C server test -race -count=3 ./internal/userdata`. Expected: PASS.
- [ ] **Step 5: Commit** `refactor(userdata): validate IDs, read state in the transaction, page continue-watching (DATA-1b)`.

---

### Task 4: Handlers — `reportProgress`, `setWatched`, `hideContinueWatching`

**Files:**
- Create: `server/internal/userdata/api.go`
- Test: `server/internal/userdata/api_test.go`

**Interfaces:**
- Produces:
  ```go
  var ErrSessionNotFound, ErrSessionEnded, ErrSourceChanged error // userdata-side; serve translates playback's
  type SessionRef struct{ ID, ItemID, VersionID, SourceRevision string }
  type SessionLookup interface {
      Session(p contracts.Principal, id string) (SessionRef, error)
  }
  // ItemFacts reports what the playback session cannot know. versionID "" asks about the item alone.
  // ok=false means "not a catalogue item" (Decision 3).
  type ItemFacts func(ctx context.Context, p contracts.Principal, itemID, versionID string) (durationMs int64, noAutoWatched, ok bool, err error)
  func NewAPI(store *Store, sessions SessionLookup, facts ItemFacts) *API
  func (a *API) Handler() http.Handler // full-path method patterns; requests carry a principal
  ```
- Routes: `POST /api/v1/playback/sessions/{sessionId}/progress`, `PUT /api/v1/items/{itemId}/watched`, `PUT /api/v1/items/{itemId}/continue-watching-hidden`.

- [ ] **Step 1: Write failing tests** (a fake `SessionLookup` and `ItemFacts`, the real `Store` on a temporary DB, requests built with `contracts.WithPrincipal`):
  - `TestReportProgressAcceptsThenDuplicates`: sequence 1 tick → `200 accepted:true`; the same body again → `200 accepted:false reason:"duplicate"` with equal state.
  - `TestReportProgressStale`: sequence 5 then 3 → `accepted:false reason:"stale"`.
  - `TestReportProgressBodyRules`: a missing `sequence`, `kind`, `positionMs` or `fromPositionMs` key, a seek without `fromPositionMs`, a tick with one, a negative sequence, an unknown field and a body over 4 KiB each give `400 invalid_request` and write nothing (Decision 6).
  - `TestReportProgressSessionErrors`: another principal → `404`; `ErrSessionEnded` → `410 playback_expired`; `ErrSourceChanged` → `409 source_changed`; none writes an event row.
  - `TestReportProgressNotACatalogueItem`: `ItemFacts` `ok=false` → `404 not_found`, nothing stored (Decision 3).
  - `TestSetWatchedRevisionConflict`: a stale `expectedRevision` → `409 revision_conflict` and unchanged state; the current revision succeeds; marking unwatched resets resume and watched.
  - `TestSetWatchedAndHideUnknownItem`: `ItemFacts` `ok=false` → `404`.
  - `TestHideAndPlayAgainRestores`: hide → `hiddenFromContinueWatching:true`; an accepted event from the session that was already open leaves it hidden; the first accepted event of a new session clears it (DATA-1a's rule, `watched.go:36-37`; do not change the store).
  - `TestStoreErrorsNeverLeak`: a closed store → `500 internal_error` with the fixed message and no SQL text.
  - `TestProfilesAreIsolated`: profile B's `setWatched` on A's item changes only B's row.
- [ ] **Step 2: Run** `go -C server test ./internal/userdata -run 'TestReportProgress|TestSetWatched|TestHide|TestStoreErrors|TestProfilesAreIsolated' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the handlers: read the body with a 4 KiB `MaxBytesReader`, check required keys from a `map[string]json.RawMessage`, then decode with `DisallowUnknownFields`; map `ErrInvalid` → 400, `ErrRevisionConflict` → 409, the session sentinels as above, anything else → 500 plus one `userdata.error` log line with the request ID and no body. Build the `Session` from `SessionRef` plus `ItemFacts`.
- [ ] **Step 4: Run** the same command plus `go -C server test -race ./internal/userdata`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(userdata): progress, watched and hide handlers (DATA-1b)`.

---

### Task 5: Serve wiring and the HTTP round trip

**Files:**
- Modify: `server/internal/app/app.go` (the 7 `app.New` calls in `app_test.go`, lines 92, 334, 338-343, pass nil for the new argument; `New` gains `userdata http.Handler`; nil means the routes are not mounted; otherwise mount `POST /api/v1/playback/sessions/{sessionId}/progress` and the two `PUT /api/v1/items/{itemId}/…` patterns behind `am.Require`), `server/internal/app/app_test.go`, `server/cmd/lisimflix/serve.go` (`userdata.Migrations()` beside `library.Migrations()` in `openStorage`; the store, API, a `SessionLookup` adapter that translates `playback`'s sentinels, and the M1 `ItemFacts` closure; **built only when `s.db != nil`**), `server/cmd/lisimflix/main_test.go` (a `reopen()` helper: shut the first server down, call `compose` again on the same `cfgPath` and DB, swap `f.ts.Config.Handler`, register cleanup for the new server, and sign in again since auth sessions are in memory; the pattern is at `main_test.go:371-385`), `server/internal/userdata/*.go` (drop the `Planned: DATA-1b` tags that are now live), `README.md` (the progress status line)

**Interfaces:**
- Consumes: Tasks 2–4; `m1config`'s configured movie (`ID`, `DurationMs` = 12,500 ms in the fixture, `main_test.go:36`).
- Produces: the three routes on the real server; the M1 `ItemFacts` is `func(_ context.Context, _ contracts.Principal, itemID, _ string) (int64, bool, bool, error) { return movie.DurationMs, false, itemID == movie.ID, nil }`.

- [ ] **Step 1: Write failing tests:**
  - `TestProgressOverHTTPSurvivesRestart` (`main_test.go`): sign in, start a session, POST progress ticks at 5,000 and 10,000, `reopen()`, then `PUT watched {"watched":true,"expectedRevision":0}` → `200`, `resume.positionMs == 10000`, `revision == 1`.
  - `TestProgressNeedsCSRFAndSignIn`: no token → `403 csrf_failed`; a token but no session → `401`; nothing stored.
  - `TestProgressRoutesAbsentWhenStorageFailed`: a composition whose storage failed still serves; after signing in (an unauthenticated POST is `401`), the three routes answer `404` with no panic.
  - `TestUserdataRoutesRequireSignIn` (`app_test.go`, fetching an anonymous CSRF token as `app_test.go:150-153` does): the three routes give `401` without a principal.
- [ ] **Step 2: Run** `go -C server test ./internal/app ./cmd/lisimflix -run 'TestProgressOver|TestProgressNeeds|TestProgressRoutesAbsent|TestUserdataRoutes' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the wiring. Update the README line that says progress "runs against fixtures". Flip `x-lisimflix.status` to `implemented` for `reportProgress`, `setWatched` and `hideContinueWatching` in `openapi.json` (CTR-2 plan: the producer does it in the PR that registers the handler), regenerate `schema.d.ts`, and add `TestProgressResponsesMatchContract` (the real HTTP responses of the three operations decode strictly, unknown fields refused, into `ProgressResult`/`UserItemState`). Then run `grep -rn 'Planned: DATA-1b' server`; the expected output is exactly `IsPlayed`, `ContinueWatching`, `Cursor` (no caller until Task 7) and `States` (until Task 6; Decision 8).
- [ ] **Step 4: Run** the same command plus `make check`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(serve): wire the progress API (DATA-1b)`.

---

## PR B (after #169 merges)

### Task 6: Catalogue facts for progress

**Files:**
- Modify: `server/cmd/lisimflix/{library.go, serve.go}` (the catalogue `ItemFacts` is chained in front of the M1 closure; the user-state adapter), `server/internal/library/` (one read: version → probe duration, and whether its file backs several episodes; the `UserStates` hook and its use in `getItem` and `listItems`), tests beside each

**Interfaces:**
- Consumes: LIB-2c's `Store`, `Resolver` and `Grant` as merged in #169 (read the real names from that code first), and LIB-3's shared-file rule when it exists (until then `noAutoWatched` is true when two episode items share the file, the read LIB-3 will extend).
- Produces:
  ```go
  // in library: optional; nil leaves userState null (LIB-2c's behaviour).
  type UserStates func(ctx context.Context, p contracts.Principal, ids []string) (map[string]contracts.UserItemState, error)
  ```
  `getItem` and `listItems` call it once per request with the IDs of the movies and episodes in the response and set `ItemSummary.userState` from the result (shows and seasons stay null). `library.NewAPI` (called at `serve.go:105`) takes it as a trailing parameter, nil allowed. `serve` adapts it to `userdata.Store.States` with the principal's profile ID. A `States` error is a `500 internal_error` through the handler's usual failure path, with no detail leaked. Lammy offered to add the hook on the LIB-2c side if that order suits better; if he does, this task only adapts it (read the real names from #169).
- Produces: an `ItemFacts`: `durationMs` from the stored probe when it matches the file revision (else 0), `noAutoWatched` as above, `ok=false` for IDs that are not catalogue items or versions, or that the principal may not see. `serve` chains it: catalogue facts first, then the M1 closure when `itemID == movie.ID` (Decision 4).

- [ ] **Step 1: Write failing tests:** `TestFactsChainKeepsM1Movie` (a regression guard: it passes on PR A code and fails if the chain drops the M1 closure; the M1 movie's progress, watched and hide still work after the chain; an unknown ID is `404`); `TestGetItemCarriesResume` (progress, then `getItem` shows `userState.resume` and the revision; a movie with no row shows the zero state; an episode listed under a season carries its state, a show and a season show null); `TestDetailStyleSetWatchedRoundTrip` (after a watched toggle and a fresh `getItem`, the next `setWatched` with that `userState.revision` gives `200`, not `409`); `TestListItemsUserStateIsBatched` (one `States` call for a page of 50 movies, none for a page of shows); `TestFactsUseProbeDuration` (a probed movie gives its duration; a stale probe gives 0); `TestFactsSharedFileDisablesAutoWatched` (two episodes on one file, both true); `TestFactsUnknownOrHiddenItem` (`ok=false`, including a library the principal can't see); `TestWatchedAtNinetyPercentOverHTTP` (a catalogue movie becomes watched after progress covering 90% of its probed duration, not at 89%); `TestWatchedRouteSurvivesCatalogueMount` (`PUT /api/v1/items/{id}/watched` reaches userdata with #169's `/api/v1/items/` prefix mounted).
- [ ] **Step 2: Run** `go -C server test ./internal/library ./cmd/lisimflix -run 'TestFacts|TestGetItemCarriesResume|TestDetailStyleSetWatched|TestListItemsUserState|TestWatchedAtNinety|TestWatchedRoute' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the read, the hook and the adapters, and remove `States`' `Planned` tag; in `serve.go` chain the catalogue facts in front of the M1 closure (do not replace it).
- [ ] **Step 4: Run** the same command plus `TestProgressOverHTTPSurvivesRestart` (Task 5's test, still green) and `make check`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(library): item facts and user state for progress (DATA-1b)`.

---

### Task 7: `listContinueWatching` and the UI-2 proof

**Files:**
- Modify: `server/internal/userdata/api.go`, `server/internal/userdata/api_test.go`, `server/cmd/lisimflix/{library.go, serve.go}` (the `Summaries` adapter), `server/internal/library/` (one read), `server/internal/app/app.go` (one more route: `GET /api/v1/continue-watching`)

**Interfaces:**
- Consumes: Task 3's `ContinueWatching`; LIB-2c's item-summary builder (read its real name from #169), which has no by-ID read yet (its `scanItem` is private to `Store.Items`), so Task 7 adds one. The handler sets each summary's `UserState` from the `UserItemState` row the store returned, so no hook and no second store call is involved (Global Constraints).
- Produces:
  ```go
  // Summaries returns the summaries of the items the profile may see, keyed by id; others are absent.
  type Summaries func(ctx context.Context, p contracts.Principal, ids []string) (map[string]contracts.ItemSummary, error)
  func NewAPI(store *Store, sessions SessionLookup, facts ItemFacts, sums Summaries) *API // gains the last parameter; Task 5's call is updated
  ```
  Route: `GET /api/v1/continue-watching?cursor=&limit=` → `ItemPage` with `total: null`. The cursor is base64url JSON of `Cursor` plus the profile ID, at most 512 bytes.

- [ ] **Step 1: Write failing tests:** `TestContinueWatchingListsNewestFirst` (three items, summaries in `lastPlayedAt` order, `total` null); `TestContinueWatchingPagesWithCursor` (limit 2 of 5 gives `nextCursor`, the next call continues, the last has null); `TestContinueWatchingBadCursorOrLimit` (undecodable, over 512 bytes, another profile's cursor, `limit` 0 or 201 → `400`); `TestContinueWatchingSkipsUnseeable` (items the profile can't see are omitted and the page is refilled from further store pages, at most 3, so a hidden run doesn't end the list early); `TestContinueWatchingNeverListsWatchedOrHidden`; `TestContinueWatchingProfileScoped`; `TestContinueWatchingItemsCarryResume` (each listed item's `userState.resume` matches the store); `TestHiddenItemReturnsInNewSession` (hide; an event from the already-open session leaves it unlisted; the first event of a new session lists it again).
- [ ] **Step 2: Run** `go -C server test ./internal/userdata -run TestContinueWatching -v` and `-run TestHiddenItemReturns`. Expected: FAIL.
- [ ] **Step 3: Implement** the handler and the `Summaries` adapter. Flip `listContinueWatching` to `implemented` in `openapi.json`, regenerate `schema.d.ts`, and add `TestContinueWatchingResponseMatchesContract` (strict decode into `ItemPage`). Remove the `Planned` tags on `ContinueWatching` and `Cursor`.
- [ ] **Step 4: Run** the same commands plus `make check`. Then the UI-2 proof (#113) by hand and record it on #152: (1) `make` the server and web, start with a catalogue root and one probed movie; (2) sign in, play it for 45 s (Continue Watching needs 30 s of covered time, `PlayedMinMs`), close the tab; (3) open the movie's detail page in a fresh tab and press Resume: the player resumes within 15 s of where you stopped; (4) the home page's Continue Watching row lists the movie, and its hero offers Resume; (5) mark it watched: it leaves the row. Expected: all five hold.
- [ ] **Step 5: Commit** `feat(userdata): list continue watching (DATA-1b)`.

---

## Self-review

- **Spec coverage:** §3.8 progress, resume, explicit overrides and Continue Watching over HTTP (Tasks 4, 5, 7); §4.9 ended and changed sessions (Tasks 2, 4); §12 User data row (all four operations); PERF-06 and DATA-01 over HTTP (Tasks 4, 5); §10.2 ownership (Decision 2); `ItemSummary.userState` for movies and episodes (Decision 9, Tasks 3, 6, 7). Not here by design: the `watched` list filter (Decision 5), Plex/Jellyfin import (#164), pruning, `lastAccepted` cost (Decision 8).
- **Types:** `SessionRef` is defined in `playback` (Task 2) and mirrored in `userdata` (Task 4); `serve` adapts and translates the sentinels. `ItemFacts` is the one signature in Tasks 4–6 (principal-aware); `NewAPI` gains `Summaries` in Task 7, which updates Task 5's call. `UserStates` is defined in `library` (Task 6) and adapted to `userdata.Store.States` (Task 3) by `serve`, so neither package imports the other.
- **Proportion:** seven tasks, each a reviewable gate, in two PRs; no step carries a function body the signature and tests already decide.
- **Open points for review:** the exact LIB-2c names for Tasks 6–7, which are read from #169's merged code, not guessed here.

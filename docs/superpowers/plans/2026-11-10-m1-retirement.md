# M1 Retirement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the M1 single-movie path (`M1App`, `GET /api/v1/movies`, `playback.Single`, the movie in `m1.json`) now that the catalogue serves and plays every library item, without breaking an existing install or DATA-1b's approved plan.

**Architecture:** Two PRs, each removing what nothing still needs:
- **PR A (Tasks 1–2, can start now):** the browse app stops falling back to `M1App`, and the `listMovies` operation and its `Movie`/`MovieList` contract types go. The configured movie still plays by its ID through `Chain(catalogue, Single)`, so DATA-1b PR A (which plays it in its Task 5 test) is unaffected.
- **PR B (Tasks 3–6, after DATA-1b PR B merges):** the test fixtures move to a catalogue movie first, then `Single`, the readiness check on the movie, the movie fields of the config, `init`'s movie flags and DATA-1b's M1 `ItemFacts` closure go.

**Tech Stack:** Go 1.27.3 standard library; React/TypeScript web with Playwright; no new dependencies.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md): §5.7 (states), §8.3 (breaking contract changes need a plan), §9.4 (LIB-2 row), §12 (API conventions: "Breaking changes need an explicit compatibility/version plan, especially after native clients ship").

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **LIB-2** follow-up: the retirement that LIB-2c Decision 11 assigns to "its own small PR with this plan's issue as owner, opened when PR C merges" (UI-2 PR C merged as #142). It also discharges UI-2 Decision 10's `// shortcut:` and CTR-2 Decision 12 |
| Issue | #184 (claim), refs #77 |
| Owner / reviewers | Lammy (claimant) / CODEOWNERS (Pete, Lammy and one backup). Pete is the integration owner for the `serve.go`/`main_test.go` overlap with DATA-1b |
| Acceptance | No production code path names M1's movie: `grep -rn 'playback.Single\|M1App\|listMovies\|MovieList\|cfg.Movie()' server web/src` is empty after PR B. Every guard that only `M1App`'s tests pinned is pinned against the browse player. An `m1.json` written by today's `init` still loads. `make check` passes on each PR |
| File scope | **PR A:** `web/src/{main.tsx, api/client.ts, player/M1App.tsx (deleted), player/VideoPlayer.tsx, player/start.ts}`, `web/tests/{player.spec.ts (deleted), player-page.spec.ts (new), browse.spec.ts, fake-api.ts}`, `web/contract-tests/fixtures.test.mjs`, `web/src/api/schema.d.ts` (generated), `docs/contracts/openapi.json`, `testdata/contracts/m1/{movies.json (deleted), README.md}`, `server/internal/contracts/{types.go, types_test.go, nulls_test.go}`, `server/internal/playback/{http.go, sessions.go, source.go}` and their tests, `server/internal/m1config/config.go`, `server/internal/app/app.go`, `server/cmd/lisimflix/{main_test.go, integration_test.go}`. **PR B:** `server/cmd/lisimflix/{serve.go, init.go, probe.go (deleted), main_test.go, integration_test.go, library_test.go}`, `server/internal/playback/{source.go, helpers_test.go, sessions_test.go, source_test.go}`, `server/internal/auth/sweep_test.go`, `server/internal/m1config/{config.go, config_test.go}`, `deploy/README.md`, `testdata/media/README.md`, `README.md`, this plan's map line |
| Consumes | LIB-2c (#169): `library.NewResolver`, `library.M1Access`, `playback.Chain`, the catalogue API. LIB-2b: `lisimflix library add`. DATA-1b (#174): PR B's chained `ItemFacts` (Decision 4) and `TestProgressOverHTTPSurvivesRestart` |
| Produces | `m1config.LegacyMovie(c Config) bool`; the test helpers `fixture.addMovieLibrary()` and `browser.startItem(itemID, versionID string, key string)`; `playback`'s test resolver `fixed`. Nothing new in production |
| Start dependency | PR A: none. PR B: **DATA-1b PR B merged** (Decision 1) |
| Standalone test | `make check` on each PR, plus `go -C server test -race -tags integration ./cmd/lisimflix/` with the ToS file (testdata/media/README.md) on PR B |
| Integration proof | PR B, Task 6: the real ToS file plays end to end through a library (login → session → original ranges → logout revokes), replacing the M1-config route |

## Global Constraints

- Go 1.27.3, `CGO_ENABLED=0`, `make check` before pushing. Read-only media (SAFE-01).
- **Contract is the source of truth:** removing an operation or schema changes `docs/contracts/openapi.json`, the Go types, the fixtures and the generated `schema.d.ts` in the same PR.
- **No leaks (§6):** an unknown item, including the retired M1 movie's ID, is `404 not_found` with the same body as any unknown ID.
- Never edit a merged migration. This plan adds none.
- Don't edit another member's plan: DATA-1b's Task 5/6 text stays as approved; PR B only does what its Decision 4 already says the retirement PR does.
- Workflow: branches `refactor/m1-retire-web-and-movies` (PR A) and `refactor/m1-retire-single` (PR B). PR titles `refactor(web): retire the M1 fallback and listMovies` and `refactor(playback): retire the M1 movie resolver and config fields`. Labels `area:playback`, `area:library`, `area:web` (PR A), `area:api`. Don't request reviewers.

## Review Focus

1. **Upgrading with today's `m1.json`**, which names a movie: the server starts, logs one WARN naming `lisimflix library add`, and serves everything else. It must not refuse the file (it rejects unknown fields today). (Task 5: `TestLegacyMovieFieldsLoadAndWarn`.)
2. **Storage down, so the catalogue isn't mounted:** the browse app shows its error state with the diagnostic ID and Retry, not a blank page or the old M1 screen, and `/health/ready` is 503. (Task 1: `librariesNotServedShowsError`; Task 4: `TestStorageDownServesNoItems`.)
3. **A player guard only `M1App`'s tests pinned** (one idempotency key across retries, a cross-origin media URL refused, autoplay rejection shows Play, heartbeats don't reload the source, an expired session shows Play again, a late start can't replace the current one): each is pinned against the browse `PlayerPage`. (Task 1.)
4. **A saved link or open tab still using the M1 movie's ID** after PR B: starting a session is `404 not_found`, the same body as any unknown ID. (Task 4: `TestRetiredM1IDIsNotFound`.)
5. **An owner whose only playable file was the M1 movie:** after PR B the home page is empty until a library covers that folder; the WARN in point 1 and the deploy README say exactly what to run. (Task 5 asserts the message; Task 6 updates the README.)

## Decisions (all proposed, please review)

1. **Two PRs, and PR B after DATA-1b PR B.** DATA-1b PR A plays the configured movie through `Single` and reads `cfg.Movie()` (#174 Decision 4, Task 5); PR B chains catalogue facts in front of the M1 closure, and its Decision 4 already says "the PR that removes `playback.Single` removes the M1 closure with it" and moves `TestProgressOverHTTPSurvivesRestart` to a catalogue movie. Retiring `Single` first would strand PR A's integration proof, and retiring it between DATA-1b's PRs would make the M1 movie's progress 404 for a release. PR A touches neither, so it lands now. **Rejected:** one PR after DATA-1b (keeps the M1 screen and `/movies` alive for weeks for no reason); retiring before DATA-1b PR A (needs #174 amended to use a catalogue fixture in PR A).
2. **`listMovies`, `Movie` and `MovieList` are removed outright, with no deprecation period.** Spec §12 asks for a compatibility plan "especially after native clients ship"; none has, and the only consumer (`M1App`) goes in the same PR. `contracts.Movie` moves to `m1config.Movie` with the same fields, so `cfg.Movie().ID` and `.DurationMs` (which DATA-1b uses) still compile. `playback.Single` takes `(movieID, sourceRevision, relativePath string, authorize func(contracts.Principal, string) bool)` instead of a `contracts.Movie`.
3. **Old configs keep loading.** `m1config.Config` keeps the nine movie fields (`movieId`, `relativePath`, `title`, `sourceRevision`, `videoCodec`, `audioCodec`, `width`, `height`, `durationMs`) as optional, unvalidated JSON fields, so `DisallowUnknownFields` still catches typos. `LegacyMovie(c)` reports whether any is set; serve logs one WARN: `m1.json still names a movie (<relativePath>); it is no longer served: add its folder with "lisimflix library add"`. The file is never rewritten (owner-provisioned, `SaveNew` never replaces), and `init` stops writing the fields. **Rejected:** refusing to start (an upgrade would take a working server down), and a `config migrate` command (more surface for nine ignorable fields).
4. **Names stay:** the `m1config` package, `/config/m1.json`, `deploy/compose.m1.yml`, the `lisimflix:m1` image tag and the `LISIMFLIX_M1_*` test variables. They are in owners' commands and scripts; renaming them belongs to the ACC-1 setup and OPS packaging work, not here.
5. **Readiness is storage only.** `/health/ready` drops `single.Check(root)`; the media root is already required at start (`os.OpenRoot` in `compose`), and an offline library root is visible state (§3.6), not unreadiness.
6. **With storage down, serve passes `playback.Chain()` (no resolvers),** which answers `ErrNotFound` for everything, so `playback.New` keeps its required `Resolver` and playback answers 404. `Chain` stays; with storage up serve passes the catalogue resolver directly.
7. **Rows stored for the M1 movie's ID stay** in `userdata` until a pruning slice removes them (#174 Decision 4). Nothing reads them once the ID can't be played.
8. **M1 evidence docs stay unchanged:** `docs/testing/m1-results.md` and `m1-evidence-template.md` record what M1 proved. The M1 plans stay as history.
9. **#170's "catalogue DB error skips the M1 fallback" note is closed by PR B** (there is no fallback left); PR B ticks it.

## File Structure

No new production files. `web/tests/player-page.spec.ts` replaces `player.spec.ts`. `server/cmd/lisimflix/probe.go` (`parseProbe`, M1's source qualifier) is deleted with `init`'s movie flags; the scanner's own probe (`internal/probe`) is unchanged.

---

## PR A

### Task 1: The browse app without the M1 fallback

**Files:**
- Modify: `web/src/main.tsx` (render `<App client={client} />` directly; delete the `// shortcut:` and the `libraries()` probe), `web/src/api/client.ts` (delete `movies()` and the `M1Client` alias), `web/src/player/VideoPlayer.tsx` and `web/src/player/start.ts` (take `Client`; fix the "Shared by M1App" comment), `web/tests/fake-api.ts` (delete the `/api/v1/movies` route)
- Delete: `web/src/player/M1App.tsx`, `web/tests/player.spec.ts`
- Create: `web/tests/player-page.spec.ts`
- Modify: `web/tests/browse.spec.ts`

**Interfaces:** Consumes the browse `PlayerPage` and the fake API's catalogue routes. Produces nothing for later tasks.

- [ ] **Step 1: Write the failing tests.** In `browse.spec.ts`, replace `fallsBackToM1WhenLibrariesNotServed` with `librariesNotServedShowsError`: the fake API answers `GET /api/v1/libraries` with `404 not_found`; after sign-in the page shows the error state with the response's request ID and a Retry button, and no "Synthetic Test Movie" heading. In `player-page.spec.ts`, port each `player.spec.ts` test to the browse flow (sign in → item detail → Play): `loginRequiresValidCredentials`, `csrfRotatesAfterLogin`, `startUsesOneIdempotencyKeyOnRetry`, `deadSessionKeyStartsAgainWithFreshKey`, `crossOriginMediaURLIsRejected`, `serverErrorLeavesRetryAvailable`, `lateStartCannotReplaceCurrentMovie`, `playbackAdvancesOnRealShortClip`, `autoplayRejectionShowsPlayControl`, `heartbeatsContinueWithoutReplacingVideoSource`, `expiredSessionShowsPlayAgain`, `endedStopsSession`, with the same assertions. A test may be dropped only if an existing spec asserts the same thing; the PR body names it (e.g. `followups-b.spec.ts › …`).
- [ ] **Step 2: Run** `npm --prefix web run test:browser -- browse.spec.ts player-page.spec.ts`. Expected: `librariesNotServedShowsError` FAILS (the M1 screen renders); the ported tests pass or fail only where the browse player really differs: record each such difference in the PR body and fix it in Step 3.
- [ ] **Step 3: Implement** the `main.tsx`, `client.ts`, player and fake-API changes above.
- [ ] **Step 4: Run** the same command, then `make check`. Expected: PASS.
- [ ] **Step 5: Commit** `refactor(web): drop the M1 fallback; pin the player guards on the browse player`.

### Task 2: Remove `listMovies` and the `Movie` contract types

**Files:**
- Modify: `docs/contracts/openapi.json` (delete `GET /api/v1/movies`, `Movie`, `MovieList`), `web/contract-tests/fixtures.test.mjs` (delete the `movies.json` entries and its five invalid variants), `testdata/contracts/m1/README.md`; delete `testdata/contracts/m1/movies.json`
- Modify: `server/internal/contracts/types.go` (delete `Movie`, `MovieList` and its `MarshalJSON`) and `types_test.go`, `nulls_test.go`
- Modify: `server/internal/m1config/config.go` (add `type Movie struct{ ID, SourceRevision, Title string; DurationMs int64; VideoCodec, AudioCodec string; Width, Height int }`; `Config.Movie() Movie`)
- Modify: `server/internal/playback/{http.go, sessions.go, source.go}` (delete the route, `Options.Movies`, `listMovies`, `M1.Movies`; `Single(movieID, sourceRevision, relativePath string, authorize func(contracts.Principal, string) bool) *M1`) and their tests; `server/internal/app/app.go` (drop the `/api/v1/movies` mount and its doc line)
- Modify: `server/cmd/lisimflix/serve.go` (the new `Single` call; no `Movies`), `main_test.go` (`browser.start` uses `f.s.cfg.MovieID`), `integration_test.go` (the two `MovieList` reads use the config's movie ID)
- Generated: `web/src/api/schema.d.ts`

**Interfaces:** Produces `m1config.Movie` and the new `playback.Single` signature (Task 4 deletes both).

- [ ] **Step 1: Write the failing test** `TestMoviesRouteIsGone` (`server/internal/app/app_test.go`): a signed-in `GET /api/v1/movies` answers `404 not_found` as JSON (the API's catch-all), not the SPA.
- [ ] **Step 2: Run** `go -C server test ./internal/app -run TestMoviesRouteIsGone`. Expected: FAIL (200 with the movie list).
- [ ] **Step 3: Implement** the removals above. `grep -rn 'listMovies\|MovieList\|contracts.Movie\b\|/api/v1/movies' server web/src web/tests docs/contracts testdata/contracts` must print nothing.
- [ ] **Step 4: Run** `go -C server test -race ./...` and `make check` (it regenerates `schema.d.ts` and fails on a stale one). Expected: PASS.
- [ ] **Step 5: Commit** `refactor(api): remove listMovies and the Movie contract types`, then open PR A (Refs #184, #77; the body lists any dropped player test from Task 1).

---

## PR B (after DATA-1b PR B merges)

### Task 3: Test fixtures on a catalogue movie

**Files:**
- Modify: `server/cmd/lisimflix/main_test.go` (`newFixtureWith` adds a `Movies` library over `media/Movies` and waits for its first scan with probes; `browser.startItem`), `server/cmd/lisimflix/library_test.go` (reuse `fastLibrary`/`waitFor`), every test that calls `browser.start`, and DATA-1b's `TestProgressOverHTTPSurvivesRestart`
- Modify: `server/internal/playback/{helpers_test.go, sessions_test.go, source_test.go}` and `server/internal/auth/sweep_test.go` (use a test resolver instead of `Single`)

**Interfaces:**
- Produces: `func (f *fixture) addMovieLibrary() (itemID, versionID string)` (adds the library through `cmdLibrary add`, waits for `RunCompleted` and a stored probe, then reads the IDs from `GET /api/v1/items?libraryId=`); `func (b *browser) startItem(itemID, versionID, key string) contracts.PlaybackSession`; in `playback` tests `type fixed struct{ src Source; allow func(contracts.Principal) bool }` implementing `Resolver`.
- Consumes: `fastLibrary`/`fakeProber` from `library_test.go`; `fakeProber` already reports 12,500 ms, the same duration the fixture's M1 probe gave, so DATA-1b's facts and watched rules see the same movie length.

- [ ] **Step 1: Refactor the fixtures** (a refactor: every existing test must stay green with `Single` still in place). `browser.start(key)` becomes `startItem` on the catalogue movie; delete `start`.
- [ ] **Step 2: Run** `go -C server test -race ./cmd/lisimflix/ ./internal/playback/ ./internal/auth/`. Expected: PASS, with no test reaching the M1 movie (check: temporarily make `Single.Resolve` return `ErrNotFound`; the same run must still pass; revert).
- [ ] **Step 3: Commit** `test: play the catalogue movie in serve and playback tests`.

### Task 4: Remove the M1 resolver

**Files:**
- Modify: `server/internal/playback/source.go` (delete `M1`, `Single`, `Check`, `source`; keep `Chain`, update its doc), `server/cmd/lisimflix/serve.go` (no `single`; resolver = `library.NewResolver(store, access)` with storage, else `playback.Chain()`; `ready` = storage only; delete the startup "movie is missing" WARN and the `server.single` field; drop DATA-1b's M1 `ItemFacts` closure from its chain), `server/internal/m1config/config.go` (delete `Movie` and `Config.Movie()`), `server/internal/app/app.go` and `server/internal/library/access.go` (comments that mention the M1 fallback)
- Test: `server/cmd/lisimflix/main_test.go`

- [ ] **Step 1: Write the failing tests:** `TestRetiredM1IDIsNotFound`: a signed-in `POST /api/v1/playback/sessions` with the config's `movieId` as item and version answers `404 not_found` with the same body (minus `requestId`) as an unknown ID. `TestStorageDownServesNoItems`: with the database path unwritable, `/health/ready` is 503, a session start is 404, and the server serves. Replace `TestServeStartsWithoutTheMovie` with `TestReadyIgnoresTheConfiguredMovie`: delete the configured file; `/health/ready` is 200.
- [ ] **Step 2: Run** `go -C server test ./cmd/lisimflix -run 'TestRetiredM1|TestStorageDown|TestReadyIgnores'`. Expected: FAIL (201 and 503 respectively).
- [ ] **Step 3: Implement** the removals above.
- [ ] **Step 4: Run** `go -C server test -race ./...` and `grep -rn 'playback.Single\|cfg.Movie()' server`. Expected: PASS, and no grep output.
- [ ] **Step 5: Commit** `refactor(playback): retire the M1 movie resolver`.

### Task 5: Config and `init` without a movie

**Files:**
- Modify: `server/internal/m1config/config.go` (movie fields optional and unvalidated; `validate` loses the title/codec/size/duration/`movieId`/`sourceRevision`/`relativePath` rules and the movie half of `validSource`, which keeps the `mediaRoot` and config-location checks; add `LegacyMovie`) and `config_test.go`
- Modify: `server/cmd/lisimflix/init.go` (delete `--movie`, `--probe-json`, `--title`; output `wrote <path> for viewer "<name>"`), `serve.go` (the WARN of Decision 3, logged once in `compose`)
- Delete: `server/cmd/lisimflix/probe.go`; the tests `TestInitRejectsAProbeForAnotherFile` (`main_test.go`) and `TestProbeOfTheRealM1Source` (`integration_test.go`)

**Interfaces:** Produces `func LegacyMovie(c Config) bool` (true if any movie field is non-zero).

- [ ] **Step 1: Write the failing tests:** `TestLegacyMovieFieldsLoadAndWarn` (`main_test.go`): a config written by today's `init` (keep one as `testdata` text in the test, movie fields filled) composes; the log has exactly one WARN containing `lisimflix library add` and the `relativePath`; nothing else changes. `TestLoadAcceptsAConfigWithoutAMovie` (`config_test.go`). `TestLoadStillRejectsUnknownFields` (a typo'd field, e.g. `"movieID"`, is still refused). `TestInitWritesNoMovie`: `init` without movie flags succeeds and the file has no movie fields; `--movie` is an unknown flag.
- [ ] **Step 2: Run** `go -C server test ./internal/m1config ./cmd/lisimflix -run 'Legacy|WithoutAMovie|UnknownFields|InitWritesNoMovie'`. Expected: FAIL.
- [ ] **Step 3: Implement** the changes above.
- [ ] **Step 4: Run** `make check`. Expected: PASS.
- [ ] **Step 5: Commit** `refactor(config): init and m1.json without the M1 movie`.

### Task 6: Real-file proof and docs

**Files:**
- Modify: `server/cmd/lisimflix/integration_test.go` (`TestM1RealHTTPRangeAndRevocation` becomes `TestRealHTTPRangeAndRevocation`: the ToS file is copied into `media/Movies/Tears of Steel (2012)/`, a library is added and scanned with the real prober, then the same login → session → ranges (byte-compared) → heartbeat → logout → revoked sequence runs by item and version; `TestServePlaysACatalogueFile` drops its M1 half)
- Modify: `deploy/README.md` (§3 provisions the viewer only; a new step adds a library with `lisimflix library add`; readiness text per Decision 5; an "Upgrading from M1" note quoting the WARN), `testdata/media/README.md` (ToS is the integration-test movie, played through a library), `README.md` (status and setup lines), this plan's line in `docs/superpowers/plans/2026-10-24-catalogue-plans-map.md` (mark done)

- [ ] **Step 1: Write** the integration test changes. **Run** `go -C server test -race -count=1 -tags integration ./cmd/lisimflix/` with `LISIMFLIX_M1_MOVIE`/`LISIMFLIX_M1_PROBE` set. Expected: PASS, with the file's sha256 unchanged before and after.
- [ ] **Step 2: Update** the three READMEs. Check by hand: following `deploy/README.md` from an empty config folder gets to a playing movie with no step that names a movie file in `init`.
- [ ] **Step 3: Run** `make check`. Expected: PASS.
- [ ] **Step 4: Commit** `docs: run Lisimflix with libraries only`, then open PR B (Refs #184, #77, #170 for its M1-fallback box; record "M1 retired" on #77).

---

## Self-review

- **Spec coverage:** §12's breaking-change rule (Decision 2), §5.7's error state (Task 1), §6 no-leak 404 for the retired ID (Task 4), §3.6 offline roots not affecting readiness (Decision 5). LIB-2c Decision 11 and UI-2 Decision 10's shortcut are discharged (Tasks 1, 4); DATA-1b Decision 4's retirement duties are Tasks 3–4.
- **Types:** `m1config.Movie` exists only between Task 2 and Task 4. `Single`'s new signature (Task 2) is deleted in Task 4. `startItem`/`addMovieLibrary` (Task 3) are used by Tasks 4–5 tests.
- **Proportion:** six tasks over two PRs; most of the work is deletion and test moves, so the steps name files, tests and grep checks rather than code.
- **Open points for review:** Decisions 1–4 above; and whether Pete wants Task 3's fixture change folded into DATA-1b PR B instead (it touches the same `main_test.go`).

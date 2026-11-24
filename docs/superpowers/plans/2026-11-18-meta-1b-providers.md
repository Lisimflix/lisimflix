# META-1b Providers (TMDB and NFO Fields) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give META-1a's matcher real data: a TMDB provider (D15) with its own rate limits, retries, auth handling and cache, show records with season and episode titles, the #114 fields, and an NFO field provider so a library without a TMDB token still gets local titles and plots.

**Architecture:**
- **`metadata/tmdb`** (new package): a `Client` (Bearer token, limiter, timeout, retries, cache) and a `Provider` implementing `metadata.Provider` over TMDB's v3 API.
- **`metadata`** (extended): the `Provider` interface gains `Episodes` (spec §3.2 names it); `Record` gains the #114 fields and seasons; `applyRecord` writes season and episode children in the same transaction; the matcher fetches a show's local seasons before applying, and applies NFO fields; a response cache lives in the metadata store.
- Nothing is wired into `serve`, and the contract doesn't change: the HTTP API, token entry and serving the results are **META-1c**.

**Tech Stack:** Go standard library (`net/http`, `encoding/json`, `encoding/xml`), `modernc.org/sqlite` through OPS-2's `storage`. No new dependencies.

**Spec:** [`2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md) §3.2 (providers, NFO, `Episodes`), §3.7 (precedence, provenance, timeouts, rate limits, retries/jitter, caching, pause on auth errors), §6.6 (provider data is untrusted), D15 (TMDB terms, token, limits, 6-month cache). Builds on [META-1a](2026-10-28-meta-1a-matching.md) (its "META-1b" section and Decisions 1, 13).

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **META-1** (basic matching, P0 / G1), slice **1b** of 3. 1a (merged, #135, #151): store, precedence, hints, scoring, review queue, edits, fix-match, bounded jobs against a fake. **1b (this plan):** TMDB provider, NFO field provider, seasons and episodes, #114 fields. **1c:** HTTP API, serve wiring, token entry and storage, serving results, artwork, actual scale |
| Issue | #73 |
| Owner / reviewers | Lammy (claimant) / CODEOWNERS |
| Acceptance | **§3.7:** TMDB calls have a timeout, a shared rate limit, bounded retries with jitter, caching and provider-directed backoff (429, `Retry-After`); an auth error pauses the provider (1a's `ErrAuth`); outages keep existing metadata. **D15:** token sent only as `Authorization: Bearer`, never logged or put in a URL; at most 180 days of cached responses, purgeable per provider; TMDB values stored on items are re-fetched before they are 180 days old and can be purged per provider (Decision 9). **§3.2:** NFO titles and plots reach items with `nfo` provenance whatever the provider outcome (match, review, conflict, retry, paused, no token), episodes included; shows get season and episode titles. **#114:** `communityRating`, `directors`, `studios` stored per item |
| File scope | **New:** `server/internal/metadata/tmdb/{client.go,provider.go,client_test.go,provider_test.go,proof_test.go}`, `server/internal/metadata/cache.go` (+ `cache_test.go`), `server/internal/metadata/migrations/20261118140000_metadata_providers.sql`, `testdata/metadata/tmdb/*.json`. **Modified:** `server/internal/metadata/{provider.go,apply.go,store.go,nfo.go,matcher.go}` and their tests (`matcher_test.go`'s `fakeProvider`, `nfo_test.go`, `store_test.go`) |
| Consumes | META-1a: `Store`, `Matcher`, `Provider`, `Record`, `Rating`, `Query`, `Candidate`, `applyRecord`, `validate`, `idPatterns`, NFO parsing, `ErrNotFound`/`ErrUnavailable`/`ErrAuth`. LIB-2a: `library_items` (`kind`, `parent_id`, `idx`). CTR-2: `contracts.MatchUnmatched`, `MatchMatched` |
| Produces | `tmdb.New(Config) *Provider`, `tmdb.Config`, `tmdb.Notice`. `metadata.Provider.Episodes`, `SeasonRecord`, `EpisodeRecord`, `CommunityRating`, `Record.{CommunityRating,Directors,Studios,Seasons}`, `FieldCommunityRating`, `FieldDirectors`, `FieldStudios`, `Store.CacheGet`, `CachePut`, `PurgeCache`, `DropExpiredCache`, `PurgeProvider`, `RequeueStale`, `metadata.Clamp` |
| Start dependency | None open: META-1a and LIB-2a are merged. Tasks 1–3 touch `metadata` only; Tasks 4–5 need Task 1's cache and Task 2's interface |
| Standalone test | `go -C server test -race -count=1 ./internal/metadata/...`, then `make check` |
| Module proof | Synthetic TMDB-shaped responses served by `httptest`, a real database, a catalogue scanned from a temporary media tree (1a's `env_test.go`). No real token or network call in any test |
| Integration proof (not in this slice) | 1c runs matching from `serve` against TMDB with an install's token after LIB-2b scans, and serves the results |

## Global Constraints

- The token is sent only as `Authorization: Bearer <token>`; it never appears in a URL, log line, error string, cache key or test fixture.
- Rate limit about **10 requests/s** with a burst of **5**; request timeout **10 s**; **2** retries with up to **10%** jitter; a `Retry-After` wait is capped at **60 s** (META-1a plan, META-1b section).
- Cached responses older than **180 days** are never served (D15 "within 6 months"); most endpoints have a much shorter TTL (Decision 7).
- Provider data is untrusted (§6.6): every stored value passes 1a's `validate` limits (`maxTitle` 512, `maxOverview` 10000, `maxGenres` 20, `maxGenre` 64, `maxRating` 16 bytes), season and episode values included. Providers clamp first (Decision 10), so one bad value never rejects a whole record.
- Matching never writes catalogue tables (1a's "offline playback unaffected").
- Code with no production caller yet gets `// Planned: META-1c (<what will call it>).`

## Decisions

1. **Language (approved by Lammy, 2026-11-18):** one install-wide metadata language, `tmdb.Config.Language`, default `en-US`. It's provider configuration, not a `Query` field, because `Lookup` and `Episodes` have no query. A per-library language later adds `Query.Language`; language variants are META-3.
2. **Region for content ratings:** the part of `Language` after `-` (`en-US` → `US`); no region, no content rating.
3. **#114 fields:** `communityRating` is TMDB's `vote_average` with source `tmdb`, only when `vote_count >= 10`; `directors` are movie credits with `job == "Director"`; shows get no `directors` in 1b (TMDB's `created_by` lists creators, usually writers or showrunners, not directors; episode directors would need per-episode credit calls, left for later); `studios` are `production_companies` names. Each list is capped at 20 entries of at most 128 bytes. These three fields aren't in `contracts.MetadataFields`: they surface in `ItemDetail` (CTR-2), which 1c serves from `metadata_fields`, so `Store.Get` skips them (Task 1).
4. **NFO beats TMDB per field:** a value in the item's NFO file (written by the user or their tools) wins over the provider's, with `nfo` provenance; locks and user edits win over both (1a). NFO fields are applied **first, on every attempt**, before the pause check, in their own transaction (`applyNFO`, which bumps the revision only when a value changes); `MatchItem` then re-reads the item and goes on. So an item that goes to review or conflict, is retried, or meets a paused provider still has its NFO title and plot. On the matched path the NFO record is applied again after the provider record, in the same transaction, so NFO still wins per field.
5. **No TMDB token:** `Matcher.Provider` may be nil. `MatchItem` then applies NFO fields only (step 0 above) and finishes `unmatched` (no provider match), leaving the queue. 1c re-queues items when a token is added.
6. **Seasons and episodes:** only seasons that exist locally are fetched (§3.7 "only relevant records"). Season items match by `idx` under the show, episodes by `idx` under their season; anything local that TMDB doesn't list is left alone. Children get `metadata_items` rows (status `matched`, not queued) on first apply. The provider calls happen before the apply transaction, so a failed season fetch fails the whole match (1a's retry), never a half-applied show.
7. **Cache:** only `200` responses are cached, keyed by provider and request path plus query (the token is a header, so it's never in the key). Each request is served from the cache only within its endpoint's TTL: **1 day** for `/3/search/*`, `/3/find/*` and `/3/tv/{id}/season/{n}` (new films reach TMDB and airing shows gain episodes), **7 days** for `/3/tv/{id}`, **30 days** for `/3/movie/{id}`. 180 days (`cacheMaxAge`) is the hard upper bound for every entry. `PurgeCache(provider)` is for removing the token (D15); `DropExpiredCache` is for 1c's job loop.
8. **Attribution:** `tmdb.Notice` holds D15's text verbatim, for 1c's About/Credits data.
9. **TMDB data stored on items (D15: refreshed or dropped within 6 months, purged when the token is removed):** `RequeueStale(provider, olderThan)` queues every item that has a field with that provider's provenance fetched before `olderThan`; 1c's job loop calls it with 150 days, so the next match re-fetches the values before they turn 180 days old. `PurgeProvider(provider)` deletes every `metadata_fields` row with that provider's provenance, locked ones included (a value the admin typed has `user` provenance and stays); external IDs stay, as identifiers needed to fetch again. On token removal 1c calls `PurgeProvider("tmdb")` and `PurgeCache("tmdb")`. Both get `// Planned: META-1c (...)`.
10. **Clamping provider values (§6.6):** `metadata.Clamp(Record) Record` cuts every text value on a UTF-8 boundary to its limit (titles 512, overviews 10000, genres 64, directors and studios 128), drops empty values and empty list entries, keeps the first 20 entries of each list, and drops a content rating, year or community rating it can't make valid; it does the same for every season and episode. The TMDB provider and `nfoRecord` both return clamped records. `applyRecord` still validates every field, season and episode fields included, and rejects the record if one fails, so only an unclamped provider can hit that.
11. **Child revisions:** applying a show bumps the revision of each season or episode whose fields change, so an `Edit` made with a revision read before the match gets `ErrRevisionConflict`.

## Review Focus

1. **Any TMDB or NFO value over its limit** (overview, title, genre, director, studio, content rating, season or episode title and overview): it's cut or dropped by `Clamp` instead of the whole record being rejected. Tests in Task 1 (`TestClampEveryField`), Task 3 (`TestNFOOutOfLimitValues`) and Task 5 (`TestLongValuesAreCutToTheLimits`).
2. **A local season or episode TMDB doesn't list** (fan-made specials, wrong numbering): left untouched, the rest applied. Test in Task 2 (`TestUnknownLocalEpisodeLeftAlone`).
3. **The token is revoked mid-batch:** 401 → `ErrAuth`, the provider pauses, nothing is cached. Test in Task 4 (`TestUnauthorizedIsErrAuthAndNotCached`).
4. **A 429 with a huge `Retry-After`** (e.g. 3600): the wait is capped at 60 s and, after the retries, the call returns `ErrUnavailable` so 1a's backoff takes over. Test in Task 4 (`TestRetryAfterIsCapped`).
5. **An admin's locked episode title** survives the show being matched again. Test in Task 2 (`TestLockedEpisodeFieldSurvives`).

---

### Task 1: Store: new fields and the response cache

**Files:**
- Create: `server/internal/metadata/migrations/20261118140000_metadata_providers.sql`, `server/internal/metadata/cache.go`, `server/internal/metadata/cache_test.go`
- Modify: `server/internal/metadata/store.go` (field constants, `validate`, `overlay`), `server/internal/metadata/provider.go` (`Clamp`), `server/internal/metadata/store_test.go`

**Interfaces:**
- Produces: `FieldCommunityRating = "communityRating"`, `FieldDirectors = "directors"`, `FieldStudios = "studios"`; `(*Store).CacheGet(ctx context.Context, provider, key string) (body []byte, ok bool, err error)` (fresh entries only), `CachePut(ctx, provider, key string, body []byte) error`, `PurgeCache(ctx, provider string) (int64, error)`, `DropExpiredCache(ctx context.Context) (int64, error)`; `PurgeProvider(ctx, provider string) (int64, error)`, `RequeueStale(ctx, provider string, olderThan time.Duration) (int64, error)`; `func Clamp(r Record) Record`; `const cacheMaxAge = 180 * 24 * time.Hour`. `CacheGet` takes the caller's TTL: `CacheGet(ctx, provider, key string, maxAge time.Duration)`, capped at `cacheMaxAge`.

- [ ] **Step 1: Failing tests.** `TestValidateNewFields`: `communityRating` `{"source":"tmdb","value":7.4}` passes; value `-1`, `10.5` or an empty source fails; `directors`/`studios` accept up to 20 strings of ≤128 bytes, reject 21 entries or a 129-byte entry. `TestGetSkipsDetailFields`: an item with `communityRating`, `directors` and `studios` rows → `Get` (and `Edit`, which returns `ItemMetadata`) succeeds and returns the other fields. `TestCacheServesFreshOnly`: put at t0; with `maxAge` 1 day, get at t0+23 h → ok, t0+25 h → not ok; with `maxAge` 365 days, get at t0+181 days → not ok (the 180-day cap). `TestPurgeProviderDropsItsFields`: `tmdb` fields (one locked), an `nfo` field and a `user` field → `PurgeProvider("tmdb")` removes both `tmdb` rows only; external IDs stay. `TestRequeueStale`: a `tmdb` field fetched 151 days ago and one fetched 10 days ago on another item → `RequeueStale("tmdb", 150 days)` queues only the first. `TestClampEveryField`: a record (with one season and episode) holding a 600-byte title, a 12000-byte overview, 25 genres with an empty one and a 70-byte one, a 130-byte director, a 20-byte content rating, year 99, community rating 11 and an over-long episode overview → every value within its limit and valid UTF-8, empties dropped, 20 genres, rating/year/community rating nil. `TestPurgeCacheIsPerProvider`: entries for `tmdb` and `other`, `PurgeCache("tmdb")` returns 1, `other` stays. `TestDropExpiredCache` removes only entries older than 180 days. Use the store's injected clock as 1a's tests do.
- [ ] **Step 2: Run** `go -C server test -count=1 -run 'NewFields|Cache' ./internal/metadata/`. Expected: FAIL (undefined).
- [ ] **Step 3: Migration.** Rebuild `metadata_fields` (SQLite can't alter a CHECK): create `metadata_fields_new` with the CHECK list plus `'communityRating', 'directors', 'studios'`, copy rows, drop, rename, keep the primary key and `STRICT`. Add `CREATE TABLE metadata_cache (provider TEXT NOT NULL, key TEXT NOT NULL, body BLOB NOT NULL, fetched_at INTEGER NOT NULL, PRIMARY KEY (provider, key)) STRICT;` and an index on `fetched_at`.
- [ ] **Step 4: Implement** the constants, the three `validate` cases (community rating `0 <= value <= 10`, source 1–32 bytes), `overlay` returning nil for the three detail fields (they're served by 1c's `ItemDetail`, Decision 3), `Clamp` (Decision 10), `PurgeProvider` and `RequeueStale` (Decision 9) and `cache.go`.
- [ ] **Step 5: Run** `go -C server test -race -count=1 ./internal/metadata/`. Expected: PASS (1a's tests unchanged).
- [ ] **Step 6: Commit** `feat(metadata): community rating, directors and studios fields; provider response cache (META-1b)`.

### Task 2: Seasons, episodes and the #114 fields through the matcher

**Files:**
- Modify: `server/internal/metadata/provider.go`, `server/internal/metadata/apply.go`, `server/internal/metadata/matcher.go`
- Test: `server/internal/metadata/matcher_test.go` (`fakeProvider` gains `Episodes`)

**Interfaces:**
- Consumes: Task 1's field constants.
- Produces:
  - `Provider` gains `Episodes(ctx context.Context, showID string, seasons []int) ([]SeasonRecord, error)` (unknown seasons are omitted, not an error).
  - `type SeasonRecord struct { Number int; Title, Overview *string; Episodes []EpisodeRecord }`, `type EpisodeRecord struct { Number int; Title, Overview *string }`.
  - `type CommunityRating struct { Source string; Value float64 }`; `Record` gains `CommunityRating *CommunityRating`, `Directors, Studios []string`, `Seasons []SeasonRecord`.

- [ ] **Step 1: Failing tests** (1a's env: scan a temp tree with a show `Show (2020)/Season 01/S01E01.mkv, S01E02.mkv` and `Season 02/S02E01.mkv`; the fake's show record has seasons 1–3):
  - `TestShowMatchAppliesSeasonsAndEpisodes`: after `MatchItem(show)`, season 1 and 2 items and all three episodes have `title` with provenance `tmdb`; the fake got `Episodes(id, [1 2])` (season 3 not asked for).
  - `TestUnknownLocalEpisodeLeftAlone`: local `S02E05.mkv` with no fake episode 5 has no `title` row; the others are applied.
  - `TestLockedEpisodeFieldSurvives`: lock episode 1's title via `Edit`, match the show again → the locked value and provenance `user` stay.
  - `TestEpisodesFailureFailsTheMatch`: fake `Episodes` returns `ErrUnavailable` → outcome `retry`, the show has no fields from this attempt.
  - `TestMovieRecordStoresNewFields`: a movie record with `CommunityRating{tmdb, 7.4}`, two directors, one studio → three fields stored with provenance `tmdb`.
  - `TestShowMatchBumpsChildRevisions`: read episode 1's revision, match the show so episode 1's title changes → `Edit(episode 1, old revision)` gets `ErrRevisionConflict`; an episode whose fields didn't change keeps its revision.
  - `TestBadEpisodeValueRejectsTheRecord`: an unclamped fake record with a 10001-byte episode overview → `ErrInvalid`, outcome `retry`, nothing stored (proves child fields go through `validate`).
- [ ] **Step 2: Run** `go -C server test -count=1 -run 'Season|Episode|NewFields' ./internal/metadata/`. Expected: FAIL (compile errors, then assertions).
- [ ] **Step 3: Implement.** `recordFields` adds the three fields. `applyRecord` takes the record's seasons: in the same transaction, find season children by `parent_id = show AND kind = 'season' AND idx = n`, episode children by `parent_id = season AND kind = 'episode' AND idx = e`, upsert their `metadata_items` row (`matched`, not queued) and their `title`/`overview` with the same "unless locked" rule, validating each value first, and bump a child's revision when any of its fields change (Decision 11). In `Matcher.lookup`, for `kind == "show"`, read the show's local season numbers and call `Episodes` before applying; its error goes through `m.failed`.
- [ ] **Step 4: Run** `go -C server test -race -count=1 ./internal/metadata/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(metadata): seasons, episodes and #114 fields from provider records (META-1b)`.

### Task 3: NFO field provider and matching without a token

**Files:**
- Modify: `server/internal/metadata/nfo.go`, `server/internal/metadata/matcher.go`, `server/internal/metadata/apply.go`
- Test: `server/internal/metadata/nfo_test.go`, `server/internal/metadata/matcher_test.go`

**Interfaces:**
- Produces: `nfoDoc` gains `title`, `originaltitle`, `sorttitle`, `plot`, `year`, `genre` (repeated), `mpaa`, `studio` (repeated), `director` (repeated); `func nfoRecord(doc nfoDoc, region string) Record` (fields only, no IDs, returned through `Clamp`). Kodi `<episodedetails>` NFOs (`title`, `plot`) are read for episodes. `Matcher` gains `Region string` (for `mpaa`, same rule as Decision 2). `<mpaa>` parsing: a `Region:` prefix (`UK:15`, `US:PG-13`) sets the region (`UK` is stored as `GB`), a leading `Rated ` is dropped (`Rated PG-13` → `PG-13` in the configured region), and a value whose region isn't two letters is dropped.

- [ ] **Step 1: Failing tests.**
  - `TestNFOFieldsParsed`: a Kodi `movie.nfo` with all nine elements gives a `Record` with those values; an empty `<title/>` is not provided.
  - `TestNFOOutOfLimitValues`: one NFO per limit (a 10001-byte `<plot>`, a 600-byte `<title>`, 25 `<genre>`s, an empty `<genre/>`, a 130-byte `<director>` and `<studio>`, a 20-byte `<mpaa>`, `<year>99</year>`) → that value is cut or dropped, the rest of the record applies, outcome `matched`.
  - `TestNFOMpaaRegions`: `UK:15` → `{GB, 15}`, `US:PG-13` → `{US, PG-13}`, `Rated PG-13` → `{US, PG-13}` (region `US`), `Germany:12` → no content rating.
  - `TestEpisodeNFOWithoutProvider`: `Matcher{Provider: nil}` on a show whose `S01E01.nfo` holds `<episodedetails><title>Pilot</title></episodedetails>` → episode 1's title is "Pilot" with `nfo` provenance.
  - `TestNFOAppliedOnEveryOutcome`: with the fake provider, a movie whose NFO has `<title>Local Title</title>` and no IDs goes to review (no confident result) → title "Local Title" (`nfo`); the same with a paused provider (outcome `skipped`) and with a search that fails (outcome `retry`).
  - `TestNFOBeatsProviderPerField`: NFO title "Local Title" and plot, provider title "TMDB Title" and genres → stored title "Local Title" (`nfo`), genres from `tmdb`.
  - `TestMatchWithoutProviderAppliesNFO`: `Matcher{Provider: nil}` on a movie with an NFO → status `unmatched`, the NFO fields stored with `nfo` provenance, the item off the queue; a movie with no NFO → `unmatched`, no fields.
  - `TestLockedFieldBeatsNFO`: a locked title stays when the NFO has another.
- [ ] **Step 2: Run** `go -C server test -count=1 -run 'NFO|WithoutProvider' ./internal/metadata/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Reuse the NFO read 1a does for hints (same file list, `maxNFOBytes`, same `os.Root`), plus for a show each local episode's `<basename>.nfo`; build `nfoRecord` once per match. `MatchItem` starts with `applyNFO` (Decision 4) before the pause check, then re-reads the item; on the matched path `applyRecord` applies the NFO record again after the provider record, in the same transaction, with provenance `nfo`. With `m.Provider == nil` it then finishes `unmatched` (`finishMatch(..., MatchUnmatched, "no provider", now)`) and skips the pause check, hints lookup and search.
- [ ] **Step 4: Run** `go -C server test -race -count=1 ./internal/metadata/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(metadata): NFO field provider and matching without a TMDB token (META-1b)`.

### Task 4: TMDB client: auth, limits, retries, cache

**Files:**
- Create: `server/internal/metadata/tmdb/client.go`, `server/internal/metadata/tmdb/client_test.go`

**Interfaces:**
- Consumes: Task 1's `CacheGet`/`CachePut` through `type Cache interface { CacheGet(context.Context, string, string, time.Duration) ([]byte, bool, error); CachePut(context.Context, string, string, []byte) error }`.
- Produces: `type Config struct { Token, Language, BaseURL string; HTTP *http.Client; Cache Cache; Now func() time.Time; Sleep func(context.Context, time.Duration) error }` (`BaseURL` default `https://api.themoviedb.org`, `Language` default `en-US`); `func (c *client) get(ctx context.Context, path string, q url.Values, out any) error`.

- [ ] **Step 1: Failing tests** (`httptest.Server`, injected `Sleep` that records waits):
  - `TestTokenOnlyInHeader`: the request has `Authorization: Bearer test-token`; the URL, the cache key and any returned error string don't contain `test-token`.
  - `TestUnauthorizedIsErrAuthAndNotCached`: 401 and 403 → `errors.Is(err, metadata.ErrAuth)`; nothing in the cache.
  - `TestNotFoundIsErrNotFound`: 404 → `metadata.ErrNotFound`.
  - `TestServerErrorsRetryThenUnavailable`: three 500s → 3 requests, then `ErrUnavailable`; two 500s then 200 → success.
  - `TestRetryAfterIsCapped`: 429 with `Retry-After: 3600` → a recorded wait of 60 s; 429 without it → waits 1 s then 2 s (±10%).
  - `TestRateLimit`: 20 calls in a row → the recorded waits keep them at ≤10/s after a burst of 5.
  - `TestCacheHitSkipsTheNetwork`: the second identical `get` makes no request; a different `language` is a different key.
  - `TestCacheTTLPerEndpoint`: a cached `/3/tv/1/season/1` is fetched again after 25 h; a cached `/3/movie/1` is still served after 25 h and fetched again after 31 days.
  - `TestTimeoutIsUnavailable`: a handler that sleeps past a 50 ms test timeout → `ErrUnavailable`.
- [ ] **Step 2: Run** `go -C server test -count=1 ./internal/metadata/tmdb/`. Expected: FAIL.
- [ ] **Step 3: Implement** with the Global Constraints' numbers: a mutex-guarded token bucket (no new dependency), `http.Client` timeout 10 s, `language` added to every query, cache key `path + "?" + q.Encode()`, each request's TTL from Decision 7.
- [ ] **Step 4: Run** `go -C server test -race -count=1 ./internal/metadata/tmdb/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(metadata): TMDB client with token header, rate limit, retries and cache (META-1b)`.

### Task 5: TMDB provider

**Files:**
- Create: `server/internal/metadata/tmdb/provider.go`, `server/internal/metadata/tmdb/provider_test.go`, `testdata/metadata/tmdb/{search-movie,search-tv,movie,tv,tv-season-1,find-imdb}.json`

**Interfaces:**
- Consumes: Task 4's `client`; Task 2's `Record`, `SeasonRecord`, `EpisodeRecord`, `CommunityRating`.
- Produces: `func New(cfg Config) *Provider` implementing `metadata.Provider` (`Name() == "tmdb"`, `Namespaces() == []string{"tmdb", "imdb", "tvdb"}`); `const Notice = "This product uses TMDB and the TMDB APIs but is not endorsed, certified, or otherwise approved by TMDB"`.

- [ ] **Step 1: Fixtures.** Write the six synthetic responses with the field names and nesting of TMDB's API reference (`developer.themoviedb.org`), checked against each endpoint's reference page while writing them; titles and IDs are invented. Endpoints: `GET /3/search/movie` and `/3/search/tv` (`query`, `year` / `first_air_date_year`, `page=1`); `GET /3/movie/{id}?append_to_response=external_ids,release_dates,credits`; `GET /3/tv/{id}?append_to_response=external_ids,content_ratings,credits`; `GET /3/tv/{id}/season/{n}`; `GET /3/find/{id}?external_source=imdb_id|tvdb_id`.
- [ ] **Step 2: Failing tests.**
  - `TestSearchMovieCandidates`: candidates in order with `Provider "tmdb"`, `Kind "movie"`, `Year` from `release_date`, `AltTitles` from `original_title`; `Search` for a show uses `/search/tv` and `first_air_date`.
  - `TestLookupMovieRecord`: title, original title, overview, year, genres, `ContentRating{US, <certification>}` from `release_dates`, `ExternalIDs{"imdb": "tt…"}`, `CommunityRating{tmdb, vote_average}` (and nil when `vote_count < 10`), directors, studios.
  - `TestLookupByIMDbUsesFind`: `Lookup(ctx, "movie", "imdb", "tt…")` calls `/find`, then the movie; a `tv_results` hit for a movie lookup → `ErrNotFound`.
  - `TestEpisodesFetchesOnlyAskedSeasons`: `Episodes(ctx, id, []int{1})` requests `/tv/{id}/season/1` only and returns its episodes by `episode_number`; a 404 season is omitted.
  - `TestLongValuesAreCutToTheLimits`: an overview of 12000 bytes comes back at ≤10000 bytes, valid UTF-8; a 600-byte title at ≤512; a 70-byte genre, a 130-byte company name and a 20-byte certification are cut or dropped; an episode with an empty name has no title and a 12000-byte episode overview is cut.
  - `TestShowHasNoDirectors`: a `tv` fixture with `created_by` → `Directors` is empty (Decision 3).
  - That every fixture record passes 1a's validation is proved through a real store in Task 6 (`applyRecord` is internal to `metadata`).
- [ ] **Step 3: Run** `go -C server test -count=1 ./internal/metadata/tmdb/`. Expected: FAIL.
- [ ] **Step 4: Implement** the mapping per Decisions 2 and 3, returning `metadata.Clamp(rec)`; `Notice` carries `// Planned: META-1c (About/Credits attribution data).`, `New` carries `// Planned: META-1c (serve builds the provider from the stored token).`
- [ ] **Step 5: Run** `go -C server test -race -count=1 ./internal/metadata/...`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(metadata): TMDB provider for movies and shows (META-1b)`.

### Task 6: Module proof and PR

- [ ] **Step 1:** `TestTMDBMatchesAScannedLibrary` in `tmdb/proof_test.go`: a temp media tree with one movie (with an NFO giving only an IMDb ID) and one show (two local seasons), scanned with its own small setup over the `library` package's exported store and scan API (1a's `env_test.go` helpers are internal to package `metadata`), then queued with `metadata.Store.Enqueue`; `Matcher{Provider: New(cfg pointing at the fixture server)}.RunDue` → the movie is `matched` with fields from `tmdb`, the show and its episodes have titles, and the fixture server saw no request for an unlisted season.
- [ ] **Step 2: Run** `make check`. Expected: PASS.
- [ ] **Step 3:** README check (`grep -n -i -E 'metadata|tmdb' README.md`); it doesn't describe metadata yet, so no change unless that's changed by then.
- [ ] **Step 4:** Open the PR: `feat(metadata): TMDB and NFO field providers (META-1b)`, `Refs #73`, labels `area:metadata`; body lists Decisions 1–8 for review and the local `make check` result. Record "1b module accepted" on #73 after merge.

# CTR-2 Catalogue and Identity Contracts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Agree the catalogue, user-data, profile and metadata-provenance wire shapes, and one source-revision rule, before LIB-2, META-1, DATA-1, ACC-2 and UI-2 build on them.

**Architecture:** This extends CTR-1's pattern. OpenAPI stays the source of truth, with matching Go types in `server/internal/contracts`, generated TypeScript types and shared synthetic fixtures. Each fixture is checked four ways:
- Go decodes it strictly and encodes it back unchanged.
- Ajv validates it against OpenAPI and rejects the invalid variants.
- TypeScript compile checks run against the generated types.
- Go constants are compared with the OpenAPI enums.

The planned operations go into the contract now, each tagged with its owning feature. This plan registers no handlers and adds no storage. The playback source-revision hash moves into `contracts` so LIB-2, DATA-1 and playback all compute it the same way.

**Tech Stack:** Go 1.27.3 standard library; OpenAPI 3.1; Node 24.22.1 with the existing pinned `openapi-typescript` 7.13.0 and `@redocly/ajv` 8.11.2. No new dependencies.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md): §3.2 (title language), §3.6–3.8 (revisions, identity, progress), §6.3 (policy), §8.2–8.3 (contracts, shared hotspots), §10 (domain model), §12 (API rules), §9.4 CTR-2 row.

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **CTR-2**: catalogue and identity contracts. P0 / G1 |
| Issue | #76 |
| Owner / reviewers | Lammy (claimant) / Pete (LIB-1 owner, first producer to consume the revision rule) and Ondo. DATA-1, ACC-2 and UI-2 are unclaimed; their claimants review the shapes in their own plans, and changes go through a CTR-2 follow-up PR (spec §8.3) |
| Acceptance | §3.8, §6.3, §10, §12. Producer/consumer tests: Go round-trip, OpenAPI–Go field and enum agreement, Ajv validation with invalid variants, TypeScript compile checks |
| File scope | **New:** `server/internal/contracts/{revision,userdata,catalogue,profiles,metadata}.go` and their tests, `fixtures_test.go`, `testdata/contracts/catalogue/**`. **Renamed:** `docs/contracts/m1-openapi.json` → `docs/contracts/openapi.json`. **Modified:** the contract, `contracts/{errors,types,types_test}.go`, `playback/source.go` (one function body), `web/package.json` (one path), `web/contract-tests/*`, generated `web/src/api/schema.d.ts`, `testdata/contracts/m1/README.md`, `README.md` (two paths). No handlers, storage, `go.mod` or lockfile changes |
| Consumes | CTR-1's contract and test pattern. LIB-1's revision rule (`library.Revision{Size, ModTime}`, plan #46) |
| Produces | The contract operations and schemas below. Go types: `Page[T]`, `Library`, `ItemSummary`, `ItemDetail`, `MediaVersion`, stream types, `UserItemState`, `ProgressEvent`, `ProgressResult`, `WatchedRequest`, `Profile`, `ProfilePolicy`, `ItemMetadata`, `Field[T]`, `Provenance` and others. Enum constants, `CodeRevisionConflict` and `SourceRevision(fileID string, size, modTimeUnixNano int64) string`. Fixtures in `testdata/contracts/catalogue/` |
| Start dependency | None. Can start now |
| Standalone test | `go -C server test -count=1 ./internal/contracts ./internal/playback`, then `npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck`, then `make check` |
| Module proof | The four fixture checks above, plus the pinned `SourceRevision` value |
| Integration proof (not in this feature) | Each producer registers its operation, flips `x-lisimflix.status` to `implemented` and tests its real response against the schema. UI-2 consumes the same fixtures. Record "module accepted" on #76 when this merges, and "integrated" when LIB-2 serves `listItems`/`getItem` and UI-2 renders them |

## Global Constraints

- Go 1.27.3 and Node 24.22.1. Module `github.com/Lisimflix/lisimflix/server`. `CGO_ENABLED=0`.
- OpenAPI is authoritative for HTTP shapes (spec §8.2, §12). Go and TypeScript must agree with it. `web/src/api/schema.d.ts` is generated, never edited by hand.
- JSON is camelCase. IDs are opaque strings. Instants are RFC 3339 UTC. Durations and positions are integer milliseconds with `Ms` in the name. Lists are cursor-paginated: default 50, max 200 (spec §12, plans map delta 5).
- Unknown values are `null`, never an invented zero or false (spec §10.3). Every property is required; nullable ones accept `null`.
- No filesystem paths, provider keys, secrets or ordering positions as identity in any public shape (spec §10.1).
- Contracts only. No handlers are registered and no storage is added. M1 operations and shapes stay exactly as they are.
- Fixtures are synthetic: no real media, paths, provider keys or personal data.
- No new Go or npm dependencies.
- Workflow:
  - Branch `feat/ctr-2-contracts`. Run `make check` locally before pushing.
  - PR title `feat(api): catalogue and identity contracts (CTR-2)`. Reviewers lisim and ondona.
  - The author squash-merges after approval. Agents never merge.

## Decisions taken in this plan (please review)

1. **One contract document.** `docs/contracts/m1-openapi.json` becomes `docs/contracts/openapi.json` (title "Lisimflix API", version 0.2.0). Spec §12 says one versioned document generates the web client. The M1 operations don't change.
2. **Planned operations are in the contract now.** Each operation carries `x-lisimflix: { owner, status }`, where status is `implemented` or `planned`. Spec §12 puts exact paths in the first API contract PR, not in each client's guesses, so UI-2 and the producers build against the same paths. A producer flips its operation to `implemented` in the PR that registers the handler. A test requires an owner and status on every operation.
3. **One source-revision rule.** `contracts.SourceRevision(fileID, size, mtimeNs)` is today's `playback.Revision` hash, generalised:
   - It's scoped to a file, so episodes sharing a file share its revision.
   - LIB-1's `library.Revision{Size, ModTime}` is the comparable internal form; the same values give the same string.
   - `playback.Revision` now calls it, and M1 revisions don't change (the test pins a value).
4. **Resource revisions are counters.** `UserItemState.revision` starts at 0 (nothing recorded yet), and `ItemMetadata.revision` starts at 1.
   - Writes send `expectedRevision` in the body. On a mismatch, the server returns 409 with the new error code `revision_conflict` and changes nothing.
   - A body field, rather than `If-Match`, keeps the request a JSON shape that the fixtures and Ajv can check.
5. **Separate identities (spec §3.7, §10.1).** Items (movie, show, season, episode), versions (a cut or quality) and files each have their own ID.
   - Two episodes in one file have versions with the same `fileId`. `segment` is null when the boundaries are unknown.
   - Provider IDs are namespaced (`tmdb`, `anidb` …) and never act as identity.
   - Provider series and season-order mapping edges are META-2's and aren't on the wire yet.
6. **Progress is its own operation** on the playback session (`reportProgress`, owned by DATA-1).
   - The session fixes the item, version and source revision. The client sends `sequence`, `kind` and `positionMs`, plus `fromPositionMs` for a seek.
   - A duplicate or stale event gets 200 with `accepted:false`, because a late heartbeat is normal, not an error.
   - Whether progress also renews the playback session is PLY-4's decision. Until then, the player sends both.
7. **Watched is separate from resume.**
   - `watched` is null when the item has never been marked either way.
   - `origin` is `auto`, `manual` or `import`. A manual value is never undone by an automatic event.
   - `playCount` is nullable for imports that have no count.
8. **Policy on the wire is for display.** `ProfilePolicy` is the effective policy, after the account ceiling (spec §6.3); the server enforces it on every request.
   - Ratings are scoped to a region, and a rating from another region counts as unrated.
   - The wire carries no rating order: per-region rating tables and the evaluator belong to ACC-2.
   - A forbidden library or item is a 404, as if it didn't exist.
9. **Metadata provenance is recorded per field.**
   - Each field has a `value`, a `source` (provider, provider ID, fetch time) and a `locked` flag.
   - `source` is null when no origin is recorded. Manual edits use the provider `user`, and NFO files use `nfo`.
   - Reading it is admin-only (non-admins get 404). The edit operation is META-1's to plan; it takes `expectedRevision`.
10. **Streams on the wire are a display subset** of LIB-1's `probe.Result`: index, codec, audio profile, language, flags and HDR. The audio `profile` is what tells TrueHD Atmos or DTS-HD MA apart (Pete, #81).
    - PLY-1 decides playback from `probe.Result` itself, not from this shape.
    - `probe` keeps no JSON tags (LIB-1's decision), so LIB-2 maps the values.
    - Untagged and `und` languages are `null`.
11. **`title` is already localised.** The server picks it in the current profile's title language (English, Romaji or Native; spec §3.2).
12. **Out of scope, with owners:**
    - Generalising `PlaybackSessionRequest` to item + version (PLY-4).
    - Retiring M1's `/movies` and `Movie` once UI-2 uses items (UI-2/PLY-2).
    - Continue Watching, Next Up, watchlist and preferences (DATA-1/DATA-2, UI-2).
    - Library roots and scan-status admin shapes (LIB-2/UI-6).
    - Metadata edits, fix-match and the artwork route (META-1).
    - Mapping edges and episode orders (META-2).
    - Child counts on shows and seasons (UI-2).
    - MIGRATE-01 compatibility fixtures (DATA-1).

    All of these are additive changes later.

## Contract summary

### Operations added (all `planned`)

| Operation | Owner | Request | 200 response | Errors |
|---|---|---|---|---|
| `GET /api/v1/libraries` (`listLibraries`) | LIB-2 | `cursor`, `limit` | `LibraryList` | 400, 401, 500 |
| `GET /api/v1/items` (`listItems`) | LIB-2 | exactly one of `libraryId` / `parentId`; `cursor`, `limit` | `ItemPage`, stable sort-title order (episodes in the chosen order) | 400, 401, 404, 500 |
| `GET /api/v1/items/{itemId}` (`getItem`) | LIB-2 (META-1 and DATA-1 fill parts) | – | `ItemDetail` | 401, 404, 500 |
| `GET /api/v1/items/{itemId}/metadata` (`getItemMetadata`) | META-1 | admin only | `ItemMetadata` | 401, 404, 500 |
| `PUT /api/v1/items/{itemId}/watched` (`setWatched`) | DATA-1 | `WatchedRequest` + CSRF | `UserItemState` | 400, 401, 403, 404, 409 `revision_conflict`, 500 |
| `POST /api/v1/playback/sessions/{sessionId}/progress` (`reportProgress`) | DATA-1 | `ProgressEvent` + CSRF | `ProgressResult` | 400, 401, 403, 404, 409 `source_changed`, 410, 500 |
| `GET /api/v1/profiles` (`listProfiles`) | ACC-1 | `cursor`, `limit` | `ProfileList` | 400, 401, 500 |

The nine M1 operations get `x-lisimflix` with status `implemented`: ACC-1 for auth, PLY-2 for movies and playback.

### Schemas added

| Schema | Fields (all required; `?` = nullable) |
|---|---|
| `Library` | `id`, `name`, `type` (`movies` \| `tv` \| `anime`) |
| `LibraryList`, `ItemPage`, `ProfileList` | `items[]`, `nextCursor?` |
| `ContentRating` | `region` (ISO 3166-1 alpha-2), `value` |
| `ItemSummary` | `id`, `kind` (`movie` \| `show` \| `season` \| `episode`), `libraryId`, `parentId?`, `title`, `sortTitle`, `year?`, `index?`, `durationMs?`, `contentRating?`, `primaryImageUrl?`, `backdropUrl?`, `userState?`. Movies and shows have no parent; seasons and episodes must. Shows and seasons have null `durationMs` and `userState` |
| `ItemDetail` | `item` (`ItemSummary`), `originalTitle?`, `overview?`, `genres[]`, `externalIds[]`, `versions[]` (empty for shows and seasons) |
| `ExternalId` | `namespace` (`^[a-z][a-z0-9]{1,31}$`), `id` |
| `MediaVersion` | `id`, `fileId`, `label?`, `durationMs?`, `sourceRevision` (64 hex), `segment?`, `container`, `video[]`, `audio[]`, `subtitles[]` |
| `Segment` | `startMs`, `endMs` |
| `VideoStream` | `index`, `codec`, `width`, `height`, `hdr` (`sdr` \| `hdr10` \| `hdr10plus` \| `hlg` \| `dolbyVision`), `default` |
| `AudioStream` | `index`, `codec`, `profile?` (e.g. `Dolby TrueHD + Dolby Atmos`, `DTS-HD MA`), `channels`, `language?` (ISO 639-2, not `und`), `title?`, `default`, `forced` |
| `SubtitleStream` | `index`, `codec`, `kind` (`text` \| `image` \| `ass`), `language?`, `title?`, `default`, `forced`, `hearingImpaired` |
| `UserItemState` | `itemId`, `revision` (≥ 0), `resume?`, `watched?`, `playCount?`, `lastPlayedAt?`, `hiddenFromContinueWatching` |
| `Resume` | `positionMs`, `durationMs`, `versionId`, `sourceRevision`, `updatedAt` |
| `Watched` | `value`, `origin` (`auto` \| `manual` \| `import`), `at` |
| `ProgressEvent` | `sequence`, `kind` (`tick` \| `pause` \| `seek` \| `stop`), `positionMs`, `fromPositionMs?` (set only for seeks) |
| `ProgressResult` | `accepted`, `reason?` (`duplicate` \| `stale`, null when accepted), `state` |
| `WatchedRequest` | `watched`, `expectedRevision` |
| `Profile` | `id`, `name`, `pinRequired`, `policy` |
| `ProfilePolicy` | `libraryIds[]`, `ratingCeiling?`, `allowUnrated` |
| `ItemMetadata` | `itemId`, `revision` (≥ 1), `matchStatus` (`unmatched` \| `matched` \| `review` \| `conflict`), `fields`, `externalIds[]` |
| `MetadataFields` | `title`, `sortTitle`, `originalTitle`, `overview` (`TextField`), `year` (`IntegerField`), `contentRating` (`RatingField`), `genres` (`TextListField`) |
| `TextField` / `IntegerField` / `RatingField` / `TextListField` | `value`, `source?` (`Provenance`), `locked` |
| `Provenance` | `provider`, `providerId?`, `fetchedAt` |
| `MetadataExternalId` | `namespace`, `id`, `source?`, `locked` |

`APIError.code` gains `revision_conflict` (409).

### Revision rules

- **R1. Source revision.** `sourceRevision` is the lowercase hex SHA-256 of `fileId 0x00 size 0x00 mtimeUnixNano`, with the numbers in decimal (`contracts.SourceRevision`). It changes exactly when the file's size or modification time changes. M1's movie uses its movie ID as the file ID, so M1 values don't change. `fileId` is LIB-2's stable opaque file ID, never a path. When LIB-2 takes over M1's movie file and gives it its own `fileId`, the revision changes once. An M1 playback session still open at that moment gets `source_changed`, which is intended: the player starts a new session.
- **R2. One rule everywhere.** LIB-1/LIB-2 store `library.Revision{Size, ModTime}` and derive the wire string with R1. Playback compares R1 strings. Progress keeps the R1 string it was recorded against. A resume position from another version or source revision needs confirmation before it's reused (spec §3.8).
- **R3. Resource revisions.** `UserItemState.revision` and `ItemMetadata.revision` go up by 1 on every committed change. A write with a different `expectedRevision` gets 409 `revision_conflict`, and nothing changes.
- **R4. Progress order.** Within a playback session, `sequence` must increase. A repeated sequence is answered `accepted:false, reason:"duplicate"`, and a lower one `reason:"stale"`. If the session's source revision no longer matches the file, the answer is 409 `source_changed`. An automatic update never overwrites a manual `watched` value.

## Review focus

1. Do the item/version/file split and `segment` cover multi-episode files and cuts without guessing equal-length episodes (spec §3.8)?
2. Is the source revision rule right for LIB-1/LIB-2? Pete: does `SourceRevision(fileID, Size, ModTime)` fit `library.Revision` and the compare-and-set on `Outcome.Rev`?
3. No path, key, secret or rating order leaks into a public shape. Policy denials stay 404.
4. Are the null rules and units clear enough for a fixture-built UI-2?
5. Is `x-lisimflix` a sensible way to mark planned operations, or would you rather the paths wait for each producer?

## File Structure

```
docs/contracts/openapi.json              renamed from m1-openapi.json; CTR-2 schemas, operations, x-lisimflix tags, revision_conflict
server/internal/contracts/
  revision.go, revision_test.go          SourceRevision (R1) with a pinned value
  errors.go                              CodeRevisionConflict → 409
  types.go, types_test.go                contract path; every schema must have a Go type
  fixtures_test.go                       CTR-2 test helpers: strict round trip, enum check
  userdata.go, userdata_test.go          UserItemState, Resume, Watched, ProgressEvent/Result, WatchedRequest
  catalogue.go, catalogue_test.go        Page[T], Library, items, versions, streams, ratings, external IDs
  profiles.go, profiles_test.go          Profile, ProfilePolicy
  metadata.go, metadata_test.go          ItemMetadata, Field[T], Provenance; every fixture round-tripped
server/internal/playback/source.go       Revision calls contracts.SourceRevision
testdata/contracts/catalogue/            11 synthetic fixtures + README
web/contract-tests/fixtures.test.mjs     ownership check; Ajv checks with invalid variants
web/contract-tests/schema.test.ts        TypeScript compile checks
web/package.json, README.md, testdata/contracts/m1/README.md   contract path
```

---

### Task 1: Shared source revision rule

**Files:**
- Create: `server/internal/contracts/revision.go`
- Create: `server/internal/contracts/revision_test.go`
- Modify: `server/internal/playback/source.go`

**Interfaces:**
- Consumes: nothing new. `playback.Revision(movieID string, info fs.FileInfo) string` (M1) keeps its signature.
- Produces: `contracts.SourceRevision(fileID string, size, modTimeUnixNano int64) string` (R1). LIB-2 derives `sourceRevision` from `library.Revision{Size, ModTime}` with it, and later tasks' fixtures use real values from it.

- [ ] **Step 1: Write the failing tests and fixtures**

**Create `server/internal/contracts/revision_test.go`:**

```go
package contracts

import "testing"

func TestSourceRevisionIsStable(t *testing.T) {
	// Pinned value: a change here breaks every stored and in-flight revision.
	const want = "26f89dcefd3415cb12f87f793cb958bd26470771b742ed88645ec98afc9d4f73"
	base := SourceRevision("mov-synthetic-0001", 734167, 1761523200000000000)
	if base != want {
		t.Fatalf("SourceRevision = %s, want %s", base, want)
	}
	for name, other := range map[string]string{
		"file ID": SourceRevision("mov-synthetic-0002", 734167, 1761523200000000000),
		"size":    SourceRevision("mov-synthetic-0001", 734168, 1761523200000000000),
		"mtime":   SourceRevision("mov-synthetic-0001", 734167, 1761523200000000001),
	} {
		if other == base {
			t.Errorf("changing the %s kept the revision", name)
		}
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
```

Expected: FAIL to build: `undefined: SourceRevision` in `revision_test.go`. `playback` still passes.

- [ ] **Step 3: Implement**

**Create `server/internal/contracts/revision.go`:**

```go
package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// SourceRevision is the public sourceRevision of one source media file:
// lowercase hex SHA-256 of fileID 0x00 size 0x00 mtimeUnixNano, with decimal
// numbers. It changes exactly when the file's size or modification time
// changes, which is LIB-1's revision rule (library.Revision holds the same two
// values). Playback, the catalogue and progress all compare this string, so
// it must stay byte-for-byte stable. M1's movie uses its movie ID as fileID.
func SourceRevision(fileID string, size, modTimeUnixNano int64) string {
	sum := sha256.Sum256([]byte(fileID + "\x00" + strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(modTimeUnixNano, 10)))
	return hex.EncodeToString(sum[:])
}
```

**Modify `server/internal/playback/source.go`:**

```diff
diff --git a/server/internal/playback/source.go b/server/internal/playback/source.go
index 42f75c5..dda13aa 100644
--- a/server/internal/playback/source.go
+++ b/server/internal/playback/source.go
@@ -4,13 +4,10 @@
 package playback
 
 import (
-	"crypto/sha256"
-	"encoding/hex"
 	"errors"
 	"io/fs"
 	"os"
 	"path/filepath"
-	"strconv"
 
 	"github.com/Lisimflix/lisimflix/server/internal/contracts"
 )
@@ -27,12 +24,11 @@ var (
 	errSourceUnavailable = errors.New("playback: the source file is not available")
 )
 
-// Revision fingerprints a source file: lowercase hex SHA-256 of
-// movieID 0x00 size 0x00 mtimeUnixNano, with decimal numbers. init, startup
-// and playback all use it, so it must stay byte-for-byte stable.
+// Revision is the source revision of the configured movie's file
+// (contracts.SourceRevision with the movie ID as the file ID). init, startup
+// and playback all use it.
 func Revision(movieID string, info fs.FileInfo) string {
-	sum := sha256.Sum256([]byte(movieID + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)))
-	return hex.EncodeToString(sum[:])
+	return contracts.SourceRevision(movieID, info.Size(), info.ModTime().UnixNano())
 }
 
 // statSource returns the source's metadata through the root, which refuses
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
```

Expected: `ok` for both packages. The playback tests pass unchanged, so M1 revisions are byte-identical.

- [ ] **Step 5: Commit**

```bash
git add server/internal/contracts/revision.go server/internal/contracts/revision_test.go server/internal/playback/source.go
git commit -m "feat(api): shared source revision rule (CTR-2)"
```

---

### Task 2: One Lisimflix API contract: owners, status and revision_conflict

**Files:**
- Rename and modify: `docs/contracts/m1-openapi.json` → `docs/contracts/openapi.json`
- Modify: `README.md`
- Modify: `server/internal/contracts/errors.go`
- Modify: `server/internal/contracts/types.go`
- Modify: `server/internal/contracts/types_test.go`
- Modify: `testdata/contracts/m1/README.md`
- Modify: `web/contract-tests/fixtures.test.mjs`
- Modify: `web/package.json`
- Regenerate: `web/src/api/schema.d.ts` (`npm --prefix web run generate:api`; never edit by hand)

**Interfaces:**
- Consumes: CTR-1's `docs/contracts/m1-openapi.json`, `codeStatus` and `TestOpenAPIMatchesGoTypes`.
- Produces: `docs/contracts/openapi.json` (the only contract path from now on). `x-lisimflix: { owner, status }` on every operation. `CodeRevisionConflict = "revision_conflict"` (409). `TestOpenAPIMatchesGoTypes` now fails for any schema without a Go type, so Tasks 3–6 add their types to its map.

- [ ] **Step 1: Write the failing tests and fixtures**

**Modify `server/internal/contracts/types_test.go`:**

```diff
diff --git a/server/internal/contracts/types_test.go b/server/internal/contracts/types_test.go
index 8c06c11..2193c8c 100644
--- a/server/internal/contracts/types_test.go
+++ b/server/internal/contracts/types_test.go
@@ -153,7 +153,7 @@ func TestWriteErrorRejectsUnknownCodeAndLongMessage(t *testing.T) {
 // the generated TypeScript types) on the same field names and required set. It
 // does not compare field types or nullability.
 func TestOpenAPIMatchesGoTypes(t *testing.T) {
-	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "contracts", "m1-openapi.json"))
+	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "contracts", "openapi.json"))
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -174,6 +174,11 @@ func TestOpenAPIMatchesGoTypes(t *testing.T) {
 		"PlaybackSession": PlaybackSession{}, "APIError": APIError{},
 		"LoginRequest": LoginRequest{}, "PlaybackSessionRequest": PlaybackSessionRequest{}, "HeartbeatRequest": HeartbeatRequest{},
 	}
+	for name := range doc.Components.Schemas {
+		if _, ok := types[name]; !ok {
+			t.Errorf("schema %s has no Go type in this test", name)
+		}
+	}
 	for name, v := range types {
 		s, ok := doc.Components.Schemas[name]
 		if !ok {
@@ -229,7 +234,7 @@ func TestPrincipalWithEmptyIDIsUnauthenticated(t *testing.T) {
 }
 
 func TestErrorCodesMatchOpenAPI(t *testing.T) {
-	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "contracts", "m1-openapi.json"))
+	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "contracts", "openapi.json"))
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -267,3 +272,15 @@ func TestWriteErrorSendsCodeStatus(t *testing.T) {
 		}
 	}
 }
+
+// CTR-2: a write whose expectedRevision is stale is a 409 the client resolves
+// by reloading.
+func TestRevisionConflictIsAConflict(t *testing.T) {
+	w := httptest.NewRecorder()
+	WriteError(w, httptest.NewRequest(http.MethodPut, "/", nil), CodeRevisionConflict, "Reload and try again.")
+	var e APIError
+	decodeStrict(t, w.Body.Bytes(), &e)
+	if w.Code != http.StatusConflict || e.Code != "revision_conflict" {
+		t.Fatalf("got %d %q", w.Code, e.Code)
+	}
+}
```

**Modify `testdata/contracts/m1/README.md`:**

```diff
diff --git a/testdata/contracts/m1/README.md b/testdata/contracts/m1/README.md
index f0cc697..25e9ae7 100644
--- a/testdata/contracts/m1/README.md
+++ b/testdata/contracts/m1/README.md
@@ -1,7 +1,7 @@
 # M1 contract fixtures
 
 Synthetic examples of the public M1 API shapes in
-[`docs/contracts/m1-openapi.json`](../../../docs/contracts/m1-openapi.json).
+[`docs/contracts/openapi.json`](../../../docs/contracts/openapi.json).
 Go (`server/internal/contracts`) round-trips them; `npm --prefix web run test:contracts`
 validates these same files against OpenAPI and rejects invalid examples. `make check`
 runs both checks plus the generated TypeScript assertions. All IDs, tokens and
```

**Modify `web/contract-tests/fixtures.test.mjs`:**

```diff
diff --git a/web/contract-tests/fixtures.test.mjs b/web/contract-tests/fixtures.test.mjs
index 4b09708..04493fb 100644
--- a/web/contract-tests/fixtures.test.mjs
+++ b/web/contract-tests/fixtures.test.mjs
@@ -4,7 +4,7 @@ import test from "node:test";
 import Ajv2020 from "@redocly/ajv/dist/2020.js";
 
 const readJSON = (path) => JSON.parse(readFileSync(new URL(path, import.meta.url), "utf8"));
-const contract = readJSON("../../docs/contracts/m1-openapi.json");
+const contract = readJSON("../../docs/contracts/openapi.json");
 const ajv = new Ajv2020({ allErrors: true });
 // OpenAPI stores schemas under components; Go decoding checks the int64 range.
 ajv.addKeyword("components");
@@ -61,3 +61,13 @@ for (const [name, file, mutate] of invalidFixtures) {
     assert.equal(validate(data), false, `${name} was accepted`);
   });
 }
+
+test("every operation names its owning feature and whether it is implemented", () => {
+  for (const [path, item] of Object.entries(contract.paths)) {
+    for (const [method, op] of Object.entries(item)) {
+      const x = op["x-lisimflix"] ?? {};
+      assert.match(x.owner ?? "", /^[A-Z]+-\d+$/, `${method} ${path} owner`);
+      assert.ok(["implemented", "planned"].includes(x.status), `${method} ${path} status`);
+    }
+  }
+});
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
npm --prefix web run test:contracts
```

Expected: Go FAILs to build: `undefined: CodeRevisionConflict`. `test:contracts` fails before running any test, because `docs/contracts/openapi.json` doesn't exist yet (ENOENT).

- [ ] **Step 3: Implement**

**Rename `docs/contracts/m1-openapi.json` → `docs/contracts/openapi.json` and change it** (`git apply` of this diff renames the file and applies the changes):

```diff
diff --git a/docs/contracts/m1-openapi.json b/docs/contracts/openapi.json
similarity index 91%
rename from docs/contracts/m1-openapi.json
rename to docs/contracts/openapi.json
index 80f325e..f4b2d22 100644
--- a/docs/contracts/m1-openapi.json
+++ b/docs/contracts/openapi.json
@@ -1,9 +1,9 @@
 {
   "openapi": "3.1.0",
   "info": {
-    "title": "Lisimflix M1 API",
-    "version": "0.1.0",
-    "description": "M1 contract (CTR-1): one authorised viewer plays one original MP4. All /api/v1 responses are private and sent with Cache-Control: no-store. Unsafe browser operations require X-CSRF-Token. Login/logout/session behaviour belongs to the runtime (OPS-1/ACC-1); playback sessions and content belong to PLY-2. Source paths, password hashes, cookies and grants never appear in response bodies other than the grant inside contentUrl."
+    "title": "Lisimflix API",
+    "version": "0.2.0",
+    "description": "Public HTTP contract. M1 (CTR-1): one authorised viewer plays one original MP4. CTR-2 adds the catalogue, user-data, profile and metadata-provenance shapes. Each operation's x-lisimflix names its owning feature and whether it is implemented or still planned. All /api/v1 responses are private and sent with Cache-Control: no-store. Unsafe browser operations require X-CSRF-Token. Login/logout/session behaviour belongs to the runtime (OPS-1/ACC-1); playback sessions and content belong to PLY-2. Source paths, password hashes, cookies and grants never appear in response bodies other than the grant inside contentUrl."
   },
   "servers": [{ "url": "/" }],
   "security": [{ "sessionCookie": [] }],
@@ -11,6 +11,7 @@
     "/api/v1/auth/session": {
       "get": {
         "operationId": "getAuthSession",
+        "x-lisimflix": { "owner": "ACC-1", "status": "implemented" },
         "summary": "Current auth state; anonymous callers get authenticated:false, viewer:null and a CSRF token",
         "security": [{}, { "sessionCookie": [] }],
         "responses": {
@@ -23,6 +24,7 @@
     "/api/v1/auth/login": {
       "post": {
         "operationId": "login",
+        "x-lisimflix": { "owner": "ACC-1", "status": "implemented" },
         "summary": "Sign in; rotates the session cookie and issues a fresh CSRF token",
         "security": [{}],
         "parameters": [{ "$ref": "#/components/parameters/CSRFToken" }],
@@ -44,6 +46,7 @@
     "/api/v1/auth/logout": {
       "post": {
         "operationId": "logout",
+        "x-lisimflix": { "owner": "ACC-1", "status": "implemented" },
         "summary": "Sign out; removes the cookie and revokes the auth session and its playback sessions",
         "parameters": [{ "$ref": "#/components/parameters/CSRFToken" }],
         "responses": {
@@ -57,6 +60,7 @@
     "/api/v1/movies": {
       "get": {
         "operationId": "listMovies",
+        "x-lisimflix": { "owner": "PLY-2", "status": "implemented" },
         "summary": "Movies the viewer may play (M1: only the configured movie)",
         "responses": {
           "200": { "description": "Movies", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/MovieList" } } } },
@@ -69,6 +73,7 @@
     "/api/v1/playback/sessions": {
       "post": {
         "operationId": "createPlaybackSession",
+        "x-lisimflix": { "owner": "PLY-2", "status": "implemented" },
         "summary": "Start playback. Replaying the same key and payload for an active session returns 200 with that session. The same key with a changed payload, or the key of a session that ended recently (its record is kept for 5 minutes), is 409 idempotency_conflict: start again with a new key.",
         "parameters": [{ "$ref": "#/components/parameters/CSRFToken" }, { "$ref": "#/components/parameters/IdempotencyKey" }],
         "requestBody": { "required": true, "content": { "application/json": { "schema": { "$ref": "#/components/schemas/PlaybackSessionRequest" } } } },
@@ -89,6 +94,7 @@
     "/api/v1/playback/sessions/{sessionId}/heartbeat": {
       "post": {
         "operationId": "heartbeatPlaybackSession",
+        "x-lisimflix": { "owner": "PLY-2", "status": "implemented" },
         "summary": "Keep a live owned session alive; expired sessions return 410 and are not revived",
         "parameters": [{ "$ref": "#/components/parameters/SessionId" }, { "$ref": "#/components/parameters/CSRFToken" }],
         "requestBody": { "required": true, "content": { "application/json": { "schema": { "$ref": "#/components/schemas/HeartbeatRequest" } } } },
@@ -106,6 +112,7 @@
     "/api/v1/playback/sessions/{sessionId}": {
       "delete": {
         "operationId": "stopPlaybackSession",
+        "x-lisimflix": { "owner": "PLY-2", "status": "implemented" },
         "summary": "Stop an owned session; idempotent",
         "parameters": [{ "$ref": "#/components/parameters/SessionId" }, { "$ref": "#/components/parameters/CSRFToken" }],
         "responses": {
@@ -120,6 +127,7 @@
     "/api/v1/playback/sessions/{sessionId}/content": {
       "get": {
         "operationId": "getPlaybackContent",
+        "x-lisimflix": { "owner": "PLY-2", "status": "implemented" },
         "summary": "Original MP4 bytes, never transformed. Needs both the user cookie and the signed grant.",
         "parameters": [
           { "$ref": "#/components/parameters/SessionId" },
@@ -144,6 +152,7 @@
       },
       "head": {
         "operationId": "headPlaybackContent",
+        "x-lisimflix": { "owner": "PLY-2", "status": "implemented" },
         "summary": "Headers of the original MP4 (length, ranges, validators) without a body",
         "parameters": [
           { "$ref": "#/components/parameters/SessionId" },
@@ -184,7 +193,7 @@
     },
     "responses": {
       "Error": {
-        "description": "Error. 400 invalid_request, 401 unauthenticated, 403 csrf_failed, 404 not_found (unknown or not owned), 409 source_changed or idempotency_conflict (reused key with a different body, or the key of a session that ended recently; start again with a fresh key), 410 playback_expired, 429 rate_limited or capacity_unavailable, 500 internal_error, 503 source_unavailable.",
+        "description": "Error. 400 invalid_request, 401 unauthenticated, 403 csrf_failed, 404 not_found (unknown or not owned), 409 source_changed, idempotency_conflict (reused key with a different body, or the key of a session that ended recently; start again with a fresh key) or revision_conflict (expectedRevision is not the current revision; reload and retry), 410 playback_expired, 429 rate_limited or capacity_unavailable, 500 internal_error, 503 source_unavailable.",
         "content": { "application/json": { "schema": { "$ref": "#/components/schemas/APIError" } } }
       }
     },
@@ -258,7 +267,7 @@
         "properties": {
           "code": {
             "type": "string",
-            "enum": ["invalid_request", "unauthenticated", "csrf_failed", "not_found", "source_changed", "idempotency_conflict", "playback_expired", "capacity_unavailable", "rate_limited", "source_unavailable", "internal_error"]
+            "enum": ["invalid_request", "unauthenticated", "csrf_failed", "not_found", "source_changed", "idempotency_conflict", "revision_conflict", "playback_expired", "capacity_unavailable", "rate_limited", "source_unavailable", "internal_error"]
           },
           "message": { "type": "string", "maxLength": 256 },
           "requestId": { "type": "string", "minLength": 1 }
```

**Modify `README.md`:**

```diff
diff --git a/README.md b/README.md
index e732179..a25f025 100644
--- a/README.md
+++ b/README.md
@@ -17,7 +17,7 @@ only: plain HTTP, one viewer and one movie.
 |---|---|
 | `docs/superpowers/specs/` | Design spec: scope, feature catalogue (§9.4), decisions (§14) |
 | `docs/superpowers/plans/` | Implementation plans, one per claimed feature |
-| `docs/contracts/` | OpenAPI contract (`m1-openapi.json`) |
+| `docs/contracts/` | OpenAPI contract (`openapi.json`) |
 | `docs/testing/` | Test evidence templates, results and reference installations |
 | `deploy/` | Container compose file and run guide |
 | `server/` | Go server |
@@ -37,7 +37,7 @@ make check
 `make check` runs gofmt, vet and Go tests (with `-race`), regenerates the API types, then runs the web
 contract tests, lint, typecheck and build. GitHub Actions runs the same target on every PR.
 It fails if `web/src/api/schema.d.ts` is stale, so commit the regenerated file after
-changing `docs/contracts/m1-openapi.json`.
+changing `docs/contracts/openapi.json`.
 
 ## Contributing
 
```

**Modify `server/internal/contracts/errors.go`:**

```diff
diff --git a/server/internal/contracts/errors.go b/server/internal/contracts/errors.go
index c5c6a78..5f5cb8c 100644
--- a/server/internal/contracts/errors.go
+++ b/server/internal/contracts/errors.go
@@ -14,6 +14,7 @@ const (
 	CodeNotFound            = "not_found"
 	CodeSourceChanged       = "source_changed"
 	CodeIdempotencyConflict = "idempotency_conflict"
+	CodeRevisionConflict    = "revision_conflict"
 	CodePlaybackExpired     = "playback_expired"
 	CodeCapacityUnavailable = "capacity_unavailable"
 	CodeRateLimited         = "rate_limited"
@@ -30,6 +31,7 @@ var codeStatus = map[string]int{
 	CodeNotFound:            http.StatusNotFound,
 	CodeSourceChanged:       http.StatusConflict,
 	CodeIdempotencyConflict: http.StatusConflict,
+	CodeRevisionConflict:    http.StatusConflict,
 	CodePlaybackExpired:     http.StatusGone,
 	CodeCapacityUnavailable: http.StatusTooManyRequests,
 	CodeRateLimited:         http.StatusTooManyRequests,
```

**Modify `server/internal/contracts/types.go`:**

```diff
diff --git a/server/internal/contracts/types.go b/server/internal/contracts/types.go
index 9e6f175..45424cb 100644
--- a/server/internal/contracts/types.go
+++ b/server/internal/contracts/types.go
@@ -1,5 +1,5 @@
 // Package contracts holds the public M1 API wire types shared by the runtime,
-// playback and web player. docs/contracts/m1-openapi.json is the source of
+// playback and web player. docs/contracts/openapi.json is the source of
 // truth; TestOpenAPIMatchesGoTypes keeps these structs' field names and
 // required set in step with it (not their types or nullability).
 package contracts
```

**Modify `web/package.json`:**

```diff
diff --git a/web/package.json b/web/package.json
index aab2cdd..e8dab96 100644
--- a/web/package.json
+++ b/web/package.json
@@ -6,7 +6,7 @@
     "node": "24.22.1"
   },
   "scripts": {
-    "generate:api": "openapi-typescript ../docs/contracts/m1-openapi.json -o src/api/schema.d.ts",
+    "generate:api": "openapi-typescript ../docs/contracts/openapi.json -o src/api/schema.d.ts",
     "test:contracts": "node --test contract-tests/fixtures.test.mjs",
     "lint": "eslint src contract-tests",
     "typecheck": "tsc --noEmit",
```

- [ ] **Step 4: Regenerate the types and run the checks**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck
npm --prefix web run lint
```

Expected: Go `ok`; `test:contracts`: 21 tests, 21 pass; typecheck and lint pass.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/contracts/m1-openapi.json docs/contracts/openapi.json server/internal/contracts/errors.go server/internal/contracts/types.go server/internal/contracts/types_test.go testdata/contracts/m1/README.md web/contract-tests/fixtures.test.mjs web/package.json web/src/api/schema.d.ts
git commit -m "feat(api): one Lisimflix API contract with owners and revision_conflict (CTR-2)"
```

---

### Task 3: Progress and watched-state contract

**Files:**
- Create: `server/internal/contracts/fixtures_test.go`
- Create: `server/internal/contracts/userdata.go`
- Create: `server/internal/contracts/userdata_test.go`
- Create: `testdata/contracts/catalogue/README.md`
- Create: `testdata/contracts/catalogue/progress-duplicate.json`
- Create: `testdata/contracts/catalogue/progress-seek.json`
- Create: `testdata/contracts/catalogue/watched-request.json`
- Modify: `docs/contracts/openapi.json`
- Modify: `server/internal/contracts/types_test.go`
- Modify: `web/contract-tests/fixtures.test.mjs`
- Modify: `web/contract-tests/schema.test.ts`
- Regenerate: `web/src/api/schema.d.ts` (`npm --prefix web run generate:api`; never edit by hand)

**Interfaces:**
- Consumes: Task 2's contract path, `CodeRevisionConflict` and `TestOpenAPIMatchesGoTypes` map. Task 1's `SourceRevision` (the fixtures' `sourceRevision` values).
- Produces: `UserItemState`, `Resume`, `Watched`, `ProgressEvent`, `ProgressResult`, `WatchedRequest`, plus the constants `WatchedAuto|Manual|Import`, `ProgressTick|Pause|Seek|Stop` and `ProgressDuplicate|Stale`. The `ItemId` parameter. Operations `setWatched` and `reportProgress` (DATA-1, planned). Test helpers `loadCatalogueFixture`, `roundTripFixtures(t, map[string]func() any)` and `checkEnum(t, schema, prop, goValues...)`, plus the web `checkFixtures(fixtures, invalid)` and `catalogue(file)` helpers. Later tasks reuse all of these.

- [ ] **Step 1: Write the failing tests and fixtures**

**Create `server/internal/contracts/fixtures_test.go`:**

```go
package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Helpers for the CTR-2 fixtures in testdata/contracts/catalogue.

func loadCatalogueFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "contracts", "catalogue", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// roundTripFixtures decodes each fixture strictly into its Go type and checks
// that encoding it again gives the same JSON, so Go producers emit exactly
// the shapes the web app is built against.
func roundTripFixtures(t *testing.T, cases map[string]func() any) {
	t.Helper()
	for name, newValue := range cases {
		t.Run(name, func(t *testing.T) {
			raw := loadCatalogueFixture(t, name)
			v := newValue()
			decodeStrict(t, raw, v)
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := canonical(t, out), canonical(t, raw); got != want {
				t.Fatalf("round trip changed fixture\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// checkEnum fails unless goValues are exactly the string values of the
// OpenAPI enum at schema.prop (a null member is ignored). The Go constants
// are producers' only spelling of each value.
func checkEnum(t *testing.T, schema, prop string, goValues ...string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Enum []any `json:"enum"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, v := range doc.Components.Schemas[schema].Properties[prop].Enum {
		if s, ok := v.(string); ok {
			want = append(want, s)
		}
	}
	got := slices.Sorted(slices.Values(goValues))
	slices.Sort(want)
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("%s.%s: go %v, openapi %v", schema, prop, got, want)
	}
}
```

**Create `server/internal/contracts/userdata_test.go`:**

```go
package contracts

import "testing"

var userDataFixtures = map[string]func() any{
	"progress-seek.json":      func() any { return &ProgressEvent{} },
	"progress-duplicate.json": func() any { return &ProgressResult{} },
	"watched-request.json":    func() any { return &WatchedRequest{} },
}

func TestUserDataFixturesRoundTrip(t *testing.T) { roundTripFixtures(t, userDataFixtures) }

func TestUserDataEnumsMatchOpenAPI(t *testing.T) {
	checkEnum(t, "Watched", "origin", WatchedAuto, WatchedManual, WatchedImport)
	checkEnum(t, "ProgressEvent", "kind", ProgressTick, ProgressPause, ProgressSeek, ProgressStop)
	checkEnum(t, "ProgressResult", "reason", ProgressDuplicate, ProgressStale)
}
```

**Create `testdata/contracts/catalogue/README.md`:**

```markdown
# Catalogue contract fixtures (CTR-2)

Synthetic examples of the catalogue, user-data, profile and metadata-provenance
shapes in [`docs/contracts/openapi.json`](../../../docs/contracts/openapi.json).
Go (`server/internal/contracts`) round-trips every file here strictly;
`npm --prefix web run test:contracts` validates them against OpenAPI and rejects
the invalid variants listed in `web/contract-tests/fixtures.test.mjs`. Producers
(LIB-2, META-1, DATA-1, ACC-1) emit these shapes; UI-2 is built against them.
All IDs, titles and provider IDs are made up. Never put real media, paths,
provider keys or personal data here.

| File | Schema | Shows |
|---|---|---|
| `progress-seek.json` | `ProgressEvent` | A seek from 30:30 to 40:00 |
| `progress-duplicate.json` | `ProgressResult` | A duplicate event: not applied, state unchanged |
| `watched-request.json` | `WatchedRequest` | Mark watched, expecting revision 7 |

The `sourceRevision` values are real `contracts.SourceRevision` outputs for the
synthetic file IDs, sizes and times, e.g. `fil-synthetic-0001`, 24000000000 bytes,
mtime 1761523200000000000 ns.
```

**Create `testdata/contracts/catalogue/progress-duplicate.json`:**

```json
{
  "accepted": false,
  "reason": "duplicate",
  "state": {
    "itemId": "mov-synthetic-0001",
    "revision": 7,
    "resume": {
      "positionMs": 1830000,
      "durationMs": 5400000,
      "versionId": "ver-synthetic-0001",
      "sourceRevision": "fbe33ec2d0dbf23469d5d40be1aff9ec70fc27ee7ca7d336cb41f0216034e659",
      "updatedAt": "2026-10-27T08:15:00Z"
    },
    "watched": null,
    "playCount": 0,
    "lastPlayedAt": "2026-10-27T08:15:00Z",
    "hiddenFromContinueWatching": false
  }
}
```

**Create `testdata/contracts/catalogue/progress-seek.json`:**

```json
{ "sequence": 42, "kind": "seek", "positionMs": 2400000, "fromPositionMs": 1830000 }
```

**Create `testdata/contracts/catalogue/watched-request.json`:**

```json
{ "watched": true, "expectedRevision": 7 }
```

**Modify `server/internal/contracts/types_test.go`:**

```diff
diff --git a/server/internal/contracts/types_test.go b/server/internal/contracts/types_test.go
index 2193c8c..12049e8 100644
--- a/server/internal/contracts/types_test.go
+++ b/server/internal/contracts/types_test.go
@@ -173,6 +173,9 @@ func TestOpenAPIMatchesGoTypes(t *testing.T) {
 		"Viewer": Viewer{}, "AuthState": AuthState{}, "Movie": Movie{}, "MovieList": MovieList{},
 		"PlaybackSession": PlaybackSession{}, "APIError": APIError{},
 		"LoginRequest": LoginRequest{}, "PlaybackSessionRequest": PlaybackSessionRequest{}, "HeartbeatRequest": HeartbeatRequest{},
+		// CTR-2
+		"UserItemState": UserItemState{}, "Resume": Resume{}, "Watched": Watched{},
+		"ProgressEvent": ProgressEvent{}, "ProgressResult": ProgressResult{}, "WatchedRequest": WatchedRequest{},
 	}
 	for name := range doc.Components.Schemas {
 		if _, ok := types[name]; !ok {
```

**Modify `web/contract-tests/fixtures.test.mjs`:**

```diff
diff --git a/web/contract-tests/fixtures.test.mjs b/web/contract-tests/fixtures.test.mjs
index 04493fb..dc2b054 100644
--- a/web/contract-tests/fixtures.test.mjs
+++ b/web/contract-tests/fixtures.test.mjs
@@ -9,6 +9,8 @@ const ajv = new Ajv2020({ allErrors: true });
 // OpenAPI stores schemas under components; Go decoding checks the int64 range.
 ajv.addKeyword("components");
 ajv.addFormat("int64", true);
+// RFC 3339 instants with an offset, as Go encodes time.Time; Go decoding checks the calendar.
+ajv.addFormat("date-time", /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/);
 ajv.addSchema({ components: contract.components }, "m1");
 
 const fixtures = {
@@ -71,3 +73,43 @@ test("every operation names its owning feature and whether it is implemented", (
     }
   }
 });
+
+// CTR-2 fixtures (testdata/contracts/catalogue). checkFixtures adds one test
+// per fixture and one rejection test per invalid variant.
+const catalogue = (file) => readJSON(`../../testdata/contracts/catalogue/${file}`);
+
+function checkFixtures(fixtures, invalid) {
+  for (const [file, schema] of Object.entries(fixtures)) {
+    test(`catalogue/${file} satisfies ${schema}`, () => {
+      const validate = ajv.getSchema(`m1#/components/schemas/${schema}`);
+      assert.ok(validate(catalogue(file)), `${file}: ${ajv.errorsText(validate.errors)}`);
+    });
+  }
+  for (const [name, file, mutate] of invalid) {
+    test(`rejects ${name}`, () => {
+      const data = catalogue(file);
+      mutate(data);
+      assert.equal(ajv.getSchema(`m1#/components/schemas/${fixtures[file]}`)(data), false, `${name} was accepted`);
+    });
+  }
+}
+
+const userDataFixtures = {
+  "progress-seek.json": "ProgressEvent",
+  "progress-duplicate.json": "ProgressResult",
+  "watched-request.json": "WatchedRequest",
+};
+
+checkFixtures(userDataFixtures, [
+  ["seek without a from position", "progress-seek.json", (d) => { d.fromPositionMs = null; }],
+  ["tick with a from position", "progress-seek.json", (d) => { d.kind = "tick"; }],
+  ["negative position", "progress-seek.json", (d) => { d.positionMs = -1; }],
+  ["rejected event without a reason", "progress-duplicate.json", (d) => { d.reason = null; }],
+  ["accepted event with a reason", "progress-duplicate.json", (d) => { d.accepted = true; }],
+  ["unknown reject reason", "progress-duplicate.json", (d) => { d.reason = "late"; }],
+  ["resume without a source revision", "progress-duplicate.json", (d) => { delete d.state.resume.sourceRevision; }],
+  ["local time without an offset", "progress-duplicate.json", (d) => { d.state.resume.updatedAt = "2026-10-27 08:15:00"; }],
+  ["unknown watched origin", "progress-duplicate.json", (d) => { d.state.watched = { value: true, origin: "plex", at: "2026-10-27T08:15:00Z" }; }],
+  ["negative play count", "progress-duplicate.json", (d) => { d.state.playCount = -1; }],
+  ["fractional expected revision", "watched-request.json", (d) => { d.expectedRevision = 1.5; }],
+]);
```

**Modify `web/contract-tests/schema.test.ts`:**

```diff
diff --git a/web/contract-tests/schema.test.ts b/web/contract-tests/schema.test.ts
index f855f04..221e0fb 100644
--- a/web/contract-tests/schema.test.ts
+++ b/web/contract-tests/schema.test.ts
@@ -5,6 +5,9 @@ import type { components } from "../src/api/schema";
 type Movie = components["schemas"]["Movie"];
 type PlaybackSession = components["schemas"]["PlaybackSession"];
 type AuthState = components["schemas"]["AuthState"];
+type UserItemState = components["schemas"]["UserItemState"];
+type ProgressEvent = components["schemas"]["ProgressEvent"];
+type ProgressResult = components["schemas"]["ProgressResult"];
 
 export const movie = {
   id: "movie-synthetic-0001",
@@ -39,3 +42,24 @@ export const badDuration: Movie = { ...movie, durationMs: "5400000" };
 
 // @ts-expect-error M1 only has the direct route
 export const badRoute: PlaybackSession = { ...session, route: "transcode" };
+
+// CTR-2 (testdata/contracts/catalogue)
+export const freshState = {
+  itemId: "epi-synthetic-0002",
+  revision: 0,
+  resume: null,
+  watched: null,
+  playCount: 0,
+  lastPlayedAt: null,
+  hiddenFromContinueWatching: false,
+} satisfies UserItemState;
+
+export const seek = { sequence: 42, kind: "seek", positionMs: 2400000, fromPositionMs: 1830000 } satisfies ProgressEvent;
+
+export const duplicate = { accepted: false, reason: "duplicate", state: freshState } satisfies ProgressResult;
+
+// @ts-expect-error positions are numbers of milliseconds, not strings
+export const badPosition: ProgressEvent = { ...seek, positionMs: "2400000" };
+
+// @ts-expect-error reject reasons are a closed set
+export const badReason: ProgressResult = { ...duplicate, reason: "late" };
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck
```

Expected: Go FAILs to build (`undefined: UserItemState`, `ProgressEvent`, `ProgressResult` …). `test:contracts`: 35 tests, 14 fail: the 3 fixtures and 11 rejections, because the schemas don't exist yet. `typecheck`: `Property 'UserItemState' does not exist`.

- [ ] **Step 3: Implement**

**Create `server/internal/contracts/userdata.go`:**

```go
package contracts

import "time"

// Per-profile user data wire types (CTR-2). Progress belongs to
// profile + logical item and keeps the version and source revision it was
// recorded against (spec §3.8, §10.3).

// Watched origins (Watched.origin).
const (
	WatchedAuto   = "auto"
	WatchedManual = "manual"
	WatchedImport = "import"
)

// Progress event kinds (ProgressEvent.kind).
const (
	ProgressTick  = "tick"
	ProgressPause = "pause"
	ProgressSeek  = "seek"
	ProgressStop  = "stop"
)

// Reasons an event was not applied (ProgressResult.reason).
const (
	ProgressDuplicate = "duplicate"
	ProgressStale     = "stale"
)

// UserItemState is the current profile's state for one movie or episode.
// Revision counts committed changes (0 = nothing recorded yet); writes send
// it back as expectedRevision.
type UserItemState struct {
	ItemID                     string     `json:"itemId"`
	Revision                   int64      `json:"revision"`
	Resume                     *Resume    `json:"resume"`
	Watched                    *Watched   `json:"watched"` // null: never marked either way
	PlayCount                  *int       `json:"playCount"`
	LastPlayedAt               *time.Time `json:"lastPlayedAt"`
	HiddenFromContinueWatching bool       `json:"hiddenFromContinueWatching"`
}

// Resume is a resume position on one version's timeline.
type Resume struct {
	PositionMs     int64     `json:"positionMs"`
	DurationMs     int64     `json:"durationMs"`
	VersionID      string    `json:"versionId"`
	SourceRevision string    `json:"sourceRevision"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Watched is the watched state and where it came from. A manual value is
// never undone by a later automatic event.
type Watched struct {
	Value  bool      `json:"value"`
	Origin string    `json:"origin"`
	At     time.Time `json:"at"`
}

// ProgressEvent is one progress report for a live playback session. The
// session fixes the item, version and source revision. FromPositionMs is set
// only for seeks.
type ProgressEvent struct {
	Sequence       int64  `json:"sequence"`
	Kind           string `json:"kind"`
	PositionMs     int64  `json:"positionMs"`
	FromPositionMs *int64 `json:"fromPositionMs"`
}

// ProgressResult says whether an event was applied; a duplicate or stale
// event is answered with accepted:false and the unchanged state, not an error.
type ProgressResult struct {
	Accepted bool          `json:"accepted"`
	Reason   *string       `json:"reason"`
	State    UserItemState `json:"state"`
}

type WatchedRequest struct {
	Watched          bool  `json:"watched"`
	ExpectedRevision int64 `json:"expectedRevision"`
}
```

**Modify `docs/contracts/openapi.json`:**

```diff
diff --git a/docs/contracts/openapi.json b/docs/contracts/openapi.json
index f4b2d22..8fa6028 100644
--- a/docs/contracts/openapi.json
+++ b/docs/contracts/openapi.json
@@ -175,6 +175,43 @@
           "503": { "description": "Source unavailable" }
         }
       }
+    },
+    "/api/v1/items/{itemId}/watched": {
+      "put": {
+        "operationId": "setWatched",
+        "x-lisimflix": { "owner": "DATA-1", "status": "planned" },
+        "summary": "Mark a movie or episode watched or unwatched for the current profile (origin manual). expectedRevision must equal the current UserItemState.revision, otherwise 409 revision_conflict and nothing changes.",
+        "parameters": [{ "$ref": "#/components/parameters/ItemId" }, { "$ref": "#/components/parameters/CSRFToken" }],
+        "requestBody": { "required": true, "content": { "application/json": { "schema": { "$ref": "#/components/schemas/WatchedRequest" } } } },
+        "responses": {
+          "200": { "description": "New state", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/UserItemState" } } } },
+          "400": { "$ref": "#/components/responses/Error" },
+          "401": { "$ref": "#/components/responses/Error" },
+          "403": { "$ref": "#/components/responses/Error" },
+          "404": { "$ref": "#/components/responses/Error" },
+          "409": { "$ref": "#/components/responses/Error" },
+          "500": { "$ref": "#/components/responses/Error" }
+        }
+      }
+    },
+    "/api/v1/playback/sessions/{sessionId}/progress": {
+      "post": {
+        "operationId": "reportProgress",
+        "x-lisimflix": { "owner": "DATA-1", "status": "planned" },
+        "summary": "Report progress for an owned live session. A duplicate or out-of-order sequence is 200 with accepted:false. A session whose source changed is 409 source_changed; an ended session is 410.",
+        "parameters": [{ "$ref": "#/components/parameters/SessionId" }, { "$ref": "#/components/parameters/CSRFToken" }],
+        "requestBody": { "required": true, "content": { "application/json": { "schema": { "$ref": "#/components/schemas/ProgressEvent" } } } },
+        "responses": {
+          "200": { "description": "Result", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/ProgressResult" } } } },
+          "400": { "$ref": "#/components/responses/Error" },
+          "401": { "$ref": "#/components/responses/Error" },
+          "403": { "$ref": "#/components/responses/Error" },
+          "404": { "$ref": "#/components/responses/Error" },
+          "409": { "$ref": "#/components/responses/Error" },
+          "410": { "$ref": "#/components/responses/Error" },
+          "500": { "$ref": "#/components/responses/Error" }
+        }
+      }
     }
   },
   "components": {
@@ -186,6 +223,7 @@
       "IdempotencyKey": { "name": "Idempotency-Key", "in": "header", "required": true, "schema": { "type": "string", "minLength": 1, "maxLength": 128 } },
       "SessionId": { "name": "sessionId", "in": "path", "required": true, "schema": { "type": "string", "minLength": 1 } },
       "Grant": { "name": "grant", "in": "query", "required": true, "description": "Signature bound to playback ID, auth session, account/profile, movie and source revision; not authority without the live session", "schema": { "type": "string", "minLength": 1 } },
+      "ItemId": { "name": "itemId", "in": "path", "required": true, "schema": { "type": "string", "minLength": 1 } },
       "Range": { "name": "Range", "in": "header", "required": false, "schema": { "type": "string" } },
       "IfRange": { "name": "If-Range", "in": "header", "required": false, "schema": { "type": "string" } },
       "IfNoneMatch": { "name": "If-None-Match", "in": "header", "required": false, "schema": { "type": "string" } },
@@ -297,6 +335,83 @@
         "properties": {
           "sequence": { "type": "integer", "format": "int64", "minimum": 0 }
         }
+      },
+      "UserItemState": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["itemId", "revision", "resume", "watched", "playCount", "lastPlayedAt", "hiddenFromContinueWatching"],
+        "description": "The current profile's state for one movie or episode. revision counts committed changes (0 = nothing recorded yet); writes send it back as expectedRevision.",
+        "properties": {
+          "itemId": { "type": "string", "minLength": 1 },
+          "revision": { "type": "integer", "format": "int64", "minimum": 0 },
+          "resume": { "oneOf": [{ "$ref": "#/components/schemas/Resume" }, { "type": "null" }] },
+          "watched": { "oneOf": [{ "$ref": "#/components/schemas/Watched" }, { "type": "null" }], "description": "Null: never marked either way" },
+          "playCount": { "type": ["integer", "null"], "minimum": 0, "description": "Null when unknown, e.g. after an import that had no count" },
+          "lastPlayedAt": { "type": ["string", "null"], "format": "date-time" },
+          "hiddenFromContinueWatching": { "type": "boolean" }
+        }
+      },
+      "Resume": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["positionMs", "durationMs", "versionId", "sourceRevision", "updatedAt"],
+        "description": "A resume position on one version's timeline. A different version or source revision needs confirmation before the position is reused.",
+        "properties": {
+          "positionMs": { "type": "integer", "format": "int64", "minimum": 0 },
+          "durationMs": { "type": "integer", "format": "int64", "minimum": 1 },
+          "versionId": { "type": "string", "minLength": 1 },
+          "sourceRevision": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
+          "updatedAt": { "type": "string", "format": "date-time" }
+        }
+      },
+      "Watched": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["value", "origin", "at"],
+        "description": "Watched state and where it came from. A manual value is never undone by a later automatic event.",
+        "properties": {
+          "value": { "type": "boolean" },
+          "origin": { "type": "string", "enum": ["auto", "manual", "import"] },
+          "at": { "type": "string", "format": "date-time" }
+        }
+      },
+      "ProgressEvent": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["sequence", "kind", "positionMs", "fromPositionMs"],
+        "description": "One progress report for a live playback session, sent every 10 s and on pause, seek and stop. The session fixes the item, version and source revision. sequence increases within a session.",
+        "properties": {
+          "sequence": { "type": "integer", "format": "int64", "minimum": 0 },
+          "kind": { "type": "string", "enum": ["tick", "pause", "seek", "stop"] },
+          "positionMs": { "type": "integer", "format": "int64", "minimum": 0 },
+          "fromPositionMs": { "type": ["integer", "null"], "format": "int64", "minimum": 0, "description": "Seeks only: the position before the seek" }
+        },
+        "if": { "properties": { "kind": { "const": "seek" } } },
+        "then": { "properties": { "fromPositionMs": { "type": "integer" } } },
+        "else": { "properties": { "fromPositionMs": { "type": "null" } } }
+      },
+      "ProgressResult": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["accepted", "reason", "state"],
+        "description": "A duplicate or stale event is answered with accepted:false and the unchanged state, not an error.",
+        "properties": {
+          "accepted": { "type": "boolean" },
+          "reason": { "type": ["string", "null"], "enum": ["duplicate", "stale", null] },
+          "state": { "$ref": "#/components/schemas/UserItemState" }
+        },
+        "if": { "properties": { "accepted": { "const": true } } },
+        "then": { "properties": { "reason": { "type": "null" } } },
+        "else": { "properties": { "reason": { "type": "string" } } }
+      },
+      "WatchedRequest": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["watched", "expectedRevision"],
+        "properties": {
+          "watched": { "type": "boolean" },
+          "expectedRevision": { "type": "integer", "format": "int64", "minimum": 0 }
+        }
       }
     }
   }
```

- [ ] **Step 4: Regenerate the types and run the checks**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck
npm --prefix web run lint
```

Expected: Go `ok`; `test:contracts`: 35 tests, 35 pass; typecheck and lint pass.

- [ ] **Step 5: Commit**

```bash
git add docs/contracts/openapi.json server/internal/contracts/fixtures_test.go server/internal/contracts/types_test.go server/internal/contracts/userdata.go server/internal/contracts/userdata_test.go testdata/contracts/catalogue/README.md testdata/contracts/catalogue/progress-duplicate.json testdata/contracts/catalogue/progress-seek.json testdata/contracts/catalogue/watched-request.json web/contract-tests/fixtures.test.mjs web/contract-tests/schema.test.ts web/src/api/schema.d.ts
git commit -m "feat(api): progress and watched-state contract (CTR-2)"
```

---

### Task 4: Library, item, version and stream contract

**Files:**
- Create: `server/internal/contracts/catalogue.go`
- Create: `server/internal/contracts/catalogue_test.go`
- Create: `testdata/contracts/catalogue/item-episode-shared-file.json`
- Create: `testdata/contracts/catalogue/item-movie.json`
- Create: `testdata/contracts/catalogue/item-show.json`
- Create: `testdata/contracts/catalogue/items-episodes.json`
- Create: `testdata/contracts/catalogue/items-movies.json`
- Create: `testdata/contracts/catalogue/libraries.json`
- Modify: `docs/contracts/openapi.json`
- Modify: `server/internal/contracts/types_test.go`
- Modify: `testdata/contracts/catalogue/README.md`
- Modify: `web/contract-tests/fixtures.test.mjs`
- Modify: `web/contract-tests/schema.test.ts`
- Regenerate: `web/src/api/schema.d.ts` (`npm --prefix web run generate:api`; never edit by hand)

**Interfaces:**
- Consumes: Task 3's `UserItemState` (`ItemSummary.userState`), test helpers and `freshState` TypeScript value. Task 2's `ItemId` parameter, which Task 3 added.
- Produces: `Page[T]` (encodes nil items as `[]`), `Library`/`LibraryList`, `ContentRating`, `ItemSummary`/`ItemPage`, `ExternalID`, `ItemDetail`, `MediaVersion`, `Segment`, `VideoStream`, `AudioStream`, `SubtitleStream`, plus the `Library*`, `Kind*`, `HDR*` and `Subtitle*` constants. The `Cursor`, `Limit`, `LibraryIdQuery` and `ParentIdQuery` parameters. Operations `listLibraries`, `listItems` and `getItem` (LIB-2, planned).

- [ ] **Step 1: Write the failing tests and fixtures**

**Create `server/internal/contracts/catalogue_test.go`:**

```go
package contracts

import (
	"encoding/json"
	"testing"
)

var catalogueFixtures = map[string]func() any{
	"libraries.json":                func() any { return &LibraryList{} },
	"items-movies.json":             func() any { return &ItemPage{} },
	"items-episodes.json":           func() any { return &ItemPage{} },
	"item-movie.json":               func() any { return &ItemDetail{} },
	"item-episode-shared-file.json": func() any { return &ItemDetail{} },
	"item-show.json":                func() any { return &ItemDetail{} },
}

func TestCatalogueFixturesRoundTrip(t *testing.T) { roundTripFixtures(t, catalogueFixtures) }

func TestEmptyPagesEncodeItemsAsArray(t *testing.T) {
	for _, v := range []any{LibraryList{}, ItemPage{}} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != `{"items":[],"nextCursor":null}` {
			t.Fatalf("%T encoded as %s", v, b)
		}
	}
}

func TestCatalogueEnumsMatchOpenAPI(t *testing.T) {
	checkEnum(t, "Library", "type", LibraryMovies, LibraryTV, LibraryAnime)
	checkEnum(t, "ItemSummary", "kind", KindMovie, KindShow, KindSeason, KindEpisode)
	checkEnum(t, "VideoStream", "hdr", HDRNone, HDR10, HDR10Plus, HDRHLG, HDRDolbyVision)
	checkEnum(t, "SubtitleStream", "kind", SubtitleText, SubtitleImage, SubtitleASS)
}
```

**Create `testdata/contracts/catalogue/item-episode-shared-file.json`:**

```json
{
  "item": {
    "id": "epi-synthetic-0002",
    "kind": "episode",
    "libraryId": "lib-synthetic-anime",
    "parentId": "sea-synthetic-0001",
    "title": "Synthetic Episode Two",
    "sortTitle": "Synthetic Episode Two",
    "year": 2025,
    "index": 2,
    "durationMs": null,
    "contentRating": null,
    "primaryImageUrl": null,
    "backdropUrl": null,
    "userState": {
      "itemId": "epi-synthetic-0002",
      "revision": 0,
      "resume": null,
      "watched": null,
      "playCount": 0,
      "lastPlayedAt": null,
      "hiddenFromContinueWatching": false
    }
  },
  "originalTitle": "Gousei Episode Two",
  "overview": null,
  "genres": [],
  "externalIds": [{ "namespace": "anidb", "id": "800002" }],
  "versions": [
    {
      "id": "ver-synthetic-0003",
      "fileId": "fil-synthetic-0003",
      "label": null,
      "durationMs": null,
      "sourceRevision": "2fed55f288c2c2f69d958fa92efcb153a77c49b68e638a946c648497e65c055b",
      "segment": null,
      "container": "matroska,webm",
      "video": [{ "index": 0, "codec": "h264", "width": 1920, "height": 1080, "hdr": "sdr", "default": true }],
      "audio": [{ "index": 1, "codec": "aac", "profile": "LC", "channels": 2, "language": "jpn", "title": null, "default": true, "forced": false }],
      "subtitles": [{ "index": 2, "codec": "ass", "kind": "ass", "language": "eng", "title": "Full", "default": true, "forced": false, "hearingImpaired": false }]
    }
  ]
}
```

**Create `testdata/contracts/catalogue/item-movie.json`:**

```json
{
  "item": {
    "id": "mov-synthetic-0001",
    "kind": "movie",
    "libraryId": "lib-synthetic-movies",
    "parentId": null,
    "title": "Synthetic Test Movie",
    "sortTitle": "Synthetic Test Movie",
    "year": 2026,
    "index": null,
    "durationMs": 5400000,
    "contentRating": { "region": "AU", "value": "M" },
    "primaryImageUrl": "/api/v1/items/mov-synthetic-0001/images/primary",
    "backdropUrl": null,
    "userState": {
      "itemId": "mov-synthetic-0001",
      "revision": 0,
      "resume": null,
      "watched": null,
      "playCount": 0,
      "lastPlayedAt": null,
      "hiddenFromContinueWatching": false
    }
  },
  "originalTitle": null,
  "overview": "A made-up film used only in contract fixtures.",
  "genres": ["Drama"],
  "externalIds": [{ "namespace": "tmdb", "id": "900001" }],
  "versions": [
    {
      "id": "ver-synthetic-0001",
      "fileId": "fil-synthetic-0001",
      "label": null,
      "durationMs": 5400000,
      "sourceRevision": "fbe33ec2d0dbf23469d5d40be1aff9ec70fc27ee7ca7d336cb41f0216034e659",
      "segment": null,
      "container": "mov,mp4,m4a,3gp,3g2,mj2",
      "video": [{ "index": 0, "codec": "h264", "width": 1920, "height": 1080, "hdr": "sdr", "default": true }],
      "audio": [{ "index": 1, "codec": "aac", "profile": "LC", "channels": 2, "language": "eng", "title": null, "default": true, "forced": false }],
      "subtitles": [{ "index": 2, "codec": "mov_text", "kind": "text", "language": "eng", "title": "English", "default": false, "forced": false, "hearingImpaired": false }]
    },
    {
      "id": "ver-synthetic-0002",
      "fileId": "fil-synthetic-0002",
      "label": "Director's Cut",
      "durationMs": 6300000,
      "sourceRevision": "e833a81e45623feec47016315972f3976ae4d7afead6345a69d10786293874f4",
      "segment": null,
      "container": "matroska,webm",
      "video": [{ "index": 0, "codec": "hevc", "width": 3840, "height": 2160, "hdr": "hdr10", "default": true }],
      "audio": [
        { "index": 1, "codec": "truehd", "profile": "Dolby TrueHD + Dolby Atmos", "channels": 8, "language": "eng", "title": "Atmos", "default": true, "forced": false },
        { "index": 2, "codec": "ac3", "profile": null, "channels": 6, "language": null, "title": null, "default": false, "forced": false }
      ],
      "subtitles": [{ "index": 3, "codec": "hdmv_pgs_subtitle", "kind": "image", "language": "eng", "title": null, "default": false, "forced": true, "hearingImpaired": false }]
    }
  ]
}
```

**Create `testdata/contracts/catalogue/item-show.json`:**

```json
{
  "item": {
    "id": "shw-synthetic-0001",
    "kind": "show",
    "libraryId": "lib-synthetic-anime",
    "parentId": null,
    "title": "Synthetic Show",
    "sortTitle": "Synthetic Show",
    "year": 2025,
    "index": null,
    "durationMs": null,
    "contentRating": { "region": "AU", "value": "PG" },
    "primaryImageUrl": "/api/v1/items/shw-synthetic-0001/images/primary",
    "backdropUrl": "/api/v1/items/shw-synthetic-0001/images/backdrop",
    "userState": null
  },
  "originalTitle": "Gousei Show",
  "overview": "A made-up series used only in contract fixtures.",
  "genres": ["Animation", "Comedy"],
  "externalIds": [{ "namespace": "anilist", "id": "700001" }, { "namespace": "tvdb", "id": "600001" }],
  "versions": []
}
```

**Create `testdata/contracts/catalogue/items-episodes.json`:**

```json
{
  "items": [
    {
      "id": "epi-synthetic-0001",
      "kind": "episode",
      "libraryId": "lib-synthetic-anime",
      "parentId": "sea-synthetic-0001",
      "title": "Synthetic Episode One",
      "sortTitle": "Synthetic Episode One",
      "year": 2025,
      "index": 1,
      "durationMs": null,
      "contentRating": null,
      "primaryImageUrl": null,
      "backdropUrl": null,
      "userState": {
        "itemId": "epi-synthetic-0001",
        "revision": 2,
        "resume": null,
        "watched": { "value": true, "origin": "manual", "at": "2026-10-26T10:00:00Z" },
        "playCount": 1,
        "lastPlayedAt": "2026-10-26T09:40:00Z",
        "hiddenFromContinueWatching": false
      }
    },
    {
      "id": "epi-synthetic-0002",
      "kind": "episode",
      "libraryId": "lib-synthetic-anime",
      "parentId": "sea-synthetic-0001",
      "title": "Synthetic Episode Two",
      "sortTitle": "Synthetic Episode Two",
      "year": 2025,
      "index": 2,
      "durationMs": null,
      "contentRating": null,
      "primaryImageUrl": null,
      "backdropUrl": null,
      "userState": {
        "itemId": "epi-synthetic-0002",
        "revision": 0,
        "resume": null,
        "watched": null,
        "playCount": 0,
        "lastPlayedAt": null,
        "hiddenFromContinueWatching": false
      }
    }
  ],
  "nextCursor": null
}
```

**Create `testdata/contracts/catalogue/items-movies.json`:**

```json
{
  "items": [
    {
      "id": "mov-synthetic-0001",
      "kind": "movie",
      "libraryId": "lib-synthetic-movies",
      "parentId": null,
      "title": "Synthetic Test Movie",
      "sortTitle": "Synthetic Test Movie",
      "year": 2026,
      "index": null,
      "durationMs": 5400000,
      "contentRating": { "region": "AU", "value": "M" },
      "primaryImageUrl": "/api/v1/items/mov-synthetic-0001/images/primary",
      "backdropUrl": null,
      "userState": {
        "itemId": "mov-synthetic-0001",
        "revision": 7,
        "resume": {
          "positionMs": 1830000,
          "durationMs": 5400000,
          "versionId": "ver-synthetic-0001",
          "sourceRevision": "fbe33ec2d0dbf23469d5d40be1aff9ec70fc27ee7ca7d336cb41f0216034e659",
          "updatedAt": "2026-10-27T08:15:00Z"
        },
        "watched": null,
        "playCount": 0,
        "lastPlayedAt": "2026-10-27T08:15:00Z",
        "hiddenFromContinueWatching": false
      }
    }
  ],
  "nextCursor": "cur-synthetic-0002"
}
```

**Create `testdata/contracts/catalogue/libraries.json`:**

```json
{
  "items": [
    { "id": "lib-synthetic-movies", "name": "Movies", "type": "movies" },
    { "id": "lib-synthetic-anime", "name": "Anime", "type": "anime" }
  ],
  "nextCursor": null
}
```

**Modify `server/internal/contracts/types_test.go`:**

```diff
diff --git a/server/internal/contracts/types_test.go b/server/internal/contracts/types_test.go
index 12049e8..060f406 100644
--- a/server/internal/contracts/types_test.go
+++ b/server/internal/contracts/types_test.go
@@ -176,6 +176,10 @@ func TestOpenAPIMatchesGoTypes(t *testing.T) {
 		// CTR-2
 		"UserItemState": UserItemState{}, "Resume": Resume{}, "Watched": Watched{},
 		"ProgressEvent": ProgressEvent{}, "ProgressResult": ProgressResult{}, "WatchedRequest": WatchedRequest{},
+		"Library": Library{}, "LibraryList": LibraryList{}, "ContentRating": ContentRating{},
+		"ItemSummary": ItemSummary{}, "ItemPage": ItemPage{}, "ExternalId": ExternalID{}, "ItemDetail": ItemDetail{},
+		"MediaVersion": MediaVersion{}, "Segment": Segment{},
+		"VideoStream": VideoStream{}, "AudioStream": AudioStream{}, "SubtitleStream": SubtitleStream{},
 	}
 	for name := range doc.Components.Schemas {
 		if _, ok := types[name]; !ok {
```

**Modify `testdata/contracts/catalogue/README.md`:**

```diff
diff --git a/testdata/contracts/catalogue/README.md b/testdata/contracts/catalogue/README.md
index 1aa7614..8ac52bc 100644
--- a/testdata/contracts/catalogue/README.md
+++ b/testdata/contracts/catalogue/README.md
@@ -14,6 +14,12 @@ provider keys or personal data here.
 | `progress-seek.json` | `ProgressEvent` | A seek from 30:30 to 40:00 |
 | `progress-duplicate.json` | `ProgressResult` | A duplicate event: not applied, state unchanged |
 | `watched-request.json` | `WatchedRequest` | Mark watched, expecting revision 7 |
+| `libraries.json` | `LibraryList` | A movie library and an anime library |
+| `items-movies.json` | `ItemPage` | A partly watched movie with a resume position, and a next page |
+| `items-episodes.json` | `ItemPage` | Episodes 1–2 of a season. Both live in one file with unknown boundaries, so neither has a duration; episode 1 was marked watched by hand |
+| `item-movie.json` | `ItemDetail` | A movie with two versions: a 1080p MP4 and a 4K HDR10 Director's Cut with TrueHD Atmos (audio `profile`), an untagged AC-3 track with no profile and forced PGS subtitles |
+| `item-episode-shared-file.json` | `ItemDetail` | Episode 2: its version's file (`fil-synthetic-0003`) also holds episode 1, and `segment` is null because the boundaries are unknown |
+| `item-show.json` | `ItemDetail` | A show: no user state, no duration, no versions |
 
 The `sourceRevision` values are real `contracts.SourceRevision` outputs for the
 synthetic file IDs, sizes and times, e.g. `fil-synthetic-0001`, 24000000000 bytes,
```

**Modify `web/contract-tests/fixtures.test.mjs`:**

```diff
diff --git a/web/contract-tests/fixtures.test.mjs b/web/contract-tests/fixtures.test.mjs
index dc2b054..f7eba8b 100644
--- a/web/contract-tests/fixtures.test.mjs
+++ b/web/contract-tests/fixtures.test.mjs
@@ -113,3 +113,30 @@ checkFixtures(userDataFixtures, [
   ["negative play count", "progress-duplicate.json", (d) => { d.state.playCount = -1; }],
   ["fractional expected revision", "watched-request.json", (d) => { d.expectedRevision = 1.5; }],
 ]);
+
+const catalogueFixtures = {
+  "libraries.json": "LibraryList",
+  "items-movies.json": "ItemPage",
+  "items-episodes.json": "ItemPage",
+  "item-movie.json": "ItemDetail",
+  "item-episode-shared-file.json": "ItemDetail",
+  "item-show.json": "ItemDetail",
+};
+
+checkFixtures(catalogueFixtures, [
+  ["null page items", "libraries.json", (d) => { d.items = null; }],
+  ["unknown library type", "libraries.json", (d) => { d.items[0].type = "music"; }],
+  ["movie with a parent", "items-movies.json", (d) => { d.items[0].parentId = "shw-synthetic-0001"; }],
+  ["episode without a parent", "items-episodes.json", (d) => { d.items[0].parentId = null; }],
+  ["unknown item kind", "items-movies.json", (d) => { d.items[0].kind = "collection"; }],
+  ["show with user state", "item-show.json", (d) => { d.item.userState = catalogue("item-movie.json").item.userState; }],
+  ["show with a duration", "item-show.json", (d) => { d.item.durationMs = 1000; }],
+  ["show with a version", "item-show.json", (d) => { d.versions = catalogue("item-movie.json").versions; }],
+  ["absolute image URL", "items-movies.json", (d) => { d.items[0].primaryImageUrl = "https://example.invalid/poster.jpg"; }],
+  ["lower-case rating region", "items-movies.json", (d) => { d.items[0].contentRating.region = "au"; }],
+  ["non-hex source revision", "item-movie.json", (d) => { d.versions[0].sourceRevision = "rev-synthetic-0001"; }],
+  ["file path on a version", "item-movie.json", (d) => { d.versions[0].path = "/media/movies/synthetic.mkv"; }],
+  ["und language instead of null", "item-movie.json", (d) => { d.versions[1].audio[1].language = "und"; }],
+  ["empty audio profile instead of null", "item-movie.json", (d) => { d.versions[1].audio[1].profile = ""; }],
+  ["unknown HDR format", "item-movie.json", (d) => { d.versions[1].video[0].hdr = "hdr12"; }],
+]);
```

**Modify `web/contract-tests/schema.test.ts`:**

```diff
diff --git a/web/contract-tests/schema.test.ts b/web/contract-tests/schema.test.ts
index 221e0fb..867ba19 100644
--- a/web/contract-tests/schema.test.ts
+++ b/web/contract-tests/schema.test.ts
@@ -8,6 +8,7 @@ type AuthState = components["schemas"]["AuthState"];
 type UserItemState = components["schemas"]["UserItemState"];
 type ProgressEvent = components["schemas"]["ProgressEvent"];
 type ProgressResult = components["schemas"]["ProgressResult"];
+type ItemSummary = components["schemas"]["ItemSummary"];
 
 export const movie = {
   id: "movie-synthetic-0001",
@@ -63,3 +64,22 @@ export const badPosition: ProgressEvent = { ...seek, positionMs: "2400000" };
 
 // @ts-expect-error reject reasons are a closed set
 export const badReason: ProgressResult = { ...duplicate, reason: "late" };
+
+export const episode = {
+  id: "epi-synthetic-0002",
+  kind: "episode",
+  libraryId: "lib-synthetic-anime",
+  parentId: "sea-synthetic-0001",
+  title: "Synthetic Episode Two",
+  sortTitle: "Synthetic Episode Two",
+  year: 2025,
+  index: 2,
+  durationMs: null,
+  contentRating: null,
+  primaryImageUrl: null,
+  backdropUrl: null,
+  userState: freshState,
+} satisfies ItemSummary;
+
+// @ts-expect-error item kinds are a closed set
+export const badKind: ItemSummary = { ...episode, kind: "collection" };
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck
```

Expected: Go FAILs to build (`undefined: LibraryList`, `ItemPage`, `ItemDetail`). `test:contracts`: 56 tests, 21 fail: 6 fixtures and 15 rejections. `typecheck`: `Property 'ItemSummary' does not exist`.

- [ ] **Step 3: Implement**

**Create `server/internal/contracts/catalogue.go`:**

```go
package contracts

import "encoding/json"

// Catalogue wire types (CTR-2). Item, file and version IDs are opaque
// strings: clients never parse them, and they survive renames, provider
// changes and episode-order changes (spec §10.1). Every property is
// required; "unknown" is null, never an invented zero. Producers send empty
// slices, not nil: a nil slice encodes as null, which the contract rejects.

// Library types (Library.type).
const (
	LibraryMovies = "movies"
	LibraryTV     = "tv"
	LibraryAnime  = "anime"
)

// Item kinds (ItemSummary.kind).
const (
	KindMovie   = "movie"
	KindShow    = "show"
	KindSeason  = "season"
	KindEpisode = "episode"
)

// HDR formats (VideoStream.hdr).
const (
	HDRNone        = "sdr"
	HDR10          = "hdr10"
	HDR10Plus      = "hdr10plus"
	HDRHLG         = "hlg"
	HDRDolbyVision = "dolbyVision"
)

// Subtitle kinds (SubtitleStream.kind).
const (
	SubtitleText  = "text"
	SubtitleImage = "image"
	SubtitleASS   = "ass"
)

// Page is one page of a cursor-paginated list. NextCursor is null on the
// last page.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// MarshalJSON encodes nil Items as [] because the contract requires an array.
func (p Page[T]) MarshalJSON() ([]byte, error) {
	items := p.Items
	if items == nil {
		items = []T{}
	}
	return json.Marshal(struct {
		Items      []T     `json:"items"`
		NextCursor *string `json:"nextCursor"`
	}{items, p.NextCursor})
}

type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type LibraryList = Page[Library]

// ContentRating is a certification in one rating region, e.g. AU "PG".
type ContentRating struct {
	Region string `json:"region"` // ISO 3166-1 alpha-2, upper case
	Value  string `json:"value"`
}

type ItemSummary struct {
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	LibraryID       string         `json:"libraryId"`
	ParentID        *string        `json:"parentId"` // season → show, episode → season; null for movies and shows
	Title           string         `json:"title"`    // in the current profile's title language
	SortTitle       string         `json:"sortTitle"`
	Year            *int           `json:"year"`
	Index           *int           `json:"index"` // season or episode number in the library's chosen order
	DurationMs      *int64         `json:"durationMs"`
	ContentRating   *ContentRating `json:"contentRating"`
	PrimaryImageURL *string        `json:"primaryImageUrl"`
	BackdropURL     *string        `json:"backdropUrl"`
	UserState       *UserItemState `json:"userState"` // null for shows and seasons
}

type ItemPage = Page[ItemSummary]

// ExternalID is a provider ID in its namespace, e.g. tmdb "603". The item's
// kind says which of the provider's ID spaces it belongs to.
type ExternalID struct {
	Namespace string `json:"namespace"`
	ID        string `json:"id"`
}

type ItemDetail struct {
	Item          ItemSummary    `json:"item"`
	OriginalTitle *string        `json:"originalTitle"`
	Overview      *string        `json:"overview"`
	Genres        []string       `json:"genres"`
	ExternalIDs   []ExternalID   `json:"externalIds"`
	Versions      []MediaVersion `json:"versions"` // empty for shows and seasons
}

// MediaVersion is one playable version of a movie or episode: a cut or
// quality backed by one file. Several episodes may share a file; Segment
// locates the episode inside it when its boundaries are known.
type MediaVersion struct {
	ID             string           `json:"id"`
	FileID         string           `json:"fileId"`
	Label          *string          `json:"label"`
	DurationMs     *int64           `json:"durationMs"`
	SourceRevision string           `json:"sourceRevision"` // SourceRevision(FileID, size, mtime)
	Segment        *Segment         `json:"segment"`
	Container      string           `json:"container"`
	Video          []VideoStream    `json:"video"`
	Audio          []AudioStream    `json:"audio"`
	Subtitles      []SubtitleStream `json:"subtitles"`
}

// Segment is a range of a file's media timeline, in milliseconds.
type Segment struct {
	StartMs int64 `json:"startMs"`
	EndMs   int64 `json:"endMs"`
}

// Stream indexes are the original ffprobe stream indexes in the file.

type VideoStream struct {
	Index   int    `json:"index"`
	Codec   string `json:"codec"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	HDR     string `json:"hdr"`
	Default bool   `json:"default"`
}

type AudioStream struct {
	Index    int     `json:"index"`
	Codec    string  `json:"codec"`
	Profile  *string `json:"profile"` // e.g. "Dolby TrueHD + Dolby Atmos", "DTS-HD MA"; null when ffprobe reports none
	Channels int     `json:"channels"`
	Language *string `json:"language"` // ISO 639-2 as tagged; null when untagged or "und"
	Title    *string `json:"title"`
	Default  bool    `json:"default"`
	Forced   bool    `json:"forced"`
}

type SubtitleStream struct {
	Index           int     `json:"index"`
	Codec           string  `json:"codec"`
	Kind            string  `json:"kind"`
	Language        *string `json:"language"`
	Title           *string `json:"title"`
	Default         bool    `json:"default"`
	Forced          bool    `json:"forced"`
	HearingImpaired bool    `json:"hearingImpaired"`
}
```

**Modify `docs/contracts/openapi.json`:**

```diff
diff --git a/docs/contracts/openapi.json b/docs/contracts/openapi.json
index 8fa6028..c9d69b6 100644
--- a/docs/contracts/openapi.json
+++ b/docs/contracts/openapi.json
@@ -212,6 +212,54 @@
           "500": { "$ref": "#/components/responses/Error" }
         }
       }
+    },
+    "/api/v1/libraries": {
+      "get": {
+        "operationId": "listLibraries",
+        "x-lisimflix": { "owner": "LIB-2", "status": "planned" },
+        "summary": "Libraries the current profile may see",
+        "parameters": [{ "$ref": "#/components/parameters/Cursor" }, { "$ref": "#/components/parameters/Limit" }],
+        "responses": {
+          "200": { "description": "Libraries", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/LibraryList" } } } },
+          "400": { "$ref": "#/components/responses/Error" },
+          "401": { "$ref": "#/components/responses/Error" },
+          "500": { "$ref": "#/components/responses/Error" }
+        }
+      }
+    },
+    "/api/v1/items": {
+      "get": {
+        "operationId": "listItems",
+        "x-lisimflix": { "owner": "LIB-2", "status": "planned" },
+        "summary": "Items the current profile may see, in stable sort-title order. Pass exactly one of libraryId (the library's movies or shows) or parentId (a show's seasons or a season's episodes, in the chosen order); otherwise 400 invalid_request. A library or parent the profile may not see is 404.",
+        "parameters": [
+          { "$ref": "#/components/parameters/LibraryIdQuery" },
+          { "$ref": "#/components/parameters/ParentIdQuery" },
+          { "$ref": "#/components/parameters/Cursor" },
+          { "$ref": "#/components/parameters/Limit" }
+        ],
+        "responses": {
+          "200": { "description": "Items", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/ItemPage" } } } },
+          "400": { "$ref": "#/components/responses/Error" },
+          "401": { "$ref": "#/components/responses/Error" },
+          "404": { "$ref": "#/components/responses/Error" },
+          "500": { "$ref": "#/components/responses/Error" }
+        }
+      }
+    },
+    "/api/v1/items/{itemId}": {
+      "get": {
+        "operationId": "getItem",
+        "x-lisimflix": { "owner": "LIB-2", "status": "planned" },
+        "summary": "One item with its versions and streams. META-1 fills the descriptive fields and DATA-1 the user state.",
+        "parameters": [{ "$ref": "#/components/parameters/ItemId" }],
+        "responses": {
+          "200": { "description": "Item", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/ItemDetail" } } } },
+          "401": { "$ref": "#/components/responses/Error" },
+          "404": { "$ref": "#/components/responses/Error" },
+          "500": { "$ref": "#/components/responses/Error" }
+        }
+      }
     }
   },
   "components": {
@@ -224,6 +272,10 @@
       "SessionId": { "name": "sessionId", "in": "path", "required": true, "schema": { "type": "string", "minLength": 1 } },
       "Grant": { "name": "grant", "in": "query", "required": true, "description": "Signature bound to playback ID, auth session, account/profile, movie and source revision; not authority without the live session", "schema": { "type": "string", "minLength": 1 } },
       "ItemId": { "name": "itemId", "in": "path", "required": true, "schema": { "type": "string", "minLength": 1 } },
+      "Cursor": { "name": "cursor", "in": "query", "required": false, "description": "nextCursor from the previous page; opaque", "schema": { "type": "string", "minLength": 1, "maxLength": 512 } },
+      "Limit": { "name": "limit", "in": "query", "required": false, "schema": { "type": "integer", "minimum": 1, "maximum": 200, "default": 50 } },
+      "LibraryIdQuery": { "name": "libraryId", "in": "query", "required": false, "schema": { "type": "string", "minLength": 1 } },
+      "ParentIdQuery": { "name": "parentId", "in": "query", "required": false, "schema": { "type": "string", "minLength": 1 } },
       "Range": { "name": "Range", "in": "header", "required": false, "schema": { "type": "string" } },
       "IfRange": { "name": "If-Range", "in": "header", "required": false, "schema": { "type": "string" } },
       "IfNoneMatch": { "name": "If-None-Match", "in": "header", "required": false, "schema": { "type": "string" } },
@@ -412,6 +464,172 @@
           "watched": { "type": "boolean" },
           "expectedRevision": { "type": "integer", "format": "int64", "minimum": 0 }
         }
+      },
+      "Library": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["id", "name", "type"],
+        "properties": {
+          "id": { "type": "string", "minLength": 1 },
+          "name": { "type": "string", "minLength": 1, "maxLength": 200 },
+          "type": { "type": "string", "enum": ["movies", "tv", "anime"] }
+        }
+      },
+      "LibraryList": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["items", "nextCursor"],
+        "properties": {
+          "items": { "type": "array", "items": { "$ref": "#/components/schemas/Library" } },
+          "nextCursor": { "type": ["string", "null"], "minLength": 1 }
+        }
+      },
+      "ContentRating": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["region", "value"],
+        "description": "A certification in one rating region, e.g. AU PG. Ratings from different regions are not comparable.",
+        "properties": {
+          "region": { "type": "string", "pattern": "^[A-Z]{2}$", "description": "ISO 3166-1 alpha-2" },
+          "value": { "type": "string", "minLength": 1, "maxLength": 32 }
+        }
+      },
+      "ItemSummary": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["id", "kind", "libraryId", "parentId", "title", "sortTitle", "year", "index", "durationMs", "contentRating", "primaryImageUrl", "backdropUrl", "userState"],
+        "description": "A logical catalogue item. IDs are opaque and survive renames, provider changes and episode-order changes. title is already in the current profile's title language (spec §3.2). Unknown values are null.",
+        "properties": {
+          "id": { "type": "string", "minLength": 1 },
+          "kind": { "type": "string", "enum": ["movie", "show", "season", "episode"] },
+          "libraryId": { "type": "string", "minLength": 1 },
+          "parentId": { "type": ["string", "null"], "minLength": 1, "description": "Season: its show. Episode: its season. Null for movies and shows." },
+          "title": { "type": "string", "minLength": 1, "maxLength": 500 },
+          "sortTitle": { "type": "string", "minLength": 1, "maxLength": 500 },
+          "year": { "type": ["integer", "null"], "minimum": 1800, "maximum": 9999 },
+          "index": { "type": ["integer", "null"], "minimum": 0, "description": "Season or episode number in the library's chosen order (0 = specials). Null for movies and shows, or when unknown." },
+          "durationMs": { "type": ["integer", "null"], "format": "int64", "minimum": 1, "description": "Default version's duration. Null for shows and seasons, or when unknown." },
+          "contentRating": { "oneOf": [{ "$ref": "#/components/schemas/ContentRating" }, { "type": "null" }] },
+          "primaryImageUrl": { "type": ["string", "null"], "pattern": "^/api/v1/", "description": "Poster, or an episode's still. Same-origin relative URL." },
+          "backdropUrl": { "type": ["string", "null"], "pattern": "^/api/v1/" },
+          "userState": { "oneOf": [{ "$ref": "#/components/schemas/UserItemState" }, { "type": "null" }], "description": "The current profile's state. Null for shows and seasons." }
+        },
+        "allOf": [
+          {
+            "if": { "properties": { "kind": { "enum": ["movie", "show"] } } },
+            "then": { "properties": { "parentId": { "type": "null" } } },
+            "else": { "properties": { "parentId": { "type": "string" } } }
+          },
+          {
+            "if": { "properties": { "kind": { "enum": ["show", "season"] } } },
+            "then": { "properties": { "durationMs": { "type": "null" }, "userState": { "type": "null" } } }
+          }
+        ]
+      },
+      "ItemPage": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["items", "nextCursor"],
+        "properties": {
+          "items": { "type": "array", "items": { "$ref": "#/components/schemas/ItemSummary" } },
+          "nextCursor": { "type": ["string", "null"], "minLength": 1 }
+        }
+      },
+      "ExternalId": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["namespace", "id"],
+        "description": "A provider ID in its namespace, e.g. tmdb 603. The item's kind says which of the provider's ID spaces it belongs to.",
+        "properties": {
+          "namespace": { "type": "string", "pattern": "^[a-z][a-z0-9]{1,31}$" },
+          "id": { "type": "string", "minLength": 1, "maxLength": 64 }
+        }
+      },
+      "ItemDetail": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["item", "originalTitle", "overview", "genres", "externalIds", "versions"],
+        "properties": {
+          "item": { "$ref": "#/components/schemas/ItemSummary" },
+          "originalTitle": { "type": ["string", "null"], "minLength": 1, "maxLength": 500 },
+          "overview": { "type": ["string", "null"], "maxLength": 10000 },
+          "genres": { "type": "array", "items": { "type": "string", "minLength": 1, "maxLength": 64 } },
+          "externalIds": { "type": "array", "items": { "$ref": "#/components/schemas/ExternalId" } },
+          "versions": { "type": "array", "items": { "$ref": "#/components/schemas/MediaVersion" }, "description": "Playable versions. Empty for shows and seasons, and for a movie or episode with no available file." }
+        },
+        "if": { "properties": { "item": { "properties": { "kind": { "enum": ["show", "season"] } } } } },
+        "then": { "properties": { "versions": { "maxItems": 0 } } }
+      },
+      "MediaVersion": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["id", "fileId", "label", "durationMs", "sourceRevision", "segment", "container", "video", "audio", "subtitles"],
+        "description": "One playable version (cut or quality) of a movie or episode, backed by one file. Episodes that share a file have the same fileId; segment locates the episode when its boundaries are known.",
+        "properties": {
+          "id": { "type": "string", "minLength": 1 },
+          "fileId": { "type": "string", "minLength": 1 },
+          "label": { "type": ["string", "null"], "minLength": 1, "maxLength": 100, "description": "e.g. Director's Cut. Null when the item has one unnamed version." },
+          "durationMs": { "type": ["integer", "null"], "format": "int64", "minimum": 1 },
+          "sourceRevision": { "type": "string", "pattern": "^[0-9a-f]{64}$", "description": "Lowercase hex SHA-256 of fileId, size and modification time (contracts.SourceRevision). Changes exactly when the file's size or modification time changes." },
+          "segment": { "oneOf": [{ "$ref": "#/components/schemas/Segment" }, { "type": "null" }] },
+          "container": { "type": "string", "minLength": 1, "description": "ffprobe format name, e.g. mov,mp4,m4a,3gp,3g2,mj2 or matroska,webm" },
+          "video": { "type": "array", "items": { "$ref": "#/components/schemas/VideoStream" } },
+          "audio": { "type": "array", "items": { "$ref": "#/components/schemas/AudioStream" } },
+          "subtitles": { "type": "array", "items": { "$ref": "#/components/schemas/SubtitleStream" } }
+        }
+      },
+      "Segment": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["startMs", "endMs"],
+        "description": "A range of the file's media timeline; endMs is after startMs.",
+        "properties": {
+          "startMs": { "type": "integer", "format": "int64", "minimum": 0 },
+          "endMs": { "type": "integer", "format": "int64", "minimum": 1 }
+        }
+      },
+      "VideoStream": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["index", "codec", "width", "height", "hdr", "default"],
+        "properties": {
+          "index": { "type": "integer", "minimum": 0, "description": "Original stream index in the file" },
+          "codec": { "type": "string", "minLength": 1 },
+          "width": { "type": "integer", "minimum": 1 },
+          "height": { "type": "integer", "minimum": 1 },
+          "hdr": { "type": "string", "enum": ["sdr", "hdr10", "hdr10plus", "hlg", "dolbyVision"] },
+          "default": { "type": "boolean" }
+        }
+      },
+      "AudioStream": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["index", "codec", "profile", "channels", "language", "title", "default", "forced"],
+        "properties": {
+          "index": { "type": "integer", "minimum": 0 },
+          "codec": { "type": "string", "minLength": 1 },
+          "profile": { "type": ["string", "null"], "minLength": 1, "description": "ffprobe profile, e.g. LC, Dolby TrueHD + Dolby Atmos, DTS-HD MA. Null when none is reported." },
+          "channels": { "type": "integer", "minimum": 1 },
+          "language": { "type": ["string", "null"], "pattern": "^(?!und$)[a-z]{3}$", "description": "ISO 639-2 as tagged; null when untagged or und" },
+          "title": { "type": ["string", "null"], "minLength": 1 },
+          "default": { "type": "boolean" },
+          "forced": { "type": "boolean" }
+        }
+      },
+      "SubtitleStream": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["index", "codec", "kind", "language", "title", "default", "forced", "hearingImpaired"],
+        "properties": {
+          "index": { "type": "integer", "minimum": 0 },
+          "codec": { "type": "string", "minLength": 1 },
+          "kind": { "type": "string", "enum": ["text", "image", "ass"] },
+          "language": { "type": ["string", "null"], "pattern": "^(?!und$)[a-z]{3}$" },
+          "title": { "type": ["string", "null"], "minLength": 1 },
+          "default": { "type": "boolean" },
+          "forced": { "type": "boolean" },
+          "hearingImpaired": { "type": "boolean" }
+        }
       }
     }
   }
```

- [ ] **Step 4: Regenerate the types and run the checks**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck
npm --prefix web run lint
```

Expected: Go `ok`; `test:contracts`: 56 tests, 56 pass; typecheck and lint pass.

- [ ] **Step 5: Commit**

```bash
git add docs/contracts/openapi.json server/internal/contracts/catalogue.go server/internal/contracts/catalogue_test.go server/internal/contracts/types_test.go testdata/contracts/catalogue/README.md testdata/contracts/catalogue/item-episode-shared-file.json testdata/contracts/catalogue/item-movie.json testdata/contracts/catalogue/item-show.json testdata/contracts/catalogue/items-episodes.json testdata/contracts/catalogue/items-movies.json testdata/contracts/catalogue/libraries.json web/contract-tests/fixtures.test.mjs web/contract-tests/schema.test.ts web/src/api/schema.d.ts
git commit -m "feat(api): library, item, version and stream contract (CTR-2)"
```

---

### Task 5: Profile and effective-policy contract

**Files:**
- Create: `server/internal/contracts/profiles.go`
- Create: `server/internal/contracts/profiles_test.go`
- Create: `testdata/contracts/catalogue/profiles.json`
- Modify: `docs/contracts/openapi.json`
- Modify: `server/internal/contracts/types_test.go`
- Modify: `testdata/contracts/catalogue/README.md`
- Modify: `web/contract-tests/fixtures.test.mjs`
- Regenerate: `web/src/api/schema.d.ts` (`npm --prefix web run generate:api`; never edit by hand)

**Interfaces:**
- Consumes: Task 4's `Page[T]` and `ContentRating`; Task 3's test helpers.
- Produces: `Profile`, `ProfilePolicy`, `ProfileList`. Operation `listProfiles` (ACC-1, planned).

- [ ] **Step 1: Write the failing tests and fixtures**

**Create `server/internal/contracts/profiles_test.go`:**

```go
package contracts

import "testing"

var profileFixtures = map[string]func() any{
	"profiles.json": func() any { return &ProfileList{} },
}

func TestProfileFixturesRoundTrip(t *testing.T) { roundTripFixtures(t, profileFixtures) }
```

**Create `testdata/contracts/catalogue/profiles.json`:**

```json
{
  "items": [
    {
      "id": "prof-synthetic-0001",
      "name": "Sam",
      "pinRequired": false,
      "policy": { "libraryIds": ["lib-synthetic-movies", "lib-synthetic-anime"], "ratingCeiling": null, "allowUnrated": true }
    },
    {
      "id": "prof-synthetic-0002",
      "name": "Kids",
      "pinRequired": true,
      "policy": { "libraryIds": ["lib-synthetic-anime"], "ratingCeiling": { "region": "AU", "value": "PG" }, "allowUnrated": false }
    }
  ],
  "nextCursor": null
}
```

**Modify `server/internal/contracts/types_test.go`:**

```diff
diff --git a/server/internal/contracts/types_test.go b/server/internal/contracts/types_test.go
index 060f406..21c8547 100644
--- a/server/internal/contracts/types_test.go
+++ b/server/internal/contracts/types_test.go
@@ -180,6 +180,7 @@ func TestOpenAPIMatchesGoTypes(t *testing.T) {
 		"ItemSummary": ItemSummary{}, "ItemPage": ItemPage{}, "ExternalId": ExternalID{}, "ItemDetail": ItemDetail{},
 		"MediaVersion": MediaVersion{}, "Segment": Segment{},
 		"VideoStream": VideoStream{}, "AudioStream": AudioStream{}, "SubtitleStream": SubtitleStream{},
+		"Profile": Profile{}, "ProfilePolicy": ProfilePolicy{}, "ProfileList": ProfileList{},
 	}
 	for name := range doc.Components.Schemas {
 		if _, ok := types[name]; !ok {
```

**Modify `testdata/contracts/catalogue/README.md`:**

```diff
diff --git a/testdata/contracts/catalogue/README.md b/testdata/contracts/catalogue/README.md
index 8ac52bc..b09c256 100644
--- a/testdata/contracts/catalogue/README.md
+++ b/testdata/contracts/catalogue/README.md
@@ -20,6 +20,7 @@ provider keys or personal data here.
 | `item-movie.json` | `ItemDetail` | A movie with two versions: a 1080p MP4 and a 4K HDR10 Director's Cut with TrueHD Atmos (audio `profile`), an untagged AC-3 track with no profile and forced PGS subtitles |
 | `item-episode-shared-file.json` | `ItemDetail` | Episode 2: its version's file (`fil-synthetic-0003`) also holds episode 1, and `segment` is null because the boundaries are unknown |
 | `item-show.json` | `ItemDetail` | A show: no user state, no duration, no versions |
+| `profiles.json` | `ProfileList` | An unrestricted profile, and a PIN-protected kids profile limited to one library, AU PG and no unrated items |
 
 The `sourceRevision` values are real `contracts.SourceRevision` outputs for the
 synthetic file IDs, sizes and times, e.g. `fil-synthetic-0001`, 24000000000 bytes,
```

**Modify `web/contract-tests/fixtures.test.mjs`:**

```diff
diff --git a/web/contract-tests/fixtures.test.mjs b/web/contract-tests/fixtures.test.mjs
index f7eba8b..758b4eb 100644
--- a/web/contract-tests/fixtures.test.mjs
+++ b/web/contract-tests/fixtures.test.mjs
@@ -140,3 +140,10 @@ checkFixtures(catalogueFixtures, [
   ["empty audio profile instead of null", "item-movie.json", (d) => { d.versions[1].audio[1].profile = ""; }],
   ["unknown HDR format", "item-movie.json", (d) => { d.versions[1].video[0].hdr = "hdr12"; }],
 ]);
+
+const profileFixtures = { "profiles.json": "ProfileList" };
+
+checkFixtures(profileFixtures, [
+  ["profile policy without allowUnrated", "profiles.json", (d) => { delete d.items[1].policy.allowUnrated; }],
+  ["null library list in a policy", "profiles.json", (d) => { d.items[1].policy.libraryIds = null; }],
+]);
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck
```

Expected: Go FAILs to build (`undefined: ProfileList`, `Profile`, `ProfilePolicy`). `test:contracts`: 59 tests, 3 fail: the fixture and 2 rejections.

- [ ] **Step 3: Implement**

**Create `server/internal/contracts/profiles.go`:**

```go
package contracts

// Profile wire types (CTR-2, spec §6.3).

// Profile is a viewing identity under the signed-in account.
type Profile struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	PinRequired bool          `json:"pinRequired"`
	Policy      ProfilePolicy `json:"policy"`
}

// ProfilePolicy is a profile's effective policy: the account's ceiling
// narrowed by the profile (spec §6.3). It is for display and fixtures; the
// server enforces policy itself.
type ProfilePolicy struct {
	LibraryIDs    []string       `json:"libraryIds"`
	RatingCeiling *ContentRating `json:"ratingCeiling"` // null: no rating limit
	AllowUnrated  bool           `json:"allowUnrated"`
}

type ProfileList = Page[Profile]
```

**Modify `docs/contracts/openapi.json`:**

```diff
diff --git a/docs/contracts/openapi.json b/docs/contracts/openapi.json
index c9d69b6..5784d1f 100644
--- a/docs/contracts/openapi.json
+++ b/docs/contracts/openapi.json
@@ -260,6 +260,20 @@
           "500": { "$ref": "#/components/responses/Error" }
         }
       }
+    },
+    "/api/v1/profiles": {
+      "get": {
+        "operationId": "listProfiles",
+        "x-lisimflix": { "owner": "ACC-1", "status": "planned" },
+        "summary": "Profiles of the signed-in account",
+        "parameters": [{ "$ref": "#/components/parameters/Cursor" }, { "$ref": "#/components/parameters/Limit" }],
+        "responses": {
+          "200": { "description": "Profiles", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/ProfileList" } } } },
+          "400": { "$ref": "#/components/responses/Error" },
+          "401": { "$ref": "#/components/responses/Error" },
+          "500": { "$ref": "#/components/responses/Error" }
+        }
+      }
     }
   },
   "components": {
@@ -630,6 +644,37 @@
           "forced": { "type": "boolean" },
           "hearingImpaired": { "type": "boolean" }
         }
+      },
+      "Profile": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["id", "name", "pinRequired", "policy"],
+        "properties": {
+          "id": { "type": "string", "minLength": 1 },
+          "name": { "type": "string", "minLength": 1, "maxLength": 64 },
+          "pinRequired": { "type": "boolean" },
+          "policy": { "$ref": "#/components/schemas/ProfilePolicy" }
+        }
+      },
+      "ProfilePolicy": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["libraryIds", "ratingCeiling", "allowUnrated"],
+        "description": "Effective policy: the account's ceiling narrowed by the profile. For display; the server enforces policy on every request.",
+        "properties": {
+          "libraryIds": { "type": "array", "items": { "type": "string", "minLength": 1 }, "description": "Libraries this profile may see. Empty means none." },
+          "ratingCeiling": { "oneOf": [{ "$ref": "#/components/schemas/ContentRating" }, { "type": "null" }], "description": "Highest allowed rating in its region. Null: no rating limit. A rating from another region counts as unrated." },
+          "allowUnrated": { "type": "boolean" }
+        }
+      },
+      "ProfileList": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["items", "nextCursor"],
+        "properties": {
+          "items": { "type": "array", "items": { "$ref": "#/components/schemas/Profile" } },
+          "nextCursor": { "type": ["string", "null"], "minLength": 1 }
+        }
       }
     }
   }
```

- [ ] **Step 4: Regenerate the types and run the checks**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck
npm --prefix web run lint
```

Expected: Go `ok`; `test:contracts`: 59 tests, 59 pass; typecheck and lint pass.

- [ ] **Step 5: Commit**

```bash
git add docs/contracts/openapi.json server/internal/contracts/profiles.go server/internal/contracts/profiles_test.go server/internal/contracts/types_test.go testdata/contracts/catalogue/README.md testdata/contracts/catalogue/profiles.json web/contract-tests/fixtures.test.mjs web/src/api/schema.d.ts
git commit -m "feat(api): profile and effective-policy contract (CTR-2)"
```

---

### Task 6: Metadata provenance contract

**Files:**
- Create: `server/internal/contracts/metadata.go`
- Create: `server/internal/contracts/metadata_test.go`
- Create: `testdata/contracts/catalogue/item-metadata.json`
- Modify: `docs/contracts/openapi.json`
- Modify: `server/internal/contracts/types_test.go`
- Modify: `testdata/contracts/catalogue/README.md`
- Modify: `web/contract-tests/fixtures.test.mjs`
- Regenerate: `web/src/api/schema.d.ts` (`npm --prefix web run generate:api`; never edit by hand)

**Interfaces:**
- Consumes: Task 4's `ContentRating`. The fixture maps `userDataFixtures`, `catalogueFixtures` and `profileFixtures` in Go and JS, for the coverage checks.
- Produces: `ItemMetadata`, `MetadataFields`, `Field[T]` with `TextField`, `IntegerField`, `RatingField` and `TextListField`, plus `Provenance`, `MetadataExternalID` and the `Match*` constants. Operation `getItemMetadata` (META-1, planned). Go and JS checks that every file in `testdata/contracts/catalogue` is tested.

- [ ] **Step 1: Write the failing tests and fixtures**

**Create `server/internal/contracts/metadata_test.go`:**

```go
package contracts

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var metadataFixtures = map[string]func() any{
	"item-metadata.json": func() any { return &ItemMetadata{} },
}

func TestMetadataFixturesRoundTrip(t *testing.T) { roundTripFixtures(t, metadataFixtures) }

func TestMetadataEnumsMatchOpenAPI(t *testing.T) {
	checkEnum(t, "ItemMetadata", "matchStatus", MatchUnmatched, MatchMatched, MatchReview, MatchConflict)
}

// Every file in testdata/contracts/catalogue is round-tripped by one of the
// CTR-2 tests, so a new fixture can't skip the Go check.
func TestEveryCatalogueFixtureIsRoundTripped(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "..", "testdata", "contracts", "catalogue"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			files = append(files, e.Name())
		}
	}
	var tested []string
	for _, m := range []map[string]func() any{userDataFixtures, catalogueFixtures, profileFixtures, metadataFixtures} {
		tested = slices.AppendSeq(tested, maps.Keys(m))
	}
	slices.Sort(files)
	slices.Sort(tested)
	if !slices.Equal(files, tested) {
		t.Fatalf("fixtures %v, round-tripped %v", files, tested)
	}
}
```

**Create `testdata/contracts/catalogue/item-metadata.json`:**

```json
{
  "itemId": "mov-synthetic-0001",
  "revision": 3,
  "matchStatus": "matched",
  "fields": {
    "title": { "value": "Synthetic Test Movie", "source": { "provider": "user", "providerId": null, "fetchedAt": "2026-10-26T12:00:00Z" }, "locked": true },
    "sortTitle": { "value": "Synthetic Test Movie", "source": { "provider": "tmdb", "providerId": "900001", "fetchedAt": "2026-10-25T03:00:00Z" }, "locked": false },
    "originalTitle": { "value": null, "source": null, "locked": false },
    "overview": { "value": "A made-up film used only in contract fixtures.", "source": { "provider": "tmdb", "providerId": "900001", "fetchedAt": "2026-10-25T03:00:00Z" }, "locked": false },
    "year": { "value": 2026, "source": { "provider": "nfo", "providerId": null, "fetchedAt": "2026-10-24T22:00:00Z" }, "locked": false },
    "contentRating": { "value": { "region": "AU", "value": "M" }, "source": { "provider": "tmdb", "providerId": "900001", "fetchedAt": "2026-10-25T03:00:00Z" }, "locked": false },
    "genres": { "value": ["Drama"], "source": { "provider": "tmdb", "providerId": "900001", "fetchedAt": "2026-10-25T03:00:00Z" }, "locked": false }
  },
  "externalIds": [
    { "namespace": "tmdb", "id": "900001", "source": { "provider": "nfo", "providerId": null, "fetchedAt": "2026-10-24T22:00:00Z" }, "locked": true },
    { "namespace": "imdb", "id": "tt9000001", "source": { "provider": "tmdb", "providerId": "900001", "fetchedAt": "2026-10-25T03:00:00Z" }, "locked": false }
  ]
}
```

**Modify `server/internal/contracts/types_test.go`:**

```diff
diff --git a/server/internal/contracts/types_test.go b/server/internal/contracts/types_test.go
index 21c8547..15c4bc1 100644
--- a/server/internal/contracts/types_test.go
+++ b/server/internal/contracts/types_test.go
@@ -181,6 +181,9 @@ func TestOpenAPIMatchesGoTypes(t *testing.T) {
 		"MediaVersion": MediaVersion{}, "Segment": Segment{},
 		"VideoStream": VideoStream{}, "AudioStream": AudioStream{}, "SubtitleStream": SubtitleStream{},
 		"Profile": Profile{}, "ProfilePolicy": ProfilePolicy{}, "ProfileList": ProfileList{},
+		"ItemMetadata": ItemMetadata{}, "MetadataFields": MetadataFields{},
+		"TextField": TextField{}, "IntegerField": IntegerField{}, "RatingField": RatingField{}, "TextListField": TextListField{},
+		"Provenance": Provenance{}, "MetadataExternalId": MetadataExternalID{},
 	}
 	for name := range doc.Components.Schemas {
 		if _, ok := types[name]; !ok {
```

**Modify `testdata/contracts/catalogue/README.md`:**

```diff
diff --git a/testdata/contracts/catalogue/README.md b/testdata/contracts/catalogue/README.md
index b09c256..a5d2804 100644
--- a/testdata/contracts/catalogue/README.md
+++ b/testdata/contracts/catalogue/README.md
@@ -21,6 +21,7 @@ provider keys or personal data here.
 | `item-episode-shared-file.json` | `ItemDetail` | Episode 2: its version's file (`fil-synthetic-0003`) also holds episode 1, and `segment` is null because the boundaries are unknown |
 | `item-show.json` | `ItemDetail` | A show: no user state, no duration, no versions |
 | `profiles.json` | `ProfileList` | An unrestricted profile, and a PIN-protected kids profile limited to one library, AU PG and no unrated items |
+| `item-metadata.json` | `ItemMetadata` | Provenance per field: a locked manual title, TMDB fields, an NFO year and a locked NFO TMDB ID |
 
 The `sourceRevision` values are real `contracts.SourceRevision` outputs for the
 synthetic file IDs, sizes and times, e.g. `fil-synthetic-0001`, 24000000000 bytes,
```

**Modify `web/contract-tests/fixtures.test.mjs`:**

```diff
diff --git a/web/contract-tests/fixtures.test.mjs b/web/contract-tests/fixtures.test.mjs
index 758b4eb..62f4e88 100644
--- a/web/contract-tests/fixtures.test.mjs
+++ b/web/contract-tests/fixtures.test.mjs
@@ -1,5 +1,5 @@
 import assert from "node:assert/strict";
-import { readFileSync } from "node:fs";
+import { readdirSync, readFileSync } from "node:fs";
 import test from "node:test";
 import Ajv2020 from "@redocly/ajv/dist/2020.js";
 
@@ -147,3 +147,17 @@ checkFixtures(profileFixtures, [
   ["profile policy without allowUnrated", "profiles.json", (d) => { delete d.items[1].policy.allowUnrated; }],
   ["null library list in a policy", "profiles.json", (d) => { d.items[1].policy.libraryIds = null; }],
 ]);
+
+const metadataFixtures = { "item-metadata.json": "ItemMetadata" };
+
+checkFixtures(metadataFixtures, [
+  ["metadata revision 0", "item-metadata.json", (d) => { d.revision = 0; }],
+  ["upper-case provider", "item-metadata.json", (d) => { d.fields.title.source.provider = "TMDB"; }],
+  ["field without a lock flag", "item-metadata.json", (d) => { delete d.fields.overview.locked; }],
+]);
+
+test("every catalogue fixture is validated", () => {
+  const files = readdirSync(new URL("../../testdata/contracts/catalogue/", import.meta.url)).filter((f) => f.endsWith(".json"));
+  const tested = Object.keys({ ...userDataFixtures, ...catalogueFixtures, ...profileFixtures, ...metadataFixtures });
+  assert.deepEqual(files.sort(), tested.sort());
+});
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck
```

Expected: Go FAILs to build (`undefined: ItemMetadata`, `MatchUnmatched` …). `test:contracts`: 64 tests, 4 fail: the fixture and 3 rejections.

- [ ] **Step 3: Implement**

**Create `server/internal/contracts/metadata.go`:**

```go
package contracts

import "time"

// Metadata provenance wire types (CTR-2, spec §3.7): where each field came
// from and whether it is locked. Admin-only.

// Match states (ItemMetadata.matchStatus).
const (
	MatchUnmatched = "unmatched"
	MatchMatched   = "matched"
	MatchReview    = "review"
	MatchConflict  = "conflict"
)

// ItemMetadata is an item's editable metadata with provenance. Revision
// starts at 1 and counts committed changes; edits send it back as
// expectedRevision.
type ItemMetadata struct {
	ItemID      string               `json:"itemId"`
	Revision    int64                `json:"revision"`
	MatchStatus string               `json:"matchStatus"`
	Fields      MetadataFields       `json:"fields"`
	ExternalIDs []MetadataExternalID `json:"externalIds"`
}

type MetadataFields struct {
	Title         TextField     `json:"title"`
	SortTitle     TextField     `json:"sortTitle"`
	OriginalTitle TextField     `json:"originalTitle"`
	Overview      TextField     `json:"overview"`
	Year          IntegerField  `json:"year"`
	ContentRating RatingField   `json:"contentRating"`
	Genres        TextListField `json:"genres"`
}

// Field is one metadata value with its provenance. Source is null when the
// value has no recorded origin yet. A locked field keeps its value through
// refreshes and rescans.
type Field[T any] struct {
	Value  T           `json:"value"`
	Source *Provenance `json:"source"`
	Locked bool        `json:"locked"`
}

type (
	TextField     = Field[*string]
	IntegerField  = Field[*int]
	RatingField   = Field[*ContentRating]
	TextListField = Field[[]string]
)

// Provenance records where a value came from: a provider namespace (tmdb,
// tvdb, anilist ...), "nfo" for local NFO files or "user" for manual edits.
type Provenance struct {
	Provider   string    `json:"provider"`
	ProviderID *string   `json:"providerId"`
	FetchedAt  time.Time `json:"fetchedAt"`
}

type MetadataExternalID struct {
	Namespace string      `json:"namespace"`
	ID        string      `json:"id"`
	Source    *Provenance `json:"source"`
	Locked    bool        `json:"locked"`
}
```

**Modify `docs/contracts/openapi.json`:**

```diff
diff --git a/docs/contracts/openapi.json b/docs/contracts/openapi.json
index 5784d1f..92c2637 100644
--- a/docs/contracts/openapi.json
+++ b/docs/contracts/openapi.json
@@ -274,6 +274,20 @@
           "500": { "$ref": "#/components/responses/Error" }
         }
       }
+    },
+    "/api/v1/items/{itemId}/metadata": {
+      "get": {
+        "operationId": "getItemMetadata",
+        "x-lisimflix": { "owner": "META-1", "status": "planned" },
+        "summary": "Admin only: the item's metadata with provenance and locks. Non-admins get 404.",
+        "parameters": [{ "$ref": "#/components/parameters/ItemId" }],
+        "responses": {
+          "200": { "description": "Metadata", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/ItemMetadata" } } } },
+          "401": { "$ref": "#/components/responses/Error" },
+          "404": { "$ref": "#/components/responses/Error" },
+          "500": { "$ref": "#/components/responses/Error" }
+        }
+      }
     }
   },
   "components": {
@@ -675,6 +689,95 @@
           "items": { "type": "array", "items": { "$ref": "#/components/schemas/Profile" } },
           "nextCursor": { "type": ["string", "null"], "minLength": 1 }
         }
+      },
+      "ItemMetadata": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["itemId", "revision", "matchStatus", "fields", "externalIds"],
+        "description": "Admin view of an item's editable metadata with provenance. revision starts at 1 and counts committed changes.",
+        "properties": {
+          "itemId": { "type": "string", "minLength": 1 },
+          "revision": { "type": "integer", "format": "int64", "minimum": 1 },
+          "matchStatus": { "type": "string", "enum": ["unmatched", "matched", "review", "conflict"] },
+          "fields": { "$ref": "#/components/schemas/MetadataFields" },
+          "externalIds": { "type": "array", "items": { "$ref": "#/components/schemas/MetadataExternalId" } }
+        }
+      },
+      "MetadataFields": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["title", "sortTitle", "originalTitle", "overview", "year", "contentRating", "genres"],
+        "properties": {
+          "title": { "$ref": "#/components/schemas/TextField" },
+          "sortTitle": { "$ref": "#/components/schemas/TextField" },
+          "originalTitle": { "$ref": "#/components/schemas/TextField" },
+          "overview": { "$ref": "#/components/schemas/TextField" },
+          "year": { "$ref": "#/components/schemas/IntegerField" },
+          "contentRating": { "$ref": "#/components/schemas/RatingField" },
+          "genres": { "$ref": "#/components/schemas/TextListField" }
+        }
+      },
+      "TextField": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["value", "source", "locked"],
+        "properties": {
+          "value": { "type": ["string", "null"] },
+          "source": { "oneOf": [{ "$ref": "#/components/schemas/Provenance" }, { "type": "null" }] },
+          "locked": { "type": "boolean" }
+        }
+      },
+      "IntegerField": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["value", "source", "locked"],
+        "properties": {
+          "value": { "type": ["integer", "null"] },
+          "source": { "oneOf": [{ "$ref": "#/components/schemas/Provenance" }, { "type": "null" }] },
+          "locked": { "type": "boolean" }
+        }
+      },
+      "RatingField": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["value", "source", "locked"],
+        "properties": {
+          "value": { "oneOf": [{ "$ref": "#/components/schemas/ContentRating" }, { "type": "null" }] },
+          "source": { "oneOf": [{ "$ref": "#/components/schemas/Provenance" }, { "type": "null" }] },
+          "locked": { "type": "boolean" }
+        }
+      },
+      "TextListField": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["value", "source", "locked"],
+        "properties": {
+          "value": { "type": "array", "items": { "type": "string", "minLength": 1 } },
+          "source": { "oneOf": [{ "$ref": "#/components/schemas/Provenance" }, { "type": "null" }] },
+          "locked": { "type": "boolean" }
+        }
+      },
+      "Provenance": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["provider", "providerId", "fetchedAt"],
+        "description": "Where a value came from: a provider namespace (tmdb, tvdb, anilist ...), nfo for local NFO files, or user for manual edits.",
+        "properties": {
+          "provider": { "type": "string", "pattern": "^[a-z][a-z0-9]{1,31}$" },
+          "providerId": { "type": ["string", "null"], "minLength": 1, "maxLength": 64 },
+          "fetchedAt": { "type": "string", "format": "date-time" }
+        }
+      },
+      "MetadataExternalId": {
+        "type": "object",
+        "additionalProperties": false,
+        "required": ["namespace", "id", "source", "locked"],
+        "properties": {
+          "namespace": { "type": "string", "pattern": "^[a-z][a-z0-9]{1,31}$" },
+          "id": { "type": "string", "minLength": 1, "maxLength": 64 },
+          "source": { "oneOf": [{ "$ref": "#/components/schemas/Provenance" }, { "type": "null" }] },
+          "locked": { "type": "boolean" }
+        }
       }
     }
   }
```

- [ ] **Step 4: Regenerate the types and run the checks**

```bash
go -C server test -count=1 ./internal/contracts ./internal/playback
npm --prefix web run generate:api && npm --prefix web run test:contracts && npm --prefix web run typecheck
npm --prefix web run lint
```

Expected: Go `ok`; `test:contracts`: 64 tests, 64 pass; typecheck and lint pass.

- [ ] **Step 5: Commit**

```bash
git add docs/contracts/openapi.json server/internal/contracts/metadata.go server/internal/contracts/metadata_test.go server/internal/contracts/types_test.go testdata/contracts/catalogue/README.md testdata/contracts/catalogue/item-metadata.json web/contract-tests/fixtures.test.mjs web/src/api/schema.d.ts
git commit -m "feat(api): metadata provenance contract (CTR-2)"
```

---

### Task 7: Full check and PR

- [ ] **Step 1: Run the full local check**

```bash
npm --prefix web ci
make check
```

Expected:
- PASS, with 64 contract tests in `test:contracts`.
- Every Go package `ok` under `-race`.
- No diff in `web/src/api/schema.d.ts`.

- [ ] **Step 2: Check the README gate**

`README.md` now names `docs/contracts/openapi.json` (Task 2); nothing else in it changes.

- [ ] **Step 3: Push and open the PR**

```bash
git push -u origin feat/ctr-2-contracts
gh pr create --title "feat(api): catalogue and identity contracts (CTR-2)" --body-file <filled PR template>
```

- **Body:**
  - Source: CTR-2, spec §3.8/§6.3/§10/§12, `Closes #76`.
  - Fill the template's local and Actions results separately.
- **Reviewers:** request lisim and ondona as two separate API calls.
- **On merge:** record "module accepted" on #76.
- **Each producer later:** flips its operation's status to `implemented` in its own PR.

## Self-review

**Spec coverage:**

| Requirement | Where |
|---|---|
| §10.1 stable opaque IDs; items, versions, files and streams kept separate; streams keep their original index | Task 4 (`ItemSummary`, `MediaVersion`, stream `index`) |
| §3.7 local show ≠ provider series ≠ order ≠ episode ≠ file; one file can hold several episodes | Task 4 (`fileId`, `segment`, shared-file fixture); provider series and order edges are META-2 (decision 5) |
| §3.6/LIB-1 a size or mtime change invalidates; jobs carry the revision | Task 1 (R1, pinned), decision 3 |
| §3.8 progress belongs to profile + logical item and keeps version and revision; session, sequence and revision reject stale events; manual beats automatic; seek events | Task 3 (`Resume`, `ProgressEvent`, `ProgressResult`, `Watched.origin`) |
| §10.3 explicit units, watched separate from resume, unknown stays null, origin and time kept | Task 3 |
| §6.3 effective policy, region-scoped ratings, unrated handling, 404 for forbidden | Task 5, decision 8 |
| §3.7 provenance: provider, provider ID, fetch time and lock per field | Task 6 |
| §12 cursors (50/200), RFC 3339, `Ms` units, revision preconditions, idempotency left unchanged, error codes | Tasks 2–6 |
| §8.2 producer/consumer tests; Go and TypeScript agree with OpenAPI | Every task: Go round-trip, field and enum agreement, Ajv, TypeScript |
| §8.3 the shared contract hotspot changes in one small PR, additively | This plan: M1 shapes unchanged, rename only |

**Verified while writing this plan:**
- **Prototype:** the plan's code ran as a prototype, and `make check` passed: every Go package under `-race`, and 64 web contract tests.
- **Rebuild:** each task was rebuilt from `main` in order. Each failed with the Step 2 messages before its implementation and passed after. The rebuilt contract equals the prototype's, compared as JSON.
- **Mutation check:** removing the conditional rules (parent by kind, null duration and user state for containers, no versions on containers, `fromPositionMs` only for seeks, a reason exactly when rejected) and the `und` exclusion made exactly the 10 matching rejection tests fail. Changing the `SourceRevision` separator made the pinned test fail.
- **Placeholders:** none.
- **Name and type consistency:** `TestOpenAPIMatchesGoTypes` (every schema has a Go type with the same fields) and the enum checks enforce it.

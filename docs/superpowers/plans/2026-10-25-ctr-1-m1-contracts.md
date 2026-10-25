# CTR-1: M1 Contracts and Workspace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** Approved by Pete 2026-10-25. Implemented on `feat/ctr-1-m1-contracts` (issue #7); local `make check` passes. Awaiting second-member review.
**Goal:** Give independently claimed runtime, delivery and player work one small, testable contract and build workspace.
**Architecture:** OpenAPI defines the public wire shapes; matching Go types and generated TypeScript types share synthetic fixtures. The seed builds without a server, database, container or playback implementation.
**Tech Stack:** Go 1.27.3; Node 24.22.1; React/TypeScript/Vite; OpenAPI 3.1.
**Spec:** [Design §§8, 9.3–9.5, 12](../specs/2026-10-24-lisimflix-design.md).

## Global constraints

- Initial target: **Linux x86-64/amd64 containers**. Browser targets: **Chrome and Firefox on Linux, Windows and macOS**.
- M1 is one complete 1080p SDR H.264 MP4, with original encoded video/audio and a read-only source. No remux, encoding, subtitles or persistent progress is required for M1.
- Local HTTP and local username/password access work without a domain, proxy or external provider. Remote access requires HTTPS.
- Contributors self-claim features; this document assigns nobody. Claim CTR-1 before changing shared manifests/schema.
- Module acceptance is distinct from integrated MOVIE-01–04 evidence. Test fakes never become production authentication/source adapters.

## M1 plan map and dependencies

| Plan | Feature slice | Start / module acceptance | Integration dependency |
|---|---|---|---|
| This file | CTR-1; initial OPS-3 check seed | Can start now; contracts compile and fixtures validate | Consumer review from another contributor |
| [Runtime](2026-10-25-ops-1-m1-runtime.md) | OPS-1/ACC-1 local runtime slice | CTR-1; isolated HTTP/config/auth tests | Real delivery handler and built player |
| [Direct playback](2026-10-25-ply-2-m1-direct-play.md) | PLY-2 plus minimal PLY-1/PLY-4 | CTR-1; real file/range/session tests | Real authentication and player |
| [Browser player](2026-10-25-ui-1-m1-player.md) | UI-1 | CTR-1; browser harness and real short test clip | Real authenticated server/delivery |
| [Integration](2026-10-25-m1-integration.md) | MOVIE-01–04, initial OPS-4 | Evidence template/fixture preparation can start now | Accepted runtime, playback and player implementations |

These are independently claimable slices, not fixed teams or all of their feature IDs' final scope. Runtime, playback and UI proceed concurrently after this small seed. A contributor can choose other ready catalogue work too. Existing three layouts, themes and later features remain in the spec; M1's basic player does not implement the polished home screen.

## Review focus

1. No source paths, hashes of passwords, cookies or grants appear in public movie/user/error schemas.
2. Field names, milliseconds/seconds and account/profile/session IDs agree across all three consumers.
3. Source replacement and duplicate play requests have explicit revision/idempotency behaviour.
4. A signed media URL stays usable throughout a full movie via the heartbeat lifecycle, while logout/expiry still revoke access.
5. The seed has genuine checks and a real frontend build; it must not claim that a placeholder UI proves playback.

## Shared decisions for the M1 slice

These are implementation proposals for review, not additional product promises:

- M1 uses one owner-provisioned **non-admin viewer account** and one explicitly configured movie. Its durable account/profile/movie IDs live in `/config/m1.json`; later SQLite work preserves them. The normal admin wizard, admin TOTP and user management remain wider G1 work. This narrow proof must not be advertised as the completed first-release setup UX.
- Media is delivered as the original file with `http.ServeContent`, opened beneath an `os.Root`. No custom MP4 parser, streaming framework or encoder is needed.
- The runtime owns authentication and CSRF. Playback consumes an authenticated `Principal`; it never implements a second login system.
- M1 playback sessions/grants are in memory and expire after **60 seconds** without a heartbeat; the player heartbeats every **10 seconds**. Restart interrupts sessions. No watch-progress database is introduced for M1.
- Media URL signatures are bound to the playback ID, authenticated session, account/profile, movie and source revision. The URL remains stable while server-side heartbeat validity is renewed. Expiry is checked on every new request; a nonce/HMAC alone is not authority without the live user/session. This avoids a five-minute URL expiring halfway through a long movie.

## Contract to implement

Create `docs/contracts/m1-openapi.json` with these operations. All `/api/v1` responses are private/no-store, errors have `{code,message,requestId}`, and unsafe browser operations require `X-CSRF-Token`. Login/logout/session behaviour belongs to the runtime plan; media/session behaviour belongs to playback.

| Method / path | Request | Successful response |
|---|---|---|
| GET `/api/v1/auth/session` | Session cookie if present | 200 `AuthState`; anonymous callers get `authenticated:false`, `viewer:null` and a CSRF token |
| POST `/api/v1/auth/login` | `{username,password}` + CSRF | 200 `AuthState`; rotated session cookie and fresh CSRF token |
| POST `/api/v1/auth/logout` | CSRF + session cookie | 204; cookie removed, auth and child playback sessions revoked |
| GET `/api/v1/movies` | Authenticated session | 200 `{items:Movie[],nextCursor:null}`; only the permitted configured movie |
| POST `/api/v1/playback/sessions` | `{movieId}` + CSRF + `Idempotency-Key` | 201 `PlaybackSession`; replay of the same active request returns 200, changed payload for same key is 409 |
| POST `/api/v1/playback/sessions/{id}/heartbeat` | `{sequence}` + CSRF | 204 for live owning session; expired sessions return 410 and are not revived |
| DELETE `/api/v1/playback/sessions/{id}` | CSRF | 204; idempotent stop for the owner |
| GET/HEAD `/api/v1/playback/sessions/{id}/content?grant=…` | User cookie + signed URL; optional Range | Original MP4 bytes; 200/206/304/416 as applicable, never a transformed file |

The table has eight operation groups; GET and HEAD content are distinct OpenAPI operations. All object schemas disallow unexpected input properties. Use 400 invalid input, 401 unauthenticated, 404 unknown/non-owned media/session, 409 changed source/idempotency conflict, 410 own expired playback, 429 rate/capacity exceeded, 503 source unavailable. Reuse one stable code enum: `invalid_request`, `unauthenticated`, `not_found`, `source_changed`, `playback_expired`, `capacity_unavailable`, `rate_limited`, `source_unavailable`, `internal_error`. CSRF rejection uses 403 `csrf_failed`. Do not echo paths, secrets or ffmpeg commands.

| Public type | Exact fields / units |
|---|---|
| `Viewer` | `accountId`, `profileId`, `username`: strings |
| `AuthState` | `authenticated`: boolean; `viewer`: Viewer or null; `csrfToken`: string |
| `Movie` | `id`, `sourceRevision`, `title`: strings; `durationMs`: positive int64; `container:"mp4"`, `videoCodec:"h264"`; `width:1920`, `height:1080`; `audioCodec`: nonempty string |
| `PlaybackSession` | `id`, `movieId`, `sourceRevision`, `contentUrl`: strings; `route:"direct"`; `heartbeatIntervalSeconds:10`, `idleTimeoutSeconds:60` |
| `APIError` | `code`, `message`, `requestId`: strings; safe bounded text |

The test movie's audio codec is qualified on all six targets before M1 sign-off. AAC stereo is a candidate fixture, not an automatic guarantee. Public URLs must be same-origin relative paths. Internal file paths never enter these objects.

Create `server/internal/contracts/types.go`, `context.go`, `errors.go` and tests. Module path: `github.com/Lisimflix/lisimflix/server`. Export Go equivalents of the public types, with JSON tags exactly as above, plus:

```go
type Principal struct { AccountID, ProfileID, AuthSessionID string }
func WithPrincipal(context.Context, Principal) context.Context
func PrincipalFrom(context.Context) (Principal, bool)
func WriteError(http.ResponseWriter, *http.Request, int, string, string)
```

`WriteError` supplies a non-secret request ID if no middleware ID exists, emits JSON once and never serialises arbitrary errors. Public response types are separate from internal principals/source configuration. Integer `sequence` is nonnegative and bounded to int64.

## Task 1: Pin the workspace and share contract fixtures

**Files:** Create `server/go.mod`, `server/internal/contracts/{types,context,errors}.go`, `server/internal/contracts/types_test.go`, `docs/contracts/m1-openapi.json`, `testdata/contracts/m1/{anonymous,authenticated,movies,playback,error}.json`, `testdata/contracts/m1/README.md`.
**Consumes:** The operation/type tables above and the agreed M1 spec.
**Produces:** The Go types/functions above and one versioned public API contract, with synthetic IDs and no real media.

- [x] Write `TestPublicFixturesRoundTrip`, `TestPrincipalAbsentIsUnauthenticated` and `TestErrorResponseDoesNotExposeSourcePath`. Fixtures assert `durationMs` is an integer, null anonymous viewer is retained, and private principal/config fields never appear in JSON. Define test-local `loadFixture(t,name)` using `../../../testdata/contracts/m1/` from the package test directory.
- [x] Run `go -C server test ./internal/contracts`; expect missing module/types or failing assertions before implementation.
- [x] Add module `go 1.27.0` with `toolchain go1.27.3`, the OpenAPI contract and minimal Go definitions/helpers. No server, database, auth implementation or future-module placeholders.
- [x] Run `go -C server test ./internal/contracts` and `go -C server vet ./internal/contracts`; expect passing fixture/context/error tests.
- [x] Commit this slice locally as `feat(api): define M1 playback contract` with its fixture assertions. Keep it on the bootstrap branch; do not publish a contracts-only code PR before the common checks exist.

## Task 2: Seed a checkable frontend and common checks

**Files:** Create `web/package.json`, `web/package-lock.json`, `web/tsconfig.json`, `web/eslint.config.mjs`, `web/index.html`, `web/src/main.tsx`, `web/src/api/schema.d.ts`, `web/contract-tests/schema.test.ts`, `.node-version`, `.github/workflows/check.yml`, `Makefile`; update `.gitignore` only for build/dependency/private-fixture outputs.
**Consumes:** OpenAPI and fixture JSON from Task 1.
**Produces:** `npm --prefix web run generate:api`, `lint`, `typecheck`, `build`; a genuine `make check` and PR check workflow.

- [x] Add compile assertions using `components["schemas"]["Movie"]` and `PlaybackSession` from generated types. Valid synthetic literals use `satisfies`; deliberately wrong duration/route examples use `@ts-expect-error` so weakening the schema fails the check. Before generation, `npm --prefix web run typecheck` must fail on absent types.
- [x] Pin Node `24.22.1`. Runtime deps: `react`/`react-dom` `19.3.1`. Exact dev pins: `typescript` `5.9.3`, `vite` `8.3.5`, `eslint` `10.13.0` (9.39.1 was reported unsupported by npm at install; typescript-eslint 8.72.0 supports ESLint 10), `typescript-eslint` `8.72.0`, `openapi-typescript` `7.13.0`, `@playwright/test` `1.64.1`, `@types/react`/`@types/react-dom` `19.3.0`, `@types/node` `24.10.1`. Generate and commit the lockfile. TypeScript 5 is intentional: the generator's published peer range is `^5.x`; the current TypeScript 7 latest tag is unsuitable for this pinned set.
- [x] Set `generate:api` to `openapi-typescript ../docs/contracts/m1-openapi.json -o src/api/schema.d.ts`, `lint` to `eslint src contract-tests`, `typecheck` to `tsc --noEmit`, `build` to `vite build`, `dev` to `vite --host 127.0.0.1`, and `test:browser` to `playwright test`. Use strict TS, DOM + ES2022 libraries, JSX `react-jsx`, JSON imports, bundler resolution and no emitted compiler files. ESLint uses typescript-eslint recommended rules; generated declarations are excluded. The tiny `main.tsx` renders an explicit “M1 player not connected” heading, with no fake successful playback.
- [x] Make `make check` run Go formatting verification, `go -C server vet ./...`, `go -C server test ./...`, API generation with a generated-file diff check once tracked, and frontend lint/typecheck/build. CI uses Linux, pinned Go/Node, dependency caches, `npm ci`, per-PR concurrency cancellation and this same command. No browser/GPU test job in hosted CI. After tracking the generated file, install locked dependencies reproducibly with `npm ci` and run the complete `make check` on this branch. Fix any failure locally before publishing. Require the normal second-member review and an actual Actions pass for this bootstrap PR too: a new `pull_request` workflow can run from the PR merge branch before reaching the default branch. Confirm a deliberately broken fixture/type fails the command before restoring it.
- [x] Commit as `chore(ci): add M1 workspace checks`. Generated declarations/manifests now become shared hotspots: runtime has the next exclusive Go-dependency edit; UI owns `web/src/main.tsx` after the seed; integration owns later root build/deploy wiring. Changes outside those scopes require coordination.

## Completion and handoff

### Approved PR review fixes (2026-10-25)

Scope: CTR-1 fixture validation and CI documentation; approved during PR #8 review.
Reuse the installed `@redocly/ajv` 8.11.2 validator as an exact direct development
dependency. No runtime dependencies or API changes.

- [x] Add `web/contract-tests/fixtures.test.mjs`: validate all five canonical JSON fixtures against the OpenAPI 3.1 schemas with Ajv's JSON Schema 2020-12 implementation; assert rejection of invalid route, content URL, heartbeat, duration, required fields and unexpected fields.
- [x] Add `test:contracts` to `web/package.json` and `make check`; document it in the fixture README. Verify the previously accepted bad playback fixture now fails the full check, then restore it and run the full check after `npm ci`.
- [x] Correct `CONTRIBUTING.md`, `CLAUDE.md`, the PR template, design §§8/13.5 and this plan: new `pull_request` workflows can run from the PR merge branch. Require local checks, actual Actions results and second-member review. Update PR/issue status with the actual Actions result when publishing.

Verification: reproducible `npm ci` and local `make check` pass, including 13
contract tests. Before the fix, `make check` accepted a transcoded-route fixture;
afterward, isolated full-check runs reject a transcoded route, external content URL
and wrong heartbeat independently (exit 2). Independent review found no issues.

CTR-1's M1 slice is module accepted when fixtures/types agree, local `make check` passes before publishing, the same command passes in GitHub Actions (including this bootstrap PR), and a producer/consumer review accepts the wire contract. It does not prove a running server or complete CTR-1's later subtitle/capability contracts. Record the local command/result and actual Actions result separately in the PR; then mark runtime, playback and UI plans ready for self-claim after merge. Never label M1 complete here.

Version pins were checked against official [Go downloads](https://go.dev/dl/), [Node releases](https://nodejs.org/dist/index.json), npm package metadata and Go module metadata on 2026-10-25. Recheck security fixes before implementation. Generation uses the documented [OpenAPI TypeScript CLI](https://openapi-ts.dev/cli). This plan installed nothing.

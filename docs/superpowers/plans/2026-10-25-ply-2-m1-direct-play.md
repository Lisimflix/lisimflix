# PLY-2: M1 Direct Playback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** Approved by @lisim on #20 (2026-10-26) with the changes in [Approved changes](#approved-changes-20), which are written into this plan. Claimed by @Lammy93. Not implemented.
**Goal:** Serve an authorised original MP4 through real HTTP ranges and a bounded playback-session lifecycle.
**Architecture:** A standalone Go handler consumes the shared authenticated principal and one configured source. It uses rooted file access and stdlib HTTP delivery; test-only principals let its package tests run before runtime/UI integration.
**Tech Stack:** Go 1.27.3 standard library; no ffmpeg, database or new dependencies.
**Spec:** [Design §§4, 6.5, 9.3](../specs/2026-10-24-lisimflix-design.md); [shared contract](2026-10-25-ctr-1-m1-contracts.md).

## Global constraints

- M1: complete 1080p SDR H.264 MP4, original encoded video/audio; source mounted read-only.
- Targets: Chrome/Firefox on Linux, Windows and macOS; the real six-target proof belongs to integration.
- Local HTTP supports real authentication; a signed URL alone is insufficient authority.
- Claim this feature slice and `server/internal/playback/**`; no manifest, auth, UI or root wiring edits.
- Start/module dependency: CTR-1 only. Integration needs runtime authentication and the real player, not a completed application skeleton.

## Review focus

1. Traversal, symlink escapes, wrong users and copied grants cannot expose files.
2. Range/HEAD/conditional delivery returns correct original bytes without reading the whole file into RAM.
3. Source replacement never silently changes the source of an existing playback session.
4. Heartbeats keep a complete movie playable; expiry/logout/stop revoke subsequent and active delivery.
5. Duplicate starts, abandoned sessions and invalid requests cannot grow unbounded work.

## Interfaces and scope

Create `server/internal/playback/source.go`, `sessions.go`, `http.go` and their `_test.go` files. Export:

```go
type Source struct { Movie contracts.Movie; RelativePath string }
type Options struct {
    Source Source
    GrantKey []byte
    MaxSessions int
    Authorize func(contracts.Principal, string) bool
}
type Server struct { /* private state */ }
func New(root *os.Root, opts Options) (*Server, error)
func (s *Server) Handler() http.Handler
func (s *Server) RevokeAuthSession(authSessionID string)
func (s *Server) Check() error // source present and unchanged; the integration's readiness uses it
func (s *Server) Close() error // ends sessions and deliveries, stops the sweeper; safe to repeat
func Revision(movieID string, info fs.FileInfo) string
```

The caller owns the root and closes it after `Server.Close`. `Handler` owns GET movies and all playback operations in CTR-1, with full paths intact. Runtime supplies `Principal`; absence fails closed. `Authorize` is mandatory and called for listing/start/content. Production composition grants only the configured account/profile/movie. Fake principals/authorizers exist only in `_test.go`.

`Revision` is the lowercase hex SHA-256 of `movieID 0x00 size 0x00 mtimeUnixNano`, numbers in decimal, so `init`, startup and playback agree byte for byte (64 characters). At preparation/open compare the configured revision and current file metadata, including file identity for already prepared sessions. Return `source_changed` on replacement; never fall back to another file. Record the limit: this controlled M1 fixture is not protection against a hostile host rewriting bytes while preserving all metadata. Final before/after checksums still prove MOVIE-04.

Store sessions in a mutex-protected bounded map. Use random opaque IDs and standard HMAC-SHA256 for grants, bound to source and authenticated session/profile. No custom crypto. Ten-second heartbeats renew a 60-second idle deadline without changing the content URL. Retain at most 64 five-minute tombstones/idempotency records; the explicit `MaxSessions` limits active sessions server-wide, and a new start over it is 429 `capacity_unavailable` (a replay of a live session's key doesn't count again). Idempotency keys are scoped to the auth session: a replay never returns another principal's session or one from before a logout. Changed payload under an existing key is 409 `idempotency_conflict`; so is the key of a session that has ended, while its tombstone is held. After the tombstone expires the key starts a new session. Expired sessions cannot be revived. A heartbeat with the last accepted `sequence` is a 204 no-op that does not renew the idle deadline; a lower one is 400 `invalid_request`. A background sweeper, started by `New` and stopped by `Close`, ends idle sessions once a second, so a delivery stuck on a stalled client is cut off within a second of expiry, not only when its next write is attempted. Stop/revoke cancels associated deliveries and closes their files; use an interruptible copy/write deadline so a stalled client cannot prevent revocation. Do not set an overall streaming timeout shorter than a movie.

## Task 1: Confined original-byte source delivery

**Consumes:** `contracts.Movie`, `PrincipalFrom` and error responses. **Produces:** `Source`, `Revision`, initial `Server`/handler source validation.

- [ ] Write `TestRangeReturnsExactSourceBytes`, `TestHeadHasNoBody`, `TestUnsatisfiableRange`, `TestPrivatePathAndSymlinkEscapeDenied`, `TestSourceReplacementRejected`, `TestSameMetadataDifferentFileRejected`, `TestMissingSourceIsUnavailable` and `TestRevisionIsStable` (checks the exact layout) using temporary files and `httptest`. Byte fixtures need not be decodable movies; these tests prove delivery semantics, not browser playback.
- [ ] Run `go -C server test ./internal/playback -run 'Test(Range|Head|Unsatisfiable|Private|Source|Missing)'`; expect missing implementation/failing tests.
- [ ] Implement rooted opens using `os.Root.Open`, validate relative paths, stat/revision and configured source, close every file, and use `http.ServeContent` with `Cache-Control: no-store` (as CTR-1 requires for all `/api/v1` responses), `ETag` set to the quoted revision and `Last-Modified`, so `Range`/`If-Range`/`If-None-Match` still give 206/304/416. Never accept a path/URL in a play request. Preserve standard content/range headers; application failures use safe JSON, protocol-level range failures expose no paths.
- [ ] Repeat the command and `go -C server vet ./internal/playback`; expect exact bytes/statuses and all negative cases passing.
- [ ] Commit as `feat(playback): deliver original MP4 ranges safely`.

## Task 2: Bound access to real playback sessions

**Consumes:** source delivery, real injected principal and policy function. **Produces:** full CTR-1 movies/start/heartbeat/stop/content handler and revocation hook.

- [ ] Write `TestSessionNeedsPrincipalAndPolicy`, `TestGrantCannotCrossAccountOrSession`, `TestIdempotentStartAndCapacity`, `TestHeartbeatKeepsLongSessionAlive`, `TestExpiredSessionCannotRevive`, `TestStopAndLogoutAbortDelivery` (stop, logout, `Close` and expiry, each revoking only after the server's write has stalled), `TestIdempotencyKeyIsScopedToAuthSession`, `TestTombstonesAreBounded`, `TestNewValidatesOptions`. Use a package-private clock seam; advance beyond repeated 60-second periods without a multi-hour sleep.
- [ ] Run `go -C server test ./internal/playback`; expect lifecycle cases to fail before implementation.
- [ ] Implement the shared routes and bounded store. Validate IDs, JSON size/unknown fields, idempotency keys and grant signatures. Keep file opens streaming; revoke ownership immediately. Log paths of API operations only, never query grants/source paths. Recheck authorisation, active session and revision on every content/range/HEAD request.
- [ ] Run `go -C server test -race ./internal/playback` and package vet; prove no data races, leaked active deliveries or unbounded expired entries. Do not count fake-auth package tests as integrated access-control proof.
- [ ] Commit as `feat(playback): bind direct streams to live viewer sessions`.

## Completion

Record passing module commands and publish the exact constructor/revocation signatures. This accepts only the M1 PLY slice; it does not complete transcoding/remux, full capability selection or wider PLY-4 behaviour. Integration must still connect real auth/UI and run MOVIE-01–04. Primary APIs: [os.Root](https://pkg.go.dev/os#Root), [http.ServeContent](https://pkg.go.dev/net/http#ServeContent).

## Approved changes (#20)

Points 1–5 were proposed on #20 and approved by @lisim with P1–P3; point 6 came from the prototype afterwards. All are written into the sections above; this list is the record.

| # | Change |
|---|---|
| 1 | A key whose session has ended returns 409 `idempotency_conflict` while its tombstone is held (the contract has no 410 on start); after 5 minutes the key starts a new session |
| 2, P2 | Heartbeat `sequence`: equal to the last accepted is a 204 no-op that does **not** renew; lower is 400 `invalid_request`; each higher one renews the 60 s idle deadline |
| 3 | Exact `Revision` layout, 64 lowercase hex characters |
| 4 | Content uses `Cache-Control: no-store`, with `ETag`/`Last-Modified` and conditional/range handling intact |
| 5 | `MaxSessions` is server-wide; over it, a new start is 429 `capacity_unavailable`; a live replay doesn't count |
| 6 | A once-a-second sweeper ends idle sessions and cuts their deliveries (write deadline set to now, then cancel); without it a stalled client on an expired session held its delivery until the 30 s per-write stall deadline |
| P1 | Idempotency keys are scoped to the auth session. Test `TestIdempotencyKeyIsScopedToAuthSession` |
| P3 | Contract follow-up: CTR-1's `idempotency_conflict` wording is widened in a separate docs change so UI-1 treats this 409 as "start again with a new key" |

Implementation notes from the prototype, no plan change: the delivery writer deliberately doesn't implement `io.ReaderFrom`, so every chunk passes the liveness check rather than `sendfile`; each delivery clears its write deadline when it ends, so a kept-alive connection doesn't carry a stale one; `Check()` lets the integration's readiness report a missing or changed source.

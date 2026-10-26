# OPS-1 / ACC-1: M1 Local Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** Approved by @lisim on #18 (2026-10-26) with the changes in [Approved changes](#approved-changes-18), which are written into this plan. Claimed by @Lammy93. Not implemented. Module acceptance only: BASE-01/02/04 close through the M1 integration (#19).
**Goal:** Supply real local username/password authentication, validated source configuration and a composable HTTP runtime for the M1 movie proof.
**Architecture:** A small file-backed M1 viewer configuration and in-memory revocable auth sessions wrap an injected delivery handler and frontend filesystem. This module runs in `httptest` before the container/player exists. The M1 account is non-admin; normal admin setup/TOTP/user management and SQLite remain wider G1 work.
**Tech Stack:** Go 1.27.3, stdlib HTTP/JSON/crypto, Argon2id via `golang.org/x/crypto`, maintained `gorilla/csrf` middleware.
**Spec:** [Design §§6, 9.3, 10.3, 11.1](../specs/2026-10-24-lisimflix-design.md); [shared M1 contract](2026-10-25-ctr-1-m1-contracts.md).

## Global constraints

- Target **Linux x86-64/amd64 containers** with local HTTP, no domain/proxy/provider requirement. Remote access requires HTTPS.
- M1's authorised viewer can read only its configured movie; no administrative privileges, auth bypass or production fake adapter.
- Media remains read-only. Configuration/secrets remain in `/config`, with source paths absent from public responses/logs.
- Preserve stable account/profile/movie IDs and explicit millisecond durations for later SQLite/import compatibility.
- Start/module dependency: merged CTR-1 seed. Integration dependency: real playback handler and built UI, not their whole parent feature sets.

## Review focus

1. Invalid/weak or hostile hash parameters cannot bypass login or exhaust resources.
2. Private-LAN IP use works with HTTP cookies and CSRF; spoofed proxy/origin headers do not turn it into an insecure public mode.
3. Logout/expiry revoke the owning playback sessions through an explicit hook.
4. Invalid config, permissions and missing roots fail readiness without echoing secrets.
5. The M1 CLI account/config is not misrepresented as finished admin onboarding or the complete G1 database layer.

## Ownership and interfaces

Own `server/internal/m1config/**`, `server/internal/auth/**`, `server/internal/app/**`. After coordinating the shared-file claim, add only these exact direct Go dependencies to `server/go.mod`/`go.sum`, on top of OPS-2's (#17): `golang.org/x/crypto v0.58.0` and `github.com/gorilla/csrf v1.7.3`. `golang.org/x/term` belongs to the integration plan, whose `init` prompt is its only user. Playback uses stdlib and must not edit the manifests concurrently. No frontend, root router command, Dockerfile or CI edits in this plan.

```go
// m1config: only its persisted public metadata becomes contracts.Movie.
type Viewer struct { AccountID, ProfileID, Username, PasswordHash string }
type Config struct {
    ListenAddr, PublicOrigin, MediaRoot string
    MovieID, RelativePath, Title, SourceRevision string
    VideoCodec, AudioCodec string // VideoCodec is "h264" in M1; both come from the probe
    Width, Height int              // within 1920x1080, from the probe
    DurationMS int64
    Viewer Viewer
    CSRFKey []byte                 // 32 bytes, base64 in JSON
    MaxPlaybackSessions int
}
func Load(path string) (Config, error)
func SaveNew(path string, cfg Config) error
func (c Config) Movie() contracts.Movie // the only public view; Container is fixed "mp4"

// auth
func HashPassword(password []byte) (string, error)
func VerifyPassword(encoded string, password []byte) bool
type Manager struct { /* private state */ }
func NewManager(viewer m1config.Viewer, secure bool, onRevoke func(string)) (*Manager, error)
func (m *Manager) Register(*http.ServeMux)
func (m *Manager) Require(http.Handler) http.Handler
func (m *Manager) Secure() bool
func CSRF(key []byte, secure bool) func(http.Handler) http.Handler

// app: production composition is implemented by the integration plan.
func New(cfg m1config.Config, authManager *auth.Manager,
    playback http.Handler, web fs.FS, ready func(context.Context) error) (http.Handler, error)
```

`Manager.Register` owns only the three auth paths in the shared contract. `Require` authenticates the hashed opaque session cookie and sets `contracts.Principal` with the configured durable viewer IDs and auth-session ID. Deny missing/expired sessions with 401. No password/hash/session IDs enter `Viewer` responses except its explicitly public identity fields. App mounts protected movies/playback routes; auth routes and health endpoints remain reachable with their own protections. The injected `playback` handler owns both `/api/v1/movies` and `/api/v1/playback/…`.

M1 implementation defaults for review: Argon2id PHC strings with 19 MiB, 2 passes, parallelism 1, 16-byte salt and 32-byte output; reject configured memory above 64 MiB or malformed encodings before allocation. Password input is 12–1024 bytes. JSON bodies are capped at 16 KiB. Auth sessions use 32 random bytes, are stored hashed, expire at 7 days idle / 30 days absolute, and the store is capped at 64 entries for this one-viewer proof. Failed login is limited to five attempts per five minutes per username/peer with a bounded 256-key expiring limiter; rate rejection is 429 with retry guidance. Unknown usernames count like wrong passwords and run the same Argon2id work against a dummy hash. A full limiter table never rejects a key: it drops expired entries, then the oldest entry for any username other than the configured viewer's, so fake usernames can neither lock the viewer out nor reset the viewer's counter (spec §6.4). At most two Argon2id verifications run at once: one shared slot, and one reserved for the configured viewer's username, so a spray of fake usernames from any number of peers can't starve the viewer (spec §6.4). Each peer IP gets one verification at a time; a second request from that peer gets 429 at once instead of queuing. A login waits up to 5 s for a slot (less if the client disconnects), then gets 429 `rate_limited` with `Retry-After: 1`. Trade-off, accepted for M1: while someone deliberately saturates the shared slot, logins for the viewer's username still go through and others wait, which reveals the viewer's username to that attacker. The password stays protected by the per-username/peer limit. These are M1 guardrails, not measured general deployment capacity. No permanent account lockout or custom cryptography.

## Task 1: Validate and persist owner-provisioned configuration

**Files:** Create `server/internal/m1config/config.go`, `config_test.go` and `server/internal/auth/password.go`, `password_test.go`; modify the exclusively claimed Go manifests.
**Consumes:** The shared Viewer/Movie semantics; no source API or database.
**Produces:** `Load`, `SaveNew`, `HashPassword`, `VerifyPassword` above.

- [ ] Write `TestConfigRejectsUnknownFieldsAndEscapingPath`, `TestConfigRejectsMissingIDsOrInvalidOrigin`, `TestSaveNewIsPrivateAndRefusesOverwrite`, `TestConfigRoundTripPreservesIdentity`, `TestPasswordVerification`, `TestHostileHashParametersAreRejected` and `TestErrorsDoNotEchoSecrets`. The round trip also checks `Config.Movie()` exposes no path, root or hash. Assert absolute/`..` paths, source roots outside configuration, empty/mutated IDs, bad durations, HTTPS-to-HTTP spoofing input, wrong passwords and oversized hash settings fail. Test source-open confinement again in playback; string validation is not its replacement.
- [ ] Run `go -C server test ./internal/m1config ./internal/auth`; expect missing implementation/failing assertions.
- [ ] Add strict JSON config reading with a 64 KiB limit, validated local HTTP or HTTPS origin, `MaxPlaybackSessions >= 1`, existing media root and relative MP4 source. Persist new config atomically with mode 0600, refuse overwrite and avoid recursive chown or source writes. Use random stable IDs and a 32-byte CSRF key supplied by the init command; JSON encodes the key as base64. Do not regenerate durable IDs on normal startup. Hash with `argon2.IDKey` and constant-time verification; bound PHC parsing before calling it.
- [ ] Run `go -C server test ./internal/m1config ./internal/auth` and `go -C server vet ./internal/m1config ./internal/auth`; all cases pass without private media or a server.
- [ ] Commit as `feat(auth): validate M1 viewer configuration`.

## Task 2: Implement real local sessions and CSRF-protected auth

**Files:** Create `server/internal/auth/session.go`, `session_test.go`, `http.go`, `http_test.go`.
**Consumes:** `Config.Viewer`, `contracts.Principal`, public auth/error shapes.
**Produces:** `Manager` methods; auth routes; `onRevoke(authSessionID)` notification on logout/expiry.

- [ ] Write `TestLoginRequiresValidPasswordAndCSRF`, `TestLoginRotatesSession`, `TestHTTPAndHTTPSCookieAttributes`, `TestUnauthenticatedAndExpiredSession`, `TestLogoutInvokesRevocation`, `TestLoginRateLimitIsBounded`, `TestUnknownUsernameDoesSameWork`, `TestFullLimiterNeverLocksOutTheViewer`, `TestBusyVerifierGetsRateLimited`, `TestUsernameSprayCannotStarveViewer`, `TestViewerSlotIsReserved`, `TestOnePasswordCheckPerPeer`, `TestSessionStoreIsBounded`, `TestLoginRejectsMalformedBodies`, `TestNewManagerRejectsBadHash`. Use a real cookie jar in `httptest`; no auth fake is shipped. Inject a package-private clock in tests to cover expiry without sleeping days.
- [ ] Run `go -C server test ./internal/auth -run 'Test(Login|HTTP|Unauthenticated|Logout)'`; expected failures before the implementation.
- [ ] Implement the manager with a mutex-protected bounded map, hashed tokens, the agreed lifetimes and an explicit revoke hook. Use host-only HttpOnly SameSite=Lax session cookies; Secure follows configured HTTPS origin, never an arbitrary header. Anonymous `/auth/session` returns a CSRF token and `viewer:null`; successful login rotates credentials and returns a fresh token. Errors use the shared envelope and never distinguish an unknown username from a wrong password.
- [ ] Verify all auth tests, including another LAN host/origin rather than only the localhost Secure-cookie exception. Confirm revocation rejects the next protected request, state-changing GETs do not exist, and untrusted forwarded headers cannot change mode.
- [ ] Commit as `feat(auth): add M1 local viewer sessions`.

## Task 3: Compose the HTTP application without waiting for UI/delivery

**Files:** Create `server/internal/app/app.go`, `app_test.go`.
**Consumes:** A real auth manager, injected `http.Handler` for movie/session routes and `fs.FS` for static assets.
**Produces:** The `app.New` handler for integration; `/health/live`, `/health/ready` and protected API mounting.

- [ ] Write `TestProtectedAPIRoutesRequireLogin`, `TestLocalHTTPLoginRoundTrip`, `TestCrossOriginWriteRejected`, `TestProxyHeadersDoNotChangeScheme`, `TestHealthDoesNotLeakConfig`, `TestStaticFallbackDoesNotSwallowAPI404`, `TestReadinessReflectsInitFailure`, `TestNewRejectsInconsistentInputs`. `TestCrossOriginWriteRejected` includes a request with neither `Origin` nor `Referer`, a spoofed `Host`, an https-for-http Origin and a wrong-origin Referer. Inject a tiny test-only movie handler and `fstest.MapFS` frontend; assert the module tests do not claim actual playback.
- [ ] Run `go -C server test ./internal/app`; expect failures before composition exists.
- [ ] Add explicit Origin/Host validation against `cfg.PublicOrigin` for unsafe requests, CSRF middleware around API routes, and cookie settings from configuration. Give `csrf.ErrorHandler` the shared JSON 403 `csrf_failed` envelope. For local HTTP configure `csrf.Secure(false)` and `csrf.PlaintextHTTPRequest` only in that mode; retain the app's own origin validation. HTTPS mode must not be selected by untrusted `X-Forwarded-*`. Serve SPA fallback only outside `/api/`/`/health/`; cap request headers/bodies and use safe errors. A plain HTTP config is private-LAN-only; remote proxy support remains a separate G3 delivery gate.
- [ ] Run `go -C server test ./internal/m1config ./internal/auth ./internal/app` and vet those packages. Integration will separately prove real movies, static build embedding and process shutdown.
- [ ] Commit as `feat(api): compose protected M1 application`.

## Handoff

Record module evidence and the precise exported signatures in the feature PR. Runtime is **module accepted**, not OPS-1/ACC-1/G1 complete. Integration supplies the real playback handler, hooks logout into child-session revocation, embeds actual UI assets and exposes the executable. Owner-only credential provisioning uses the integration CLI; credentials never appear in argv/logs/repo examples.

Use the maintained [gorilla/csrf v1.7.3 API](https://github.com/gorilla/csrf/tree/v1.7.3), including its explicit plaintext request handling. Review password work against [OWASP password storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html) before implementation. No HTTP security assurance is claimed by this plan.

## Approved changes (#18)

Changes 1–9 were proposed on #18 and approved by @lisim with points P1–P5 and one question. All are written into the sections above; this list is the record.

| # | Change | Where |
|---|---|---|
| 1 | Drop `golang.org/x/term`; it moves to the integration plan | Ownership |
| 2 | `app.New` takes `ready func(context.Context) error`; `/health/ready` returns 503 with a fixed, non-leaking body while it errors, 200 otherwise; liveness never depends on it. Test `TestReadinessReflectsInitFailure` | Interfaces, Task 3 |
| 3 | `go.mod`/`go.sum` edit goes on top of OPS-2 (#17, merged) | Ownership |
| 4 | Equal Argon2id work for unknown usernames. Test `TestUnknownUsernameDoesSameWork` counts calls through an injected hook | Defaults, Task 2 |
| 5, P3 | `Config` gains `VideoCodec`, `Width`, `Height`; `Config.Movie()` is the only public view and fixes `Container: "mp4"` | Interfaces, Task 1 |
| 6 | `NewManager` returns an error for an unparseable or out-of-bounds hash and a nil `onRevoke` | Interfaces, Task 2 |
| 7 | `auth.CSRF(key, secure)` holds the gorilla/csrf settings once: `X-CSRF-Token`, cookie `lisimflix_csrf` (HttpOnly, Lax, Secure from config), plaintext marking for http origins, the shared 403 `csrf_failed` body | Interfaces, Task 3 |
| 8 | `Manager.Secure()`; `app.New` refuses a cookie mode that doesn't match the origin's scheme | Interfaces, Task 3 |
| 9, P1 | At most two concurrent Argon2id runs; a full limiter evicts rather than rejects, protecting the viewer's key | Defaults, Task 2 |
| P2 | An unsafe request with neither `Origin` nor `Referer` gets 403 `csrf_failed`. The app's own check requires a matching `Host` and `Origin` (or `Referer`), never reads `X-Forwarded-*`, and is the only Referer defence on plain HTTP because gorilla/csrf skips that check for plaintext requests | Task 3 |
| P4 | Module acceptance only; BASE-01/02/04 close through #19 | Status |
| P5 | Storage wiring (`Open`, `MigrateWithBackup`, not-ready on error, `Nightly.Run`) belongs to #19; `ready` is its hook | Handoff |
| Q | Both Argon2id slots busy: wait up to 5 s, then 429 `rate_limited` with `Retry-After: 1`. Test `TestBusyVerifierGetsRateLimited` | Defaults, Task 2 |
| R1 | Review of #24: a fake-username spray could hold both slots. Now one slot is reserved for the viewer's username and each peer gets one verification at a time (extra requests 429 at once). Tests `TestUsernameSprayCannotStarveViewer` (10 peers spraying), `TestViewerSlotIsReserved` (deterministic), `TestOnePasswordCheckPerPeer`. Accepted trade-off: under deliberate saturation the viewer's username is distinguishable | Defaults, Task 2 |

Additional tests from the review: `TestFullLimiterNeverLocksOutTheViewer`, `TestSessionStoreIsBounded`, `TestLoginRejectsMalformedBodies`, `TestNewManagerRejectsBadHash`, `TestNewRejectsInconsistentInputs`, `TestErrorsDoNotEchoSecrets`.

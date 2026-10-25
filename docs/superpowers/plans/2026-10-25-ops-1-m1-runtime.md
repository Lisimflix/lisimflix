# OPS-1 / ACC-1: M1 Local Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** Draft; unassigned and unimplemented.
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

Own `server/internal/m1config/**`, `server/internal/auth/**`, `server/internal/app/**`. After coordinating the shared-file claim, add only these exact direct Go dependencies to `server/go.mod`/`go.sum`: `golang.org/x/crypto v0.58.0`, `golang.org/x/term v0.47.0`, `github.com/gorilla/csrf v1.7.3`. Playback uses stdlib and must not edit the manifests concurrently. No frontend, root router command, Dockerfile or CI edits in this plan.

```go
// m1config: only its persisted public metadata becomes contracts.Movie.
type Viewer struct { AccountID, ProfileID, Username, PasswordHash string }
type Config struct {
    ListenAddr, PublicOrigin, MediaRoot string
    MovieID, RelativePath, Title, SourceRevision, AudioCodec string
    DurationMS int64
    Viewer Viewer
    CSRFKey []byte
    MaxPlaybackSessions int
}
func Load(path string) (Config, error)
func SaveNew(path string, cfg Config) error

// auth
func HashPassword(password []byte) (string, error)
func VerifyPassword(encoded string, password []byte) bool
type Manager struct { /* private state */ }
func NewManager(viewer m1config.Viewer, secure bool, onRevoke func(string)) *Manager
func (m *Manager) Register(*http.ServeMux)
func (m *Manager) Require(http.Handler) http.Handler

// app: production composition is implemented by the integration plan.
func New(cfg m1config.Config, authManager *auth.Manager,
    playback http.Handler, web fs.FS) (http.Handler, error)
```

`Manager.Register` owns only the three auth paths in the shared contract. `Require` authenticates the hashed opaque session cookie and sets `contracts.Principal` with the configured durable viewer IDs and auth-session ID. Deny missing/expired sessions with 401. No password/hash/session IDs enter `Viewer` responses except its explicitly public identity fields. App mounts protected movies/playback routes; auth routes and health endpoints remain reachable with their own protections. The injected `playback` handler owns both `/api/v1/movies` and `/api/v1/playback/…`.

M1 implementation defaults for review: Argon2id PHC strings with 19 MiB, 2 passes, parallelism 1, 16-byte salt and 32-byte output; reject configured memory above 64 MiB or malformed encodings before allocation. Password input is 12–1024 bytes. JSON bodies are capped at 16 KiB. Auth sessions use 32 random bytes, are stored hashed, expire at 7 days idle / 30 days absolute, and the store is capped at 64 entries for this one-viewer proof. Failed login is limited to five attempts per five minutes per username/peer with a bounded 256-key expiring limiter; rate rejection is 429 with retry guidance. These are M1 guardrails, not measured general deployment capacity. No permanent account lockout or custom cryptography.

## Task 1: Validate and persist owner-provisioned configuration

**Files:** Create `server/internal/m1config/config.go`, `config_test.go` and `server/internal/auth/password.go`, `password_test.go`; modify the exclusively claimed Go manifests.
**Consumes:** The shared Viewer/Movie semantics; no source API or database.
**Produces:** `Load`, `SaveNew`, `HashPassword`, `VerifyPassword` above.

- [ ] Write `TestConfigRejectsUnknownFieldsAndEscapingPath`, `TestConfigRejectsMissingIDsOrInvalidOrigin`, `TestSaveNewIsPrivateAndRefusesOverwrite`, `TestConfigRoundTripPreservesIdentity`, `TestPasswordVerification` and `TestHostileHashParametersAreRejected`. Assert absolute/`..` paths, source roots outside configuration, empty/mutated IDs, bad durations, HTTPS-to-HTTP spoofing input, wrong passwords and oversized hash settings fail. Test source-open confinement again in playback; string validation is not its replacement.
- [ ] Run `go -C server test ./internal/m1config ./internal/auth`; expect missing implementation/failing assertions.
- [ ] Add strict JSON config reading with a 64 KiB limit, validated local HTTP or HTTPS origin, `MaxPlaybackSessions >= 1`, existing media root and relative MP4 source. Persist new config atomically with mode 0600, refuse overwrite and avoid recursive chown or source writes. Use random stable IDs and a 32-byte CSRF key supplied by the init command; JSON encodes the key as base64. Do not regenerate durable IDs on normal startup. Hash with `argon2.IDKey` and constant-time verification; bound PHC parsing before calling it.
- [ ] Run `go -C server test ./internal/m1config ./internal/auth` and `go -C server vet ./internal/m1config ./internal/auth`; all cases pass without private media or a server.
- [ ] Commit as `feat(auth): validate M1 viewer configuration`.

## Task 2: Implement real local sessions and CSRF-protected auth

**Files:** Create `server/internal/auth/session.go`, `session_test.go`, `http.go`, `http_test.go`.
**Consumes:** `Config.Viewer`, `contracts.Principal`, public auth/error shapes.
**Produces:** `Manager` methods; auth routes; `onRevoke(authSessionID)` notification on logout/expiry.

- [ ] Write `TestLoginRequiresValidPasswordAndCSRF`, `TestLoginRotatesSession`, `TestHTTPAndHTTPSCookieAttributes`, `TestUnauthenticatedAndExpiredSession`, `TestLogoutInvokesRevocation`, `TestLoginRateLimitIsBounded`. Use a real cookie jar in `httptest`; no auth fake is shipped. Inject a package-private clock in tests to cover expiry without sleeping days.
- [ ] Run `go -C server test ./internal/auth -run 'Test(Login|HTTP|Unauthenticated|Logout)'`; expected failures before the implementation.
- [ ] Implement the manager with a mutex-protected bounded map, hashed tokens, the agreed lifetimes and an explicit revoke hook. Use host-only HttpOnly SameSite=Lax session cookies; Secure follows configured HTTPS origin, never an arbitrary header. Anonymous `/auth/session` returns a CSRF token and `viewer:null`; successful login rotates credentials and returns a fresh token. Errors use the shared envelope and never distinguish an unknown username from a wrong password.
- [ ] Verify all auth tests, including another LAN host/origin rather than only the localhost Secure-cookie exception. Confirm revocation rejects the next protected request, state-changing GETs do not exist, and untrusted forwarded headers cannot change mode.
- [ ] Commit as `feat(auth): add M1 local viewer sessions`.

## Task 3: Compose the HTTP application without waiting for UI/delivery

**Files:** Create `server/internal/app/app.go`, `app_test.go`.
**Consumes:** A real auth manager, injected `http.Handler` for movie/session routes and `fs.FS` for static assets.
**Produces:** The `app.New` handler for integration; `/health/live`, `/health/ready` and protected API mounting.

- [ ] Write `TestProtectedAPIRoutesRequireLogin`, `TestLocalHTTPLoginRoundTrip`, `TestCrossOriginWriteRejected`, `TestProxyHeadersDoNotChangeScheme`, `TestHealthDoesNotLeakConfig`, `TestStaticFallbackDoesNotSwallowAPI404`. Inject a tiny test-only movie handler and `fstest.MapFS` frontend; assert the module tests do not claim actual playback.
- [ ] Run `go -C server test ./internal/app`; expect failures before composition exists.
- [ ] Add explicit Origin/Host validation against `cfg.PublicOrigin` for unsafe requests, CSRF middleware around API routes, and cookie settings from configuration. Give `csrf.ErrorHandler` the shared JSON 403 `csrf_failed` envelope. For local HTTP configure `csrf.Secure(false)` and `csrf.PlaintextHTTPRequest` only in that mode; retain the app's own origin validation. HTTPS mode must not be selected by untrusted `X-Forwarded-*`. Serve SPA fallback only outside `/api/`/`/health/`; cap request headers/bodies and use safe errors. A plain HTTP config is private-LAN-only; remote proxy support remains a separate G3 delivery gate.
- [ ] Run `go -C server test ./internal/m1config ./internal/auth ./internal/app` and vet those packages. Integration will separately prove real movies, static build embedding and process shutdown.
- [ ] Commit as `feat(api): compose protected M1 application`.

## Handoff

Record module evidence and the precise exported signatures in the feature PR. Runtime is **module accepted**, not OPS-1/ACC-1/G1 complete. Integration supplies the real playback handler, hooks logout into child-session revocation, embeds actual UI assets and exposes the executable. Owner-only credential provisioning uses the integration CLI; credentials never appear in argv/logs/repo examples.

Use the maintained [gorilla/csrf v1.7.3 API](https://github.com/gorilla/csrf/tree/v1.7.3), including its explicit plaintext request handling. Review password work against [OWASP password storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html) before implementation. No HTTP security assurance is claimed by this plan.

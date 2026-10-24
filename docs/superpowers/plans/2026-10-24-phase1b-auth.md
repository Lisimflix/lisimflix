# Phase 1B: Auth & Accounts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Accounts, home profiles, sessions, invites and sign-in for Lisimflix: the setup wizard
(first admin with Argon2id password + mandatory TOTP), local admin login with lockout, opaque
per-device sessions (Bearer header and HttpOnly cookie with CSRF protection), profiles with PINs,
admin-managed per-user policies, single-use invites, and Google + Microsoft OIDC sign-in, all
behind the real `authctx.Guard` that replaces `DenyAllGuard` in `main`.

**Architecture:** One package, `server/internal/auth`. `auth.Service` owns the eight auth tables,
implements `authctx.Guard` (via `Guard()`), answers policy queries for other modules
(`VisibleLibraryIDs`, `MaxContentRating`, plus additive `StreamLimits` and `RatingAge`) and is
itself an `app.Module` (`Name() == "auth"`) mounting `/api/v1/auth`, `/api/v1/users`,
`/api/v1/profiles` and `/api/v1/invites`. OIDC uses go-oidc + x/oauth2 against provider discovery;
tests run a fake OpenID provider in-process (httptest + RSA-signed JWTs via go-jose).

**Tech Stack:** Go 1.27, chi v5, modernc.org/sqlite, `golang.org/x/crypto/argon2`,
`github.com/pquerna/otp`, `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`,
`github.com/go-jose/go-jose/v4` (tests only).

**Spec:** `docs/superpowers/specs/2026-10-24-lisimflix-design.md` (§3.3 accounts and profiles,
§3.4 per-profile data, §5.4 TV mode, §6.1 sign-in, §6.2 sessions and hardening)
**Index / shared contracts:** `docs/superpowers/plans/2026-10-24-phase1-index.md`
**Builds on:** plan 1A (`docs/superpowers/plans/2026-10-24-phase1a-core.md`), assumed merged.

## Global Constraints

- Go 1.27 (`golang:1.27-trixie`). Module `github.com/Lisimflix/lisimflix/server` in `server/`. `CGO_ENABLED=0`.
- Run everything through the Docker-wrapped Makefile: `make test`, `make vet`, `make fmt`, `make tidy`. Go is not installed on the host.
- SQLite via `modernc.org/sqlite`. Migrations in `server/internal/db/migrations/`, named `YYYYMMDDHHMMSS_<module>_<what>.sql`, `STRICT` tables, explicit `ON DELETE`. Never edit a merged migration.
- Router chi v5, logs `log/slog` only. API under `/api/v1`, JSON only, errors via `httpx.WriteError(w, status, "snake_code", "message")`.
- Contract names in the index are fixed: `auth.NewService(d *db.DB, cfg config.Config, log *slog.Logger, now func() time.Time) *Service`, `(*Service) Guard() authctx.Guard`, `(*Service) VisibleLibraryIDs(ctx, authctx.Principal) ([]int64, error)`, `(*Service) MaxContentRating(ctx, authctx.Principal) (string, error)` ("" = unrestricted). This plan only **adds** to package `auth`.
- Tables owned by 1B: `users`, `user_identities`, `profiles`, `sessions`, `invites`, `signup_requests` (reserved, created in phase 4), `user_library_access`, `oauth_providers`. Never write to another module's tables. **No foreign key to `libraries`** (plan 1C may merge after 1B).
- No network in unit tests: OIDC is tested against the in-process fake provider from Task 12.
- Secrets (passwords, TOTP secrets, session/invite tokens, OAuth client secrets) are never logged or returned by the API.
- Phase 4 items stay out: Apple, Facebook, generic OIDC, sign-up requests, TOTP/local passwords for non-admins, trusted-proxy `X-Forwarded-For`. The design below keeps each of them additive.
- Workflow (see `CONTRIBUTING.md`, `CLAUDE.md`): work on branch `feat/1b-auth` off an up-to-date `main`; never commit to or push `main`; never `--no-verify`. Per-task commits use Conventional Commits with scope `auth` (`feat(auth): ...`), ending with a `Co-Authored-By:` line when an AI agent wrote them. The PR is squash-merged, so its **title** is the final commit message. This plan does not touch `CONTRIBUTING.md`, `CLAUDE.md` or `.gitignore`.

## Design decisions (read before Task 1)

| Topic | Decision |
|---|---|
| Times in auth tables | `INTEGER` Unix seconds (UTC). Cheap comparisons for expiry. |
| Session token | 32 random bytes, unpadded base64url (43 chars). Stored as `SHA-256(token)` in `sessions.token_hash`. Shown once. |
| Session lifetime | Sliding 30 days idle (`sessionTTL`), absolute cap 180 days (`sessionMaxAge`). `last_seen_at`/`expires_at` are written at most once an hour per session. Revocation = delete the row. |
| Token transport | API clients send `Authorization: Bearer <token>` (returned in the JSON body of `/auth/setup` and `/auth/login`). The web app uses the `lisimflix_session` cookie: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when the request came over HTTPS (`r.TLS` or `X-Forwarded-Proto: https`). Plain-HTTP LAN access (`http://zraid:8420`) gets a non-Secure cookie because browsers drop Secure cookies on plain HTTP. If an `Authorization` header is present it must be valid; the guard never falls back to the cookie. |
| CSRF | Double-submit, bound to the session: `csrfToken = base64url(SHA-256("lisimflix-csrf\x00" + sessionToken))`. It is sent as the readable `lisimflix_csrf` cookie and in sign-in JSON. Cookie-authenticated `POST/PUT/PATCH/DELETE` must send header `X-Lisimflix-CSRF: <csrfToken>` or get `403 csrf_failed`. Bearer requests are exempt (browsers never attach them on their own). Unauthenticated POSTs (`/auth/setup*`, `/auth/login`) require `Content-Type: application/json` (`415` otherwise): a cross-site form cannot send that without a CORS preflight, which we never grant. |
| Passwords | Argon2id, PHC string `$argon2id$v=19$m=65536,t=3,p=2$salt$key` (64 MiB, 3 passes, 2 lanes, 16-byte salt, 32-byte key). Verification parses params from the string and compares with `subtle.ConstantTimeCompare`. Unknown usernames run a dummy verify (same timing). Local passwords exist only for the admin in phase 1 (min 12 chars). |
| TOTP | 6 digits, SHA-1, 30 s, ±1 step. The matched step is stored in `users.totp_last_step`; codes at or before it are refused (no replay). Mandatory for every local login. |
| Lockout | In-memory per key `login:<lower(username)>|<ip>` (and `pin:<profile>|<ip>` for profile PINs): 5 free failures, then 30 s doubling to 15 min max; forgotten after 24 h quiet. Lockout logs a `WARN` ("admin alert"; the phase 3 dashboard surfaces it). IP = TCP peer; `X-Forwarded-For` is ignored until phase 4 adds trusted proxies. |
| Library visibility | **Explicit allow-list** (index contract). Admins see every library ID; other users see only their `user_library_access` rows (empty list = no libraries). Invites carry the list. `VisibleLibraryIDs` always returns a sorted, non-nil slice. It reads plan 1C's `libraries` table directly with SQL (`SELECT id FROM libraries` for admins; users' grants are joined to `libraries`), so auth never imports 1C's packages, and grants for deleted libraries simply drop out (there is deliberately no FK from `user_library_access` to `libraries`, so 1C needs no cleanup). Until 1C's migration is present the query returns a "no such table" error; nothing calls it before 1C. Tests create the table only if it does not exist yet. Admin-ness is read from the DB, not trusted from the principal. Per-profile library restriction is not in phase 1 (additive later). |
| Content rating | `users.max_content_rating` and `profiles.max_content_rating`; `MaxContentRating` returns the stricter of user and selected profile. Labels from US film/TV, AU and UK are mapped to minimum ages by the exported `auth.RatingAge(label) (age int, known bool)`; unknown labels count as 18. Consumers allow an item when `RatingAge(item) <= RatingAge(max)`. |
| Profiles | Every account gets a primary profile (named after the user) on creation. Managing profiles (create/edit/delete, PINs, ratings) requires the session's selected profile to be the primary one, so a kid's profile cannot raise its own rating. Selecting a PIN-protected profile needs the PIN (Argon2id-hashed, lockout as above). Max 10 profiles. `Principal.ProfileID` = `sessions.profile_id`. |
| OIDC | Authorization Code + PKCE (S256) + `state` + `nonce`, ID token verified by go-oidc against the provider JWKS (signature, `aud`, `exp`, `iss`), then nonce and `sub` checked by us. Flow state is in memory (10 min TTL, single use) and bound to the browser by the `lisimflix_oauth` cookie (HttpOnly, Lax, path `/api/v1/auth/callback/`). Redirect URL = `cfg.PublicURL + "/api/v1/auth/callback/{provider}"`; sign-in must start on `PublicURL`'s host. Callback outcomes are redirects: success → `return_to` (relative paths only), failure → `/signin?error=<code>` for the phase 3 web app. |
| Microsoft | Uses `https://login.microsoftonline.com/common/v2.0` (personal + work/school). Its discovery document advertises issuer `https://login.microsoftonline.com/{tenantid}/v2.0` literally, so discovery uses `oidc.InsecureIssuerURLContext(ctx, template)`, the verifier runs with `SkipIssuerCheck: true`, and `verifyIDToken` then requires a GUID `tid` claim and `iss == template with {tenantid} replaced by tid`. |
| Identity matching | `(provider, subject)` only, never email. Unknown identity without a valid invite → `/signin?error=not_invited`. A known identity signs in even if the URL also carries an invite (the invite stays unused). Users can link more providers (`POST /auth/link/{provider}`) and unlink them; unlinking deletes the sessions created through that identity (`sessions.identity_id ON DELETE CASCADE`). The last sign-in method cannot be removed. |
| OAuth client secrets | Stored as-is in `oauth_providers.client_secret` (`/config` is the trust boundary; no extra key management in phase 1). In Go they are the `secret` type, which prints `[redacted]` for `fmt`, `slog` and JSON; `reveal()` is used only for the DB write and the token request. The admin API returns `secret_set`, never the value. |
| `signup_requests` | **Not created in 1B.** The table is reserved for 1B in the index, but its shape depends on the phase 4 approval flow (who reviews, what is captured from the provider). An unused table now would need a migration later anyway. Phase 4 adds `…_auth_signup_requests.sql`; the callback's `not_invited` branch is the single place that will create a request instead of failing. |
| Phase 4 hooks | New providers = one entry in `supportedProviders` + `defaultOIDCSpecs()` (generic OIDC adds an `issuer_url` column to `oauth_providers`); Facebook (not OIDC) adds a second verifier path next to `verifyIDToken`. Local passwords/TOTP for non-admins reuse the existing `users.password_hash/totp_secret` columns and `/auth/login`. |

### API summary (all under `/api/v1`)

| Method & path | Who | Purpose |
|---|---|---|
| `GET /auth/setup` | public | `{"needed": bool}` |
| `POST /auth/setup/totp` | public, JSON | wizard step 1: `{username}` → `{enrollment_id, secret, otpauth_url, expires_at}`; 409 after setup |
| `POST /auth/setup` | public, JSON | wizard step 2: `{username, password, display_name, enrollment_id, totp_code, device_name}` → 201 session; 409 after setup |
| `POST /auth/login` | public, JSON | `{username, password, totp_code, device_name}` → session; 401 `invalid_credentials`; 429 `too_many_attempts` + `Retry-After` |
| `GET /auth/providers` | public | enabled sign-in buttons `[{name, display_name, login_url}]` |
| `GET /auth/invite/{token}` | public | check an invite link |
| `GET /auth/login/{provider}?invite=&return_to=` | public | 302 to the provider |
| `GET /auth/callback/{provider}` | public | OIDC callback, 302 |
| `GET /auth/me` | user | `{user, session_id, profile_id}` |
| `POST /auth/logout`, `POST /auth/logout-all` | user | end this session / every session (204) |
| `GET /auth/sessions`, `DELETE /auth/sessions/{id}` | user | my devices |
| `POST /auth/link/{provider}` | user | `{url}` to link another provider |
| `GET /auth/identities`, `DELETE /auth/identities/{id}` | user | linked providers |
| `GET /auth/oauth-providers`, `PUT /auth/oauth-providers/{name}` | admin | provider setup (`{client_id, client_secret?, enabled}`) |
| `GET /users`, `GET/PATCH/DELETE /users/{id}` | admin | accounts (`PATCH {display_name?, disabled?}`) |
| `PUT /users/{id}/policy` | admin | `{library_ids, max_content_rating, max_bitrate_kbps, max_streams}` |
| `GET/DELETE /users/{id}/sessions` | admin | a user's devices / sign them out everywhere |
| `GET/POST /profiles`, `PATCH/DELETE /profiles/{id}`, `POST /profiles/{id}/select` | user | home profiles |
| `GET/POST /invites`, `DELETE /invites/{id}` | admin | invites (`POST` returns the token and link once) |

Session response (setup and login): `{"token","csrf_token","expires_at","user":{id,username,display_name,email,is_admin,disabled,has_password,policy,created_at}}`.

## File Structure

```
server/internal/db/migrations/
  20261025100000_auth_users.sql            users, user_identities, profiles, user_library_access
  20261025100100_auth_sessions.sql         sessions
  20261025100200_auth_invites.sql          invites
  20261025100300_auth_oauth_providers.sql  oauth_providers
server/internal/auth/
  doc.go                package comment
  password.go           Argon2id hash/verify, password policy
  token.go              random tokens, token hashing, CSRF derivation
  totp.go               TOTP key generation and replay-safe check
  limiter.go            in-memory failure limiter with backoff
  service.go            Service, NewService, Name, shared helpers
  sessions.go           session store, sliding expiry, cookies
  guard.go              authctx.Guard implementation + CSRF check
  routes.go             Mount: every route of the module
  users.go              user store, policy struct, user JSON
  setup.go              setup wizard, /auth/me, /auth/logout, requireJSON
  login.go              local login, session list/revoke, logout-all
  ratings.go            RatingAge and rating comparison
  policy.go             VisibleLibraryIDs, MaxContentRating, StreamLimits
  users_admin.go        admin user API
  profiles.go           profiles API
  providers.go          oauth_providers config, secret type, provider APIs
  invites.go            invites API and redemption
  oidc.go               OIDC sign-in (start, callback, ID-token checks incl. Microsoft)
  identities.go         link / list / unlink provider identities
  *_test.go             one per file above, plus:
  schema_test.go        migration constraints
  helpers_test.go       test harness (full app router, fake clock, request helpers)
  fakeidp_test.go       in-process fake OpenID provider
  microsoft_test.go     Microsoft common-tenant issuer tests
server/cmd/lisimflix/main.go(+_test)   wire the auth service (modify)
server/go.mod, go.sum                  new dependencies (via make tidy)
```

---

### Task 1: Auth schema (migrations)

**Files:**
- Create: `server/internal/auth/doc.go`
- Create: `server/internal/db/migrations/20261025100000_auth_users.sql`, `20261025100100_auth_sessions.sql`, `20261025100200_auth_invites.sql`, `20261025100300_auth_oauth_providers.sql`
- Test: `server/internal/auth/schema_test.go`

**Interfaces:**
- Consumes: `dbtest.Open(t) *db.DB` (1A; runs every embedded migration).
- Produces: the tables listed in Global Constraints (except `signup_requests`). Test helpers used by every later task: `mustExec(t, d, query, args...) int64` (returns LastInsertId) and `count(t, d, query, args...) int`.

- [ ] **Step 0: Branch**

```bash
git switch main && git pull && git switch -c feat/1b-auth
```

- [ ] **Step 1: Write the failing schema test**

`server/internal/auth/doc.go`:
```go
// Package auth owns accounts, home profiles, sessions, invites, sign-in
// (local admin with Argon2id + TOTP, and OAuth/OIDC with Google and
// Microsoft) and per-user access policy. It implements authctx.Guard and is
// mounted as the "auth" app module under /api/v1/{auth,users,profiles,invites}.
package auth
```

`server/internal/auth/schema_test.go`:
```go
package auth

import (
	"context"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
)

func mustExec(t *testing.T, d *db.DB, q string, args ...any) int64 {
	t.Helper()
	res, err := d.ExecContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func count(t *testing.T, d *db.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestSchemaConstraints(t *testing.T) {
	d := dbtest.Open(t)
	uid := mustExec(t, d, `INSERT INTO users(username, display_name, created_at, updated_at) VALUES('Zac', 'Zac', 1, 1)`)

	// usernames are case-insensitive unique
	if _, err := d.Exec(`INSERT INTO users(username, display_name, created_at, updated_at) VALUES('zac', 'x', 1, 1)`); err == nil {
		t.Fatal("duplicate username (different case) was accepted")
	}
	// OAuth-only users have NULL usernames, and several may exist
	mustExec(t, d, `INSERT INTO users(display_name, created_at, updated_at) VALUES('A', 1, 1)`)
	mustExec(t, d, `INSERT INTO users(display_name, created_at, updated_at) VALUES('B', 1, 1)`)

	idn := mustExec(t, d, `INSERT INTO user_identities(user_id, provider, subject, created_at) VALUES(?, 'google', 'sub-1', 1)`, uid)
	if _, err := d.Exec(`INSERT INTO user_identities(user_id, provider, subject, created_at) VALUES(?, 'google', 'sub-1', 1)`, uid); err == nil {
		t.Fatal("duplicate (provider, subject) accepted")
	}

	pid := mustExec(t, d, `INSERT INTO profiles(user_id, name, is_primary, created_at, updated_at) VALUES(?, 'Zac', 1, 1, 1)`, uid)
	if _, err := d.Exec(`INSERT INTO profiles(user_id, name, is_primary, created_at, updated_at) VALUES(?, 'Kids', 1, 1, 1)`, uid); err == nil {
		t.Fatal("second primary profile accepted")
	}

	// library ids are not foreign keys: 1C's libraries table may not exist yet
	mustExec(t, d, `INSERT INTO user_library_access(user_id, library_id) VALUES(?, 999)`, uid)

	mustExec(t, d, `INSERT INTO sessions(user_id, profile_id, identity_id, token_hash, method, created_at, last_seen_at, expires_at)
		VALUES(?, ?, ?, x'01', 'oauth:google', 1, 1, 2)`, uid, pid, idn)
	mustExec(t, d, `INSERT INTO sessions(user_id, token_hash, method, created_at, last_seen_at, expires_at)
		VALUES(?, x'02', 'local', 1, 1, 2)`, uid)

	// deleting a profile keeps the session but clears its profile
	mustExec(t, d, `DELETE FROM profiles WHERE id = ?`, pid)
	if n := count(t, d, `SELECT count(*) FROM sessions WHERE profile_id IS NULL`); n != 2 {
		t.Fatalf("sessions with NULL profile = %d, want 2", n)
	}
	// unlinking an identity ends the sessions it created
	mustExec(t, d, `DELETE FROM user_identities WHERE id = ?`, idn)
	if n := count(t, d, `SELECT count(*) FROM sessions`); n != 1 {
		t.Fatalf("sessions after unlink = %d, want 1", n)
	}
	// deleting the user removes everything it owns
	mustExec(t, d, `DELETE FROM users WHERE id = ?`, uid)
	for _, table := range []string{"sessions", "user_library_access", "user_identities", "profiles"} {
		if n := count(t, d, `SELECT count(*) FROM `+table); n != 0 {
			t.Fatalf("%s rows after user delete = %d", table, n)
		}
	}

	mustExec(t, d, `INSERT INTO oauth_providers(name, client_id, client_secret, enabled, updated_at) VALUES('google', 'id', 'secret', 1, 1)`)
	mustExec(t, d, `INSERT INTO invites(token_hash, created_at, expires_at) VALUES(x'03', 1, 2)`)
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `make test`
Expected: FAIL in `internal/auth` with `no such table: users`.

- [ ] **Step 3: Write the migrations**

`server/internal/db/migrations/20261025100000_auth_users.sql`:
```sql
-- Accounts, home profiles, linked sign-in identities and per-user library access.
-- Times are Unix seconds (UTC).
CREATE TABLE users (
    id                 INTEGER PRIMARY KEY,
    username           TEXT UNIQUE COLLATE NOCASE,  -- local sign-in name; NULL for OAuth-only users
    display_name       TEXT NOT NULL,
    email              TEXT NOT NULL DEFAULT '',     -- informational only, never used for matching
    is_admin           INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    disabled           INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    password_hash      TEXT,                         -- Argon2id PHC string; NULL = no local password
    totp_secret        TEXT,                         -- base32; NULL = no TOTP
    totp_last_step     INTEGER NOT NULL DEFAULT 0,   -- last accepted TOTP time step (replay guard)
    max_content_rating TEXT NOT NULL DEFAULT '',     -- '' = unrestricted
    max_bitrate_kbps   INTEGER NOT NULL DEFAULT 0,   -- 0 = unlimited
    max_streams        INTEGER NOT NULL DEFAULT 0,   -- 0 = unlimited
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
) STRICT;

CREATE TABLE user_identities (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider      TEXT NOT NULL,                     -- 'google' | 'microsoft' (phase 4 adds more)
    subject       TEXT NOT NULL,                     -- provider's stable "sub" claim
    email         TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    last_login_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE (provider, subject)
) STRICT;
CREATE INDEX user_identities_user ON user_identities(user_id);

CREATE TABLE profiles (
    id                 INTEGER PRIMARY KEY,
    user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    is_primary         INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
    pin_hash           TEXT,                         -- Argon2id of the 4-digit PIN; NULL = no PIN
    max_content_rating TEXT NOT NULL DEFAULT '',     -- '' = same as the account
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    UNIQUE (user_id, name)
) STRICT;
CREATE UNIQUE INDEX profiles_one_primary ON profiles(user_id) WHERE is_primary = 1;

-- Explicit allow-list. library_id refers to libraries(id) from plan 1C. There is
-- deliberately no foreign key: 1B may merge before the libraries table exists.
-- Orphaned ids are ignored at read time (VisibleLibraryIDs joins libraries).
CREATE TABLE user_library_access (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id INTEGER NOT NULL,
    PRIMARY KEY (user_id, library_id)
) STRICT, WITHOUT ROWID;
```

`server/internal/db/migrations/20261025100100_auth_sessions.sql`:
```sql
-- Opaque session tokens, stored as SHA-256 hashes. One row per signed-in device.
CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    profile_id   INTEGER REFERENCES profiles(id) ON DELETE SET NULL,
    identity_id  INTEGER REFERENCES user_identities(id) ON DELETE CASCADE, -- unlinking ends its sessions
    token_hash   BLOB NOT NULL UNIQUE,
    method       TEXT NOT NULL,                      -- 'local' | 'oauth:google' | 'oauth:microsoft'
    device_name  TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    ip           TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
) STRICT;
CREATE INDEX sessions_user ON sessions(user_id);
CREATE INDEX sessions_expires ON sessions(expires_at);
```

`server/internal/db/migrations/20261025100200_auth_invites.sql`:
```sql
-- Single-use invite links. The token is shown once; only its SHA-256 is stored.
CREATE TABLE invites (
    id                 INTEGER PRIMARY KEY,
    token_hash         BLOB NOT NULL UNIQUE,
    note               TEXT NOT NULL DEFAULT '',     -- admin's own label, e.g. "Mum's tablet"
    display_name       TEXT NOT NULL DEFAULT '',     -- '' = take the name from the provider
    library_ids        TEXT NOT NULL DEFAULT '[]',   -- JSON array copied to user_library_access
    max_content_rating TEXT NOT NULL DEFAULT '',
    max_bitrate_kbps   INTEGER NOT NULL DEFAULT 0,
    max_streams        INTEGER NOT NULL DEFAULT 0,
    created_by         INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at         INTEGER NOT NULL,
    expires_at         INTEGER NOT NULL,
    used_at            INTEGER,
    used_by            INTEGER REFERENCES users(id) ON DELETE SET NULL
) STRICT;
```

`server/internal/db/migrations/20261025100300_auth_oauth_providers.sql`:
```sql
-- OAuth/OIDC sign-in providers configured by the admin. client_secret is stored
-- as-is: /config is the trust boundary in phase 1 (no extra key management).
CREATE TABLE oauth_providers (
    name          TEXT PRIMARY KEY,                  -- 'google' | 'microsoft'
    client_id     TEXT NOT NULL,
    client_secret TEXT NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    updated_at    INTEGER NOT NULL
) STRICT;
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`
Expected: `ok  .../internal/auth` and `internal/db` still passes (1A's runner applies the new files in name order).

- [ ] **Step 5: Commit**

```bash
git add server/internal/auth server/internal/db/migrations
git commit -m "feat(auth): schema for users, profiles, identities, sessions, invites and providers"
```

---

### Task 2: Password hashing (Argon2id) and tokens

**Files:**
- Create: `server/internal/auth/password.go`, `server/internal/auth/token.go`
- Test: `server/internal/auth/password_test.go`, `server/internal/auth/token_test.go`
- Modify: `server/go.mod`, `server/go.sum` (via `make tidy`: adds `golang.org/x/crypto`)

**Interfaces:**
- Consumes: none
- Produces: `type argonParams struct{memory, time uint32; threads uint8; keyLen uint32; saltLen int}`, `defaultArgon`, `hashPasswordWith(pw string, p argonParams) (string, error)`, `verifyPassword(pw, encoded string) (bool, error)`, `errBadHash`, `validatePassword(pw string) error`; `newToken() (string, error)`, `validTokenFormat(s string) bool`, `hashToken(token string) []byte`, `csrfToken(sessionToken string) string`. Test var `cheapArgon` (used by the harness in Task 5).

- [ ] **Step 1: Write the failing tests**

`server/internal/auth/password_test.go`:
```go
package auth

import (
	"strings"
	"testing"
)

var cheapArgon = argonParams{memory: 64, time: 1, threads: 1, keyLen: 32, saltLen: 16}

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := hashPasswordWith("correct horse battery", cheapArgon)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("unexpected encoding %q", h)
	}
	ok, err := verifyPassword("correct horse battery", h)
	if err != nil || !ok {
		t.Fatalf("right password: ok=%v err=%v", ok, err)
	}
	ok, err = verifyPassword("correct horse battery!", h)
	if err != nil || ok {
		t.Fatalf("wrong password: ok=%v err=%v", ok, err)
	}
	h2, _ := hashPasswordWith("correct horse battery", cheapArgon)
	if h2 == h {
		t.Fatal("salt is not random")
	}
}

func TestDefaultArgonParamsEncoded(t *testing.T) {
	h, err := hashPasswordWith("x", defaultArgon)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("default params not encoded: %q", h)
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, h := range []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=18$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=99999999,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=1,p=1$!!!$a2V5a2V5a2V5a2V5a2V5a2V5",
	} {
		if ok, err := verifyPassword("x", h); ok || err == nil {
			t.Errorf("%q: ok=%v err=%v, want errBadHash", h, ok, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := validatePassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := validatePassword(strings.Repeat("a", 257)); err == nil {
		t.Fatal("huge password accepted")
	}
	if err := validatePassword("twelve chars"); err != nil {
		t.Fatal(err)
	}
}
```

`server/internal/auth/token_test.go`:
```go
package auth

import (
	"bytes"
	"testing"
)

func TestNewToken(t *testing.T) {
	a, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newToken()
	if len(a) != 43 || a == b {
		t.Fatalf("tokens %q %q", a, b)
	}
	if !validTokenFormat(a) {
		t.Fatal("own token rejected")
	}
	for _, bad := range []string{"", "abc", a + "x", a[:42] + "!"} {
		if validTokenFormat(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestHashAndCSRF(t *testing.T) {
	a, _ := newToken()
	b, _ := newToken()
	if !bytes.Equal(hashToken(a), hashToken(a)) || bytes.Equal(hashToken(a), hashToken(b)) {
		t.Fatal("hashToken not deterministic/distinct")
	}
	if len(hashToken(a)) != 32 {
		t.Fatal("hash length")
	}
	if csrfToken(a) != csrfToken(a) || csrfToken(a) == csrfToken(b) || csrfToken(a) == a {
		t.Fatal("csrfToken must be deterministic, per-session and differ from the session token")
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: hashPasswordWith`, `undefined: newToken`.

- [ ] **Step 3: Implement**

`server/internal/auth/password.go`:
```go
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// argonParams are Argon2id cost settings. They are written into every hash,
// so changing defaultArgon later only affects new hashes.
type argonParams struct {
	memory  uint32 // KiB
	time    uint32 // passes
	threads uint8
	keyLen  uint32
	saltLen int
}

// defaultArgon: 64 MiB, 3 passes, 2 lanes (above the OWASP / RFC 9106 minimums,
// roughly 100 ms on the zraid CPU). Tests swap in cheap params via Service.argon.
var defaultArgon = argonParams{memory: 64 * 1024, time: 3, threads: 2, keyLen: 32, saltLen: 16}

const (
	minPasswordLen = 12
	maxPasswordLen = 256
)

var errBadHash = errors.New("malformed password hash")

// hashPasswordWith returns a PHC-format Argon2id hash:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<key> (unpadded standard base64).
func hashPasswordWith(password string, p argonParams) (string, error) {
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// verifyPassword checks password against a hash from hashPasswordWith using a
// constant-time comparison. It returns errBadHash for unparseable hashes.
func verifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var memory, passes uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &passes, &threads); err != nil {
		return false, errBadHash
	}
	if memory == 0 || memory > 1<<21 || passes == 0 || passes > 16 || threads == 0 {
		return false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) < 16 {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(password), salt, passes, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// validatePassword enforces the local password policy (admin accounts only in phase 1).
func validatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < minPasswordLen {
		return fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(pw) > maxPasswordLen {
		return fmt.Errorf("password must be at most %d bytes", maxPasswordLen)
	}
	return nil
}
```

`server/internal/auth/token.go`:
```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// tokenBytes is the entropy of session, invite and OAuth state tokens.
const tokenBytes = 32

// newToken returns 32 random bytes as unpadded base64url (43 characters).
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// validTokenFormat reports whether s looks like a newToken value. Used to
// reject junk before touching the database.
func validTokenFormat(s string) bool {
	if len(s) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil
}

// hashToken is how tokens are stored: SHA-256 of the token string. Tokens are
// 256-bit random values, so a fast unsalted hash is sufficient.
func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// csrfToken derives the CSRF token for a session token. It is bound to the
// session (an attacker who can plant cookies still cannot compute it) and the
// session token cannot be recovered from it.
func csrfToken(sessionToken string) string {
	h := sha256.Sum256([]byte("lisimflix-csrf\x00" + sessionToken))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
```

- [ ] **Step 4: Fetch the dependency and run tests**

Run: `make tidy && make test`
Expected: `go.mod` gains `golang.org/x/crypto`. All `internal/auth` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/go.mod server/go.sum server/internal/auth
git commit -m "feat(auth): Argon2id password hashing and session/CSRF tokens"
```

---

### Task 3: TOTP with replay protection

**Files:**
- Create: `server/internal/auth/totp.go`
- Test: `server/internal/auth/totp_test.go`
- Modify: `server/go.mod`, `server/go.sum` (adds `github.com/pquerna/otp`)

**Interfaces:**
- Consumes: none
- Produces: `newTOTPKey(account string) (*otp.Key, error)` (`key.Secret()` base32, `key.URL()` otpauth URL with issuer "Lisimflix"), `checkTOTP(secret, code string, now time.Time, lastStep int64) (step int64, ok bool)`, `totpPeriod = 30`, `totpOpts`. Test helper `code(t, secret, at time.Time) string` (used by later tests).

- [ ] **Step 1: Write the failing test**

`server/internal/auth/totp_test.go`:
```go
package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func code(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCodeCustom(secret, at, totpOpts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewTOTPKey(t *testing.T) {
	k, err := newTOTPKey("zac")
	if err != nil {
		t.Fatal(err)
	}
	if k.Secret() == "" || !strings.HasPrefix(k.URL(), "otpauth://totp/Lisimflix:zac?") {
		t.Fatalf("key url %q", k.URL())
	}
}

func TestCheckTOTP(t *testing.T) {
	k, _ := newTOTPKey("zac")
	secret := k.Secret()
	now := time.Date(2026, 10, 25, 9, 0, 10, 0, time.UTC)
	cur := now.Unix() / totpPeriod

	step, ok := checkTOTP(secret, code(t, secret, now), now, 0)
	if !ok || step != cur {
		t.Fatalf("current code: step=%d ok=%v", step, ok)
	}
	// one step of skew either way is accepted
	if _, ok := checkTOTP(secret, code(t, secret, now.Add(-30*time.Second)), now, 0); !ok {
		t.Fatal("previous step rejected")
	}
	if _, ok := checkTOTP(secret, code(t, secret, now.Add(30*time.Second)), now, 0); !ok {
		t.Fatal("next step rejected")
	}
	// two steps away is rejected
	if _, ok := checkTOTP(secret, code(t, secret, now.Add(-90*time.Second)), now, 0); ok {
		t.Fatal("stale code accepted")
	}
	// replay: the same step cannot be used again
	if _, ok := checkTOTP(secret, code(t, secret, now), now, cur); ok {
		t.Fatal("replayed code accepted")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := checkTOTP(secret, bad, now, 0); ok {
			t.Fatalf("%q accepted", bad)
		}
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make tidy && make test` (tidy pulls `pquerna/otp` for the test file)
Expected: FAIL, `undefined: newTOTPKey`, `undefined: checkTOTP`.

- [ ] **Step 3: Implement**

`server/internal/auth/totp.go`:
```go
package auth

import (
	"crypto/subtle"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	totpIssuer = "Lisimflix"
	totpPeriod = 30 // seconds
)

var totpOpts = totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// newTOTPKey creates a fresh TOTP secret. key.Secret() is the base32 secret
// to store; key.URL() is the otpauth:// URL for the authenticator QR code.
func newTOTPKey(account string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: account,
		Period:      totpPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
}

// checkTOTP validates a 6-digit code with ±1 step of clock skew. It returns
// the matched time step, which the caller stores so the same code (or an
// older one) can never be used twice: steps <= lastStep are refused.
func checkTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for _, step := range []int64{cur - 1, cur, cur + 1} {
		if step <= lastStep {
			continue
		}
		want, err := totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0).UTC(), totpOpts)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make tidy && make test`

- [ ] **Step 5: Commit**

```bash
git add server/go.mod server/go.sum server/internal/auth
git commit -m "feat(auth): TOTP enrollment and replay-safe code check"
```

---

### Task 4: Login limiter with lockout and backoff

**Files:**
- Create: `server/internal/auth/limiter.go`
- Test: `server/internal/auth/limiter_test.go`

**Interfaces:**
- Consumes: none
- Produces: `newLimiter(now func() time.Time) *limiter`; `(*limiter) allow(key) (ok bool, wait time.Duration)`, `fail(key) (lockout time.Duration)`, `success(key)`. Defaults: 5 free failures, 30 s doubling to 15 min, forget after 24 h. Test helper `fakeClock` (`newFakeClock()`, `Now()`, `Advance(d)`; starts 2026-10-25 09:00:00 UTC), used by every later test.

- [ ] **Step 1: Write the failing test**

`server/internal/auth/limiter_test.go`:
```go
package auth

import (
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock shared by all auth tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestLimiterBackoff(t *testing.T) {
	clk := newFakeClock()
	l := newLimiter(clk.Now)
	key := "login:zac|10.0.0.1"

	for i := 1; i <= 5; i++ {
		if ok, _ := l.allow(key); !ok {
			t.Fatalf("attempt %d blocked", i)
		}
		if d := l.fail(key); d != 0 {
			t.Fatalf("failure %d locked for %v", i, d)
		}
	}
	// 6th failure locks for 30s, then 60s, 120s ...
	if d := l.fail(key); d != 30*time.Second {
		t.Fatalf("6th failure lock = %v", d)
	}
	if ok, wait := l.allow(key); ok || wait != 30*time.Second {
		t.Fatalf("allow during lockout: ok=%v wait=%v", ok, wait)
	}
	clk.Advance(30 * time.Second)
	if ok, _ := l.allow(key); !ok {
		t.Fatal("still locked after lockout expired")
	}
	if d := l.fail(key); d != 60*time.Second {
		t.Fatalf("7th failure lock = %v", d)
	}
	for i := 0; i < 10; i++ {
		clk.Advance(time.Hour)
		l.fail(key)
	}
	if _, wait := l.allow(key); wait != 15*time.Minute {
		t.Fatalf("lockout not capped: %v", wait)
	}

	// other keys are unaffected
	if ok, _ := l.allow("login:zac|10.0.0.2"); !ok {
		t.Fatal("other IP blocked")
	}

	// success resets
	clk.Advance(15 * time.Minute)
	l.success(key)
	if d := l.fail(key); d != 0 {
		t.Fatalf("after success, first failure locked for %v", d)
	}
}

func TestLimiterForgetsOldFailures(t *testing.T) {
	clk := newFakeClock()
	l := newLimiter(clk.Now)
	for i := 0; i < 5; i++ {
		l.fail("k")
	}
	clk.Advance(25 * time.Hour)
	if d := l.fail("k"); d != 0 {
		t.Fatalf("old failures still counted: locked %v", d)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: newLimiter`.

- [ ] **Step 3: Implement**

`server/internal/auth/limiter.go`:
```go
package auth

import (
	"sync"
	"time"
)

// limiter counts failed attempts per key (e.g. "login:<user>|<ip>") and locks
// the key out with exponential backoff once the free attempts are used up.
// It is in memory: a restart clears it, which is acceptable for a home server.
type limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	free    int           // failures allowed before the first lockout
	base    time.Duration // first lockout
	max     time.Duration // longest lockout
	forget  time.Duration // a key with no failures for this long starts fresh
	entries map[string]*limitEntry
}

type limitEntry struct {
	failures    int
	lockedUntil time.Time
	lastFail    time.Time
}

const limiterMaxEntries = 10000

func newLimiter(now func() time.Time) *limiter {
	return &limiter{
		now:     now,
		free:    5,
		base:    30 * time.Second,
		max:     15 * time.Minute,
		forget:  24 * time.Hour,
		entries: map[string]*limitEntry{},
	}
}

// allow reports whether an attempt for key may proceed. When it may not,
// wait is how long until it may.
func (l *limiter) allow(key string) (ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entry(key, false)
	if e == nil {
		return true, 0
	}
	if now := l.now(); now.Before(e.lockedUntil) {
		return false, e.lockedUntil.Sub(now)
	}
	return true, 0
}

// fail records a failed attempt and returns the lockout it triggered (0 if none).
func (l *limiter) fail(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.entries) >= limiterMaxEntries {
		l.sweep(now)
	}
	e := l.entry(key, true)
	e.failures++
	e.lastFail = now
	over := e.failures - l.free
	if over <= 0 {
		return 0
	}
	d := l.base
	for i := 1; i < over && d < l.max; i++ {
		d *= 2
	}
	if d > l.max {
		d = l.max
	}
	e.lockedUntil = now.Add(d)
	return d
}

// success clears the key after a successful attempt.
func (l *limiter) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// entry returns the live entry for key, resetting it if it has been forgotten.
// Must be called with l.mu held.
func (l *limiter) entry(key string, create bool) *limitEntry {
	e := l.entries[key]
	if e != nil && l.now().Sub(e.lastFail) >= l.forget && !l.now().Before(e.lockedUntil) {
		delete(l.entries, key)
		e = nil
	}
	if e == nil && create {
		e = &limitEntry{}
		l.entries[key] = e
	}
	return e
}

func (l *limiter) sweep(now time.Time) {
	for k, e := range l.entries {
		if now.Sub(e.lastFail) >= l.forget && !now.Before(e.lockedUntil) {
			delete(l.entries, k)
		}
	}
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 5: Commit**

```bash
git add server/internal/auth
git commit -m "feat(auth): in-memory failure limiter with exponential lockout"
```

---

### Task 5: Service, sessions and the real Guard (Bearer + cookie + CSRF)

**Files:**
- Create: `server/internal/auth/service.go`, `server/internal/auth/sessions.go`, `server/internal/auth/guard.go`, `server/internal/auth/routes.go`
- Test: `server/internal/auth/helpers_test.go`, `server/internal/auth/guard_test.go`

**Interfaces:**
- Consumes: `config.Config`, `db.DB`, `dbtest.Open`, `app.New`, `authctx.{Principal, WithPrincipal, FromContext, Guard}`, `httpx.{WriteJSON, WriteError}`; Task 2 tokens, Task 4 limiter, `cheapArgon`, `fakeClock`, `mustExec`, `count`.
- Produces:
  - `type Service struct` (fields `db, cfg, log, now, argon, limit, dummyOnce, dummyHash, mu, enrollments`), **contract** `NewService(d *db.DB, cfg config.Config, log *slog.Logger, now func() time.Time) *Service` (nil `now` = `time.Now`; warns when `PublicURL` is empty), `(*Service) Name() string` = `"auth"`, `(*Service) Mount(r chi.Router, g authctx.Guard)`, **contract** `(*Service) Guard() authctx.Guard`.
  - Helpers: `(*Service) hashPassword(pw) (string, error)`, `dummyVerify(pw)`, `internalError(w, r, msg, err)` (500 `internal`), `clientIP(r) string`, `isHTTPS(r) bool`, `truncate(s, n) string`, `nullInt(int64) any`, `nullString(string) any`, `unix(sec int64) time.Time`, `type enrollment{secret string; expires time.Time}`.
  - Sessions: `type session{ID, UserID, ProfileID, IdentityID int64; Method, DeviceName, UserAgent, IP string; CreatedAt, LastSeenAt, ExpiresAt time.Time; IsAdmin bool}`, `type newSession{UserID, ProfileID, IdentityID int64; Method, DeviceName, UserAgent, IP string}`, `sessionFromRequest(r, userID, identityID int64, method, deviceName string) newSession`, `(*Service) createSession(ctx, newSession) (token string, session, error)`, `sessionByToken(ctx, token) (session, error)` (`errNoSession`), `listSessions(ctx, userID) ([]session, error)`, `revokeSession(ctx, userID, sessionID) (bool, error)`, `revokeAllSessions(ctx, userID) error`, `setSessionProfile(ctx, sessionID, profileID) error`, `setSessionCookies(w, r, token)`, `clearSessionCookies(w, r)`, constants `sessionTTL, sessionMaxAge, sessionTouchEvery, sessionCookie="lisimflix_session", csrfCookie="lisimflix_csrf", csrfHeader="X-Lisimflix-CSRF"`.
  - Guard: `currentSession(ctx) (session, bool)`; 401 `unauthenticated` (+ `WWW-Authenticate: Bearer`), 403 `csrf_failed`, 403 `forbidden`.
  - Test harness (`helpers_test.go`): `newHarness(t) *harness` with fields `t, d, s, h (full app handler), clock, logs`; `testPublicURL = "https://watch.example.com"`; `(*harness) do(method, path, body any, opts ...reqOpt) *httptest.ResponseRecorder`; options `bearer(tok)`, `cookieAuth(tok)` (cookie + CSRF header), `withCookies(...)`, `withHeader(k, v)`; `decodeBody[T](t, rec) T`, `errCode(t, rec) string`, `wantStatus(t, rec, status)`, `findCookie(rec, name) *http.Cookie`; `(*harness) insertUser(name string, admin bool) int64` (user + primary profile via SQL), `primaryProfile(userID) int64`, `sessionFor(userID) string`.

- [ ] **Step 1: Write the test harness and the failing guard tests**

`server/internal/auth/helpers_test.go`:
```go
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/app"
	"github.com/Lisimflix/lisimflix/server/internal/config"
	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/db/dbtest"
)

// harness runs the auth module inside the real app router with a fake clock.
type harness struct {
	t     *testing.T
	d     *db.DB
	s     *Service
	h     http.Handler
	clock *fakeClock
	logs  *bytes.Buffer
}

const testPublicURL = "https://watch.example.com"

func newHarness(t *testing.T) *harness {
	t.Helper()
	d := dbtest.Open(t)
	clock := newFakeClock()
	cfg := config.Config{PublicURL: testPublicURL}
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := NewService(d, cfg, log, clock.Now)
	s.argon = cheapArgon
	a := app.New(cfg, d, log, s.Guard(), s)
	return &harness{t: t, d: d, s: s, h: a.Handler(), clock: clock, logs: logs}
}

type reqOpt func(*http.Request)

func bearer(token string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// cookieAuth sends the session like the web app does: cookie + CSRF header.
func cookieAuth(token string) reqOpt {
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set(csrfHeader, csrfToken(token))
	}
}

func withCookies(cs ...*http.Cookie) reqOpt {
	return func(r *http.Request) {
		for _, c := range cs {
			r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
		}
	}
}

func withHeader(k, v string) reqOpt {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

// do sends a request through the full app handler. A non-nil body is sent
// as JSON (a string body is sent as-is, still labelled JSON).
func (h *harness) do(method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rd = bytes.NewBufferString(s)
		} else {
			b, err := json.Marshal(body)
			if err != nil {
				h.t.Fatal(err)
			}
			rd = bytes.NewReader(b)
		}
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Error.Code
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
}

// insertUser creates a user with a primary profile directly in the DB and
// returns its id.
func (h *harness) insertUser(name string, admin bool) int64 {
	h.t.Helper()
	now := h.clock.Now().Unix()
	res, err := h.d.Exec(`INSERT INTO users(display_name, is_admin, created_at, updated_at) VALUES(?, ?, ?, ?)`,
		name, admin, now, now)
	if err != nil {
		h.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := h.d.Exec(`INSERT INTO profiles(user_id, name, is_primary, created_at, updated_at) VALUES(?, ?, 1, ?, ?)`,
		id, name, now, now); err != nil {
		h.t.Fatal(err)
	}
	return id
}

// primaryProfile returns the id of a user's primary profile.
func (h *harness) primaryProfile(userID int64) int64 {
	h.t.Helper()
	var id int64
	if err := h.d.QueryRow(`SELECT id FROM profiles WHERE user_id = ? AND is_primary = 1`, userID).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

// sessionFor signs userID in (no profile selected) and returns the token.
func (h *harness) sessionFor(userID int64) string {
	h.t.Helper()
	tok, _, err := h.s.createSession(context.Background(), newSession{UserID: userID, Method: "local", DeviceName: "test"})
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}

func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}
```

`server/internal/auth/guard_test.go`:
```go
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

// guarded wraps an echo handler with the real guard.
func (h *harness) guarded(admin bool) http.Handler {
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := authctx.FromContext(r.Context())
		ss, _ := currentSession(r.Context())
		httpx.WriteJSON(w, 200, map[string]any{"user": p.UserID, "profile": p.ProfileID, "admin": p.IsAdmin, "session": ss.ID})
	})
	if admin {
		return h.s.Guard().RequireAdmin(echo)
	}
	return h.s.Guard().RequireUser(echo)
}

func call(hd http.Handler, method string, opts ...reqOpt) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/x", nil)
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	hd.ServeHTTP(rec, req)
	return rec
}

func TestGuardCredentials(t *testing.T) {
	h := newHarness(t)
	uid := h.insertUser("Ann", false)
	tok := h.sessionFor(uid)
	user := h.guarded(false)

	wantStatus(t, call(user, "GET"), 401)
	if rec := call(user, "GET"); errCode(t, rec) != "unauthenticated" {
		t.Fatalf("code %q", errCode(t, rec))
	}

	rec := call(user, "GET", bearer(tok))
	wantStatus(t, rec, 200)
	if got := decodeBody[map[string]any](t, rec); got["user"] != float64(uid) || got["profile"] != float64(0) {
		t.Fatalf("principal %v", got)
	}

	// an invalid Authorization header never falls back to the cookie
	wantStatus(t, call(user, "GET", bearer("nope"), withCookies(&http.Cookie{Name: sessionCookie, Value: tok})), 401)
	wantStatus(t, call(user, "GET", withHeader("Authorization", "Basic "+tok)), 401)

	// cookie works for safe methods without CSRF header
	wantStatus(t, call(user, "GET", withCookies(&http.Cookie{Name: sessionCookie, Value: tok})), 200)
}

func TestGuardCSRF(t *testing.T) {
	h := newHarness(t)
	uid := h.insertUser("Ann", false)
	tok := h.sessionFor(uid)
	user := h.guarded(false)
	cookie := withCookies(&http.Cookie{Name: sessionCookie, Value: tok})

	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		rec := call(user, m, cookie)
		if rec.Code != 403 || errCode(t, rec) != "csrf_failed" {
			t.Fatalf("%s without CSRF header: %d %s", m, rec.Code, rec.Body.String())
		}
	}
	wantStatus(t, call(user, "POST", cookie, withHeader(csrfHeader, csrfToken("other"))), 403)
	wantStatus(t, call(user, "POST", cookieAuth(tok)), 200)
	// bearer requests are exempt
	wantStatus(t, call(user, "POST", bearer(tok)), 200)
}

func TestGuardAdmin(t *testing.T) {
	h := newHarness(t)
	ann := h.sessionFor(h.insertUser("Ann", false))
	root := h.sessionFor(h.insertUser("Root", true))
	admin := h.guarded(true)

	rec := call(admin, "GET", bearer(ann))
	if rec.Code != 403 || errCode(t, rec) != "forbidden" {
		t.Fatalf("non-admin: %d %s", rec.Code, rec.Body.String())
	}
	rec = call(admin, "GET", bearer(root))
	wantStatus(t, rec, 200)
	if decodeBody[map[string]any](t, rec)["admin"] != true {
		t.Fatal("IsAdmin not set")
	}
}

func TestSessionExpiryAndSliding(t *testing.T) {
	h := newHarness(t)
	uid := h.insertUser("Ann", false)
	user := h.guarded(false)

	idle := h.sessionFor(uid)
	h.clock.Advance(sessionTTL)
	wantStatus(t, call(user, "GET", bearer(idle)), 401)
	if n := count(t, h.d, `SELECT count(*) FROM sessions WHERE token_hash = ?`, hashToken(idle)); n != 0 {
		t.Fatal("expired session not deleted")
	}

	// used every 20 days, a session slides... until the 180-day absolute cap
	active := h.sessionFor(uid)
	for day := 20; day < 180; day += 20 {
		h.clock.Advance(20 * 24 * time.Hour)
		wantStatus(t, call(user, "GET", bearer(active)), 200)
	}
	h.clock.Advance(20 * 24 * time.Hour) // day 180
	wantStatus(t, call(user, "GET", bearer(active)), 401)
}

func TestGuardRejectsDisabledAndRevoked(t *testing.T) {
	h := newHarness(t)
	uid := h.insertUser("Ann", false)
	a, b := h.sessionFor(uid), h.sessionFor(uid)
	user := h.guarded(false)

	if err := h.s.revokeAllSessions(context.Background(), uid); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, call(user, "GET", bearer(a)), 401)
	wantStatus(t, call(user, "GET", bearer(b)), 401)

	c := h.sessionFor(uid)
	mustExec(t, h.d, `UPDATE users SET disabled = 1 WHERE id = ?`, uid)
	wantStatus(t, call(user, "GET", bearer(c)), 401)
}

func TestSessionCookieAttributes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		https  bool
		secure bool
	}{{"plain http LAN", false, false}, {"https via proxy", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", nil)
			if tc.https {
				req.Header.Set("X-Forwarded-Proto", "https")
			}
			rec := httptest.NewRecorder()
			setSessionCookies(rec, req, "tok")
			sc, cc := findCookie(rec, sessionCookie), findCookie(rec, csrfCookie)
			if sc == nil || cc == nil {
				t.Fatal("cookies missing")
			}
			if !sc.HttpOnly || cc.HttpOnly {
				t.Fatal("session cookie must be HttpOnly, CSRF cookie must not")
			}
			if sc.SameSite != http.SameSiteLaxMode || sc.Path != "/" || sc.Secure != tc.secure || cc.Secure != tc.secure {
				t.Fatalf("attributes: %+v", sc)
			}
			if cc.Value != csrfToken("tok") {
				t.Fatal("csrf cookie value")
			}
		})
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: NewService`, `undefined: sessionCookie`, ...

- [ ] **Step 3: Implement the service**

`server/internal/auth/service.go`:
```go
package auth

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/config"
	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

// Service is the auth module: accounts, profiles, sessions, invites, sign-in
// and access policy. It is safe for concurrent use.
type Service struct {
	db    *db.DB
	cfg   config.Config
	log   *slog.Logger
	now   func() time.Time
	argon argonParams // tests swap in cheap parameters
	limit *limiter

	dummyOnce sync.Once
	dummyHash string

	mu          sync.Mutex
	enrollments map[string]enrollment // pending setup-wizard TOTP enrollments, by id
}

// enrollment is a TOTP secret shown to the first admin during setup but not
// yet confirmed with a code.
type enrollment struct {
	secret  string
	expires time.Time
}

// NewService creates the auth service. now is the clock (time.Now in production).
func NewService(d *db.DB, cfg config.Config, log *slog.Logger, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	if cfg.PublicURL == "" {
		log.Warn("auth: LISIMFLIX_PUBLIC_URL is not set; Google/Microsoft sign-in stays disabled until it is")
	}
	return &Service{
		db:          d,
		cfg:         cfg,
		log:         log,
		now:         now,
		argon:       defaultArgon,
		limit:       newLimiter(now),
		enrollments: map[string]enrollment{},
	}
}

// Name implements app.Module.
func (s *Service) Name() string { return "auth" }

func (s *Service) hashPassword(pw string) (string, error) { return hashPasswordWith(pw, s.argon) }

// dummyVerify spends the same time as a real password check, so unknown
// usernames cannot be told apart from wrong passwords by timing.
func (s *Service) dummyVerify(pw string) {
	s.dummyOnce.Do(func() { s.dummyHash, _ = hashPasswordWith("lisimflix-dummy-password", s.argon) })
	_, _ = verifyPassword(pw, s.dummyHash)
}

func (s *Service) internalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	s.log.Error("auth: "+msg, "err", err, "path", r.URL.Path)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal server error")
}

// clientIP is the TCP peer address. X-Forwarded-For is deliberately ignored:
// it is client-controlled unless a trusted proxy is configured (phase 4).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isHTTPS reports whether the browser reached us over HTTPS (directly or via
// a TLS-terminating proxy). Cookies are only marked Secure then, because
// browsers drop Secure cookies on plain-HTTP LAN addresses like http://zraid:8420.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// truncate limits free-text fields that come from clients.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
```

- [ ] **Step 4: Implement sessions and cookies**

`server/internal/auth/sessions.go`:
```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	sessionTTL        = 30 * 24 * time.Hour  // idle lifetime; every use slides it forward
	sessionMaxAge     = 180 * 24 * time.Hour // absolute lifetime from sign-in
	sessionTouchEvery = time.Hour            // throttle for last_seen/expiry writes

	sessionCookie = "lisimflix_session" // HttpOnly; the session token
	csrfCookie    = "lisimflix_csrf"    // readable by the web app; echo it in csrfHeader
	csrfHeader    = "X-Lisimflix-CSRF"
)

var errNoSession = errors.New("no valid session")

// session is a signed-in device, joined with the owning user's flags.
type session struct {
	ID         int64
	UserID     int64
	ProfileID  int64 // 0 = none selected
	IdentityID int64 // 0 = local sign-in
	Method     string
	DeviceName string
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IsAdmin    bool
}

type newSession struct {
	UserID     int64
	ProfileID  int64
	IdentityID int64
	Method     string // "local" | "oauth:<provider>"
	DeviceName string
	UserAgent  string
	IP         string
}

func sessionFromRequest(r *http.Request, userID, identityID int64, method, deviceName string) newSession {
	if deviceName == "" {
		deviceName = "Unknown device"
	}
	return newSession{
		UserID:     userID,
		IdentityID: identityID,
		Method:     method,
		DeviceName: truncate(deviceName, 100),
		UserAgent:  truncate(r.UserAgent(), 300),
		IP:         clientIP(r),
	}
}

// createSession stores a new session and returns its token (shown once).
func (s *Service) createSession(ctx context.Context, ns newSession) (string, session, error) {
	token, err := newToken()
	if err != nil {
		return "", session{}, err
	}
	now := s.now()
	exp := now.Add(sessionTTL)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return "", session{}, fmt.Errorf("purge expired sessions: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(user_id, profile_id, identity_id, token_hash, method, device_name,
		                     user_agent, ip, created_at, last_seen_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ns.UserID, nullInt(ns.ProfileID), nullInt(ns.IdentityID), hashToken(token), ns.Method,
		ns.DeviceName, ns.UserAgent, ns.IP, now.Unix(), now.Unix(), exp.Unix())
	if err != nil {
		return "", session{}, fmt.Errorf("insert session: %w", err)
	}
	id, _ := res.LastInsertId()
	return token, session{
		ID: id, UserID: ns.UserID, ProfileID: ns.ProfileID, IdentityID: ns.IdentityID,
		Method: ns.Method, DeviceName: ns.DeviceName, UserAgent: ns.UserAgent, IP: ns.IP,
		CreatedAt: unix(now.Unix()), LastSeenAt: unix(now.Unix()), ExpiresAt: unix(exp.Unix()),
	}, nil
}

const sessionColumns = `s.id, s.user_id, s.profile_id, s.identity_id, s.method, s.device_name,
	s.user_agent, s.ip, s.created_at, s.last_seen_at, s.expires_at, u.is_admin`

func scanSession(sc interface{ Scan(...any) error }) (session, error) {
	var ss session
	var profile, identity sql.NullInt64
	var created, seen, expires int64
	err := sc.Scan(&ss.ID, &ss.UserID, &profile, &identity, &ss.Method, &ss.DeviceName,
		&ss.UserAgent, &ss.IP, &created, &seen, &expires, &ss.IsAdmin)
	ss.ProfileID, ss.IdentityID = profile.Int64, identity.Int64
	ss.CreatedAt, ss.LastSeenAt, ss.ExpiresAt = unix(created), unix(seen), unix(expires)
	return ss, err
}

// sessionByToken resolves a token to a live session of an enabled user and
// slides its expiry. Any failure to find one is errNoSession.
func (s *Service) sessionByToken(ctx context.Context, token string) (session, error) {
	if !validTokenFormat(token) {
		return session{}, errNoSession
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+`
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND u.disabled = 0`, hashToken(token))
	ss, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return session{}, errNoSession
	}
	if err != nil {
		return session{}, fmt.Errorf("load session: %w", err)
	}
	now := s.now()
	if !now.Before(ss.ExpiresAt) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, ss.ID)
		return session{}, errNoSession
	}
	if now.Sub(ss.LastSeenAt) >= sessionTouchEvery {
		exp := now.Add(sessionTTL)
		if hard := ss.CreatedAt.Add(sessionMaxAge); exp.After(hard) {
			exp = hard
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
			now.Unix(), exp.Unix(), ss.ID); err != nil {
			return session{}, fmt.Errorf("touch session: %w", err)
		}
		ss.LastSeenAt, ss.ExpiresAt = unix(now.Unix()), unix(exp.Unix())
	}
	return ss, nil
}

func (s *Service) listSessions(ctx context.Context, userID int64) ([]session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionColumns+`
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.user_id = ? AND s.expires_at > ? ORDER BY s.last_seen_at DESC, s.id DESC`,
		userID, s.now().Unix())
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	out := []session{}
	for rows.Next() {
		ss, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// revokeSession deletes one of userID's sessions. false = no such session.
func (s *Service) revokeSession(ctx context.Context, userID, sessionID int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND user_id = ?`, sessionID, userID)
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// revokeAllSessions is "sign out everywhere".
func (s *Service) revokeAllSessions(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("revoke all sessions: %w", err)
	}
	return nil
}

func (s *Service) setSessionProfile(ctx context.Context, sessionID, profileID int64) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET profile_id = ? WHERE id = ?`,
		nullInt(profileID), sessionID); err != nil {
		return fmt.Errorf("set session profile: %w", err)
	}
	return nil
}

// setSessionCookies sends the session cookie (HttpOnly) and the CSRF cookie
// (readable by the web app). Both are SameSite=Lax and Secure on HTTPS.
func setSessionCookies(w http.ResponseWriter, r *http.Request, token string) {
	secure := isHTTPS(r)
	maxAge := int(sessionMaxAge / time.Second)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookie, Value: csrfToken(token), Path: "/", MaxAge: maxAge,
		HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	secure := isHTTPS(r)
	for _, name := range []string{sessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: name == sessionCookie, Secure: secure, SameSite: http.SameSiteLaxMode,
		})
	}
}

func unix(sec int64) time.Time { return time.Unix(sec, 0).UTC() }
```

- [ ] **Step 5: Implement the guard**

`server/internal/auth/guard.go`:
```go
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

// guard is the real authctx.Guard.
//
// Credentials, in order:
//  1. "Authorization: Bearer <token>" (API clients, Android later). If this
//     header is present it must be valid; there is no fallback to cookies.
//  2. the lisimflix_session cookie (web app).
//
// CSRF: cookie-authenticated requests with an unsafe method (anything but
// GET/HEAD/OPTIONS) must send X-Lisimflix-CSRF equal to csrfToken(session
// token), which the web app reads from the lisimflix_csrf cookie or the
// sign-in response. Bearer requests are exempt: browsers never attach them
// automatically.
type guard struct{ s *Service }

// Guard returns the request guard backed by this service's sessions.
func (s *Service) Guard() authctx.Guard { return guard{s} }

func (g guard) RequireUser(next http.Handler) http.Handler  { return g.wrap(next, false) }
func (g guard) RequireAdmin(next http.Handler) http.Handler { return g.wrap(next, true) }

type sessionCtxKey struct{}

// currentSession returns the session the guard authenticated.
func currentSession(ctx context.Context) (session, bool) {
	ss, ok := ctx.Value(sessionCtxKey{}).(session)
	return ss, ok
}

func (g guard) wrap(next http.Handler, admin bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ss, token, viaCookie, err := g.s.authenticate(r)
		if errors.Is(err, errNoSession) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		if err != nil {
			g.s.internalError(w, r, "authenticate", err)
			return
		}
		if viaCookie && !safeMethod(r.Method) {
			got := r.Header.Get(csrfHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(csrfToken(token))) != 1 {
				httpx.WriteError(w, http.StatusForbidden, "csrf_failed", "missing or wrong "+csrfHeader+" header")
				return
			}
		}
		if admin && !ss.IsAdmin {
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "admin access required")
			return
		}
		ctx := authctx.WithPrincipal(r.Context(), authctx.Principal{
			UserID: ss.UserID, ProfileID: ss.ProfileID, IsAdmin: ss.IsAdmin,
		})
		ctx = context.WithValue(ctx, sessionCtxKey{}, ss)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authenticate finds the session for r. viaCookie reports which credential was used.
func (s *Service) authenticate(r *http.Request) (ss session, token string, viaCookie bool, err error) {
	if h := r.Header.Get("Authorization"); h != "" {
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			return session{}, "", false, errNoSession
		}
		tok = strings.TrimSpace(tok)
		ss, err := s.sessionByToken(r.Context(), tok)
		return ss, tok, false, err
	}
	c, cerr := r.Cookie(sessionCookie)
	if cerr != nil {
		return session{}, "", false, errNoSession
	}
	ss, err = s.sessionByToken(r.Context(), c.Value)
	return ss, c.Value, true, err
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}
```

- [ ] **Step 6: Add the module's route table** (empty groups for now; each later task adds its routes and shows the whole file)

`server/internal/auth/routes.go`:
```go
package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

// Mount implements app.Module. r is already scoped to /api/v1.
func (s *Service) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/auth", func(r chi.Router) { s.authRoutes(r, g) })
	r.Route("/users", func(r chi.Router) { s.userRoutes(r, g) })
	r.Route("/profiles", func(r chi.Router) { s.profileRoutes(r, g) })
	r.Route("/invites", func(r chi.Router) { s.inviteRoutes(r, g) })
}

func (s *Service) authRoutes(r chi.Router, g authctx.Guard) {}

func (s *Service) userRoutes(r chi.Router, g authctx.Guard) {}

func (s *Service) profileRoutes(r chi.Router, g authctx.Guard) {}

func (s *Service) inviteRoutes(r chi.Router, g authctx.Guard) {}
```

- [ ] **Step 7: Run tests, expect PASS**

Run: `make test`
Expected: all `internal/auth` tests PASS, including `TestSessionExpiryAndSliding` (idle expiry at 30 days, sliding use until the 180-day cap).

- [ ] **Step 8: Commit**

```bash
git add server/internal/auth
git commit -m "feat(auth): service, hashed per-device sessions and guard with CSRF check"
```

---

### Task 6: Setup wizard (first admin with password + TOTP), `/auth/me`, logout

**Files:**
- Create: `server/internal/auth/users.go`, `server/internal/auth/setup.go`
- Modify: `server/internal/auth/routes.go` (full file below)
- Test: `server/internal/auth/setup_test.go`

**Interfaces:**
- Consumes: Tasks 2-5 (`validatePassword`, `newTOTPKey`, `checkTOTP`, `createSession`, `setSessionCookies`, `currentSession`, harness, `code`).
- Produces:
  - `users.go`: `errNotFound`; `type policy struct{LibraryIDs []int64; MaxContentRating string; MaxBitrateKbps, MaxStreams int64}` (JSON `library_ids, max_content_rating, max_bitrate_kbps, max_streams`); `type user struct{ID; Username, DisplayName, Email; IsAdmin, Disabled; PasswordHash, TOTPSecret; TOTPLastStep; Policy policy; CreatedAt}`; `type userJSON` + `(user) toJSON() userJSON`; `userColumns`, `scanUser(sc)`; `(*Service) getUser(ctx, id)`, `getUserByUsername(ctx, name)`, `adminExists(ctx) (bool, error)`; `libraryAccess(ctx, q querier, userID) ([]int64, error)`; `replaceLibraryAccessTx(ctx, tx, userID, ids) error`; `type newUser{Username, DisplayName, Email; IsAdmin; PasswordHash, TOTPSecret; TOTPLastStep; Policy}`; `insertUserTx(ctx, tx, newUser, now) (int64, error)` (also creates the primary profile and library grants); `sortedIDs([]int64) []int64`.
  - `setup.go`: `requireJSON` middleware (415 `unsupported_media_type`); `type sessionResponse{Token, CSRFToken, ExpiresAt, User}`; `(*Service) startSession(w, r, status, newSession, user)`; handlers `handleSetupStatus`, `handleSetupTOTP`, `handleSetup`, `handleMe`, `handleLogout`; constants `enrollmentTTL = 15m`, `maxEnrollments = 20`, `maxProfileName = 50`; `usernamePattern`. Error codes: 409 `setup_complete`, 400 `invalid_username` / `weak_password` / `enrollment_expired` / `invalid_totp`, 429 `too_many_attempts`.
  - Test helpers: `(*harness) enroll(username) enrollResp`, `(*harness) setupAdmin(username, password) (sessionResponse, totpSecret string)`.

- [ ] **Step 1: Write the failing tests**

`server/internal/auth/setup_test.go`:
```go
package auth

import (
	"net/http"
	"strings"
	"testing"
)

type enrollResp struct {
	EnrollmentID string `json:"enrollment_id"`
	Secret       string `json:"secret"`
	OTPAuthURL   string `json:"otpauth_url"`
}

func (h *harness) enroll(username string) enrollResp {
	h.t.Helper()
	rec := h.do("POST", "/api/v1/auth/setup/totp", map[string]string{"username": username})
	wantStatus(h.t, rec, 201)
	return decodeBody[enrollResp](h.t, rec)
}

// setupAdmin runs the whole wizard and returns the session response and TOTP secret.
func (h *harness) setupAdmin(username, password string) (sessionResponse, string) {
	h.t.Helper()
	en := h.enroll(username)
	rec := h.do("POST", "/api/v1/auth/setup", map[string]string{
		"username": username, "password": password, "enrollment_id": en.EnrollmentID,
		"totp_code": code(h.t, en.Secret, h.clock.Now()), "device_name": "Zac's PC",
	})
	wantStatus(h.t, rec, 201)
	return decodeBody[sessionResponse](h.t, rec), en.Secret
}

func TestSetupWizard(t *testing.T) {
	h := newHarness(t)

	rec := h.do("GET", "/api/v1/auth/setup", nil)
	wantStatus(t, rec, 200)
	if !decodeBody[map[string]bool](t, rec)["needed"] {
		t.Fatal("setup should be needed on a fresh install")
	}

	en := h.enroll("zac")
	if en.Secret == "" || !strings.HasPrefix(en.OTPAuthURL, "otpauth://totp/Lisimflix:zac") {
		t.Fatalf("enrollment %+v", en)
	}
	body := map[string]string{
		"username": "zac", "password": "a long enough password", "display_name": "Zac",
		"enrollment_id": en.EnrollmentID, "totp_code": "000000",
	}
	rec = h.do("POST", "/api/v1/auth/setup", body)
	if rec.Code != 400 || errCode(t, rec) != "invalid_totp" {
		t.Fatalf("wrong code: %d %s", rec.Code, rec.Body.String())
	}

	body["totp_code"] = code(t, en.Secret, h.clock.Now())
	rec = h.do("POST", "/api/v1/auth/setup", body)
	wantStatus(t, rec, 201)
	resp := decodeBody[sessionResponse](t, rec)
	if !validTokenFormat(resp.Token) || resp.CSRFToken != csrfToken(resp.Token) || !resp.User.IsAdmin || !resp.User.HasPassword {
		t.Fatalf("setup response %+v", resp)
	}
	if c := findCookie(rec, sessionCookie); c == nil || c.Value != resp.Token || !c.HttpOnly {
		t.Fatal("session cookie not set")
	}

	var hash string
	var step int64
	h.d.QueryRow(`SELECT password_hash, totp_last_step FROM users WHERE username = 'zac'`).Scan(&hash, &step)
	if !strings.HasPrefix(hash, "$argon2id$") || step != h.clock.Now().Unix()/totpPeriod {
		t.Fatalf("stored hash %q step %d", hash, step)
	}

	rec = h.do("GET", "/api/v1/auth/me", nil, bearer(resp.Token))
	wantStatus(t, rec, 200)
	me := decodeBody[struct {
		User      userJSON `json:"user"`
		ProfileID int64    `json:"profile_id"`
	}](t, rec)
	if me.User.Username != "zac" || me.User.DisplayName != "Zac" || !me.User.IsAdmin || me.ProfileID != 0 {
		t.Fatalf("me %+v", me)
	}
	if n := count(t, h.d, `SELECT count(*) FROM profiles WHERE user_id = ? AND is_primary = 1`, me.User.ID); n != 1 {
		t.Fatal("admin has no primary profile")
	}

	// after setup everything returns 409
	if decodeBody[map[string]bool](t, h.do("GET", "/api/v1/auth/setup", nil))["needed"] {
		t.Fatal("setup still needed")
	}
	rec = h.do("POST", "/api/v1/auth/setup/totp", map[string]string{"username": "evil"})
	if rec.Code != 409 || errCode(t, rec) != "setup_complete" {
		t.Fatalf("enroll after setup: %d", rec.Code)
	}
	// even a well-formed request is refused
	rec = h.do("POST", "/api/v1/auth/setup", map[string]string{
		"username": "evil", "password": "a long enough password",
		"enrollment_id": en.EnrollmentID, "totp_code": code(t, en.Secret, h.clock.Now()),
	})
	if rec.Code != 409 {
		t.Fatalf("second setup: %d", rec.Code)
	}
}

func TestSetupValidation(t *testing.T) {
	h := newHarness(t)
	en := h.enroll("zac")
	good := func() map[string]string {
		return map[string]string{
			"username": "zac", "password": "a long enough password",
			"enrollment_id": en.EnrollmentID, "totp_code": code(t, en.Secret, h.clock.Now()),
		}
	}
	cases := map[string]struct {
		mut  func(map[string]string)
		code string
	}{
		"bad username":   {func(m map[string]string) { m["username"] = "a b" }, "invalid_username"},
		"short password": {func(m map[string]string) { m["password"] = "short" }, "weak_password"},
		"unknown enroll": {func(m map[string]string) { m["enrollment_id"] = "nope" }, "enrollment_expired"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := good()
			c.mut(b)
			rec := h.do("POST", "/api/v1/auth/setup", b)
			if rec.Code != 400 || errCode(t, rec) != c.code {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
		})
	}

	rec := h.do("POST", "/api/v1/auth/setup/totp", nil, withHeader("Content-Type", "application/x-www-form-urlencoded"))
	wantStatus(t, rec, http.StatusUnsupportedMediaType)

	h.clock.Advance(enrollmentTTL)
	rec = h.do("POST", "/api/v1/auth/setup", good())
	if rec.Code != 400 || errCode(t, rec) != "enrollment_expired" {
		t.Fatalf("expired enrollment: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.setupAdmin("zac", "a long enough password")
	rec := h.do("POST", "/api/v1/auth/logout", nil, cookieAuth(resp.Token))
	wantStatus(t, rec, 204)
	if c := findCookie(rec, sessionCookie); c == nil || c.MaxAge >= 0 {
		t.Fatal("session cookie not cleared")
	}
	wantStatus(t, h.do("GET", "/api/v1/auth/me", nil, bearer(resp.Token)), 401)
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: sessionResponse`, `undefined: userJSON`.

- [ ] **Step 3: Implement the user store**

`server/internal/auth/users.go`:
```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

var errNotFound = errors.New("not found")

// policy is a user's access policy (spec 3.3). Zero values mean unrestricted,
// except LibraryIDs: it is an explicit allow-list for non-admins (empty = no
// libraries). Admins always see every library.
type policy struct {
	LibraryIDs       []int64 `json:"library_ids"`
	MaxContentRating string  `json:"max_content_rating"`
	MaxBitrateKbps   int64   `json:"max_bitrate_kbps"`
	MaxStreams       int64   `json:"max_streams"`
}

type user struct {
	ID           int64
	Username     string // "" for OAuth-only users
	DisplayName  string
	Email        string
	IsAdmin      bool
	Disabled     bool
	PasswordHash string
	TOTPSecret   string
	TOTPLastStep int64
	Policy       policy
	CreatedAt    time.Time
}

// userJSON is the API shape of a user. Secrets never appear in it.
type userJSON struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username,omitempty"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email,omitempty"`
	IsAdmin     bool      `json:"is_admin"`
	Disabled    bool      `json:"disabled"`
	HasPassword bool      `json:"has_password"`
	Policy      policy    `json:"policy"`
	CreatedAt   time.Time `json:"created_at"`
}

func (u user) toJSON() userJSON {
	p := u.Policy
	if p.LibraryIDs == nil {
		p.LibraryIDs = []int64{}
	}
	return userJSON{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email,
		IsAdmin: u.IsAdmin, Disabled: u.Disabled, HasPassword: u.PasswordHash != "",
		Policy: p, CreatedAt: u.CreatedAt,
	}
}

const userColumns = `id, username, display_name, email, is_admin, disabled, password_hash,
	totp_secret, totp_last_step, max_content_rating, max_bitrate_kbps, max_streams, created_at`

func scanUser(sc interface{ Scan(...any) error }) (user, error) {
	var u user
	var username, pw, totpSecret sql.NullString
	var created int64
	err := sc.Scan(&u.ID, &username, &u.DisplayName, &u.Email, &u.IsAdmin, &u.Disabled, &pw,
		&totpSecret, &u.TOTPLastStep, &u.Policy.MaxContentRating, &u.Policy.MaxBitrateKbps,
		&u.Policy.MaxStreams, &created)
	u.Username, u.PasswordHash, u.TOTPSecret = username.String, pw.String, totpSecret.String
	u.CreatedAt = unix(created)
	return u, err
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Service) loadUser(ctx context.Context, where string, arg any) (user, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return user{}, errNotFound
	}
	if err != nil {
		return user{}, fmt.Errorf("load user: %w", err)
	}
	if u.Policy.LibraryIDs, err = libraryAccess(ctx, s.db, u.ID); err != nil {
		return user{}, err
	}
	return u, nil
}

func (s *Service) getUser(ctx context.Context, id int64) (user, error) {
	return s.loadUser(ctx, "id = ?", id)
}

func (s *Service) getUserByUsername(ctx context.Context, username string) (user, error) {
	return s.loadUser(ctx, "username = ?", username)
}

func (s *Service) adminExists(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE is_admin = 1)`).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check admin: %w", err)
	}
	return n == 1, nil
}

// libraryAccess returns the user's allow-listed library ids, ascending.
func libraryAccess(ctx context.Context, q querier, userID int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT library_id FROM user_library_access WHERE user_id = ? ORDER BY library_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("library access: %w", err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func replaceLibraryAccessTx(ctx context.Context, tx *sql.Tx, userID int64, ids []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_library_access WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("clear library access: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO user_library_access(user_id, library_id) VALUES(?, ?)`, userID, id); err != nil {
			return fmt.Errorf("grant library %d: %w", id, err)
		}
	}
	return nil
}

type newUser struct {
	Username     string // "" for OAuth-only users
	DisplayName  string
	Email        string
	IsAdmin      bool
	PasswordHash string
	TOTPSecret   string
	TOTPLastStep int64
	Policy       policy
}

// insertUserTx creates the user, its primary profile (named after the user)
// and its library allow-list. Every account has at least one profile.
func insertUserTx(ctx context.Context, tx *sql.Tx, u newUser, now time.Time) (int64, error) {
	ts := now.Unix()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO users(username, display_name, email, is_admin, password_hash, totp_secret,
		                  totp_last_step, max_content_rating, max_bitrate_kbps, max_streams,
		                  created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullString(u.Username), u.DisplayName, u.Email, u.IsAdmin, nullString(u.PasswordHash),
		nullString(u.TOTPSecret), u.TOTPLastStep, u.Policy.MaxContentRating,
		u.Policy.MaxBitrateKbps, u.Policy.MaxStreams, ts, ts)
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO profiles(user_id, name, is_primary, created_at, updated_at) VALUES(?, ?, 1, ?, ?)`,
		id, truncate(u.DisplayName, maxProfileName), ts, ts); err != nil {
		return 0, fmt.Errorf("insert primary profile: %w", err)
	}
	if err := replaceLibraryAccessTx(ctx, tx, id, u.Policy.LibraryIDs); err != nil {
		return 0, err
	}
	return id, nil
}

// sortedIDs returns a sorted, de-duplicated, never-nil copy.
func sortedIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
```

- [ ] **Step 4: Implement the wizard, `/me` and logout**

`server/internal/auth/setup.go`:
```go
package auth

import (
	"errors"
	"mime"
	"net/http"
	"regexp"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

const (
	enrollmentTTL  = 15 * time.Minute
	maxEnrollments = 20
	maxProfileName = 50
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,31}$`)

// requireJSON guards unauthenticated POST endpoints: a cross-site HTML form
// cannot send Content-Type application/json without a CORS preflight, which
// we never approve. Together with SameSite=Lax this blocks login CSRF.
func requireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			httpx.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "send Content-Type: application/json")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type sessionResponse struct {
	Token     string    `json:"token"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      userJSON  `json:"user"`
}

// startSession creates a session, sets the cookies and writes the token response.
func (s *Service) startSession(w http.ResponseWriter, r *http.Request, status int, ns newSession, u user) {
	token, ss, err := s.createSession(r.Context(), ns)
	if err != nil {
		s.internalError(w, r, "create session", err)
		return
	}
	setSessionCookies(w, r, token)
	httpx.WriteJSON(w, status, sessionResponse{
		Token: token, CSRFToken: csrfToken(token), ExpiresAt: ss.ExpiresAt, User: u.toJSON(),
	})
}

// GET /auth/setup
func (s *Service) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	done, err := s.adminExists(r.Context())
	if err != nil {
		s.internalError(w, r, "setup status", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"needed": !done})
}

func (s *Service) setupDone(w http.ResponseWriter, r *http.Request) bool {
	done, err := s.adminExists(r.Context())
	if err != nil {
		s.internalError(w, r, "setup status", err)
		return true
	}
	if done {
		httpx.WriteError(w, http.StatusConflict, "setup_complete", "the server already has an admin")
		return true
	}
	return false
}

// POST /auth/setup/totp: step 1 of the wizard, issue a TOTP secret to scan.
func (s *Service) handleSetupTOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if s.setupDone(w, r) {
		return
	}
	if !usernamePattern.MatchString(in.Username) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_username", "username must be 3-32 letters, digits, '.', '_' or '-'")
		return
	}
	key, err := newTOTPKey(in.Username)
	if err != nil {
		s.internalError(w, r, "generate totp", err)
		return
	}
	id, err := newToken()
	if err != nil {
		s.internalError(w, r, "enrollment id", err)
		return
	}
	now := s.now()
	s.mu.Lock()
	for k, e := range s.enrollments {
		if !now.Before(e.expires) {
			delete(s.enrollments, k)
		}
	}
	if len(s.enrollments) >= maxEnrollments {
		s.mu.Unlock()
		httpx.WriteError(w, http.StatusTooManyRequests, "too_many_attempts", "too many pending setups, try again later")
		return
	}
	s.enrollments[id] = enrollment{secret: key.Secret(), expires: now.Add(enrollmentTTL)}
	s.mu.Unlock()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"enrollment_id": id,
		"secret":        key.Secret(),
		"otpauth_url":   key.URL(),
		"expires_at":    now.Add(enrollmentTTL).UTC(),
	})
}

// POST /auth/setup: step 2, create the first admin and sign them in.
func (s *Service) handleSetup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		DisplayName  string `json:"display_name"`
		EnrollmentID string `json:"enrollment_id"`
		TOTPCode     string `json:"totp_code"`
		DeviceName   string `json:"device_name"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if s.setupDone(w, r) {
		return
	}
	if !usernamePattern.MatchString(in.Username) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_username", "username must be 3-32 letters, digits, '.', '_' or '-'")
		return
	}
	if err := validatePassword(in.Password); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	now := s.now()
	s.mu.Lock()
	en, ok := s.enrollments[in.EnrollmentID]
	s.mu.Unlock()
	if !ok || !now.Before(en.expires) {
		httpx.WriteError(w, http.StatusBadRequest, "enrollment_expired", "start TOTP enrollment again")
		return
	}
	step, ok := checkTOTP(en.secret, in.TOTPCode, now, 0)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_totp", "the authenticator code is wrong; check the phone's clock")
		return
	}
	hash, err := s.hashPassword(in.Password)
	if err != nil {
		s.internalError(w, r, "hash password", err)
		return
	}
	display := truncate(in.DisplayName, 100)
	if display == "" {
		display = in.Username
	}

	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.internalError(w, r, "begin setup", err)
		return
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE is_admin = 1)`).Scan(&exists); err != nil {
		s.internalError(w, r, "setup check", err)
		return
	}
	if exists == 1 {
		httpx.WriteError(w, http.StatusConflict, "setup_complete", "the server already has an admin")
		return
	}
	uid, err := insertUserTx(ctx, tx, newUser{
		Username: in.Username, DisplayName: display, IsAdmin: true,
		PasswordHash: hash, TOTPSecret: en.secret, TOTPLastStep: step,
	}, now)
	if err != nil {
		s.internalError(w, r, "create admin", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.internalError(w, r, "commit setup", err)
		return
	}
	s.mu.Lock()
	delete(s.enrollments, in.EnrollmentID)
	s.mu.Unlock()
	s.log.Info("auth: setup complete, admin created", "user_id", uid, "username", in.Username)

	u, err := s.getUser(ctx, uid)
	if err != nil {
		s.internalError(w, r, "load admin", err)
		return
	}
	s.startSession(w, r, http.StatusCreated, sessionFromRequest(r, uid, 0, "local", in.DeviceName), u)
}

// GET /auth/me
func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	ss, _ := currentSession(r.Context())
	u, err := s.getUser(r.Context(), ss.UserID)
	if errors.Is(err, errNotFound) {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	if err != nil {
		s.internalError(w, r, "load me", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user": u.toJSON(), "session_id": ss.ID, "profile_id": ss.ProfileID,
	})
}

// POST /auth/logout: end this session only.
func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	ss, _ := currentSession(r.Context())
	p, _ := authctx.FromContext(r.Context())
	if _, err := s.revokeSession(r.Context(), p.UserID, ss.ID); err != nil {
		s.internalError(w, r, "logout", err)
		return
	}
	clearSessionCookies(w, r)
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 5: Register the routes**

`server/internal/auth/routes.go`:
```go
package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

// Mount implements app.Module. r is already scoped to /api/v1.
func (s *Service) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/auth", func(r chi.Router) { s.authRoutes(r, g) })
	r.Route("/users", func(r chi.Router) { s.userRoutes(r, g) })
	r.Route("/profiles", func(r chi.Router) { s.profileRoutes(r, g) })
	r.Route("/invites", func(r chi.Router) { s.inviteRoutes(r, g) })
}

func (s *Service) authRoutes(r chi.Router, g authctx.Guard) {
	// public
	r.Get("/setup", s.handleSetupStatus)
	r.With(requireJSON).Post("/setup/totp", s.handleSetupTOTP)
	r.With(requireJSON).Post("/setup", s.handleSetup)
	// signed in
	r.With(g.RequireUser).Get("/me", s.handleMe)
	r.With(g.RequireUser).Post("/logout", s.handleLogout)
}

func (s *Service) userRoutes(r chi.Router, g authctx.Guard) {}

func (s *Service) profileRoutes(r chi.Router, g authctx.Guard) {}

func (s *Service) inviteRoutes(r chi.Router, g authctx.Guard) {}
```

- [ ] **Step 6: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 7: Commit**

```bash
git add server/internal/auth
git commit -m "feat(auth): setup wizard creates the first admin with Argon2id password and TOTP"
```

---

### Task 7: Local admin login with lockout; session list, revoke, sign out everywhere

**Files:**
- Create: `server/internal/auth/login.go`
- Modify: `server/internal/auth/routes.go` (full file below)
- Test: `server/internal/auth/login_test.go`

**Interfaces:**
- Consumes: Tasks 2-6 (`verifyPassword`, `checkTOTP`, `limiter`, `getUserByUsername`, `dummyVerify`, `startSession`, `listSessions`, `revokeSession`, `revokeAllSessions`, `setupAdmin`).
- Produces: `tooManyAttempts(w, wait)` (429 + `Retry-After` seconds); `type sessionJSON` + `sessionsJSON(list []session, currentID int64) []sessionJSON`; `pathID(w, r, name string) (int64, bool)` (400 `invalid_id`); handlers `handleLogin` (200 session or 401 `invalid_credentials` / 429 `too_many_attempts`), `handleListSessions`, `handleRevokeSession` (404 `not_found`), `handleLogoutAll`. Test helpers `fromIP(ip) reqOpt`, `adminPW`, `loginBody(...)`.

- [ ] **Step 1: Write the failing tests**

`server/internal/auth/login_test.go`:
```go
package auth

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func fromIP(ip string) reqOpt {
	return func(r *http.Request) { r.RemoteAddr = ip + ":40000" }
}

const adminPW = "a long enough password"

func loginBody(user, pw, totpCode string) map[string]string {
	return map[string]string{"username": user, "password": pw, "totp_code": totpCode, "device_name": "Laptop"}
}

func TestLocalLogin(t *testing.T) {
	h := newHarness(t)
	_, secret := h.setupAdmin("zac", adminPW)

	// the code used during setup cannot be replayed
	rec := h.do("POST", "/api/v1/auth/login", loginBody("zac", adminPW, code(t, secret, h.clock.Now())))
	if rec.Code != 401 || errCode(t, rec) != "invalid_credentials" {
		t.Fatalf("replayed setup code: %d %s", rec.Code, rec.Body.String())
	}

	h.clock.Advance(30 * time.Second)
	c := code(t, secret, h.clock.Now())
	rec = h.do("POST", "/api/v1/auth/login", loginBody("ZAC", adminPW, c)) // usernames are case-insensitive
	wantStatus(t, rec, 200)
	resp := decodeBody[sessionResponse](t, rec)
	if !resp.User.IsAdmin || findCookie(rec, sessionCookie) == nil {
		t.Fatalf("login response %+v", resp)
	}
	wantStatus(t, h.do("GET", "/api/v1/auth/me", nil, bearer(resp.Token)), 200)

	// same code again: replay refused
	wantStatus(t, h.do("POST", "/api/v1/auth/login", loginBody("zac", adminPW, c)), 401)

	h.clock.Advance(30 * time.Second)
	for name, body := range map[string]map[string]string{
		"wrong password": loginBody("zac", "wrong password!!", code(t, secret, h.clock.Now())),
		"wrong code":     loginBody("zac", adminPW, "123456"),
		"unknown user":   loginBody("nobody", adminPW, code(t, secret, h.clock.Now())),
		"no code":        loginBody("zac", adminPW, ""),
	} {
		rec := h.do("POST", "/api/v1/auth/login", body, fromIP(fmt.Sprintf("10.0.0.%d", len(name))))
		if rec.Code != 401 || errCode(t, rec) != "invalid_credentials" {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestLoginLockout(t *testing.T) {
	h := newHarness(t)
	_, secret := h.setupAdmin("zac", adminPW)
	ip := fromIP("203.0.113.9")
	h.clock.Advance(30 * time.Second) // move past the TOTP step used during setup

	for i := 0; i < 6; i++ {
		wantStatus(t, h.do("POST", "/api/v1/auth/login", loginBody("zac", "wrong password!!", "000000"), ip), 401)
	}
	if !strings.Contains(h.logs.String(), "login locked") {
		t.Fatal("lockout was not logged for the admin")
	}

	h.clock.Advance(10 * time.Second)
	good := func() map[string]string { return loginBody("zac", adminPW, code(t, secret, h.clock.Now())) }
	rec := h.do("POST", "/api/v1/auth/login", good(), ip)
	if rec.Code != 429 || errCode(t, rec) != "too_many_attempts" || rec.Header().Get("Retry-After") != "20" {
		t.Fatalf("locked: %d %s Retry-After=%q", rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
	}
	// another IP is not locked out by this one
	wantStatus(t, h.do("POST", "/api/v1/auth/login", good(), fromIP("198.51.100.7")), 200)

	h.clock.Advance(30 * time.Second)
	wantStatus(t, h.do("POST", "/api/v1/auth/login", good(), ip), 200)
}

func TestSessionManagement(t *testing.T) {
	h := newHarness(t)
	uid := h.insertUser("Ann", false)
	a, b := h.sessionFor(uid), h.sessionFor(uid)
	other := h.sessionFor(h.insertUser("Bob", false))

	rec := h.do("GET", "/api/v1/auth/sessions", nil, bearer(a))
	wantStatus(t, rec, 200)
	list := decodeBody[struct{ Sessions []sessionJSON }](t, rec).Sessions
	if len(list) != 2 {
		t.Fatalf("sessions %+v", list)
	}
	var currentID, otherID int64
	for _, ss := range list {
		if ss.Current {
			currentID = ss.ID
		} else {
			otherID = ss.ID
		}
	}
	if currentID == 0 || otherID == 0 {
		t.Fatalf("current flag wrong: %+v", list)
	}

	// cannot revoke someone else's session
	bobList := decodeBody[struct{ Sessions []sessionJSON }](t, h.do("GET", "/api/v1/auth/sessions", nil, bearer(other))).Sessions
	wantStatus(t, h.do("DELETE", fmt.Sprintf("/api/v1/auth/sessions/%d", bobList[0].ID), nil, bearer(a)), 404)
	wantStatus(t, h.do("DELETE", "/api/v1/auth/sessions/abc", nil, bearer(a)), 400)

	wantStatus(t, h.do("DELETE", fmt.Sprintf("/api/v1/auth/sessions/%d", otherID), nil, bearer(a)), 204)
	wantStatus(t, h.do("GET", "/api/v1/auth/me", nil, bearer(b)), 401)
	wantStatus(t, h.do("GET", "/api/v1/auth/me", nil, bearer(a)), 200)

	c := h.sessionFor(uid)
	wantStatus(t, h.do("POST", "/api/v1/auth/logout-all", nil, bearer(a)), 204)
	wantStatus(t, h.do("GET", "/api/v1/auth/me", nil, bearer(a)), 401)
	wantStatus(t, h.do("GET", "/api/v1/auth/me", nil, bearer(c)), 401)
	wantStatus(t, h.do("GET", "/api/v1/auth/me", nil, bearer(other)), 200)
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL. `/api/v1/auth/login` returns 404 and `undefined: sessionJSON`.

- [ ] **Step 3: Implement**

`server/internal/auth/login.go`:
```go
package auth

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

func tooManyAttempts(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	httpx.WriteError(w, http.StatusTooManyRequests, "too_many_attempts",
		fmt.Sprintf("too many failed attempts, try again in %s", wait.Round(time.Second)))
}

// POST /auth/login: local sign-in with password + TOTP (the admin fallback, spec 6.1).
func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		TOTPCode   string `json:"totp_code"`
		DeviceName string `json:"device_name"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	ip := clientIP(r)
	key := "login:" + strings.ToLower(in.Username) + "|" + ip
	if ok, wait := s.limit.allow(key); !ok {
		tooManyAttempts(w, wait)
		return
	}
	ctx := r.Context()
	u, err := s.getUserByUsername(ctx, in.Username)
	if err != nil && !errors.Is(err, errNotFound) {
		s.internalError(w, r, "login lookup", err)
		return
	}
	valid := false
	if err == nil && u.PasswordHash != "" && u.TOTPSecret != "" && !u.Disabled {
		pwOK, err := verifyPassword(in.Password, u.PasswordHash)
		if err != nil {
			s.internalError(w, r, "verify password", err)
			return
		}
		if pwOK {
			if step, ok := checkTOTP(u.TOTPSecret, in.TOTPCode, s.now(), u.TOTPLastStep); ok {
				// Conditional update: two concurrent logins with one code cannot both win.
				res, err := s.db.ExecContext(ctx,
					`UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`, step, u.ID, step)
				if err != nil {
					s.internalError(w, r, "record totp step", err)
					return
				}
				n, _ := res.RowsAffected()
				valid = n == 1
			}
		}
	} else {
		s.dummyVerify(in.Password)
	}
	if !valid {
		if d := s.limit.fail(key); d > 0 {
			// Admin alert hook: phase 3's admin dashboard surfaces these warnings.
			s.log.Warn("auth: login locked after repeated failures", "username", in.Username, "ip", ip, "lock", d.String())
		}
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "wrong username, password or authenticator code")
		return
	}
	s.limit.success(key)
	s.log.Info("auth: local sign-in", "user_id", u.ID, "ip", ip)
	s.startSession(w, r, http.StatusOK, sessionFromRequest(r, u.ID, 0, "local", in.DeviceName), u)
}

type sessionJSON struct {
	ID         int64     `json:"id"`
	DeviceName string    `json:"device_name"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	Method     string    `json:"method"`
	ProfileID  int64     `json:"profile_id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

func sessionsJSON(list []session, currentID int64) []sessionJSON {
	out := make([]sessionJSON, 0, len(list))
	for _, ss := range list {
		out = append(out, sessionJSON{
			ID: ss.ID, DeviceName: ss.DeviceName, UserAgent: ss.UserAgent, IP: ss.IP,
			Method: ss.Method, ProfileID: ss.ProfileID, CreatedAt: ss.CreatedAt,
			LastSeenAt: ss.LastSeenAt, ExpiresAt: ss.ExpiresAt, Current: ss.ID == currentID,
		})
	}
	return out
}

// GET /auth/sessions: my signed-in devices.
func (s *Service) handleListSessions(w http.ResponseWriter, r *http.Request) {
	ss, _ := currentSession(r.Context())
	list, err := s.listSessions(r.Context(), ss.UserID)
	if err != nil {
		s.internalError(w, r, "list sessions", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": sessionsJSON(list, ss.ID)})
}

// DELETE /auth/sessions/{id}: sign one of my devices out.
func (s *Service) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	p, _ := authctx.FromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	found, err := s.revokeSession(r.Context(), p.UserID, id)
	if err != nil {
		s.internalError(w, r, "revoke session", err)
		return
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /auth/logout-all: sign out everywhere, including this device.
func (s *Service) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	p, _ := authctx.FromContext(r.Context())
	if err := s.revokeAllSessions(r.Context(), p.UserID); err != nil {
		s.internalError(w, r, "logout all", err)
		return
	}
	clearSessionCookies(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// pathID parses a positive integer URL parameter, writing a 400 if it is not one.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", name+" must be a positive integer")
		return 0, false
	}
	return id, true
}
```

- [ ] **Step 4: Register the routes**

`server/internal/auth/routes.go`:
```go
package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

// Mount implements app.Module. r is already scoped to /api/v1.
func (s *Service) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/auth", func(r chi.Router) { s.authRoutes(r, g) })
	r.Route("/users", func(r chi.Router) { s.userRoutes(r, g) })
	r.Route("/profiles", func(r chi.Router) { s.profileRoutes(r, g) })
	r.Route("/invites", func(r chi.Router) { s.inviteRoutes(r, g) })
}

func (s *Service) authRoutes(r chi.Router, g authctx.Guard) {
	// public
	r.Get("/setup", s.handleSetupStatus)
	r.With(requireJSON).Post("/setup/totp", s.handleSetupTOTP)
	r.With(requireJSON).Post("/setup", s.handleSetup)
	r.With(requireJSON).Post("/login", s.handleLogin)
	// signed in
	r.With(g.RequireUser).Get("/me", s.handleMe)
	r.With(g.RequireUser).Post("/logout", s.handleLogout)
	r.With(g.RequireUser).Post("/logout-all", s.handleLogoutAll)
	r.With(g.RequireUser).Get("/sessions", s.handleListSessions)
	r.With(g.RequireUser).Delete("/sessions/{id}", s.handleRevokeSession)
}

func (s *Service) userRoutes(r chi.Router, g authctx.Guard) {}

func (s *Service) profileRoutes(r chi.Router, g authctx.Guard) {}

func (s *Service) inviteRoutes(r chi.Router, g authctx.Guard) {}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/auth
git commit -m "feat(auth): local admin login with TOTP, lockout backoff and session management"
```

---

### Task 8: Policies: ratings, `VisibleLibraryIDs`, `MaxContentRating`, admin user API

**Files:**
- Create: `server/internal/auth/ratings.go`, `server/internal/auth/policy.go`, `server/internal/auth/users_admin.go`
- Modify: `server/internal/auth/routes.go` (full file below)
- Test: `server/internal/auth/ratings_test.go`, `server/internal/auth/policy_test.go`

**Interfaces:**
- Consumes: Tasks 5-7 (`getUser`, `libraryAccess`, `replaceLibraryAccessTx`, `revokeAllSessions`, `listSessions`, `sessionsJSON`, `pathID`, harness).
- Produces:
  - **Contract** `(*Service) VisibleLibraryIDs(ctx, authctx.Principal) ([]int64, error)` (admins: every ID in 1C's `libraries` table; users: their allow-list joined to `libraries`; empty = none; never nil) and `(*Service) MaxContentRating(ctx, authctx.Principal) (string, error)` with the semantics in "Design decisions".
  - **Additions** (exported, for later plans): `func RatingAge(rating string) (age int, known bool)`; `type StreamLimits struct{MaxBitrateKbps, MaxStreams int64}` and `(*Service) StreamLimits(ctx, authctx.Principal) (StreamLimits, error)` (0 = unlimited; for phase 2).
  - Internal: `validRating(r) bool`, `normalizeRating(r) string`, `stricterRating(a, b) string`, `validatePolicy(*policy) error`; handlers `handleListUsers`, `handleGetUser`, `handlePatchUser` (400 `cannot_disable_self`, `invalid_name`), `handlePutPolicy` (400 `invalid_policy`), `handleDeleteUser` (400 `cannot_delete_self`), `handleUserSessions`, `handleRevokeUserSessions`; `(*Service) userParam(w, r) (user, bool)`.

- [ ] **Step 1: Write the failing tests**

`server/internal/auth/ratings_test.go`:
```go
package auth

import "testing"

func TestRatingAge(t *testing.T) {
	for r, want := range map[string]int{"G": 0, "pg-13": 13, " MA15+ ": 15, "TV-MA": 17, "R18+": 18, "12A": 12} {
		got, ok := RatingAge(r)
		if !ok || got != want {
			t.Errorf("RatingAge(%q) = %d,%v want %d", r, got, ok, want)
		}
	}
	if age, ok := RatingAge("Unrated"); ok || age != 18 {
		t.Fatalf("unknown rating = %d,%v; want 18,false", age, ok)
	}
}

func TestStricterRating(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"", "", ""},
		{"", "PG", "PG"},
		{"M", "", "M"},
		{"PG", "M", "PG"},
		{"MA15+", "TV-Y7", "TV-Y7"},
	}
	for _, c := range cases {
		if got := stricterRating(c.a, c.b); got != c.want {
			t.Errorf("stricterRating(%q,%q) = %q want %q", c.a, c.b, got, c.want)
		}
	}
	if !validRating("") || !validRating("pg") || validRating("XYZ") {
		t.Fatal("validRating")
	}
}
```

`server/internal/auth/policy_test.go`:
```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/db"
)

// ensureLibraries creates plan 1C's libraries table if this branch does not
// have it yet (1B can merge before 1C), then inserts the given library ids.
// The columns match 1C's 20261025110000_library_libraries.sql.
func ensureLibraries(t *testing.T, d *db.DB, ids ...int64) {
	t.Helper()
	mustExec(t, d, `CREATE TABLE IF NOT EXISTS libraries (
		id         INTEGER PRIMARY KEY,
		name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
		type       TEXT NOT NULL,
		options    TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	) STRICT`)
	for _, id := range ids {
		mustExec(t, d, `INSERT INTO libraries(id, name, type, created_at, updated_at)
			VALUES(?, ?, 'movies', '2026-10-25T00:00:00Z', '2026-10-25T00:00:00Z')`, id, fmt.Sprintf("Library %d", id))
	}
}

func TestVisibleLibraryIDs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ensureLibraries(t, h.d, 4, 3, 2, 1)
	admin := h.insertUser("Root", true)
	ann := h.insertUser("Ann", false)
	// 99 is a library that was deleted (no FK, so the grant row survives)
	mustExec(t, h.d, `INSERT INTO user_library_access(user_id, library_id) VALUES(?, 3), (?, 1), (?, 99)`, ann, ann, ann)

	if got, err := h.s.VisibleLibraryIDs(ctx, authctx.Principal{UserID: admin, IsAdmin: true}); err != nil || !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("admin = %v, %v; want every library", got, err)
	}
	if got, _ := h.s.VisibleLibraryIDs(ctx, authctx.Principal{UserID: ann}); !reflect.DeepEqual(got, []int64{1, 3}) {
		t.Fatalf("ann = %v; want her allow-list without the deleted library", got)
	}
	// a user with no grants sees nothing (explicit allow-list)
	bob := h.insertUser("Bob", false)
	if got, err := h.s.VisibleLibraryIDs(ctx, authctx.Principal{UserID: bob}); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("bob = %#v, %v; want empty non-nil", got, err)
	}
	// admin-ness comes from the database, not from the principal
	if got, _ := h.s.VisibleLibraryIDs(ctx, authctx.Principal{UserID: bob, IsAdmin: true}); len(got) != 0 {
		t.Fatalf("forged admin principal = %v", got)
	}
	mustExec(t, h.d, `UPDATE users SET disabled = 1 WHERE id = ?`, ann)
	if got, _ := h.s.VisibleLibraryIDs(ctx, authctx.Principal{UserID: ann}); got == nil || len(got) != 0 {
		t.Fatalf("disabled ann = %v", got)
	}
	if _, err := h.s.VisibleLibraryIDs(ctx, authctx.Principal{UserID: 12345}); !errors.Is(err, errNotFound) {
		t.Fatalf("unknown user err = %v", err)
	}
}

func TestMaxContentRatingAndLimits(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	uid := h.insertUser("Family", false)
	parent := h.primaryProfile(uid)
	kids := mustExec(t, h.d, `INSERT INTO profiles(user_id, name, max_content_rating, created_at, updated_at) VALUES(?, 'Kids', 'PG', 1, 1)`, uid)

	check := func(p authctx.Principal, want string) {
		t.Helper()
		got, err := h.s.MaxContentRating(ctx, p)
		if err != nil || got != want {
			t.Fatalf("MaxContentRating(%+v) = %q, %v; want %q", p, got, err, want)
		}
	}
	check(authctx.Principal{UserID: uid}, "")
	check(authctx.Principal{UserID: uid, ProfileID: parent}, "")
	check(authctx.Principal{UserID: uid, ProfileID: kids}, "PG")

	mustExec(t, h.d, `UPDATE users SET max_content_rating = 'M', max_bitrate_kbps = 10000, max_streams = 2 WHERE id = ?`, uid)
	check(authctx.Principal{UserID: uid, ProfileID: parent}, "M")
	check(authctx.Principal{UserID: uid, ProfileID: kids}, "PG") // stricter of M and PG
	// a profile id that belongs to someone else is ignored
	other := h.insertUser("Other", false)
	check(authctx.Principal{UserID: other, ProfileID: kids}, "")

	l, err := h.s.StreamLimits(ctx, authctx.Principal{UserID: uid})
	if err != nil || l != (StreamLimits{MaxBitrateKbps: 10000, MaxStreams: 2}) {
		t.Fatalf("StreamLimits = %+v, %v", l, err)
	}
}

func TestAdminUserAPI(t *testing.T) {
	h := newHarness(t)
	rootID := h.insertUser("Root", true)
	root := h.sessionFor(rootID)
	annID := h.insertUser("Ann", false)
	ann := h.sessionFor(annID)
	annPath := fmt.Sprintf("/api/v1/users/%d", annID)

	wantStatus(t, h.do("GET", "/api/v1/users", nil, bearer(ann)), 403)
	rec := h.do("GET", "/api/v1/users", nil, bearer(root))
	wantStatus(t, rec, 200)
	if list := decodeBody[struct{ Users []userJSON }](t, rec).Users; len(list) != 2 || list[1].DisplayName != "Ann" {
		t.Fatalf("users %+v", list)
	}

	rec = h.do("PUT", annPath+"/policy", map[string]any{
		"library_ids": []int64{5, 2, 5}, "max_content_rating": "ma15+", "max_bitrate_kbps": 10000, "max_streams": 2,
	}, bearer(root))
	wantStatus(t, rec, 200)
	got := decodeBody[userJSON](t, rec)
	want := policy{LibraryIDs: []int64{2, 5}, MaxContentRating: "MA15+", MaxBitrateKbps: 10000, MaxStreams: 2}
	if !reflect.DeepEqual(got.Policy, want) {
		t.Fatalf("policy %+v", got.Policy)
	}
	for name, body := range map[string]map[string]any{
		"bad rating":  {"library_ids": []int64{}, "max_content_rating": "XYZ"},
		"neg bitrate": {"library_ids": []int64{}, "max_bitrate_kbps": -1},
		"bad library": {"library_ids": []int64{0}},
	} {
		if rec := h.do("PUT", annPath+"/policy", body, bearer(root)); rec.Code != 400 || errCode(t, rec) != "invalid_policy" {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	rec = h.do("PATCH", annPath, map[string]any{"display_name": "Annie", "disabled": true}, bearer(root))
	wantStatus(t, rec, 200)
	if u := decodeBody[userJSON](t, rec); u.DisplayName != "Annie" || !u.Disabled {
		t.Fatalf("patched %+v", u)
	}
	wantStatus(t, h.do("GET", "/api/v1/auth/me", nil, bearer(ann)), 401) // disabled ends sessions

	self := fmt.Sprintf("/api/v1/users/%d", rootID)
	if rec := h.do("PATCH", self, map[string]any{"disabled": true}, bearer(root)); errCode(t, rec) != "cannot_disable_self" {
		t.Fatalf("disable self: %d", rec.Code)
	}
	if rec := h.do("DELETE", self, nil, bearer(root)); errCode(t, rec) != "cannot_delete_self" {
		t.Fatalf("delete self: %d", rec.Code)
	}

	bobID := h.insertUser("Bob", false)
	h.sessionFor(bobID)
	bobPath := fmt.Sprintf("/api/v1/users/%d", bobID)
	rec = h.do("GET", bobPath+"/sessions", nil, bearer(root))
	if list := decodeBody[struct{ Sessions []sessionJSON }](t, rec).Sessions; len(list) != 1 {
		t.Fatalf("bob sessions %+v", list)
	}
	wantStatus(t, h.do("DELETE", bobPath+"/sessions", nil, bearer(root)), 204)
	if n := count(t, h.d, `SELECT count(*) FROM sessions WHERE user_id = ?`, bobID); n != 0 {
		t.Fatal("bob still has sessions")
	}
	wantStatus(t, h.do("DELETE", bobPath, nil, bearer(root)), 204)
	wantStatus(t, h.do("GET", bobPath, nil, bearer(root)), 404)
	wantStatus(t, h.do("GET", "/api/v1/users/x", nil, bearer(root)), 400)
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: RatingAge`, `(*Service).VisibleLibraryIDs undefined`.
(`ensureLibraries` in the test creates 1C's `libraries` table with `CREATE TABLE IF NOT EXISTS` and 1C's exact columns, so the test passes whether or not 1C has merged.)

- [ ] **Step 3: Implement ratings**

`server/internal/auth/ratings.go`:
```go
package auth

import "strings"

// ratingAges maps content-rating labels to the minimum viewer age they imply,
// so ratings from different systems can be compared. Covers the systems our
// metadata providers return for AU/US/UK. Unknown labels are treated as adult.
var ratingAges = map[string]int{
	// US film (MPA)
	"G": 0, "PG": 10, "PG-13": 13, "R": 17, "NC-17": 18,
	// US TV
	"TV-Y": 0, "TV-G": 0, "TV-Y7": 7, "TV-PG": 10, "TV-14": 14, "TV-MA": 17,
	// Australia (ACB); G and PG shared with the US
	"M": 15, "MA15+": 15, "R18+": 18, "X18+": 18,
	// UK (BBFC); PG shared
	"U": 0, "12": 12, "12A": 12, "15": 15, "18": 18, "R18": 18,
}

const adultAge = 18

func normalizeRating(r string) string { return strings.ToUpper(strings.TrimSpace(r)) }

// RatingAge returns the minimum viewer age for a content rating label and
// whether the label is known. Other modules use it to compare an item's
// rating with MaxContentRating: an item is allowed when
// RatingAge(item) <= RatingAge(max). Unknown/unrated items count as adult (18).
func RatingAge(rating string) (age int, known bool) {
	age, known = ratingAges[normalizeRating(rating)]
	if !known {
		return adultAge, false
	}
	return age, true
}

// validRating reports whether r may be stored as a max rating ("" = unrestricted).
func validRating(r string) bool {
	if r == "" {
		return true
	}
	_, ok := ratingAges[normalizeRating(r)]
	return ok
}

// stricterRating returns whichever max rating allows less ("" = unrestricted).
func stricterRating(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	ageA, _ := RatingAge(a)
	ageB, _ := RatingAge(b)
	if ageB < ageA {
		return b
	}
	return a
}
```

- [ ] **Step 4: Implement the policy queries**

`server/internal/auth/policy.go`:
```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

// VisibleLibraryIDs returns the library ids p may see, ascending and never
// nil (index contract). Admins see every library. Other users see only their
// explicit allow-list (empty = none); new users get the list from their
// invite. Disabled users see nothing.
//
// The libraries table belongs to plan 1C. It is read directly here (not via
// 1C's package) so auth does not import library code. Joining it also drops
// allow-listed ids whose library was deleted: user_library_access has no
// foreign key to libraries, so no cleanup is needed when 1C deletes one.
func (s *Service) VisibleLibraryIDs(ctx context.Context, p authctx.Principal) ([]int64, error) {
	var isAdmin, disabled bool
	err := s.db.QueryRowContext(ctx, `SELECT is_admin, disabled FROM users WHERE id = ?`, p.UserID).Scan(&isAdmin, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("visible libraries for user %d: %w", p.UserID, errNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("visible libraries: %w", err)
	}
	ids := []int64{}
	if disabled {
		return ids, nil
	}
	var rows *sql.Rows
	if isAdmin {
		rows, err = s.db.QueryContext(ctx, `SELECT id FROM libraries ORDER BY id`)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT a.library_id FROM user_library_access a
			JOIN libraries l ON l.id = a.library_id
			WHERE a.user_id = ? ORDER BY a.library_id`, p.UserID)
	}
	if err != nil {
		return nil, fmt.Errorf("visible libraries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("visible libraries: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("visible libraries: %w", err)
	}
	return ids, nil
}

// MaxContentRating returns the strictest of the user's and the selected
// profile's max rating, or "" when unrestricted. Compare with RatingAge.
func (s *Service) MaxContentRating(ctx context.Context, p authctx.Principal) (string, error) {
	var userMax string
	err := s.db.QueryRowContext(ctx, `SELECT max_content_rating FROM users WHERE id = ?`, p.UserID).Scan(&userMax)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("max rating for user %d: %w", p.UserID, errNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("max rating: %w", err)
	}
	var profileMax string
	if p.ProfileID != 0 {
		err := s.db.QueryRowContext(ctx, `SELECT max_content_rating FROM profiles WHERE id = ? AND user_id = ?`,
			p.ProfileID, p.UserID).Scan(&profileMax)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("profile max rating: %w", err)
		}
	}
	return stricterRating(userMax, profileMax), nil
}

// StreamLimits are the user's playback limits (spec 3.3). 0 = unlimited.
// Consumed by the playback/transcode modules in phase 2.
type StreamLimits struct {
	MaxBitrateKbps int64
	MaxStreams     int64
}

// StreamLimits returns p's playback limits.
func (s *Service) StreamLimits(ctx context.Context, p authctx.Principal) (StreamLimits, error) {
	var l StreamLimits
	err := s.db.QueryRowContext(ctx, `SELECT max_bitrate_kbps, max_streams FROM users WHERE id = ?`, p.UserID).
		Scan(&l.MaxBitrateKbps, &l.MaxStreams)
	if errors.Is(err, sql.ErrNoRows) {
		return l, fmt.Errorf("stream limits for user %d: %w", p.UserID, errNotFound)
	}
	if err != nil {
		return l, fmt.Errorf("stream limits: %w", err)
	}
	return l, nil
}

// validatePolicy checks an admin-supplied policy and normalises it.
func validatePolicy(p *policy) error {
	if !validRating(p.MaxContentRating) {
		return fmt.Errorf("unknown content rating %q", p.MaxContentRating)
	}
	p.MaxContentRating = normalizeRating(p.MaxContentRating)
	if p.MaxBitrateKbps < 0 || p.MaxBitrateKbps > 1_000_000 {
		return errors.New("max_bitrate_kbps must be 0 (unlimited) to 1000000")
	}
	if p.MaxStreams < 0 || p.MaxStreams > 100 {
		return errors.New("max_streams must be 0 (unlimited) to 100")
	}
	if len(p.LibraryIDs) > 1000 {
		return errors.New("too many library ids")
	}
	for _, id := range p.LibraryIDs {
		if id <= 0 {
			return fmt.Errorf("invalid library id %d", id)
		}
	}
	p.LibraryIDs = sortedIDs(p.LibraryIDs)
	return nil
}
```

- [ ] **Step 5: Implement the admin user API**

`server/internal/auth/users_admin.go`:
```go
package auth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

// GET /users (admin)
func (s *Service) handleListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY id`)
	if err != nil {
		s.internalError(w, r, "list users", err)
		return
	}
	var users []user
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			rows.Close()
			s.internalError(w, r, "scan user", err)
			return
		}
		users = append(users, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.internalError(w, r, "list users", err)
		return
	}
	out := make([]userJSON, 0, len(users))
	for _, u := range users {
		if u.Policy.LibraryIDs, err = libraryAccess(ctx, s.db, u.ID); err != nil {
			s.internalError(w, r, "library access", err)
			return
		}
		out = append(out, u.toJSON())
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": out})
}

// userParam loads the {id} user, writing 400/404 as needed.
func (s *Service) userParam(w http.ResponseWriter, r *http.Request) (user, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return user{}, false
	}
	u, err := s.getUser(r.Context(), id)
	if errors.Is(err, errNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such user")
		return user{}, false
	}
	if err != nil {
		s.internalError(w, r, "load user", err)
		return user{}, false
	}
	return u, true
}

// GET /users/{id} (admin)
func (s *Service) handleGetUser(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.userParam(w, r); ok {
		httpx.WriteJSON(w, http.StatusOK, u.toJSON())
	}
}

// PATCH /users/{id} (admin): rename or disable. Disabling ends all sessions.
func (s *Service) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userParam(w, r)
	if !ok {
		return
	}
	var in struct {
		DisplayName *string `json:"display_name"`
		Disabled    *bool   `json:"disabled"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	p, _ := authctx.FromContext(r.Context())
	ctx := r.Context()
	now := s.now().Unix()
	if in.DisplayName != nil {
		name := truncate(*in.DisplayName, 100)
		if name == "" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_name", "display_name must not be empty")
			return
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?`, name, now, u.ID); err != nil {
			s.internalError(w, r, "rename user", err)
			return
		}
	}
	if in.Disabled != nil {
		if *in.Disabled && u.ID == p.UserID {
			httpx.WriteError(w, http.StatusBadRequest, "cannot_disable_self", "you cannot disable your own account")
			return
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?`, *in.Disabled, now, u.ID); err != nil {
			s.internalError(w, r, "disable user", err)
			return
		}
		if *in.Disabled {
			if err := s.revokeAllSessions(ctx, u.ID); err != nil {
				s.internalError(w, r, "disable user", err)
				return
			}
		}
	}
	s.handleGetUser(w, r)
}

// PUT /users/{id}/policy (admin): replace the user's access policy.
func (s *Service) handlePutPolicy(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userParam(w, r)
	if !ok {
		return
	}
	var in policy
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := validatePolicy(&in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}
	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.internalError(w, r, "begin policy", err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET max_content_rating = ?, max_bitrate_kbps = ?, max_streams = ?, updated_at = ?
		WHERE id = ?`, in.MaxContentRating, in.MaxBitrateKbps, in.MaxStreams, s.now().Unix(), u.ID); err != nil {
		s.internalError(w, r, "update policy", err)
		return
	}
	if err := replaceLibraryAccessTx(ctx, tx, u.ID, in.LibraryIDs); err != nil {
		s.internalError(w, r, "update library access", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.internalError(w, r, "commit policy", err)
		return
	}
	s.handleGetUser(w, r)
}

// DELETE /users/{id} (admin): removes the account, its profiles, identities and sessions.
func (s *Service) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userParam(w, r)
	if !ok {
		return
	}
	p, _ := authctx.FromContext(r.Context())
	if u.ID == p.UserID {
		httpx.WriteError(w, http.StatusBadRequest, "cannot_delete_self", "you cannot delete your own account")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM users WHERE id = ?`, u.ID); err != nil {
		s.internalError(w, r, "delete user", fmt.Errorf("user %d: %w", u.ID, err))
		return
	}
	s.log.Info("auth: user deleted", "user_id", u.ID, "by", p.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// GET /users/{id}/sessions (admin)
func (s *Service) handleUserSessions(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userParam(w, r)
	if !ok {
		return
	}
	list, err := s.listSessions(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, r, "list sessions", err)
		return
	}
	ss, _ := currentSession(r.Context())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": sessionsJSON(list, ss.ID)})
}

// DELETE /users/{id}/sessions (admin): sign the user out everywhere.
func (s *Service) handleRevokeUserSessions(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userParam(w, r)
	if !ok {
		return
	}
	if err := s.revokeAllSessions(r.Context(), u.ID); err != nil {
		s.internalError(w, r, "revoke sessions", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 6: Register the routes**

`server/internal/auth/routes.go`:
```go
package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

// Mount implements app.Module. r is already scoped to /api/v1.
func (s *Service) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/auth", func(r chi.Router) { s.authRoutes(r, g) })
	r.Route("/users", func(r chi.Router) { s.userRoutes(r, g) })
	r.Route("/profiles", func(r chi.Router) { s.profileRoutes(r, g) })
	r.Route("/invites", func(r chi.Router) { s.inviteRoutes(r, g) })
}

func (s *Service) authRoutes(r chi.Router, g authctx.Guard) {
	// public
	r.Get("/setup", s.handleSetupStatus)
	r.With(requireJSON).Post("/setup/totp", s.handleSetupTOTP)
	r.With(requireJSON).Post("/setup", s.handleSetup)
	r.With(requireJSON).Post("/login", s.handleLogin)
	// signed in
	r.With(g.RequireUser).Get("/me", s.handleMe)
	r.With(g.RequireUser).Post("/logout", s.handleLogout)
	r.With(g.RequireUser).Post("/logout-all", s.handleLogoutAll)
	r.With(g.RequireUser).Get("/sessions", s.handleListSessions)
	r.With(g.RequireUser).Delete("/sessions/{id}", s.handleRevokeSession)
}

func (s *Service) userRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireAdmin)
	r.Get("/", s.handleListUsers)
	r.Get("/{id}", s.handleGetUser)
	r.Patch("/{id}", s.handlePatchUser)
	r.Put("/{id}/policy", s.handlePutPolicy)
	r.Delete("/{id}", s.handleDeleteUser)
	r.Get("/{id}/sessions", s.handleUserSessions)
	r.Delete("/{id}/sessions", s.handleRevokeUserSessions)
}

func (s *Service) profileRoutes(r chi.Router, g authctx.Guard) {}

func (s *Service) inviteRoutes(r chi.Router, g authctx.Guard) {}
```

- [ ] **Step 7: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 8: Commit**

```bash
git add server/internal/auth
git commit -m "feat(auth): per-user policies, library visibility, rating limits and admin user API"
```

---

### Task 9: Home profiles with PINs and profile selection

**Files:**
- Create: `server/internal/auth/profiles.go`
- Modify: `server/internal/auth/routes.go` (full file below)
- Test: `server/internal/auth/profiles_test.go`

**Interfaces:**
- Consumes: `hashPassword`, `verifyPassword`, `limiter`, `tooManyAttempts`, `pathID`, `setSessionProfile`, `currentSession`, `validRating`, `normalizeRating`, `maxProfileName`, harness.
- Produces: `maxProfiles = 10`; `type profile{ID, UserID; Name; IsPrimary; PinHash; MaxContentRating}`, `type profileJSON{id, name, is_primary, has_pin, max_content_rating}`; `(*Service) listProfiles(ctx, userID)`, `getProfile(ctx, userID, id)` (`errNotFound`), `requireManager(w, r, session) bool` (403 `primary_profile_required`); handlers `handleListProfiles` (`{profiles, current_profile_id}`), `handleCreateProfile` (201; 409 `profile_exists` / `profile_limit`; 400 `invalid_profile`), `handlePatchProfile` (`pin: ""` removes the PIN), `handleDeleteProfile` (400 `cannot_delete_primary`), `handleSelectProfile` (`{pin}`; 403 `invalid_pin`; 429 after repeated failures). After select, `Principal.ProfileID` on later requests is that profile.

- [ ] **Step 1: Write the failing tests**

`server/internal/auth/profiles_test.go`:
```go
package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestProfiles(t *testing.T) {
	h := newHarness(t)
	uid := h.insertUser("Family", false)
	tok := h.sessionFor(uid)
	mainID := h.primaryProfile(uid)
	auth := bearer(tok)

	// managing requires the primary profile to be selected
	rec := h.do("POST", "/api/v1/profiles", map[string]any{"name": "Kids"}, auth)
	if rec.Code != 403 || errCode(t, rec) != "primary_profile_required" {
		t.Fatalf("create without primary: %d %s", rec.Code, rec.Body.String())
	}
	wantStatus(t, h.do("POST", fmt.Sprintf("/api/v1/profiles/%d/select", mainID), nil, auth), 200)

	rec = h.do("POST", "/api/v1/profiles", map[string]any{"name": "Kids", "max_content_rating": "pg"}, auth)
	wantStatus(t, rec, 201)
	kids := decodeBody[profileJSON](t, rec)
	if kids.Name != "Kids" || kids.MaxContentRating != "PG" || kids.HasPIN || kids.IsPrimary {
		t.Fatalf("kids %+v", kids)
	}
	if rec := h.do("POST", "/api/v1/profiles", map[string]any{"name": "Kids"}, auth); errCode(t, rec) != "profile_exists" {
		t.Fatalf("duplicate: %d", rec.Code)
	}
	for _, body := range []map[string]any{{"name": ""}, {"name": "X", "pin": "12a4"}, {"name": "Y", "max_content_rating": "nope"}} {
		if rec := h.do("POST", "/api/v1/profiles", body, auth); errCode(t, rec) != "invalid_profile" {
			t.Fatalf("%v: %d %s", body, rec.Code, rec.Body.String())
		}
	}

	// PIN on the main profile
	mainPath := fmt.Sprintf("/api/v1/profiles/%d", mainID)
	rec = h.do("PATCH", mainPath, map[string]any{"pin": "4321"}, auth)
	wantStatus(t, rec, 200)
	if !decodeBody[profileJSON](t, rec).HasPIN {
		t.Fatal("pin not set")
	}

	rec = h.do("GET", "/api/v1/profiles", nil, auth)
	list := decodeBody[struct {
		Profiles         []profileJSON
		CurrentProfileID int64 `json:"current_profile_id"`
	}](t, rec)
	if len(list.Profiles) != 2 || !list.Profiles[0].IsPrimary || list.CurrentProfileID != mainID {
		t.Fatalf("list %+v", list)
	}

	// switch to Kids: Principal.ProfileID follows the session
	kidsPath := fmt.Sprintf("/api/v1/profiles/%d", kids.ID)
	wantStatus(t, h.do("POST", kidsPath+"/select", map[string]string{}, auth), 200)
	me := decodeBody[map[string]any](t, h.do("GET", "/api/v1/auth/me", nil, auth))
	if me["profile_id"] != float64(kids.ID) {
		t.Fatalf("me %v", me)
	}
	// kids cannot raise their own rating
	if rec := h.do("PATCH", kidsPath, map[string]any{"max_content_rating": ""}, auth); rec.Code != 403 {
		t.Fatalf("kids self-edit: %d", rec.Code)
	}

	// back to main needs the PIN
	if rec := h.do("POST", mainPath+"/select", map[string]string{"pin": "0000"}, auth); rec.Code != 403 || errCode(t, rec) != "invalid_pin" {
		t.Fatalf("wrong pin: %d %s", rec.Code, rec.Body.String())
	}
	wantStatus(t, h.do("POST", mainPath+"/select", map[string]string{"pin": "4321"}, auth), 200)

	if rec := h.do("DELETE", mainPath, nil, auth); errCode(t, rec) != "cannot_delete_primary" {
		t.Fatalf("delete primary: %d", rec.Code)
	}
	wantStatus(t, h.do("DELETE", kidsPath, nil, auth), 204)
	wantStatus(t, h.do("DELETE", kidsPath, nil, auth), 404)

	// someone else's profile is invisible
	other := h.sessionFor(h.insertUser("Other", false))
	wantStatus(t, h.do("POST", mainPath+"/select", map[string]string{"pin": "4321"}, bearer(other)), 404)
}

func TestProfilePINLockout(t *testing.T) {
	h := newHarness(t)
	uid := h.insertUser("Family", false)
	mainID := h.primaryProfile(uid)
	pin, _ := hashPasswordWith("1234", cheapArgon)
	mustExec(t, h.d, `UPDATE profiles SET pin_hash = ? WHERE id = ?`, pin, mainID)
	auth := bearer(h.sessionFor(uid))
	path := fmt.Sprintf("/api/v1/profiles/%d/select", mainID)

	for i := 0; i < 6; i++ {
		wantStatus(t, h.do("POST", path, map[string]string{"pin": fmt.Sprintf("%04d", i)}, auth), 403)
	}
	rec := h.do("POST", path, map[string]string{"pin": "1234"}, auth)
	if rec.Code != 429 {
		t.Fatalf("locked: %d", rec.Code)
	}
	h.clock.Advance(31 * time.Second)
	wantStatus(t, h.do("POST", path, map[string]string{"pin": "1234"}, auth), 200)
}

func TestProfileLimit(t *testing.T) {
	h := newHarness(t)
	uid := h.insertUser("Big", false)
	tok := h.sessionFor(uid)
	wantStatus(t, h.do("POST", fmt.Sprintf("/api/v1/profiles/%d/select", h.primaryProfile(uid)), nil, bearer(tok)), 200)
	for i := 1; i < maxProfiles; i++ {
		wantStatus(t, h.do("POST", "/api/v1/profiles", map[string]any{"name": fmt.Sprintf("P%d", i)}, bearer(tok)), 201)
	}
	if rec := h.do("POST", "/api/v1/profiles", map[string]any{"name": "One too many"}, bearer(tok)); errCode(t, rec) != "profile_limit" {
		t.Fatalf("limit: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: profileJSON`.

- [ ] **Step 3: Implement**

`server/internal/auth/profiles.go`:
```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

const maxProfiles = 10

var pinPattern = regexp.MustCompile(`^[0-9]{4}$`)

// profile is a home profile ("Who's watching?") under an account.
//
// Management rule: creating, editing or deleting profiles requires the
// session to have the account's primary profile selected (entering its PIN
// if it has one). That stops a kid's profile from raising its own rating or
// removing a parent's PIN. Any session may list profiles and select one.
type profile struct {
	ID               int64
	UserID           int64
	Name             string
	IsPrimary        bool
	PinHash          string
	MaxContentRating string
}

type profileJSON struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	IsPrimary        bool   `json:"is_primary"`
	HasPIN           bool   `json:"has_pin"`
	MaxContentRating string `json:"max_content_rating"`
}

func (p profile) toJSON() profileJSON {
	return profileJSON{ID: p.ID, Name: p.Name, IsPrimary: p.IsPrimary, HasPIN: p.PinHash != "", MaxContentRating: p.MaxContentRating}
}

const profileColumns = `id, user_id, name, is_primary, pin_hash, max_content_rating`

func scanProfile(sc interface{ Scan(...any) error }) (profile, error) {
	var p profile
	var pin sql.NullString
	err := sc.Scan(&p.ID, &p.UserID, &p.Name, &p.IsPrimary, &pin, &p.MaxContentRating)
	p.PinHash = pin.String
	return p, err
}

func (s *Service) listProfiles(ctx context.Context, userID int64) ([]profile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+profileColumns+` FROM profiles
		WHERE user_id = ? ORDER BY is_primary DESC, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer rows.Close()
	var out []profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) getProfile(ctx context.Context, userID, id int64) (profile, error) {
	p, err := scanProfile(s.db.QueryRowContext(ctx,
		`SELECT `+profileColumns+` FROM profiles WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return profile{}, errNotFound
	}
	if err != nil {
		return profile{}, fmt.Errorf("load profile: %w", err)
	}
	return p, nil
}

// profileParam loads the {id} profile owned by the session's user (404 otherwise).
func (s *Service) profileParam(w http.ResponseWriter, r *http.Request, ss session) (profile, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return profile{}, false
	}
	p, err := s.getProfile(r.Context(), ss.UserID, id)
	if errors.Is(err, errNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such profile")
		return profile{}, false
	}
	if err != nil {
		s.internalError(w, r, "load profile", err)
		return profile{}, false
	}
	return p, true
}

// requireManager enforces the management rule described on profile.
func (s *Service) requireManager(w http.ResponseWriter, r *http.Request, ss session) bool {
	if ss.ProfileID != 0 {
		p, err := s.getProfile(r.Context(), ss.UserID, ss.ProfileID)
		if err != nil && !errors.Is(err, errNotFound) {
			s.internalError(w, r, "load current profile", err)
			return false
		}
		if err == nil && p.IsPrimary {
			return true
		}
	}
	httpx.WriteError(w, http.StatusForbidden, "primary_profile_required",
		"select the main profile (and enter its PIN) to manage profiles")
	return false
}

// GET /profiles
func (s *Service) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	ss, _ := currentSession(r.Context())
	list, err := s.listProfiles(r.Context(), ss.UserID)
	if err != nil {
		s.internalError(w, r, "list profiles", err)
		return
	}
	out := make([]profileJSON, 0, len(list))
	for _, p := range list {
		out = append(out, p.toJSON())
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"profiles": out, "current_profile_id": ss.ProfileID})
}

type profileInput struct {
	Name             *string `json:"name"`
	PIN              *string `json:"pin"` // 4 digits; "" removes the PIN
	MaxContentRating *string `json:"max_content_rating"`
}

// validate checks the fields that are present and normalises them.
func (in *profileInput) validate() error {
	if in.Name != nil {
		n := truncate(*in.Name, maxProfileName)
		if n == "" {
			return errors.New("name must not be empty")
		}
		in.Name = &n
	}
	if in.PIN != nil && *in.PIN != "" && !pinPattern.MatchString(*in.PIN) {
		return errors.New("pin must be exactly 4 digits")
	}
	if in.MaxContentRating != nil {
		if !validRating(*in.MaxContentRating) {
			return fmt.Errorf("unknown content rating %q", *in.MaxContentRating)
		}
		n := normalizeRating(*in.MaxContentRating)
		in.MaxContentRating = &n
	}
	return nil
}

func (s *Service) nameTaken(ctx context.Context, userID int64, name string, exceptID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM profiles WHERE user_id = ? AND name = ? AND id != ?)`,
		userID, name, exceptID).Scan(&n)
	return n == 1, err
}

// pinHashFor returns the value to store for a PIN field ("" clears it).
func (s *Service) pinHashFor(pin string) (any, error) {
	if pin == "" {
		return nil, nil
	}
	return s.hashPassword(pin)
}

// POST /profiles
func (s *Service) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	ss, _ := currentSession(r.Context())
	var in profileInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if !s.requireManager(w, r, ss) {
		return
	}
	if in.Name == nil {
		empty := ""
		in.Name = &empty
	}
	if err := in.validate(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_profile", err.Error())
		return
	}
	ctx := r.Context()
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM profiles WHERE user_id = ?`, ss.UserID).Scan(&n); err != nil {
		s.internalError(w, r, "count profiles", err)
		return
	}
	if n >= maxProfiles {
		httpx.WriteError(w, http.StatusConflict, "profile_limit", "an account can have at most "+strconv.Itoa(maxProfiles)+" profiles")
		return
	}
	if taken, err := s.nameTaken(ctx, ss.UserID, *in.Name, 0); err != nil || taken {
		if err != nil {
			s.internalError(w, r, "check profile name", err)
		} else {
			httpx.WriteError(w, http.StatusConflict, "profile_exists", "a profile with that name already exists")
		}
		return
	}
	pin := ""
	if in.PIN != nil {
		pin = *in.PIN
	}
	pinHash, err := s.pinHashFor(pin)
	if err != nil {
		s.internalError(w, r, "hash pin", err)
		return
	}
	rating := ""
	if in.MaxContentRating != nil {
		rating = *in.MaxContentRating
	}
	now := s.now().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO profiles(user_id, name, pin_hash, max_content_rating, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)`, ss.UserID, *in.Name, pinHash, rating, now, now)
	if err != nil {
		s.internalError(w, r, "create profile", err)
		return
	}
	id, _ := res.LastInsertId()
	p, err := s.getProfile(ctx, ss.UserID, id)
	if err != nil {
		s.internalError(w, r, "load profile", err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p.toJSON())
}

// PATCH /profiles/{id}
func (s *Service) handlePatchProfile(w http.ResponseWriter, r *http.Request) {
	ss, _ := currentSession(r.Context())
	var in profileInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	p, ok := s.profileParam(w, r, ss)
	if !ok || !s.requireManager(w, r, ss) {
		return
	}
	if err := in.validate(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_profile", err.Error())
		return
	}
	ctx := r.Context()
	if in.Name != nil {
		if taken, err := s.nameTaken(ctx, ss.UserID, *in.Name, p.ID); err != nil || taken {
			if err != nil {
				s.internalError(w, r, "check profile name", err)
			} else {
				httpx.WriteError(w, http.StatusConflict, "profile_exists", "a profile with that name already exists")
			}
			return
		}
		p.Name = *in.Name
	}
	var pinHash any = nullString(p.PinHash)
	if in.PIN != nil {
		h, err := s.pinHashFor(*in.PIN)
		if err != nil {
			s.internalError(w, r, "hash pin", err)
			return
		}
		pinHash = h
	}
	if in.MaxContentRating != nil {
		p.MaxContentRating = *in.MaxContentRating
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE profiles SET name = ?, pin_hash = ?, max_content_rating = ?, updated_at = ?
		WHERE id = ? AND user_id = ?`, p.Name, pinHash, p.MaxContentRating, s.now().Unix(), p.ID, ss.UserID); err != nil {
		s.internalError(w, r, "update profile", err)
		return
	}
	p, err := s.getProfile(ctx, ss.UserID, p.ID)
	if err != nil {
		s.internalError(w, r, "load profile", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p.toJSON())
}

// DELETE /profiles/{id}: sessions using it fall back to "no profile selected".
func (s *Service) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	ss, _ := currentSession(r.Context())
	p, ok := s.profileParam(w, r, ss)
	if !ok || !s.requireManager(w, r, ss) {
		return
	}
	if p.IsPrimary {
		httpx.WriteError(w, http.StatusBadRequest, "cannot_delete_primary", "the main profile cannot be deleted")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM profiles WHERE id = ? AND user_id = ?`, p.ID, ss.UserID); err != nil {
		s.internalError(w, r, "delete profile", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /profiles/{id}/select: "Who's watching?". Sets the session's profile,
// which becomes Principal.ProfileID on later requests.
func (s *Service) handleSelectProfile(w http.ResponseWriter, r *http.Request) {
	ss, _ := currentSession(r.Context())
	var in struct {
		PIN string `json:"pin"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil && !errors.Is(err, io.EOF) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	p, ok := s.profileParam(w, r, ss)
	if !ok {
		return
	}
	if p.PinHash != "" {
		key := "pin:" + strconv.FormatInt(p.ID, 10) + "|" + clientIP(r)
		if ok, wait := s.limit.allow(key); !ok {
			tooManyAttempts(w, wait)
			return
		}
		match, err := verifyPassword(in.PIN, p.PinHash)
		if err != nil {
			s.internalError(w, r, "verify pin", err)
			return
		}
		if !match {
			s.limit.fail(key)
			httpx.WriteError(w, http.StatusForbidden, "invalid_pin", "wrong PIN")
			return
		}
		s.limit.success(key)
	}
	if err := s.setSessionProfile(r.Context(), ss.ID, p.ID); err != nil {
		s.internalError(w, r, "select profile", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"profile": p.toJSON()})
}
```

- [ ] **Step 4: Register the routes**

`server/internal/auth/routes.go`:
```go
package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

// Mount implements app.Module. r is already scoped to /api/v1.
func (s *Service) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/auth", func(r chi.Router) { s.authRoutes(r, g) })
	r.Route("/users", func(r chi.Router) { s.userRoutes(r, g) })
	r.Route("/profiles", func(r chi.Router) { s.profileRoutes(r, g) })
	r.Route("/invites", func(r chi.Router) { s.inviteRoutes(r, g) })
}

func (s *Service) authRoutes(r chi.Router, g authctx.Guard) {
	// public
	r.Get("/setup", s.handleSetupStatus)
	r.With(requireJSON).Post("/setup/totp", s.handleSetupTOTP)
	r.With(requireJSON).Post("/setup", s.handleSetup)
	r.With(requireJSON).Post("/login", s.handleLogin)
	// signed in
	r.With(g.RequireUser).Get("/me", s.handleMe)
	r.With(g.RequireUser).Post("/logout", s.handleLogout)
	r.With(g.RequireUser).Post("/logout-all", s.handleLogoutAll)
	r.With(g.RequireUser).Get("/sessions", s.handleListSessions)
	r.With(g.RequireUser).Delete("/sessions/{id}", s.handleRevokeSession)
}

func (s *Service) userRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireAdmin)
	r.Get("/", s.handleListUsers)
	r.Get("/{id}", s.handleGetUser)
	r.Patch("/{id}", s.handlePatchUser)
	r.Put("/{id}/policy", s.handlePutPolicy)
	r.Delete("/{id}", s.handleDeleteUser)
	r.Get("/{id}/sessions", s.handleUserSessions)
	r.Delete("/{id}/sessions", s.handleRevokeUserSessions)
}

func (s *Service) profileRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireUser)
	r.Get("/", s.handleListProfiles)
	r.Post("/", s.handleCreateProfile)
	r.Patch("/{id}", s.handlePatchProfile)
	r.Delete("/{id}", s.handleDeleteProfile)
	r.Post("/{id}/select", s.handleSelectProfile)
}

func (s *Service) inviteRoutes(r chi.Router, g authctx.Guard) {}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/auth
git commit -m "feat(auth): home profiles with PINs, primary-profile management and selection"
```

---

### Task 10: OAuth provider configuration (admin) and public provider list

**Files:**
- Create: `server/internal/auth/providers.go`
- Modify: `server/internal/auth/routes.go` (full file below)
- Test: `server/internal/auth/providers_test.go`

**Interfaces:**
- Consumes: harness, `internalError`.
- Produces: `type secret string` (redacts in `String`, `GoString`, `Format`, `LogValue`, `MarshalJSON`; `isSet()`, `reveal()`), `redacted = "[redacted]"`; `supportedProviders` (`google` "Google", `microsoft` "Microsoft"), `providerDisplayName(name) (string, bool)`; `type providerConfig{Name, ClientID string; ClientSecret secret; Enabled bool; UpdatedAt time.Time}`; `(*Service) usable(providerConfig) bool`, `redirectURL(name) string`, `getProviderConfig(ctx, name) (providerConfig, found bool, error)`; handlers `handlePublicProviders`, `handleAdminProviders`, `handlePutProvider` (404 `unknown_provider`; 400 `incomplete_provider`, `public_url_required`); `type adminProviderJSON`. Test const `testSecret`.

- [ ] **Step 1: Write the failing tests**

`server/internal/auth/providers_test.go`:
```go
package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const testSecret = "s3cr3t-value-never-logged"

func TestSecretNeverPrints(t *testing.T) {
	sec := secret(testSecret)
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("x", "secret", sec)
	js, _ := json.Marshal(map[string]any{"s": sec})
	out := fmt.Sprintf("%v %s %+v %#v %q", sec, sec, sec, sec, sec) + string(js) + logs.String()
	if strings.Contains(out, testSecret) || !strings.Contains(out, redacted) {
		t.Fatalf("secret leaked: %s", out)
	}
	if sec.reveal() != testSecret {
		t.Fatal("reveal")
	}
}

func TestProviderConfigAPI(t *testing.T) {
	h := newHarness(t)
	root := bearer(h.sessionFor(h.insertUser("Root", true)))
	ann := bearer(h.sessionFor(h.insertUser("Ann", false)))

	public := func() []map[string]string {
		rec := h.do("GET", "/api/v1/auth/providers", nil)
		wantStatus(t, rec, 200)
		return decodeBody[struct{ Providers []map[string]string }](t, rec).Providers
	}
	if p := public(); len(p) != 0 {
		t.Fatalf("fresh install shows providers: %v", p)
	}

	wantStatus(t, h.do("GET", "/api/v1/auth/oauth-providers", nil, ann), 403)
	rec := h.do("GET", "/api/v1/auth/oauth-providers", nil, root)
	wantStatus(t, rec, 200)
	list := decodeBody[struct{ Providers []adminProviderJSON }](t, rec).Providers
	if len(list) != 2 || list[0].Name != "google" || list[1].Name != "microsoft" ||
		list[0].RedirectURL != "https://watch.example.com/api/v1/auth/callback/google" || list[0].SecretSet {
		t.Fatalf("admin list %+v", list)
	}

	rec = h.do("PUT", "/api/v1/auth/oauth-providers/google", map[string]any{"client_id": "cid", "enabled": true}, root)
	if rec.Code != 400 || errCode(t, rec) != "incomplete_provider" {
		t.Fatalf("enable without secret: %d %s", rec.Code, rec.Body.String())
	}
	rec = h.do("PUT", "/api/v1/auth/oauth-providers/google",
		map[string]any{"client_id": "cid", "client_secret": testSecret, "enabled": true}, root)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), testSecret) {
		t.Fatal("secret returned by the API")
	}
	if p := decodeBody[adminProviderJSON](t, rec); !p.Enabled || !p.SecretSet || p.ClientID != "cid" {
		t.Fatalf("saved %+v", p)
	}
	if p := public(); len(p) != 1 || p[0]["name"] != "google" || p[0]["login_url"] != "/api/v1/auth/login/google" {
		t.Fatalf("public %v", p)
	}

	// omitting client_secret keeps the stored one
	rec = h.do("PUT", "/api/v1/auth/oauth-providers/google", map[string]any{"client_id": "cid2", "enabled": false}, root)
	wantStatus(t, rec, 200)
	pc, _, _ := h.s.getProviderConfig(t.Context(), "google")
	if pc.ClientID != "cid2" || pc.ClientSecret.reveal() != testSecret || pc.Enabled {
		t.Fatalf("stored %+v", pc)
	}
	if p := public(); len(p) != 0 {
		t.Fatalf("disabled provider still public: %v", p)
	}

	wantStatus(t, h.do("PUT", "/api/v1/auth/oauth-providers/myspace", map[string]any{"client_id": "x"}, root), 404)
	if strings.Contains(h.logs.String(), testSecret) {
		t.Fatal("secret written to the log")
	}

	h.s.cfg.PublicURL = ""
	rec = h.do("PUT", "/api/v1/auth/oauth-providers/microsoft",
		map[string]any{"client_id": "m", "client_secret": "x", "enabled": true}, root)
	if errCode(t, rec) != "public_url_required" {
		t.Fatalf("no public url: %d %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: secret`.

- [ ] **Step 3: Implement**

`server/internal/auth/providers.go`:
```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

// secret holds an OAuth client secret. It redacts itself in logs, fmt and
// JSON so it cannot leak by accident. reveal() is only for the two places
// that need the real value: the DB write and the token request to the provider.
type secret string

const redacted = "[redacted]"

func (secret) String() string               { return redacted }
func (secret) GoString() string             { return redacted }
func (secret) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (s secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }
func (s secret) isSet() bool                { return s != "" }
func (s secret) reveal() string             { return string(s) }

// supportedProviders lists the sign-in providers phase 1 implements, in
// display order. Phase 4 appends apple, facebook and a generic OIDC slot.
var supportedProviders = []struct{ Name, DisplayName string }{
	{"google", "Google"},
	{"microsoft", "Microsoft"},
}

func providerDisplayName(name string) (string, bool) {
	for _, p := range supportedProviders {
		if p.Name == name {
			return p.DisplayName, true
		}
	}
	return "", false
}

type providerConfig struct {
	Name         string
	ClientID     string
	ClientSecret secret
	Enabled      bool
	UpdatedAt    time.Time
}

// usable: enabled, fully configured, and the server knows its public URL
// (needed for the redirect URL).
func (s *Service) usable(pc providerConfig) bool {
	return pc.Enabled && pc.ClientID != "" && pc.ClientSecret.isSet() && s.cfg.PublicURL != ""
}

func (s *Service) redirectURL(name string) string {
	return s.cfg.PublicURL + "/api/v1/auth/callback/" + name
}

func (s *Service) getProviderConfig(ctx context.Context, name string) (providerConfig, bool, error) {
	pc := providerConfig{Name: name}
	var sec string
	var updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT client_id, client_secret, enabled, updated_at FROM oauth_providers WHERE name = ?`, name).
		Scan(&pc.ClientID, &sec, &pc.Enabled, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return pc, false, nil
	}
	if err != nil {
		return pc, false, fmt.Errorf("load provider %s: %w", name, err)
	}
	pc.ClientSecret, pc.UpdatedAt = secret(sec), unix(updated)
	return pc, true, nil
}

// GET /auth/providers (public): the sign-in buttons to show. Unconfigured or
// disabled providers are left out, so a fresh install shows none.
func (s *Service) handlePublicProviders(w http.ResponseWriter, r *http.Request) {
	out := []map[string]string{}
	for _, sp := range supportedProviders {
		pc, ok, err := s.getProviderConfig(r.Context(), sp.Name)
		if err != nil {
			s.internalError(w, r, "list providers", err)
			return
		}
		if ok && s.usable(pc) {
			out = append(out, map[string]string{
				"name": sp.Name, "display_name": sp.DisplayName,
				"login_url": "/api/v1/auth/login/" + sp.Name,
			})
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"providers": out})
}

type adminProviderJSON struct {
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	ClientID    string    `json:"client_id"`
	SecretSet   bool      `json:"secret_set"` // the secret itself is never returned
	RedirectURL string    `json:"redirect_url"`
	UpdatedAt   time.Time `json:"updated_at,omitzero"`
}

func (s *Service) adminProvider(ctx context.Context, name string) (adminProviderJSON, error) {
	display, _ := providerDisplayName(name)
	pc, _, err := s.getProviderConfig(ctx, name)
	if err != nil {
		return adminProviderJSON{}, err
	}
	out := adminProviderJSON{
		Name: name, DisplayName: display, Enabled: pc.Enabled, ClientID: pc.ClientID,
		SecretSet: pc.ClientSecret.isSet(), UpdatedAt: pc.UpdatedAt,
	}
	if s.cfg.PublicURL != "" {
		out.RedirectURL = s.redirectURL(name)
	}
	return out, nil
}

// GET /auth/oauth-providers (admin): setup screen with the exact redirect URL.
func (s *Service) handleAdminProviders(w http.ResponseWriter, r *http.Request) {
	out := []adminProviderJSON{}
	for _, sp := range supportedProviders {
		p, err := s.adminProvider(r.Context(), sp.Name)
		if err != nil {
			s.internalError(w, r, "list providers", err)
			return
		}
		out = append(out, p)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"providers": out, "public_url": s.cfg.PublicURL})
}

// PUT /auth/oauth-providers/{name} (admin). Omit client_secret to keep the
// stored one.
func (s *Service) handlePutProvider(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if _, ok := providerDisplayName(name); !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown_provider", "unsupported sign-in provider")
		return
	}
	var in struct {
		ClientID     string  `json:"client_id"`
		ClientSecret *string `json:"client_secret"`
		Enabled      bool    `json:"enabled"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "invalid JSON body")
		return
	}
	ctx := r.Context()
	cur, _, err := s.getProviderConfig(ctx, name)
	if err != nil {
		s.internalError(w, r, "load provider", err)
		return
	}
	next := providerConfig{Name: name, ClientID: strings.TrimSpace(in.ClientID), ClientSecret: cur.ClientSecret, Enabled: in.Enabled}
	if in.ClientSecret != nil {
		next.ClientSecret = secret(strings.TrimSpace(*in.ClientSecret))
	}
	if next.Enabled {
		if next.ClientID == "" || !next.ClientSecret.isSet() {
			httpx.WriteError(w, http.StatusBadRequest, "incomplete_provider", "client_id and client_secret are required to enable a provider")
			return
		}
		if s.cfg.PublicURL == "" {
			httpx.WriteError(w, http.StatusBadRequest, "public_url_required", "set LISIMFLIX_PUBLIC_URL first; it is part of the redirect URL")
			return
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO oauth_providers(name, client_id, client_secret, enabled, updated_at) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET client_id = excluded.client_id, client_secret = excluded.client_secret,
			enabled = excluded.enabled, updated_at = excluded.updated_at`,
		name, next.ClientID, next.ClientSecret.reveal(), next.Enabled, s.now().Unix()); err != nil {
		s.internalError(w, r, "save provider", err)
		return
	}
	s.log.Info("auth: sign-in provider updated", "provider", name, "enabled", next.Enabled, "client_secret", next.ClientSecret)
	out, err := s.adminProvider(ctx, name)
	if err != nil {
		s.internalError(w, r, "load provider", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 4: Register the routes**

`server/internal/auth/routes.go`:
```go
package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

// Mount implements app.Module. r is already scoped to /api/v1.
func (s *Service) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/auth", func(r chi.Router) { s.authRoutes(r, g) })
	r.Route("/users", func(r chi.Router) { s.userRoutes(r, g) })
	r.Route("/profiles", func(r chi.Router) { s.profileRoutes(r, g) })
	r.Route("/invites", func(r chi.Router) { s.inviteRoutes(r, g) })
}

func (s *Service) authRoutes(r chi.Router, g authctx.Guard) {
	// public
	r.Get("/setup", s.handleSetupStatus)
	r.With(requireJSON).Post("/setup/totp", s.handleSetupTOTP)
	r.With(requireJSON).Post("/setup", s.handleSetup)
	r.With(requireJSON).Post("/login", s.handleLogin)
	r.Get("/providers", s.handlePublicProviders)
	// signed in
	r.With(g.RequireUser).Get("/me", s.handleMe)
	r.With(g.RequireUser).Post("/logout", s.handleLogout)
	r.With(g.RequireUser).Post("/logout-all", s.handleLogoutAll)
	r.With(g.RequireUser).Get("/sessions", s.handleListSessions)
	r.With(g.RequireUser).Delete("/sessions/{id}", s.handleRevokeSession)
	// admin
	r.With(g.RequireAdmin).Get("/oauth-providers", s.handleAdminProviders)
	r.With(g.RequireAdmin).Put("/oauth-providers/{name}", s.handlePutProvider)
}

func (s *Service) userRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireAdmin)
	r.Get("/", s.handleListUsers)
	r.Get("/{id}", s.handleGetUser)
	r.Patch("/{id}", s.handlePatchUser)
	r.Put("/{id}/policy", s.handlePutPolicy)
	r.Delete("/{id}", s.handleDeleteUser)
	r.Get("/{id}/sessions", s.handleUserSessions)
	r.Delete("/{id}/sessions", s.handleRevokeUserSessions)
}

func (s *Service) profileRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireUser)
	r.Get("/", s.handleListProfiles)
	r.Post("/", s.handleCreateProfile)
	r.Patch("/{id}", s.handlePatchProfile)
	r.Delete("/{id}", s.handleDeleteProfile)
	r.Post("/{id}/select", s.handleSelectProfile)
}

func (s *Service) inviteRoutes(r chi.Router, g authctx.Guard) {}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/auth
git commit -m "feat(auth): admin OAuth provider setup with redacted client secrets"
```

---

### Task 11: Invites (admin API, link check, redemption)

**Files:**
- Create: `server/internal/auth/invites.go`
- Modify: `server/internal/auth/routes.go` (full file below)
- Test: `server/internal/auth/invites_test.go`

**Interfaces:**
- Consumes: `validatePolicy`, `insertUserTx`, `newToken`, `hashToken`, `validTokenFormat`, `pathID`, harness.
- Produces: `errInviteInvalid`; `type externalIdentity{Provider, Subject, Email, Name string}`; `type inviteJSON` (`status`: `pending|used|expired`); handlers `handleCreateInvite` (201 `{invite, token, url}` where `url = cfg.PublicURL + "/invite/" + token`; 400 `invalid_invite`), `handleListInvites`, `handleDeleteInvite`, `handleCheckInvite` (404 `invite_invalid`); `(*Service) redeemInvite(ctx, token string, ext externalIdentity) (userID, identityID int64, err error)` — one transaction: create user with the invite's policy and primary profile, insert the identity, mark the invite used (single use, race-safe). Test helper `(*harness) createInvite(adminToken string, body map[string]any) createdInvite`.

- [ ] **Step 1: Write the failing tests**

`server/internal/auth/invites_test.go`:
```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type createdInvite struct {
	Invite inviteJSON `json:"invite"`
	Token  string     `json:"token"`
	URL    string     `json:"url"`
}

// createInvite makes an invite as an admin and returns it with its token.
func (h *harness) createInvite(adminToken string, body map[string]any) createdInvite {
	h.t.Helper()
	rec := h.do("POST", "/api/v1/invites", body, bearer(adminToken))
	wantStatus(h.t, rec, 201)
	return decodeBody[createdInvite](h.t, rec)
}

func TestInviteAdminAPI(t *testing.T) {
	h := newHarness(t)
	root := h.sessionFor(h.insertUser("Root", true))
	ann := h.sessionFor(h.insertUser("Ann", false))

	wantStatus(t, h.do("POST", "/api/v1/invites", map[string]any{}, bearer(ann)), 403)

	inv := h.createInvite(root, map[string]any{
		"note": "for Mum", "display_name": "Mum", "library_ids": []int64{2, 1},
		"max_content_rating": "m", "max_bitrate_kbps": 8000, "max_streams": 1,
	})
	if !validTokenFormat(inv.Token) || inv.URL != testPublicURL+"/invite/"+inv.Token {
		t.Fatalf("token/url %+v", inv)
	}
	want := policy{LibraryIDs: []int64{1, 2}, MaxContentRating: "M", MaxBitrateKbps: 8000, MaxStreams: 1}
	if inv.Invite.Status != "pending" || !reflect.DeepEqual(inv.Invite.Policy, want) ||
		!inv.Invite.ExpiresAt.Equal(h.clock.Now().Add(7*24*time.Hour)) {
		t.Fatalf("invite %+v", inv.Invite)
	}
	if n := count(t, h.d, `SELECT count(*) FROM invites WHERE token_hash = ?`, hashToken(inv.Token)); n != 1 {
		t.Fatal("token not stored hashed")
	}

	for name, body := range map[string]map[string]any{
		"bad rating": {"max_content_rating": "XYZ"},
		"too long":   {"expires_in_hours": 10000},
	} {
		if rec := h.do("POST", "/api/v1/invites", body, bearer(root)); errCode(t, rec) != "invalid_invite" {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	rec := h.do("GET", "/api/v1/auth/invite/"+inv.Token, nil)
	wantStatus(t, rec, 200)
	if got := decodeBody[map[string]any](t, rec); got["display_name"] != "Mum" {
		t.Fatalf("check %v", got)
	}
	wantStatus(t, h.do("GET", "/api/v1/auth/invite/not-a-token", nil), 404)

	short := h.createInvite(root, map[string]any{"expires_in_hours": 1})
	h.clock.Advance(2 * time.Hour)
	wantStatus(t, h.do("GET", "/api/v1/auth/invite/"+short.Token, nil), 404)

	rec = h.do("GET", "/api/v1/invites", nil, bearer(root))
	list := decodeBody[struct{ Invites []inviteJSON }](t, rec).Invites
	if len(list) != 2 || list[0].Status != "expired" || list[1].Status != "pending" || strings.Contains(rec.Body.String(), inv.Token) {
		t.Fatalf("list %+v", list)
	}

	wantStatus(t, h.do("DELETE", fmt.Sprintf("/api/v1/invites/%d", inv.Invite.ID), nil, bearer(root)), 204)
	wantStatus(t, h.do("DELETE", fmt.Sprintf("/api/v1/invites/%d", inv.Invite.ID), nil, bearer(root)), 404)
	wantStatus(t, h.do("GET", "/api/v1/auth/invite/"+inv.Token, nil), 404)
}

func TestRedeemInvite(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	root := h.sessionFor(h.insertUser("Root", true))
	inv := h.createInvite(root, map[string]any{"library_ids": []int64{3}, "max_content_rating": "PG", "max_streams": 2})
	ext := externalIdentity{Provider: "google", Subject: "g-123", Email: "mum@example.com", Name: "Mum G"}

	uid, idn, err := h.s.redeemInvite(ctx, inv.Token, ext)
	if err != nil || uid == 0 || idn == 0 {
		t.Fatalf("redeem: %d %d %v", uid, idn, err)
	}
	u, err := h.s.getUser(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if u.DisplayName != "Mum G" || u.Email != "mum@example.com" || u.IsAdmin || u.Username != "" ||
		!reflect.DeepEqual(u.Policy, policy{LibraryIDs: []int64{3}, MaxContentRating: "PG", MaxStreams: 2}) {
		t.Fatalf("user %+v", u)
	}
	if n := count(t, h.d, `SELECT count(*) FROM user_identities WHERE user_id = ? AND provider = 'google' AND subject = 'g-123'`, uid); n != 1 {
		t.Fatal("identity not linked")
	}
	if n := count(t, h.d, `SELECT count(*) FROM profiles WHERE user_id = ? AND is_primary = 1`, uid); n != 1 {
		t.Fatal("no primary profile")
	}
	if n := count(t, h.d, `SELECT count(*) FROM invites WHERE used_by = ? AND used_at IS NOT NULL`, uid); n != 1 {
		t.Fatal("invite not consumed")
	}

	// single use
	other := externalIdentity{Provider: "google", Subject: "g-999"}
	if _, _, err := h.s.redeemInvite(ctx, inv.Token, other); !errors.Is(err, errInviteInvalid) {
		t.Fatalf("second redeem: %v", err)
	}
	// expired
	late := h.createInvite(root, map[string]any{"expires_in_hours": 1})
	h.clock.Advance(time.Hour)
	if _, _, err := h.s.redeemInvite(ctx, late.Token, other); !errors.Is(err, errInviteInvalid) {
		t.Fatalf("expired redeem: %v", err)
	}
	if _, _, err := h.s.redeemInvite(ctx, "garbage", other); !errors.Is(err, errInviteInvalid) {
		t.Fatalf("garbage token: %v", err)
	}
	// an invite's own display name wins over the provider's
	named := h.createInvite(root, map[string]any{"display_name": "Grandma"})
	uid2, _, err := h.s.redeemInvite(ctx, named.Token, other)
	if err != nil {
		t.Fatal(err)
	}
	if u2, _ := h.s.getUser(ctx, uid2); u2.DisplayName != "Grandma" || len(u2.Policy.LibraryIDs) != 0 {
		t.Fatalf("named invite user %+v", u2)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL, `undefined: inviteJSON`, `undefined: externalIdentity`.

- [ ] **Step 3: Implement**

`server/internal/auth/invites.go`:
```go
package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

const (
	defaultInviteHours = 7 * 24
	maxInviteHours     = 30 * 24
)

var errInviteInvalid = errors.New("invite is invalid, used or expired")

// externalIdentity is a verified sign-in from an OAuth/OIDC provider.
type externalIdentity struct {
	Provider string // "google" | "microsoft"
	Subject  string // stable provider user id ("sub")
	Email    string // informational only
	Name     string
}

type inviteJSON struct {
	ID          int64      `json:"id"`
	Note        string     `json:"note"`
	DisplayName string     `json:"display_name"`
	Policy      policy     `json:"policy"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	UsedAt      *time.Time `json:"used_at"`
	UsedBy      *int64     `json:"used_by"`
	Status      string     `json:"status"` // "pending" | "used" | "expired"
}

const inviteColumns = `id, note, display_name, library_ids, max_content_rating, max_bitrate_kbps,
	max_streams, created_at, expires_at, used_at, used_by`

func (s *Service) scanInvite(sc interface{ Scan(...any) error }) (inviteJSON, error) {
	var in inviteJSON
	var libs string
	var created, expires int64
	var usedAt, usedBy sql.NullInt64
	if err := sc.Scan(&in.ID, &in.Note, &in.DisplayName, &libs, &in.Policy.MaxContentRating,
		&in.Policy.MaxBitrateKbps, &in.Policy.MaxStreams, &created, &expires, &usedAt, &usedBy); err != nil {
		return in, err
	}
	if err := json.Unmarshal([]byte(libs), &in.Policy.LibraryIDs); err != nil {
		return in, fmt.Errorf("invite %d library_ids: %w", in.ID, err)
	}
	in.Policy.LibraryIDs = sortedIDs(in.Policy.LibraryIDs)
	in.CreatedAt, in.ExpiresAt = unix(created), unix(expires)
	switch {
	case usedAt.Valid:
		t := unix(usedAt.Int64)
		in.UsedAt, in.Status = &t, "used"
		if usedBy.Valid {
			in.UsedBy = &usedBy.Int64
		}
	case !s.now().Before(in.ExpiresAt):
		in.Status = "expired"
	default:
		in.Status = "pending"
	}
	return in, nil
}

// POST /invites (admin): returns the token once, with the link to send.
func (s *Service) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Note           string `json:"note"`
		DisplayName    string `json:"display_name"`
		ExpiresInHours int    `json:"expires_in_hours"`
		policy
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if in.ExpiresInHours == 0 {
		in.ExpiresInHours = defaultInviteHours
	}
	if in.ExpiresInHours < 1 || in.ExpiresInHours > maxInviteHours {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_invite", fmt.Sprintf("expires_in_hours must be 1 to %d", maxInviteHours))
		return
	}
	if err := validatePolicy(&in.policy); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_invite", err.Error())
		return
	}
	token, err := newToken()
	if err != nil {
		s.internalError(w, r, "invite token", err)
		return
	}
	libs, _ := json.Marshal(in.LibraryIDs)
	p, _ := authctx.FromContext(r.Context())
	now := s.now()
	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO invites(token_hash, note, display_name, library_ids, max_content_rating,
		                    max_bitrate_kbps, max_streams, created_by, created_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hashToken(token), truncate(in.Note, 200), truncate(in.DisplayName, 100), string(libs),
		in.MaxContentRating, in.MaxBitrateKbps, in.MaxStreams, p.UserID, now.Unix(),
		now.Add(time.Duration(in.ExpiresInHours)*time.Hour).Unix())
	if err != nil {
		s.internalError(w, r, "create invite", err)
		return
	}
	id, _ := res.LastInsertId()
	inv, err := s.scanInvite(s.db.QueryRowContext(r.Context(), `SELECT `+inviteColumns+` FROM invites WHERE id = ?`, id))
	if err != nil {
		s.internalError(w, r, "load invite", err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"invite": inv,
		"token":  token,
		// The web app (phase 3) serves /invite/{token}: it shows the provider
		// buttons, each linking to /api/v1/auth/login/{provider}?invite={token}.
		"url": s.cfg.PublicURL + "/invite/" + token,
	})
}

// GET /invites (admin)
func (s *Service) handleListInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT `+inviteColumns+` FROM invites ORDER BY id DESC`)
	if err != nil {
		s.internalError(w, r, "list invites", err)
		return
	}
	defer rows.Close()
	out := []inviteJSON{}
	for rows.Next() {
		inv, err := s.scanInvite(rows)
		if err != nil {
			s.internalError(w, r, "scan invite", err)
			return
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, r, "list invites", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"invites": out})
}

// DELETE /invites/{id} (admin): revoke an invite.
func (s *Service) handleDeleteInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM invites WHERE id = ?`, id)
	if err != nil {
		s.internalError(w, r, "delete invite", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such invite")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /auth/invite/{token} (public): lets the invite page check a link
// before sending the user to a provider.
func (s *Service) handleCheckInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if !validTokenFormat(token) {
		httpx.WriteError(w, http.StatusNotFound, "invite_invalid", "this invite link is invalid, used or expired")
		return
	}
	var display string
	var expires int64
	err := s.db.QueryRowContext(r.Context(), `SELECT display_name, expires_at FROM invites
		WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`, hashToken(token), s.now().Unix()).
		Scan(&display, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "invite_invalid", "this invite link is invalid, used or expired")
		return
	}
	if err != nil {
		s.internalError(w, r, "check invite", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"valid": true, "display_name": display, "expires_at": unix(expires)})
}

// redeemInvite consumes a pending invite: it creates the user with the
// invite's policy and binds ext to it, all in one transaction. The caller
// has already checked that ext is not linked to any user.
func (s *Service) redeemInvite(ctx context.Context, token string, ext externalIdentity) (userID, identityID int64, err error) {
	if !validTokenFormat(token) {
		return 0, 0, errInviteInvalid
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin redeem: %w", err)
	}
	defer tx.Rollback()

	var inviteID int64
	var display, libs string
	var pol policy
	err = tx.QueryRowContext(ctx, `
		SELECT id, display_name, library_ids, max_content_rating, max_bitrate_kbps, max_streams
		FROM invites WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		hashToken(token), now.Unix()).
		Scan(&inviteID, &display, &libs, &pol.MaxContentRating, &pol.MaxBitrateKbps, &pol.MaxStreams)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, errInviteInvalid
	}
	if err != nil {
		return 0, 0, fmt.Errorf("load invite: %w", err)
	}
	if err := json.Unmarshal([]byte(libs), &pol.LibraryIDs); err != nil {
		return 0, 0, fmt.Errorf("invite %d library_ids: %w", inviteID, err)
	}
	for _, candidate := range []string{display, ext.Name, ext.Email, "New user"} {
		if display = truncate(candidate, 100); display != "" {
			break
		}
	}
	userID, err = insertUserTx(ctx, tx, newUser{DisplayName: display, Email: truncate(ext.Email, 200), Policy: pol}, now)
	if err != nil {
		return 0, 0, err
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO user_identities(user_id, provider, subject, email, created_at, last_login_at)
		VALUES(?, ?, ?, ?, ?, ?)`, userID, ext.Provider, ext.Subject, truncate(ext.Email, 200), now.Unix(), now.Unix())
	if err != nil {
		return 0, 0, fmt.Errorf("link identity: %w", err)
	}
	identityID, _ = res.LastInsertId()
	res, err = tx.ExecContext(ctx, `UPDATE invites SET used_at = ?, used_by = ? WHERE id = ? AND used_at IS NULL`,
		now.Unix(), userID, inviteID)
	if err != nil {
		return 0, 0, fmt.Errorf("consume invite: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, 0, errInviteInvalid
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit redeem: %w", err)
	}
	s.log.Info("auth: invite redeemed", "invite_id", inviteID, "user_id", userID, "provider", ext.Provider)
	return userID, identityID, nil
}
```

- [ ] **Step 4: Register the routes**

`server/internal/auth/routes.go`:
```go
package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

// Mount implements app.Module. r is already scoped to /api/v1.
func (s *Service) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/auth", func(r chi.Router) { s.authRoutes(r, g) })
	r.Route("/users", func(r chi.Router) { s.userRoutes(r, g) })
	r.Route("/profiles", func(r chi.Router) { s.profileRoutes(r, g) })
	r.Route("/invites", func(r chi.Router) { s.inviteRoutes(r, g) })
}

func (s *Service) authRoutes(r chi.Router, g authctx.Guard) {
	// public
	r.Get("/setup", s.handleSetupStatus)
	r.With(requireJSON).Post("/setup/totp", s.handleSetupTOTP)
	r.With(requireJSON).Post("/setup", s.handleSetup)
	r.With(requireJSON).Post("/login", s.handleLogin)
	r.Get("/providers", s.handlePublicProviders)
	r.Get("/invite/{token}", s.handleCheckInvite)
	// signed in
	r.With(g.RequireUser).Get("/me", s.handleMe)
	r.With(g.RequireUser).Post("/logout", s.handleLogout)
	r.With(g.RequireUser).Post("/logout-all", s.handleLogoutAll)
	r.With(g.RequireUser).Get("/sessions", s.handleListSessions)
	r.With(g.RequireUser).Delete("/sessions/{id}", s.handleRevokeSession)
	// admin
	r.With(g.RequireAdmin).Get("/oauth-providers", s.handleAdminProviders)
	r.With(g.RequireAdmin).Put("/oauth-providers/{name}", s.handlePutProvider)
}

func (s *Service) userRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireAdmin)
	r.Get("/", s.handleListUsers)
	r.Get("/{id}", s.handleGetUser)
	r.Patch("/{id}", s.handlePatchUser)
	r.Put("/{id}/policy", s.handlePutPolicy)
	r.Delete("/{id}", s.handleDeleteUser)
	r.Get("/{id}/sessions", s.handleUserSessions)
	r.Delete("/{id}/sessions", s.handleRevokeUserSessions)
}

func (s *Service) profileRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireUser)
	r.Get("/", s.handleListProfiles)
	r.Post("/", s.handleCreateProfile)
	r.Patch("/{id}", s.handlePatchProfile)
	r.Delete("/{id}", s.handleDeleteProfile)
	r.Post("/{id}/select", s.handleSelectProfile)
}

func (s *Service) inviteRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireAdmin)
	r.Get("/", s.handleListInvites)
	r.Post("/", s.handleCreateInvite)
	r.Delete("/{id}", s.handleDeleteInvite)
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `make test`

- [ ] **Step 6: Commit**

```bash
git add server/internal/auth
git commit -m "feat(auth): single-use invites with preset policy"
```

---

### Task 12: OIDC sign-in (Google) with a fake OpenID provider in tests

**Files:**
- Create: `server/internal/auth/oidc.go`
- Modify: `server/internal/auth/service.go` (adds the `oidc` field; full file below), `server/internal/auth/routes.go` (full file below)
- Modify: `server/go.mod`, `server/go.sum` (adds `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`, and `github.com/go-jose/go-jose/v4` for tests)
- Test: `server/internal/auth/fakeidp_test.go`, `server/internal/auth/oidc_test.go`

**Interfaces:**
- Consumes: Task 10 (`getProviderConfig`, `usable`, `redirectURL`, `providerDisplayName`, `secret.reveal`), Task 11 (`redeemInvite`, `errInviteInvalid`, `externalIdentity`, `createInvite` helper), Task 5 (`createSession`, `setSessionCookies`, `isHTTPS`).
- Produces:
  - `type oidcSpec{DiscoveryURL, IssuerTemplate string; Scopes []string}`, `defaultOIDCSpecs()` (Google: `https://accounts.google.com`; Microsoft: `https://login.microsoftonline.com/common/v2.0` with issuer template `https://login.microsoftonline.com/{tenantid}/v2.0`), `microsoftTenantPlaceholder = "{tenantid}"`, `tenantIDPattern`.
  - `type oauthFlow{Provider, Nonce, Verifier, Invite string; LinkUserID int64; ReturnTo string; Expires time.Time}` (`LinkUserID` is used from Task 13), `type oidcState{mu; specs; httpClient *http.Client; providers map[string]*oidc.Provider; flows map[string]oauthFlow}`, `newOIDCState()`, `(*oidcState) clientContext(ctx)`.
  - `(*Service) provider(ctx, name) (*oidc.Provider, oidcSpec, error)` (discovery cached per process), `oauthConfig(pc, p, spec) *oauth2.Config`, `verifyIDToken(ctx, p, spec, pc, raw, nonce string) (externalIdentity, error)`, `safeReturnTo(p) string`, `usableProvider(w, r) (providerConfig, bool)` (404 `provider_not_available`), `beginFlow(w, r, pc, flow) (authURL string, err error)`, `takeFlow(state, provider) (oauthFlow, bool)`, `findIdentity(ctx, ext) (userID, identityID int64, err error)` (`errNotFound`).
  - Handlers `handleOAuthStart` (`GET /auth/login/{provider}`), `handleOAuthCallback` (`GET /auth/callback/{provider}`); callback error codes `provider_denied`, `state_mismatch`, `provider_not_available`, `provider_unavailable`, `token_exchange_failed`, `invalid_id_token`, `invite_invalid`, `not_invited`, `account_disabled`. Constants `oauthStateCookie = "lisimflix_oauth"`, `oauthFlowTTL = 10m`, `maxOAuthFlows = 1000`.
  - Test helpers (`fakeidp_test.go`): `newFakeIdP(t, clientID, clientSecret string, now func() time.Time, tenantMode bool) *fakeIdP` with `DiscoveryURL()`, `IssuerTemplate()`, fields `srv`, `mu`, `tamper func(map[string]any)`; `type fakeUser{Subject, Email, Name, TenantID string}`; `(*harness) useIdP(name, idp)`, `oauthSignIn(provider, idp, user, query, opts...)`, `followProvider(authorizeURL, stateCookie, idp, user)`. In `oidc_test.go`: `googleUser(sub)`, `newGoogle(t, h)`, `wantRedirect(t, rec, location)`.

How the fake provider works: it serves discovery, `/authorize` (signs in the preset `fakeUser` and redirects to our callback with `code` and `state`, like a remembered login), `/token` (checks client id/secret, the PKCE `code_verifier` against the S256 challenge and the `redirect_uri`, then returns an RS256 ID token signed with a per-test RSA key) and `/keys` (JWKS). `tamper` edits claims before signing, for negative tests. `tenantMode` imitates Microsoft's `common` endpoint (used in Task 13).

- [ ] **Step 1: Write the fake provider and the failing tests**

`server/internal/auth/fakeidp_test.go`:
```go
package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// fakeIdP is an in-process OpenID provider for tests: discovery, an
// /authorize endpoint that "signs in" a preset user and redirects back,
// a /token endpoint that checks the client secret and the PKCE verifier,
// and a JWKS endpoint. ID tokens are RS256 JWTs signed with a fresh key.
//
// tenantMode imitates Microsoft's multi-tenant "common" endpoint: discovery
// lives under /common/v2.0 and advertises the templated issuer
// <base>/{tenantid}/v2.0, while tokens carry iss = <base>/<tid>/v2.0.
type fakeIdP struct {
	t            *testing.T
	srv          *httptest.Server
	key          *rsa.PrivateKey
	keyID        string
	clientID     string
	clientSecret string
	now          func() time.Time
	tenantMode   bool

	mu     sync.Mutex
	next   fakeUser                    // who signs in at /authorize
	codes  map[string]fakeCodeGrant    // authorization code -> grant
	tamper func(claims map[string]any) // optional: edit claims before signing
}

type fakeUser struct {
	Subject  string
	Email    string
	Name     string
	TenantID string // tenantMode only
}

type fakeCodeGrant struct {
	user          fakeUser
	nonce         string
	challenge     string
	redirectURI   string
	challengeMeth string
}

func newFakeIdP(t *testing.T, clientID, clientSecret string, now func() time.Time, tenantMode bool) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{
		t: t, key: key, keyID: "test-key-1", clientID: clientID, clientSecret: clientSecret,
		now: now, tenantMode: tenantMode, codes: map[string]fakeCodeGrant{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+f.discoveryPath()+"/.well-known/openid-configuration", f.handleDiscovery)
	mux.HandleFunc("GET /authorize", f.handleAuthorize)
	mux.HandleFunc("POST /token", f.handleToken)
	mux.HandleFunc("GET /keys", f.handleKeys)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) discoveryPath() string {
	if f.tenantMode {
		return "/common/v2.0"
	}
	return ""
}

// DiscoveryURL is what oidcSpec.DiscoveryURL should be set to.
func (f *fakeIdP) DiscoveryURL() string { return f.srv.URL + f.discoveryPath() }

// IssuerTemplate is the tenantMode issuer template ("" otherwise).
func (f *fakeIdP) IssuerTemplate() string {
	if !f.tenantMode {
		return ""
	}
	return f.srv.URL + "/" + microsoftTenantPlaceholder + "/v2.0"
}

func (f *fakeIdP) issuerFor(u fakeUser) string {
	if f.tenantMode {
		return f.srv.URL + "/" + u.TenantID + "/v2.0"
	}
	return f.srv.URL
}

func (f *fakeIdP) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	issuer := f.srv.URL
	if f.tenantMode {
		issuer = f.IssuerTemplate()
	}
	writeTestJSON(w, map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                f.srv.URL + "/authorize",
		"token_endpoint":                        f.srv.URL + "/token",
		"jwks_uri":                              f.srv.URL + "/keys",
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
	})
}

// handleAuthorize skips the login page: it signs in f.next and redirects
// back to redirect_uri with a code, like a provider with a remembered login.
func (f *fakeIdP) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != f.clientID || q.Get("response_type") != "code" {
		http.Error(w, "bad client or response_type", http.StatusBadRequest)
		return
	}
	code := strings.ReplaceAll(rand.Text(), "-", "")
	f.mu.Lock()
	f.codes[code] = fakeCodeGrant{
		user: f.next, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"),
		challengeMeth: q.Get("code_challenge_method"), redirectURI: q.Get("redirect_uri"),
	}
	f.mu.Unlock()
	back, _ := url.Parse(q.Get("redirect_uri"))
	bq := back.Query()
	bq.Set("code", code)
	bq.Set("state", q.Get("state"))
	back.RawQuery = bq.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (f *fakeIdP) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, sec, ok := r.BasicAuth()
	if !ok {
		id, sec = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != f.clientID || sec != f.clientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		writeTestJSON(w, map[string]string{"error": "invalid_client"})
		return
	}
	f.mu.Lock()
	grant, ok := f.codes[r.PostForm.Get("code")]
	delete(f.codes, r.PostForm.Get("code"))
	tamper := f.tamper
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || grant.challengeMeth != "S256" ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != grant.challenge ||
		r.PostForm.Get("redirect_uri") != grant.redirectURI {
		w.WriteHeader(http.StatusBadRequest)
		writeTestJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	now := f.now()
	claims := map[string]any{
		"iss": f.issuerFor(grant.user), "sub": grant.user.Subject, "aud": f.clientID,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": grant.nonce,
		"email": grant.user.Email, "name": grant.user.Name,
	}
	if f.tenantMode {
		claims["tid"] = grant.user.TenantID
	}
	if tamper != nil {
		tamper(claims)
	}
	writeTestJSON(w, map[string]any{
		"access_token": "fake-access-token", "token_type": "Bearer", "expires_in": 3600,
		"id_token": f.sign(claims),
	})
}

func (f *fakeIdP) handleKeys(w http.ResponseWriter, _ *http.Request) {
	writeTestJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: &f.key.PublicKey, KeyID: f.keyID, Algorithm: string(jose.RS256), Use: "sig",
	}}})
}

// sign returns a compact RS256 JWT for claims.
func (f *fakeIdP) sign(claims map[string]any) string {
	f.t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: f.key, KeyID: f.keyID}},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		f.t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		f.t.Fatal(err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	raw, err := jws.CompactSerialize()
	if err != nil {
		f.t.Fatal(err)
	}
	return raw
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// useIdP points provider name at idp and enables it in the DB.
func (h *harness) useIdP(name string, idp *fakeIdP) {
	h.t.Helper()
	h.s.oidc.mu.Lock()
	h.s.oidc.specs[name] = oidcSpec{
		DiscoveryURL: idp.DiscoveryURL(), IssuerTemplate: idp.IssuerTemplate(),
		Scopes: []string{"openid", "email", "profile"},
	}
	h.s.oidc.httpClient = idp.srv.Client()
	delete(h.s.oidc.providers, name)
	h.s.oidc.mu.Unlock()
	mustExec(h.t, h.d, `INSERT INTO oauth_providers(name, client_id, client_secret, enabled, updated_at)
		VALUES(?, ?, ?, 1, 0)`, name, idp.clientID, idp.clientSecret)
}

// oauthSignIn drives a browser through start -> provider -> callback as user
// and returns the callback response. query is appended to the start URL.
func (h *harness) oauthSignIn(provider string, idp *fakeIdP, user fakeUser, query string, opts ...reqOpt) *httptest.ResponseRecorder {
	h.t.Helper()
	start := h.do("GET", "/api/v1/auth/login/"+provider+query, nil, opts...)
	if start.Code != http.StatusFound {
		h.t.Fatalf("start: %d %s", start.Code, start.Body.String())
	}
	return h.followProvider(start.Header().Get("Location"), findCookie(start, oauthStateCookie), idp, user)
}

// followProvider visits the provider's authorize URL as user and delivers
// the redirect to our callback with the state cookie.
func (h *harness) followProvider(authorizeURL string, stateCookie *http.Cookie, idp *fakeIdP, user fakeUser) *httptest.ResponseRecorder {
	h.t.Helper()
	if stateCookie == nil {
		h.t.Fatal("no state cookie set by start")
	}
	idp.mu.Lock()
	idp.next = user
	idp.mu.Unlock()
	client := idp.srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Get(authorizeURL)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(back.String(), testPublicURL+"/api/v1/auth/callback/") {
		h.t.Fatalf("provider redirected to %q", resp.Header.Get("Location"))
	}
	return h.do("GET", back.RequestURI(), nil, withCookies(stateCookie))
}
```

`server/internal/auth/oidc_test.go`:
```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func googleUser(sub string) fakeUser {
	return fakeUser{Subject: sub, Email: sub + "@gmail.example", Name: "User " + sub}
}

func newGoogle(t *testing.T, h *harness) *fakeIdP {
	idp := newFakeIdP(t, "google-client", "google-secret", h.clock.Now, false)
	h.useIdP("google", idp)
	return idp
}

func wantRedirect(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
		t.Fatalf("got %d Location=%q, want redirect to %q; body %s", rec.Code, rec.Header().Get("Location"), want, rec.Body.String())
	}
}

func TestOAuthStartRedirect(t *testing.T) {
	h := newHarness(t)
	idp := newGoogle(t, h)

	rec := h.do("GET", "/api/v1/auth/login/google?return_to=/movies/7", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	q := loc.Query()
	if !strings.HasPrefix(loc.String(), idp.srv.URL+"/authorize?") ||
		q.Get("client_id") != "google-client" || q.Get("response_type") != "code" ||
		q.Get("redirect_uri") != testPublicURL+"/api/v1/auth/callback/google" ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
		q.Get("nonce") == "" || q.Get("state") == "" || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("authorize URL %s", loc)
	}
	c := findCookie(rec, oauthStateCookie)
	if c == nil || c.Value != q.Get("state") || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie %+v", c)
	}

	wantStatus(t, h.do("GET", "/api/v1/auth/login/microsoft", nil), 404) // not configured
	wantStatus(t, h.do("GET", "/api/v1/auth/login/myspace", nil), 404)
}

func TestOAuthInviteThenSignIn(t *testing.T) {
	h := newHarness(t)
	idp := newGoogle(t, h)
	root := h.sessionFor(h.insertUser("Root", true))
	inv := h.createInvite(root, map[string]any{"library_ids": []int64{4}, "max_content_rating": "PG"})

	rec := h.oauthSignIn("google", idp, googleUser("g-1"), "?invite="+inv.Token+"&return_to=/welcome")
	wantRedirect(t, rec, "/welcome")
	sess := findCookie(rec, sessionCookie)
	if sess == nil || findCookie(rec, csrfCookie) == nil {
		t.Fatal("no session cookies after invite sign-in")
	}
	me := h.do("GET", "/api/v1/auth/me", nil, withCookies(sess))
	wantStatus(t, me, 200)
	u := decodeBody[struct{ User userJSON }](t, me).User
	if u.DisplayName != "User g-1" || u.IsAdmin || u.Policy.MaxContentRating != "PG" || len(u.Policy.LibraryIDs) != 1 {
		t.Fatalf("invited user %+v", u)
	}
	var method string
	h.d.QueryRow(`SELECT method FROM sessions WHERE token_hash = ?`, hashToken(sess.Value)).Scan(&method)
	if method != "oauth:google" {
		t.Fatalf("session method %q", method)
	}

	// the invite is single-use: a different Google account cannot reuse it
	rec = h.oauthSignIn("google", idp, googleUser("g-2"), "?invite="+inv.Token)
	wantRedirect(t, rec, "/signin?error=invite_invalid")

	// the linked identity signs in again without an invite
	h.clock.Advance(time.Hour)
	rec = h.oauthSignIn("google", idp, googleUser("g-1"), "")
	wantRedirect(t, rec, "/")
	if findCookie(rec, sessionCookie) == nil {
		t.Fatal("returning user got no session")
	}
	if n := count(t, h.d, `SELECT count(*) FROM users`); n != 2 {
		t.Fatalf("users = %d, want 2 (no duplicate account)", n)
	}
}

func TestOAuthRejectsUninvited(t *testing.T) {
	h := newHarness(t)
	idp := newGoogle(t, h)
	// same email as an existing user must not matter: identities match on sub only
	h.insertUser("Ann", false)
	mustExec(t, h.d, `UPDATE users SET email = 'stranger@gmail.example'`)

	rec := h.oauthSignIn("google", idp, fakeUser{Subject: "g-x", Email: "stranger@gmail.example"}, "")
	wantRedirect(t, rec, "/signin?error=not_invited")
	if findCookie(rec, sessionCookie) != nil || count(t, h.d, `SELECT count(*) FROM sessions`) != 0 {
		t.Fatal("uninvited sign-in created a session")
	}
}

func TestOAuthCallbackFailures(t *testing.T) {
	h := newHarness(t)
	idp := newGoogle(t, h)
	root := h.sessionFor(h.insertUser("Root", true))

	start := func() (string, *http.Cookie) {
		inv := h.createInvite(root, map[string]any{})
		rec := h.do("GET", "/api/v1/auth/login/google?invite="+inv.Token, nil)
		return rec.Header().Get("Location"), findCookie(rec, oauthStateCookie)
	}

	t.Run("missing state cookie", func(t *testing.T) {
		loc, _ := start()
		other := &http.Cookie{Name: oauthStateCookie, Value: "attacker-state"}
		wantRedirect(t, h.followProvider(loc, other, idp, googleUser("a")), "/signin?error=state_mismatch")
	})
	t.Run("state replay", func(t *testing.T) {
		loc, c := start()
		wantRedirect(t, h.followProvider(loc, c, idp, googleUser("b")), "/")
		wantRedirect(t, h.followProvider(loc, c, idp, googleUser("b")), "/signin?error=state_mismatch")
	})
	t.Run("expired flow", func(t *testing.T) {
		loc, c := start()
		h.clock.Advance(oauthFlowTTL)
		wantRedirect(t, h.followProvider(loc, c, idp, googleUser("c")), "/signin?error=state_mismatch")
	})
	t.Run("provider error", func(t *testing.T) {
		_, c := start()
		rec := h.do("GET", "/api/v1/auth/callback/google?error=access_denied&state="+c.Value, nil, withCookies(c))
		wantRedirect(t, rec, "/signin?error=provider_denied")
	})
	for name, tamper := range map[string]func(map[string]any){
		"wrong nonce":    func(c map[string]any) { c["nonce"] = "other" },
		"wrong audience": func(c map[string]any) { c["aud"] = "someone-else" },
		"wrong issuer":   func(c map[string]any) { c["iss"] = "https://evil.example" },
		"expired token":  func(c map[string]any) { c["exp"] = h.clock.Now().Add(-time.Hour).Unix() },
		"no subject":     func(c map[string]any) { c["sub"] = "" },
	} {
		t.Run(name, func(t *testing.T) {
			loc, c := start()
			idp.mu.Lock()
			idp.tamper = tamper
			idp.mu.Unlock()
			defer func() { idp.mu.Lock(); idp.tamper = nil; idp.mu.Unlock() }()
			wantRedirect(t, h.followProvider(loc, c, idp, googleUser("t-"+name)), "/signin?error=invalid_id_token")
		})
	}
	t.Run("wrong client secret", func(t *testing.T) {
		loc, c := start()
		mustExec(t, h.d, `UPDATE oauth_providers SET client_secret = 'wrong' WHERE name = 'google'`)
		defer mustExec(t, h.d, `UPDATE oauth_providers SET client_secret = 'google-secret' WHERE name = 'google'`)
		wantRedirect(t, h.followProvider(loc, c, idp, googleUser("d")), "/signin?error=token_exchange_failed")
	})
	t.Run("disabled account", func(t *testing.T) {
		loc, c := start()
		wantRedirect(t, h.followProvider(loc, c, idp, googleUser("e")), "/")
		mustExec(t, h.d, `UPDATE users SET disabled = 1 WHERE email = 'e@gmail.example'`)
		rec := h.oauthSignIn("google", idp, googleUser("e"), "")
		wantRedirect(t, rec, "/signin?error=account_disabled")
	})
	if strings.Contains(h.logs.String(), "google-secret") {
		t.Fatal("client secret logged")
	}
}

func TestSafeReturnTo(t *testing.T) {
	for in, want := range map[string]string{
		"": "/", "/movies": "/movies", "//evil.example": "/", "https://evil.example": "/", `/\evil`: "/",
	} {
		if got := safeReturnTo(in); got != want {
			t.Errorf("safeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make tidy && make test`
Expected: `go.mod` gains `github.com/go-jose/go-jose/v4`; FAIL with `undefined: oidcSpec`, `h.s.oidc undefined`.

- [ ] **Step 3: Add the OIDC state to the service**

`server/internal/auth/service.go` (replaces the Task 5 version; the only changes are the `oidc` field and its initialisation in `NewService`):
```go
package auth

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/config"
	"github.com/Lisimflix/lisimflix/server/internal/db"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

// Service is the auth module: accounts, profiles, sessions, invites, sign-in
// and access policy. It is safe for concurrent use.
type Service struct {
	db    *db.DB
	cfg   config.Config
	log   *slog.Logger
	now   func() time.Time
	argon argonParams // tests swap in cheap parameters
	limit *limiter

	dummyOnce sync.Once
	dummyHash string

	mu          sync.Mutex
	enrollments map[string]enrollment // pending setup-wizard TOTP enrollments, by id

	oidc *oidcState // OAuth/OIDC sign-in: provider specs, discovery cache, pending flows
}

// enrollment is a TOTP secret shown to the first admin during setup but not
// yet confirmed with a code.
type enrollment struct {
	secret  string
	expires time.Time
}

// NewService creates the auth service. now is the clock (time.Now in production).
func NewService(d *db.DB, cfg config.Config, log *slog.Logger, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	if cfg.PublicURL == "" {
		log.Warn("auth: LISIMFLIX_PUBLIC_URL is not set; Google/Microsoft sign-in stays disabled until it is")
	}
	return &Service{
		db:          d,
		cfg:         cfg,
		log:         log,
		now:         now,
		argon:       defaultArgon,
		limit:       newLimiter(now),
		enrollments: map[string]enrollment{},
		oidc:        newOIDCState(),
	}
}

// Name implements app.Module.
func (s *Service) Name() string { return "auth" }

func (s *Service) hashPassword(pw string) (string, error) { return hashPasswordWith(pw, s.argon) }

// dummyVerify spends the same time as a real password check, so unknown
// usernames cannot be told apart from wrong passwords by timing.
func (s *Service) dummyVerify(pw string) {
	s.dummyOnce.Do(func() { s.dummyHash, _ = hashPasswordWith("lisimflix-dummy-password", s.argon) })
	_, _ = verifyPassword(pw, s.dummyHash)
}

func (s *Service) internalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	s.log.Error("auth: "+msg, "err", err, "path", r.URL.Path)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal server error")
}

// clientIP is the TCP peer address. X-Forwarded-For is deliberately ignored:
// it is client-controlled unless a trusted proxy is configured (phase 4).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isHTTPS reports whether the browser reached us over HTTPS (directly or via
// a TLS-terminating proxy). Cookies are only marked Secure then, because
// browsers drop Secure cookies on plain-HTTP LAN addresses like http://zraid:8420.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// truncate limits free-text fields that come from clients.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
```

- [ ] **Step 4: Implement OIDC sign-in**

`server/internal/auth/oidc.go`:
```go
package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

const (
	oauthStateCookie = "lisimflix_oauth"
	oauthFlowTTL     = 10 * time.Minute
	maxOAuthFlows    = 1000
	// microsoftTenantPlaceholder appears literally in the issuer of
	// Microsoft's multi-tenant ("common") discovery document.
	microsoftTenantPlaceholder = "{tenantid}"
)

var tenantIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// oidcSpec describes how to talk to one provider.
type oidcSpec struct {
	// DiscoveryURL is passed to oidc.NewProvider; it serves
	// <DiscoveryURL>/.well-known/openid-configuration.
	DiscoveryURL string
	// IssuerTemplate is set for multi-tenant issuers (Microsoft "common").
	// It must contain {tenantid}. go-oidc's own issuer check is then turned
	// off and verifyIDToken checks iss == template with the token's tid.
	IssuerTemplate string
	Scopes         []string
}

func defaultOIDCSpecs() map[string]oidcSpec {
	scopes := []string{oidc.ScopeOpenID, "email", "profile"}
	return map[string]oidcSpec{
		"google": {DiscoveryURL: "https://accounts.google.com", Scopes: scopes},
		// "common" accepts personal Microsoft accounts and any work/school
		// tenant (spec 6.1). Tokens carry iss = .../<tenant id>/v2.0.
		"microsoft": {
			DiscoveryURL:   "https://login.microsoftonline.com/common/v2.0",
			IssuerTemplate: "https://login.microsoftonline.com/" + microsoftTenantPlaceholder + "/v2.0",
			Scopes:         scopes,
		},
	}
}

// oauthFlow is a sign-in in progress, keyed by its state value.
type oauthFlow struct {
	Provider   string
	Nonce      string
	Verifier   string // PKCE code_verifier
	Invite     string // invite token, "" for plain sign-in
	LinkUserID int64  // != 0: link the identity to this signed-in user
	ReturnTo   string
	Expires    time.Time
}

type oidcState struct {
	mu         sync.Mutex
	specs      map[string]oidcSpec
	httpClient *http.Client // nil = http.DefaultClient; tests point it at the fake IdP
	providers  map[string]*oidc.Provider
	flows      map[string]oauthFlow
}

func newOIDCState() *oidcState {
	return &oidcState{
		specs:     defaultOIDCSpecs(),
		providers: map[string]*oidc.Provider{},
		flows:     map[string]oauthFlow{},
	}
}

func (o *oidcState) clientContext(ctx context.Context) context.Context {
	if o.httpClient != nil {
		ctx = oidc.ClientContext(ctx, o.httpClient)
		ctx = context.WithValue(ctx, oauth2.HTTPClient, o.httpClient)
	}
	return ctx
}

// provider returns the discovered provider, cached for the process lifetime
// (go-oidc refreshes signing keys by itself when it sees a new key id).
func (s *Service) provider(ctx context.Context, name string) (*oidc.Provider, oidcSpec, error) {
	o := s.oidc
	o.mu.Lock()
	spec, ok := o.specs[name]
	p := o.providers[name]
	o.mu.Unlock()
	if !ok {
		return nil, spec, fmt.Errorf("unknown provider %q", name)
	}
	if p != nil {
		return p, spec, nil
	}
	dctx, cancel := context.WithTimeout(o.clientContext(context.WithoutCancel(ctx)), 10*time.Second)
	defer cancel()
	if spec.IssuerTemplate != "" {
		// Microsoft's common endpoint advertises a templated issuer that never
		// equals the discovery URL. This only relaxes discovery; every token's
		// issuer is still checked exactly in verifyIDToken.
		dctx = oidc.InsecureIssuerURLContext(dctx, spec.IssuerTemplate)
	}
	p, err := oidc.NewProvider(dctx, spec.DiscoveryURL)
	if err != nil {
		return nil, spec, fmt.Errorf("discover %s: %w", name, err)
	}
	o.mu.Lock()
	o.providers[name] = p
	o.mu.Unlock()
	return p, spec, nil
}

func (s *Service) oauthConfig(pc providerConfig, p *oidc.Provider, spec oidcSpec) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     pc.ClientID,
		ClientSecret: pc.ClientSecret.reveal(),
		Endpoint:     p.Endpoint(),
		RedirectURL:  s.redirectURL(pc.Name),
		Scopes:       spec.Scopes,
	}
}

// verifyIDToken checks signature (provider JWKS), audience, expiry, issuer
// and nonce, and returns the identity. Identities are keyed on sub, never email.
func (s *Service) verifyIDToken(ctx context.Context, p *oidc.Provider, spec oidcSpec, pc providerConfig, raw, nonce string) (externalIdentity, error) {
	v := p.Verifier(&oidc.Config{
		ClientID:        pc.ClientID,
		SkipIssuerCheck: spec.IssuerTemplate != "",
		Now:             s.now,
	})
	tok, err := v.Verify(ctx, raw)
	if err != nil {
		return externalIdentity{}, err
	}
	var claims struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		TenantID string `json:"tid"`
	}
	if err := tok.Claims(&claims); err != nil {
		return externalIdentity{}, fmt.Errorf("id token claims: %w", err)
	}
	if spec.IssuerTemplate != "" {
		if !tenantIDPattern.MatchString(claims.TenantID) {
			return externalIdentity{}, errors.New("id token has no valid tid claim")
		}
		want := strings.ReplaceAll(spec.IssuerTemplate, microsoftTenantPlaceholder, claims.TenantID)
		if tok.Issuer != want {
			return externalIdentity{}, fmt.Errorf("id token issuer %q, want %q", tok.Issuer, want)
		}
	}
	if subtle.ConstantTimeCompare([]byte(tok.Nonce), []byte(nonce)) != 1 {
		return externalIdentity{}, errors.New("id token nonce mismatch")
	}
	if tok.Subject == "" {
		return externalIdentity{}, errors.New("id token has no subject")
	}
	return externalIdentity{Provider: pc.Name, Subject: tok.Subject, Email: claims.Email, Name: claims.Name}, nil
}

// safeReturnTo only allows same-site relative paths (no open redirects).
func safeReturnTo(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.Contains(p, `\`) {
		return "/"
	}
	return p
}

// usableProvider loads the {provider} config, writing 404 if it cannot be used.
func (s *Service) usableProvider(w http.ResponseWriter, r *http.Request) (providerConfig, bool) {
	name := chi.URLParam(r, "provider")
	pc, ok, err := s.getProviderConfig(r.Context(), name)
	if err != nil {
		s.internalError(w, r, "load provider", err)
		return pc, false
	}
	if _, known := providerDisplayName(name); !known || !ok || !s.usable(pc) {
		httpx.WriteError(w, http.StatusNotFound, "provider_not_available", "this sign-in method is not set up")
		return pc, false
	}
	return pc, true
}

// beginFlow stores a new flow, sets the state cookie and returns the
// provider's authorization URL (Authorization Code + PKCE S256 + nonce).
func (s *Service) beginFlow(w http.ResponseWriter, r *http.Request, pc providerConfig, flow oauthFlow) (string, error) {
	p, spec, err := s.provider(r.Context(), pc.Name)
	if err != nil {
		return "", err
	}
	state, err := newToken()
	if err != nil {
		return "", err
	}
	nonce, err := newToken()
	if err != nil {
		return "", err
	}
	flow.Provider, flow.Nonce, flow.Verifier = pc.Name, nonce, oauth2.GenerateVerifier()
	flow.Expires = s.now().Add(oauthFlowTTL)

	o := s.oidc
	o.mu.Lock()
	now := s.now()
	for k, f := range o.flows {
		if !now.Before(f.Expires) {
			delete(o.flows, k)
		}
	}
	if len(o.flows) >= maxOAuthFlows {
		o.mu.Unlock()
		return "", errors.New("too many sign-ins in progress")
	}
	o.flows[state] = flow
	o.mu.Unlock()

	// Scoped to the callback path; SameSite=Lax still sends it on the
	// provider's top-level redirect back to us.
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: state, Path: "/api/v1/auth/callback/",
		MaxAge: int(oauthFlowTTL / time.Second), HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
	return s.oauthConfig(pc, p, spec).AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(flow.Verifier),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	), nil
}

// takeFlow removes and returns the flow for state (single use).
func (s *Service) takeFlow(state, provider string) (oauthFlow, bool) {
	o := s.oidc
	o.mu.Lock()
	defer o.mu.Unlock()
	f, ok := o.flows[state]
	delete(o.flows, state)
	if !ok || f.Provider != provider || !s.now().Before(f.Expires) {
		return oauthFlow{}, false
	}
	return f, true
}

// GET /auth/login/{provider}?invite=<token>&return_to=/path (public).
// Must be opened on cfg.PublicURL so the state cookie and the callback share a host.
func (s *Service) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	pc, ok := s.usableProvider(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	flow := oauthFlow{Invite: q.Get("invite"), ReturnTo: safeReturnTo(q.Get("return_to"))}
	if flow.Invite != "" && !validTokenFormat(flow.Invite) {
		http.Redirect(w, r, "/signin?error=invite_invalid", http.StatusFound)
		return
	}
	authURL, err := s.beginFlow(w, r, pc, flow)
	if err != nil {
		s.log.Warn("auth: cannot start sign-in", "provider", pc.Name, "err", err)
		httpx.WriteError(w, http.StatusBadGateway, "provider_unavailable", "the sign-in provider could not be reached")
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// GET /auth/callback/{provider}?code&state (public). Browser-facing: all
// outcomes are redirects. Errors go to /signin?error=<code>, which the web
// app (phase 3) turns into a message.
func (s *Service) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	fail := func(code string) {
		http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: "", Path: "/api/v1/auth/callback/", MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/signin?error="+url.QueryEscape(code), http.StatusFound)
	}
	q := r.URL.Query()
	if q.Get("error") != "" {
		fail("provider_denied")
		return
	}
	state := q.Get("state")
	c, err := r.Cookie(oauthStateCookie)
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		fail("state_mismatch")
		return
	}
	flow, ok := s.takeFlow(state, name)
	if !ok {
		fail("state_mismatch")
		return
	}
	ctx := r.Context()
	pc, ok, err := s.getProviderConfig(ctx, name)
	if err != nil || !ok || !s.usable(pc) {
		fail("provider_not_available")
		return
	}
	p, spec, err := s.provider(ctx, name)
	if err != nil {
		s.log.Warn("auth: provider discovery failed", "provider", name, "err", err)
		fail("provider_unavailable")
		return
	}
	xctx := s.oidc.clientContext(ctx)
	tok, err := s.oauthConfig(pc, p, spec).Exchange(xctx, q.Get("code"), oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		s.log.Warn("auth: code exchange failed", "provider", name, "err", err)
		fail("token_exchange_failed")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		fail("invalid_id_token")
		return
	}
	ext, err := s.verifyIDToken(xctx, p, spec, pc, raw, flow.Nonce)
	if err != nil {
		s.log.Warn("auth: id token rejected", "provider", name, "err", err)
		fail("invalid_id_token")
		return
	}

	userID, identityID, err := s.findIdentity(ctx, ext)
	if err != nil && !errors.Is(err, errNotFound) {
		s.internalError(w, r, "find identity", err)
		return
	}
	found := err == nil

	switch {
	case found:
		// A known identity signs in; an invite in the URL is left unused.
	case flow.Invite != "":
		userID, identityID, err = s.redeemInvite(ctx, flow.Invite, ext)
		if errors.Is(err, errInviteInvalid) {
			fail("invite_invalid")
			return
		}
		if err != nil {
			s.internalError(w, r, "redeem invite", err)
			return
		}
	default:
		// Spec 3.3 / 6.1: a social login on its own never grants access.
		s.log.Info("auth: sign-in refused, identity not invited", "provider", name)
		fail("not_invited")
		return
	}

	u, err := s.getUser(ctx, userID)
	if err != nil {
		s.internalError(w, r, "load user", err)
		return
	}
	if u.Disabled {
		fail("account_disabled")
		return
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE user_identities SET last_login_at = ?, email = ? WHERE id = ?`,
		s.now().Unix(), truncate(ext.Email, 200), identityID); err != nil {
		s.internalError(w, r, "touch identity", err)
		return
	}
	token, _, err := s.createSession(ctx, sessionFromRequest(r, userID, identityID, "oauth:"+name, ""))
	if err != nil {
		s.internalError(w, r, "create session", err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: "", Path: "/api/v1/auth/callback/", MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	setSessionCookies(w, r, token)
	s.log.Info("auth: oauth sign-in", "user_id", userID, "provider", name)
	http.Redirect(w, r, flow.ReturnTo, http.StatusFound)
}

// findIdentity looks up (provider, subject). errNotFound if unlinked.
func (s *Service) findIdentity(ctx context.Context, ext externalIdentity) (userID, identityID int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT user_id, id FROM user_identities WHERE provider = ? AND subject = ?`,
		ext.Provider, ext.Subject).Scan(&userID, &identityID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, errNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("find identity: %w", err)
	}
	return userID, identityID, nil
}
```

Notes for the implementer:
- `oidc.NewProvider` is called with a context derived from `context.WithoutCancel(ctx)`. go-oidc keeps only the HTTP client from that context (its key set uses a background context), so caching the provider beyond the request is safe.
- The Microsoft branch in `provider()` and `verifyIDToken()` is generic (driven by `IssuerTemplate`); Task 13 tests it.

- [ ] **Step 5: Register the routes**

`server/internal/auth/routes.go`:
```go
package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

// Mount implements app.Module. r is already scoped to /api/v1.
func (s *Service) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/auth", func(r chi.Router) { s.authRoutes(r, g) })
	r.Route("/users", func(r chi.Router) { s.userRoutes(r, g) })
	r.Route("/profiles", func(r chi.Router) { s.profileRoutes(r, g) })
	r.Route("/invites", func(r chi.Router) { s.inviteRoutes(r, g) })
}

func (s *Service) authRoutes(r chi.Router, g authctx.Guard) {
	// public
	r.Get("/setup", s.handleSetupStatus)
	r.With(requireJSON).Post("/setup/totp", s.handleSetupTOTP)
	r.With(requireJSON).Post("/setup", s.handleSetup)
	r.With(requireJSON).Post("/login", s.handleLogin)
	r.Get("/providers", s.handlePublicProviders)
	r.Get("/invite/{token}", s.handleCheckInvite)
	r.Get("/login/{provider}", s.handleOAuthStart)
	r.Get("/callback/{provider}", s.handleOAuthCallback)
	// signed in
	r.With(g.RequireUser).Get("/me", s.handleMe)
	r.With(g.RequireUser).Post("/logout", s.handleLogout)
	r.With(g.RequireUser).Post("/logout-all", s.handleLogoutAll)
	r.With(g.RequireUser).Get("/sessions", s.handleListSessions)
	r.With(g.RequireUser).Delete("/sessions/{id}", s.handleRevokeSession)
	// admin
	r.With(g.RequireAdmin).Get("/oauth-providers", s.handleAdminProviders)
	r.With(g.RequireAdmin).Put("/oauth-providers/{name}", s.handlePutProvider)
}

func (s *Service) userRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireAdmin)
	r.Get("/", s.handleListUsers)
	r.Get("/{id}", s.handleGetUser)
	r.Patch("/{id}", s.handlePatchUser)
	r.Put("/{id}/policy", s.handlePutPolicy)
	r.Delete("/{id}", s.handleDeleteUser)
	r.Get("/{id}/sessions", s.handleUserSessions)
	r.Delete("/{id}/sessions", s.handleRevokeUserSessions)
}

func (s *Service) profileRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireUser)
	r.Get("/", s.handleListProfiles)
	r.Post("/", s.handleCreateProfile)
	r.Patch("/{id}", s.handlePatchProfile)
	r.Delete("/{id}", s.handleDeleteProfile)
	r.Post("/{id}/select", s.handleSelectProfile)
}

func (s *Service) inviteRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireAdmin)
	r.Get("/", s.handleListInvites)
	r.Post("/", s.handleCreateInvite)
	r.Delete("/{id}", s.handleDeleteInvite)
}
```

- [ ] **Step 6: Run tests, expect PASS**

Run: `make tidy && make test`
Expected: `go.mod` gains `github.com/coreos/go-oidc/v3` and `golang.org/x/oauth2`; all tests PASS, including every `TestOAuthCallbackFailures` subtest (wrong nonce, audience, issuer, expired token, empty subject, bad client secret, replayed or foreign state, disabled account).

- [ ] **Step 7: Commit**

```bash
git add server/go.mod server/go.sum server/internal/auth
git commit -m "feat(auth): Google sign-in via OIDC with PKCE, nonce and invite binding"
```

---

### Task 13: Microsoft (common tenant) and linking/unlinking providers

**Files:**
- Create: `server/internal/auth/identities.go`
- Modify: `server/internal/auth/oidc.go` (insert the link branch in `handleOAuthCallback`), `server/internal/auth/routes.go` (full file below)
- Test: `server/internal/auth/microsoft_test.go`

**Interfaces:**
- Consumes: Task 12 (`fakeIdP` in `tenantMode`, `oauthFlow.LinkUserID`, `beginFlow`, `usableProvider`, `findIdentity`, `newGoogle`, `googleUser`, `wantRedirect`, `followProvider`, `oauthSignIn`).
- Produces: `handleOAuthLink` (`POST /auth/link/{provider}` → `{"url"}`; `ReturnTo = "/settings/account"`), `(*Service) linkIdentity(ctx, userID, ext) error`, `type identityJSON{id, provider, email, created_at, last_login_at}`, `handleListIdentities`, `handleUnlinkIdentity` (404 `not_found`; 409 `last_sign_in_method`). Callback link outcomes: success → `/settings/account`; identity owned by another user → `/signin?error=identity_in_use`.

- [ ] **Step 1: Write the failing tests**

`server/internal/auth/microsoft_test.go`:
```go
package auth

import (
	"fmt"
	"net/http"
	"testing"
)

const (
	tenantContoso  = "72f988bf-86f1-41af-91ab-2d7cd011db47"
	tenantPersonal = "9188040d-6c67-4c5b-b112-36a304b66dad" // Microsoft personal accounts
)

func newMicrosoft(t *testing.T, h *harness) *fakeIdP {
	idp := newFakeIdP(t, "ms-client", "ms-secret", h.clock.Now, true)
	h.useIdP("microsoft", idp)
	return idp
}

func TestMicrosoftCommonTenantIssuer(t *testing.T) {
	h := newHarness(t)
	idp := newMicrosoft(t, h)
	root := h.sessionFor(h.insertUser("Root", true))

	// work/school tenant and personal account both work through "common"
	for i, tid := range []string{tenantContoso, tenantPersonal} {
		inv := h.createInvite(root, map[string]any{})
		user := fakeUser{Subject: fmt.Sprintf("ms-%d", i), Email: "x@example.com", Name: "MS User", TenantID: tid}
		wantRedirect(t, h.oauthSignIn("microsoft", idp, user, "?invite="+inv.Token), "/")
	}

	for name, c := range map[string]struct {
		tid    string
		tamper func(map[string]any)
	}{
		// iss must be exactly the template with the token's own tid
		"issuer of another tenant": {tenantContoso, func(c map[string]any) {
			c["iss"] = idp.srv.URL + "/" + tenantPersonal + "/v2.0"
		}},
		"templated issuer literally": {tenantContoso, func(c map[string]any) { c["iss"] = idp.IssuerTemplate() }},
		"missing tid":                {tenantContoso, func(c map[string]any) { delete(c, "tid") }},
		"non-guid tid":               {"../evil", nil},
		"foreign issuer":             {tenantContoso, func(c map[string]any) { c["iss"] = "https://evil.example/" + tenantContoso + "/v2.0" }},
	} {
		t.Run(name, func(t *testing.T) {
			inv := h.createInvite(root, map[string]any{})
			idp.mu.Lock()
			idp.tamper = c.tamper
			idp.mu.Unlock()
			defer func() { idp.mu.Lock(); idp.tamper = nil; idp.mu.Unlock() }()
			user := fakeUser{Subject: "ms-bad-" + name, TenantID: c.tid}
			wantRedirect(t, h.oauthSignIn("microsoft", idp, user, "?invite="+inv.Token), "/signin?error=invalid_id_token")
		})
	}
}

func TestLinkAndUnlinkIdentities(t *testing.T) {
	h := newHarness(t)
	google := newGoogle(t, h)
	ms := newMicrosoft(t, h)
	root := h.sessionFor(h.insertUser("Root", true))
	inv := h.createInvite(root, map[string]any{})

	rec := h.oauthSignIn("google", google, googleUser("g-1"), "?invite="+inv.Token)
	wantRedirect(t, rec, "/")
	googleSession := findCookie(rec, sessionCookie).Value

	// link Microsoft to the same account (POST: CSRF-protected for cookie auth)
	wantStatus(t, h.do("POST", "/api/v1/auth/link/microsoft", nil, withCookies(&http.Cookie{Name: sessionCookie, Value: googleSession})), 403)
	rec = h.do("POST", "/api/v1/auth/link/microsoft", nil, cookieAuth(googleSession))
	wantStatus(t, rec, 200)
	authURL := decodeBody[map[string]string](t, rec)["url"]
	msUser := fakeUser{Subject: "ms-1", Email: "me@outlook.example", TenantID: tenantPersonal}
	wantRedirect(t, h.followProvider(authURL, findCookie(rec, oauthStateCookie), ms, msUser), "/settings/account")

	// now Microsoft signs in to the same user, without an invite
	rec = h.oauthSignIn("microsoft", ms, msUser, "")
	wantRedirect(t, rec, "/")
	msSession := findCookie(rec, sessionCookie).Value
	a := decodeBody[struct{ User userJSON }](t, h.do("GET", "/api/v1/auth/me", nil, bearer(googleSession))).User
	b := decodeBody[struct{ User userJSON }](t, h.do("GET", "/api/v1/auth/me", nil, bearer(msSession))).User
	if a.ID != b.ID {
		t.Fatalf("linked sign-in went to user %d, want %d", b.ID, a.ID)
	}

	// an identity already linked to someone else cannot be linked again
	other := h.sessionFor(h.insertUser("Other", false))
	rec = h.do("POST", "/api/v1/auth/link/microsoft", nil, bearer(other))
	wantRedirect(t, h.followProvider(decodeBody[map[string]string](t, rec)["url"], findCookie(rec, oauthStateCookie), ms, msUser),
		"/signin?error=identity_in_use")

	rec = h.do("GET", "/api/v1/auth/identities", nil, bearer(googleSession))
	ids := decodeBody[struct{ Identities []identityJSON }](t, rec).Identities
	if len(ids) != 2 || ids[0].Provider != "google" || ids[1].Provider != "microsoft" {
		t.Fatalf("identities %+v", ids)
	}

	// unlinking Microsoft ends the sessions it created, not the others
	wantStatus(t, h.do("DELETE", fmt.Sprintf("/api/v1/auth/identities/%d", ids[1].ID), nil, bearer(other)), 404)
	wantStatus(t, h.do("DELETE", fmt.Sprintf("/api/v1/auth/identities/%d", ids[1].ID), nil, bearer(googleSession)), 204)
	wantStatus(t, h.do("GET", "/api/v1/auth/me", nil, bearer(msSession)), 401)
	wantStatus(t, h.do("GET", "/api/v1/auth/me", nil, bearer(googleSession)), 200)

	// the last sign-in method cannot be removed
	rec = h.do("DELETE", fmt.Sprintf("/api/v1/auth/identities/%d", ids[0].ID), nil, bearer(googleSession))
	if rec.Code != 409 || errCode(t, rec) != "last_sign_in_method" {
		t.Fatalf("unlink last: %d %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL to compile, `undefined: identityJSON`. (Once it compiles, the Microsoft issuer subtests pass straight away because Task 12's `verifyIDToken` is already template-driven; they pin that behaviour. `TestLinkAndUnlinkIdentities` is the one that needs this task's code.)

- [ ] **Step 3: Implement linking, listing and unlinking**

`server/internal/auth/identities.go`:
```go
package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
	"github.com/Lisimflix/lisimflix/server/internal/httpx"
)

// POST /auth/link/{provider} (signed in): returns {"url"} for the web app to
// open. Signing in there links that provider identity to the current account
// (spec 6.1: users can link several providers).
func (s *Service) handleOAuthLink(w http.ResponseWriter, r *http.Request) {
	pc, ok := s.usableProvider(w, r)
	if !ok {
		return
	}
	p, _ := authctx.FromContext(r.Context())
	authURL, err := s.beginFlow(w, r, pc, oauthFlow{LinkUserID: p.UserID, ReturnTo: "/settings/account"})
	if err != nil {
		s.log.Warn("auth: cannot start link", "provider", pc.Name, "err", err)
		httpx.WriteError(w, http.StatusBadGateway, "provider_unavailable", "the sign-in provider could not be reached")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": authURL})
}

func (s *Service) linkIdentity(ctx context.Context, userID int64, ext externalIdentity) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_identities(user_id, provider, subject, email, created_at, last_login_at)
		VALUES(?, ?, ?, ?, ?, 0)`, userID, ext.Provider, ext.Subject, truncate(ext.Email, 200), s.now().Unix())
	if err != nil {
		return fmt.Errorf("link %s identity: %w", ext.Provider, err)
	}
	return nil
}

type identityJSON struct {
	ID          int64     `json:"id"`
	Provider    string    `json:"provider"`
	Email       string    `json:"email"`
	CreatedAt   time.Time `json:"created_at"`
	LastLoginAt time.Time `json:"last_login_at,omitzero"`
}

// GET /auth/identities (signed in): my linked sign-in providers.
func (s *Service) handleListIdentities(w http.ResponseWriter, r *http.Request) {
	p, _ := authctx.FromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT id, provider, email, created_at, last_login_at
		FROM user_identities WHERE user_id = ? ORDER BY id`, p.UserID)
	if err != nil {
		s.internalError(w, r, "list identities", err)
		return
	}
	defer rows.Close()
	out := []identityJSON{}
	for rows.Next() {
		var id identityJSON
		var created, last int64
		if err := rows.Scan(&id.ID, &id.Provider, &id.Email, &created, &last); err != nil {
			s.internalError(w, r, "scan identity", err)
			return
		}
		id.CreatedAt = unix(created)
		if last != 0 {
			id.LastLoginAt = unix(last)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, r, "list identities", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"identities": out})
}

// DELETE /auth/identities/{id} (signed in): unlink a provider. Sessions that
// were signed in through it end (sessions.identity_id ON DELETE CASCADE).
// The last way to sign in cannot be removed.
func (s *Service) handleUnlinkIdentity(w http.ResponseWriter, r *http.Request) {
	p, _ := authctx.FromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.internalError(w, r, "begin unlink", err)
		return
	}
	defer tx.Rollback()
	var owned, others int
	var hasPassword bool
	err = tx.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM user_identities WHERE id = ? AND user_id = ?),
			(SELECT count(*) FROM user_identities WHERE id != ? AND user_id = ?),
			(SELECT password_hash IS NOT NULL FROM users WHERE id = ?)`,
		id, p.UserID, id, p.UserID, p.UserID).Scan(&owned, &others, &hasPassword)
	if err != nil {
		s.internalError(w, r, "check identities", err)
		return
	}
	if owned == 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such linked sign-in")
		return
	}
	if others == 0 && !hasPassword {
		httpx.WriteError(w, http.StatusConflict, "last_sign_in_method", "link another provider before removing this one")
		return
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_identities WHERE id = ? AND user_id = ?`, id, p.UserID); err != nil {
		s.internalError(w, r, "unlink identity", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.internalError(w, r, "commit unlink", err)
		return
	}
	s.log.Info("auth: identity unlinked", "user_id", p.UserID, "identity_id", id)
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Handle link flows in the callback**

In `server/internal/auth/oidc.go`, `handleOAuthCallback`, replace the line

```go
	found := err == nil
```

with:

```go
	found := err == nil

	if flow.LinkUserID != 0 {
		switch {
		case found && userID != flow.LinkUserID:
			fail("identity_in_use")
		case found:
			http.Redirect(w, r, flow.ReturnTo, http.StatusFound)
		default:
			if err := s.linkIdentity(ctx, flow.LinkUserID, ext); err != nil {
				s.internalError(w, r, "link identity", err)
				return
			}
			s.log.Info("auth: identity linked", "user_id", flow.LinkUserID, "provider", name)
			http.Redirect(w, r, flow.ReturnTo, http.StatusFound)
		}
		return
	}
```

(The block sits before the `switch` that handles sign-in, so a link flow never creates a session or redeems an invite.)

- [ ] **Step 5: Register the routes**

`server/internal/auth/routes.go`:
```go
package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/Lisimflix/lisimflix/server/internal/authctx"
)

// Mount implements app.Module. r is already scoped to /api/v1.
func (s *Service) Mount(r chi.Router, g authctx.Guard) {
	r.Route("/auth", func(r chi.Router) { s.authRoutes(r, g) })
	r.Route("/users", func(r chi.Router) { s.userRoutes(r, g) })
	r.Route("/profiles", func(r chi.Router) { s.profileRoutes(r, g) })
	r.Route("/invites", func(r chi.Router) { s.inviteRoutes(r, g) })
}

func (s *Service) authRoutes(r chi.Router, g authctx.Guard) {
	// public
	r.Get("/setup", s.handleSetupStatus)
	r.With(requireJSON).Post("/setup/totp", s.handleSetupTOTP)
	r.With(requireJSON).Post("/setup", s.handleSetup)
	r.With(requireJSON).Post("/login", s.handleLogin)
	r.Get("/providers", s.handlePublicProviders)
	r.Get("/invite/{token}", s.handleCheckInvite)
	r.Get("/login/{provider}", s.handleOAuthStart)
	r.Get("/callback/{provider}", s.handleOAuthCallback)
	// signed in
	r.With(g.RequireUser).Get("/me", s.handleMe)
	r.With(g.RequireUser).Post("/logout", s.handleLogout)
	r.With(g.RequireUser).Post("/logout-all", s.handleLogoutAll)
	r.With(g.RequireUser).Get("/sessions", s.handleListSessions)
	r.With(g.RequireUser).Delete("/sessions/{id}", s.handleRevokeSession)
	r.With(g.RequireUser).Post("/link/{provider}", s.handleOAuthLink)
	r.With(g.RequireUser).Get("/identities", s.handleListIdentities)
	r.With(g.RequireUser).Delete("/identities/{id}", s.handleUnlinkIdentity)
	// admin
	r.With(g.RequireAdmin).Get("/oauth-providers", s.handleAdminProviders)
	r.With(g.RequireAdmin).Put("/oauth-providers/{name}", s.handlePutProvider)
}

func (s *Service) userRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireAdmin)
	r.Get("/", s.handleListUsers)
	r.Get("/{id}", s.handleGetUser)
	r.Patch("/{id}", s.handlePatchUser)
	r.Put("/{id}/policy", s.handlePutPolicy)
	r.Delete("/{id}", s.handleDeleteUser)
	r.Get("/{id}/sessions", s.handleUserSessions)
	r.Delete("/{id}/sessions", s.handleRevokeUserSessions)
}

func (s *Service) profileRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireUser)
	r.Get("/", s.handleListProfiles)
	r.Post("/", s.handleCreateProfile)
	r.Patch("/{id}", s.handlePatchProfile)
	r.Delete("/{id}", s.handleDeleteProfile)
	r.Post("/{id}/select", s.handleSelectProfile)
}

func (s *Service) inviteRoutes(r chi.Router, g authctx.Guard) {
	r.Use(g.RequireAdmin)
	r.Get("/", s.handleListInvites)
	r.Post("/", s.handleCreateInvite)
	r.Delete("/{id}", s.handleDeleteInvite)
}
```

- [ ] **Step 6: Run tests, expect PASS**

Run: `make test`
Expected: all PASS. Sanity check that the issuer test is meaningful: temporarily change `if tok.Issuer != want {` to `if false {` in `verifyIDToken`, run `make test`, see `TestMicrosoftCommonTenantIssuer` fail on "issuer of another tenant", then revert.

- [ ] **Step 7: Commit**

```bash
git add server/internal/auth
git commit -m "feat(auth): Microsoft sign-in via common tenant and linking of extra providers"
```

---

### Task 14: Wire the auth service into `main`, open the PR

**Files:**
- Modify: `server/cmd/lisimflix/main.go`, `server/cmd/lisimflix/main_test.go`

**Interfaces:**
- Consumes: `auth.NewService`, `(*auth.Service) Guard()`, `auth.Service` as `app.Module`.
- Produces: the running server uses the real guard and serves the auth module. 1C's modules can be given `authSvc` wherever they take a `library.Visibility` (same `VisibleLibraryIDs` signature).

- [ ] **Step 1: Extend the end-to-end test (failing)**

In `server/cmd/lisimflix/main_test.go`, add `"io"` and `"net/http"` to the imports, and in `TestServeHealthcheckAndShutdown` insert this block directly **before** the `cancel()` line:

```go
	// the auth module is wired in: a fresh install asks for the setup wizard
	resp, err := http.Get("http://" + addr + "/api/v1/auth/setup")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"needed":true`) {
		t.Fatalf("auth setup status: %d %s", resp.StatusCode, body)
	}
	// and protected routes use the real guard (401 JSON, not the deny-all stub)
	resp, err = http.Get("http://" + addr + "/api/v1/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("auth me: %d %v", resp.StatusCode, resp.Header)
	}
```

- [ ] **Step 2: Run and confirm failure**

Run: `make test`
Expected: FAIL in `cmd/lisimflix`: `auth setup status: 404`.

- [ ] **Step 3: Wire it up**

In `server/cmd/lisimflix/main.go`, replace the import line

```go
	"github.com/Lisimflix/lisimflix/server/internal/authctx"
```

with

```go
	"github.com/Lisimflix/lisimflix/server/internal/auth"
```

and replace

```go
	a := app.New(cfg, d, log, authctx.DenyAllGuard{})
```

with

```go
	authSvc := auth.NewService(d, cfg, log, time.Now)
	a := app.New(cfg, d, log, authSvc.Guard(), authSvc)
```

(`time` is already imported by `main.go`. `authctx.DenyAllGuard` stays in `authctx` for tests.)

- [ ] **Step 4: Run all checks**

Run: `make fmt && make vet && make test`
Expected: no files reformatted, vet clean, every package PASS.

- [ ] **Step 5: Manual smoke test on zraid**

```bash
make image
T=$(mktemp -d)
docker run -d --name lisimflix-1b -p 8422:8420 -v $T/config:/config -v $T/cache:/cache -v $T:/media:ro lisimflix:dev
sleep 3
curl -s http://127.0.0.1:8422/api/v1/auth/setup
curl -s -X POST -H 'Content-Type: application/json' -d '{"username":"zac"}' http://127.0.0.1:8422/api/v1/auth/setup/totp
docker rm -f lisimflix-1b
```
Expected: `{"needed":true}`, then a JSON object with `enrollment_id`, `secret` and an `otpauth://totp/Lisimflix:zac?...` URL. (Optionally finish setup by scanning the URL with an authenticator app and POSTing `/api/v1/auth/setup`.)

- [ ] **Step 6: Commit, push, open the PR**

```bash
git add server/cmd/lisimflix
git commit -m "feat(auth): use the real auth guard and module in the server"
git push -u origin feat/1b-auth
gh pr create --base main --title "feat(auth): accounts, profiles, sessions, invites, local admin and Google/Microsoft sign-in" \
  --body "$(printf '## What changed\nPlan 1B: auth & accounts (setup wizard with Argon2id + TOTP, local admin login with lockout, hashed per-device sessions via Bearer or cookie + CSRF, profiles with PINs, per-user policies, invites, Google and Microsoft OIDC sign-in, real guard in main).\n\n## Source\nSpec §3.3, §6.1, §6.2; plan docs/superpowers/plans/2026-10-24-phase1b-auth.md\n\n## How it was tested\nmake fmt/vet/test (OIDC against the in-process fake provider); smoke test on zraid (Task 14 step 5)\n')"
gh pr checks --watch
```
Expected: CI `test` passes. Another member reviews (`/code-review` first pass, then reads it). Once approved, the **author** squash-merges (agents never merge).

---

## Done when

- `make test` and `make vet` pass; CI green on the PR.
- A fresh server reports `{"needed":true}` from `GET /api/v1/auth/setup`; after the wizard it returns 409 for both setup endpoints.
- Local admin login needs password + current TOTP code, refuses replays, and locks out with backoff.
- Sessions work via Bearer and via cookie (+ `X-Lisimflix-CSRF` for writes), slide for 30 days, cap at 180, and can be revoked singly or everywhere.
- Profiles with PINs work; `Principal.ProfileID` follows profile selection.
- Admin can set per-user library/rating/bitrate/stream policy; `VisibleLibraryIDs` and `MaxContentRating` reflect it.
- Google and Microsoft sign-in work only through an invite (first time) or an already linked identity; identities are matched on (provider, subject).

## Notes for plans that consume 1B

- **1C** (libraries): pass `authSvc` as the `library.Visibility` in `main` (same `VisibleLibraryIDs`
  signature). Deleting a library needs no change to `user_library_access`: `VisibleLibraryIDs` joins
  `libraries`, so stale grants drop out.
- **1D/1F and later:** filter items with `VisibleLibraryIDs` and compare ratings with
  `auth.RatingAge(item) <= auth.RatingAge(max)` when `MaxContentRating` is not "".
- **Phase 2:** `StreamLimits(ctx, p)` gives `MaxBitrateKbps` and `MaxStreams` (0 = unlimited).
- **Phase 3 web app:** reads `lisimflix_csrf` and sends it as `X-Lisimflix-CSRF` on writes; shows
  `/signin?error=<code>` messages; serves `/invite/{token}`; starts OAuth on `LISIMFLIX_PUBLIC_URL`.

## Self-review

- **Spec coverage:** §3.3 admin vs invited users, home profiles with 4-digit PIN, per-user visible
  libraries / max rating / max bitrate / max streams (Tasks 6, 8, 9, 11); download permission is
  phase 6 and not stored yet. §6.1 Google + Microsoft OIDC with PKCE, state, nonce, JWKS
  validation, admin-configured providers hidden when unconfigured, exact redirect URL shown in
  admin, invite binding, subject-not-email matching, multiple linked providers, local admin
  Argon2id + TOTP (Tasks 3, 6, 7, 10-13). §6.2 hashed per-device revocable sessions, sign out
  everywhere, unlink ends that identity's sessions, rate limiting with backoff and an admin-visible
  warning, secrets never logged (Tasks 5, 7, 10, 13). Apple/Facebook/generic OIDC, sign-up
  requests, non-admin local passwords and QR TV sign-in (§5.4) are phase 4/5 by the spec's phase table.
- **Placeholders:** none; every code step contains the full file or the exact replacement.
- **Type consistency:** `session`, `user`, `policy`, `profile`, `providerConfig`, `externalIdentity`
  and `oauthFlow` are defined once and used with the same fields in later tasks; `routes.go` and
  `service.go` are shown in full whenever they change. The code was compiled and the full test
  suite (also with `-race`) run against plan 1A's packages while writing this plan.

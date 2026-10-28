# OPS-5 (G1 baseline): Server Event and Error Logging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** Draft for review. Claimed by @Lammy93 in #37; reviewer @lisim. Not implemented. Every code block and diff below was built and tested on `main` at `d07a4c7` in a scratch copy before this plan was written: `go vet`, `go test -race ./...`, repeated runs of the timing tests, and 8 mutation checks (listed in the self-review). That is evidence for the plan, not module acceptance.

**Goal:** Give the M1 server a structured, redacted record of what happened: sign-ins, session ends, playback sessions, source errors and failed requests, each tied to a request ID that the client also sees.

**Architecture:** A tiny `obs` package carries the request ID in the context and builds loggers that include it. `app` wraps every request in an `observe` middleware that assigns the ID (`X-Request-Id`, which `contracts.WriteError` already reuses) and logs requests that end in 4xx/5xx. `auth` and `playback` log their own events through an injected `*slog.Logger`, and the `lisimflix` command passes its JSON logger to all three. Nothing new is stored; logs go to stderr (`docker logs`).

**Tech Stack:** Go 1.27.3 standard library (`log/slog`, including `slog.DiscardHandler`). No new dependencies.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md): §6 ("Log security events with actor/action/result and correlation ID, redacting secrets"), §9.4 OPS-5 ("baseline errors required in G1"), §11 ("structured bounded logs with request/session/job correlation IDs and redaction").

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **OPS-5**, G1 baseline slice only (the rest of OPS-5, diagnostics UI and job/backup health, stays P1 / G2–G3) |
| Issue | #37 |
| Owner / reviewer | Lammy (claimant) / Pete |
| Acceptance | Spec §6 security-event logging and §11 correlation and redaction for the M1 server. Proven by captured-log tests, including one full sign-in → play → logout run through the real composition |
| File scope | New: `server/internal/obs/**`, `server/internal/app/observe.go` (+ test), `log_test.go` in `auth`, `playback` and `cmd/lisimflix`. Modified: `app/app.go`, `auth/session.go`, `auth/http.go`, `playback/sessions.go`, `playback/http.go`, `cmd/lisimflix/serve.go`, test helpers in `app/app_test.go` and `cmd/lisimflix/main_test.go`, and `deploy/README.md` (one paragraph). No contract, storage, web, CI or `go.mod` changes |
| Consumes | The merged M1 modules (OPS-1 #29, PLY-2 #27, M1 integration #31) |
| Produces | `obs.NewRequestID`, `obs.WithRequestID`, `obs.RequestID`, `obs.Log`; `auth.(*Manager).SetLogger`; `playback.Options.Log`; `app.New(…, log *slog.Logger)` |
| Start dependency | None. Can start now |
| Standalone test | `go -C server test -race ./internal/obs ./internal/app ./internal/auth ./internal/playback ./cmd/lisimflix`, then `make check` |
| Integration proof | `TestLogsTellTheStoryWithoutSecrets` runs the real composition (config, auth, playback, storage, app). Afterwards, one run of the image shows the events in `docker logs` |

## Global Constraints

- Go 1.27.3, `CGO_ENABLED=0`, standard library only. `make check` passes locally before pushing; Actions runs it on the PR.
- **Never logged:** passwords, session cookies or tokens, CSRF tokens, content grants, query strings or full content URLs, request or response bodies, the media root or source paths. Request paths are logged without the query string.
- **Failed sign-ins never log the username:** a password typed into the username field would otherwise end up in the log.
- Logs are JSON lines on stderr through the command's existing `slog.NewJSONHandler`. Packages log nothing by default: a nil or unset logger discards.
- IDs that are logged are not secrets: request IDs (`req-…`), auth session IDs (`as-…`), playback session IDs (`ps-…`) and movie IDs. The session ID is not the cookie value.
- `observe`'s response wrapper must keep `Unwrap()`, so `http.ResponseController` still reaches the connection (PLY-2's write deadlines depend on it), and must not add `io.ReaderFrom`.
- Workflow: branch `feat/ops-5-logging`, never push `main`, never `--no-verify`. PR title `feat(platform): baseline event and error logging (OPS-5)`, template filled, reviewer Pete, author squash-merges after approval. Agents never merge.

## Event catalogue

| Event (`msg`) | Level | Fields | When |
|---|---|---|---|
| `auth.login` | INFO | `result=ok`, `username`, `session_id`, `peer`, `request_id` | Sign-in succeeded |
| `auth.login` | INFO | `result=fail`, `reason` (`invalid_request`, `rate_limited`, `busy`, `bad_credentials`), `peer`, `request_id` | Sign-in refused. **No username** |
| `auth.session_end` | INFO | `session_id`, `reason` (`logout`, `rotated`, `expired`, `evicted`), `request_id` when caused by a request | An auth session ended. Expiry by the background sweep has no request ID |
| `playback.start` | INFO | `session_id`, `auth_session`, `item_id`, `version_id`, `request_id` | A playback session was created (not on idempotent replay) |
| `playback.end` | INFO | `session_id`, `auth_session`, `reason` (`stopped`, `expired`, `revoked`, `closed`) | A playback session ended; heartbeat timeout is `expired`, logout is `revoked`, shutdown is `closed` |
| `playback.source_error` | WARN | `code` (`source_changed`, `source_unavailable`), `session_id` (empty on start), `item_id`, `version_id`, `request_id` | The movie file is missing or changed |
| `http.request` | INFO for 4xx, ERROR for 5xx | `method`, `path` (no query), `status`, `bytes`, `duration_ms`, `request_id` | Any request ending in 4xx/5xx. Successful requests are not logged |

`peer` is the same key the rate limiter uses: the IPv4 address, or the /64 for IPv6.

## Decisions taken in this plan

- **Request ID for every response, not just the API.** `observe` is the outermost handler, so static and health responses get an `X-Request-Id` too. `apiHeaders` stops setting its own. The ID in the error JSON (`requestId`) and in the log always matches the header.
- **Errors only for `http.request`.** Logging every 206 range request of a movie would be noise. Successful flows are covered by the domain events instead.
- **Reasons, not separate events,** for session ends, so one query covers every way a session can end.
- **Interface changes** (all additive except one):
  - `app.New` gains a final `log *slog.Logger` parameter (nil discards). This changes an approved signature (#18/#24); the integration caller is updated in this plan.
  - `auth.(*Manager).SetLogger(*slog.Logger)`, called before serving. `NewManager` keeps its signature.
  - `playback.Options.Log`.
- **Logging while holding playback's mutex:** `playback.end` is logged inside `endLocked`. The handler writes a short line to stderr; nothing it does can call back into playback.

## File Structure

```
server/internal/obs/
  obs.go                   request ID in context; Log(ctx, base) adds request_id
  obs_test.go
server/internal/app/
  observe.go               observe middleware + recorder (status, bytes, Unwrap)
  observe_test.go
  app.go                   New(…, log); observe outermost; apiHeaders no longer sets the ID
  app_test.go              New calls gain the log argument
server/internal/auth/
  session.go               log field, SetLogger, revoke(ctx, reason, ids…), create/lookup/remove take ctx
  http.go                  auth.login ok/fail events; reasons for rotation and logout
  log_test.go
server/internal/playback/
  sessions.go              Options.Log, Server.log, endLocked(…, reason) logs playback.end
  http.go                  playback.start, sourceError method logs playback.source_error
  log_test.go
server/cmd/lisimflix/
  serve.go                 pass the logger to playback, auth and app
  main_test.go             newFixtureWith(t, size, log, extra…)
  log_test.go              end-to-end captured-log test
deploy/README.md           one paragraph on reading the logs
```

---

### Task 1: Request IDs and request-error logging

**Files:**
- Create: `server/internal/obs/obs.go`, `server/internal/obs/obs_test.go`, `server/internal/app/observe.go`, `server/internal/app/observe_test.go`
- Modify: `server/internal/app/app.go`, `server/internal/app/app_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  ```go
  func obs.NewRequestID() string                                 // "req-" + rand.Text()
  func obs.WithRequestID(ctx context.Context, id string) context.Context
  func obs.RequestID(ctx context.Context) string                 // "" outside a request
  func obs.Log(ctx context.Context, base *slog.Logger) *slog.Logger // base + request_id; nil base discards
  func app.New(cfg m1config.Config, am *auth.Manager, playback http.Handler, web fs.FS,
      ready func(context.Context) error, log *slog.Logger) (http.Handler, error)
  ```

- [ ] **Step 1: Write the failing tests**

`server/internal/obs/obs_test.go`:
```go
package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"testing"
)

func TestRequestIDRoundTrip(t *testing.T) {
	id := NewRequestID()
	if !regexp.MustCompile(`^req-[A-Z2-7]{26}$`).MatchString(id) || NewRequestID() == id {
		t.Fatalf("NewRequestID = %q", id)
	}
	ctx := WithRequestID(context.Background(), id)
	if RequestID(ctx) != id || RequestID(context.Background()) != "" {
		t.Fatal("RequestID did not round-trip")
	}
}

func TestLogAddsTheRequestID(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	Log(WithRequestID(context.Background(), "req-X"), base).Info("auth.login")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil || line["request_id"] != "req-X" {
		t.Fatalf("line = %s", buf.Bytes())
	}
	buf.Reset()
	Log(context.Background(), base).Info("background")
	if bytes.Contains(buf.Bytes(), []byte("request_id")) {
		t.Fatalf("a request ID was added outside a request: %s", buf.Bytes())
	}
	Log(context.Background(), nil).Info("discarded") // a nil base must not panic
}
```

`server/internal/app/observe_test.go`:
```go
package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/obs"
)

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func TestEveryResponseHasARequestID(t *testing.T) {
	var seen string
	h := observe(slog.New(slog.DiscardHandler), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = obs.RequestID(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/watch", nil))
	id := rec.Header().Get("X-Request-Id")
	if id == "" || id != seen {
		t.Fatalf("header %q, context %q: every response needs one ID, the same in both", id, seen)
	}
}

func TestErrorRequestsAreLoggedWithoutQuery(t *testing.T) {
	var buf bytes.Buffer
	h := observe(slog.New(slog.NewJSONHandler(&buf, nil)), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			io.WriteString(w, "fine")
		case "/missing":
			http.Error(w, "nope", http.StatusNotFound)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	for _, p := range []string{"/ok", "/missing?grant=SECRET-GRANT", "/broken"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
	}
	if bytes.Contains(buf.Bytes(), []byte("SECRET-GRANT")) {
		t.Fatalf("the query string was logged: %s", buf.String())
	}
	lines := logLines(t, &buf)
	if len(lines) != 2 {
		t.Fatalf("logged %d lines, want the two error requests only: %s", len(lines), buf.String())
	}
	for i, want := range []struct {
		path, level string
		status      float64
	}{{"/missing", "INFO", 404}, {"/broken", "ERROR", 500}} {
		l := lines[i]
		if l["msg"] != "http.request" || l["path"] != want.path || l["status"] != want.status || l["level"] != want.level ||
			l["method"] != "GET" || l["request_id"] == nil || l["bytes"] == nil || l["duration_ms"] == nil {
			t.Errorf("line %d = %v", i, l)
		}
	}
}

// Playback sets write deadlines through http.ResponseController; the logging
// wrapper must pass them through or a stalled client can't be cut off.
func TestWriteDeadlinesReachThroughTheLogger(t *testing.T) {
	srv := httptest.NewServer(observe(slog.New(slog.DiscardHandler), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		io.WriteString(w, "ok")
	})))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "ok" {
		t.Fatalf("SetWriteDeadline through the logger = %q", b)
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go -C server test ./internal/obs ./internal/app`
Expected: FAIL, `undefined: NewRequestID` (and `observe`).

- [ ] **Step 3: Implement**

`server/internal/obs/obs.go`:
```go
// Package obs carries the per-request ID and builds loggers that include it.
// Logs are structured (log/slog) and must never contain passwords, cookies,
// CSRF tokens, content grants or query strings (spec §6, OPS-5).
package obs

import (
	"context"
	"crypto/rand"
	"log/slog"
)

type requestIDKey struct{}

// NewRequestID returns a fresh, non-secret correlation ID.
func NewRequestID() string { return "req-" + rand.Text() }

// WithRequestID attaches id to ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request's ID, or "" outside a request.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Log returns base with the request's ID attached. A nil base discards.
func Log(ctx context.Context, base *slog.Logger) *slog.Logger {
	if base == nil {
		return slog.New(slog.DiscardHandler)
	}
	if id := RequestID(ctx); id != "" {
		return base.With("request_id", id)
	}
	return base
}
```

`server/internal/app/observe.go`:
```go
package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/obs"
)

// observe gives every request an ID (context and X-Request-Id, which
// contracts.WriteError reuses) and logs requests that end in an error:
// 4xx at INFO, 5xx at ERROR. It logs the path without the query string, so
// content grants never reach the log.
func observe(log *slog.Logger, next http.Handler) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := obs.NewRequestID()
		w.Header().Set("X-Request-Id", id)
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r.WithContext(obs.WithRequestID(r.Context(), id)))
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		if status < 400 {
			return
		}
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		}
		log.Log(r.Context(), level, "http.request", "request_id", id, "method", r.Method, "path", r.URL.Path,
			"status", status, "bytes", rec.bytes, "duration_ms", time.Since(start).Milliseconds())
	})
}

// recorder notes the status and body size. Unwrap lets
// http.ResponseController reach the connection, so playback's write
// deadlines still work; it deliberately adds no io.ReaderFrom.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
```

`server/internal/app/app.go`: `New` takes the logger and wraps everything in `observe`; `apiHeaders` no longer sets the ID.
```diff
--- a/server/internal/app/app.go
+++ b/server/internal/app/app.go
@@ -6,8 +6,8 @@ package app
 import (
 	"context"
-	"crypto/rand"
 	"errors"
 	"io"
 	"io/fs"
+	"log/slog"
 	"net/http"
 	"net/url"
@@ -32,5 +32,5 @@ const (
 // session. web must contain index.html. ready reports whether initialisation
 // succeeded; /health/ready returns 503 while it returns an error.
-func New(cfg m1config.Config, am *auth.Manager, playback http.Handler, web fs.FS, ready func(context.Context) error) (http.Handler, error) {
+func New(cfg m1config.Config, am *auth.Manager, playback http.Handler, web fs.FS, ready func(context.Context) error, log *slog.Logger) (http.Handler, error) {
 	if am == nil || playback == nil || web == nil || ready == nil {
 		return nil, errors.New("app: auth manager, playback handler, web assets and ready check are required")
@@ -76,5 +76,5 @@ func New(cfg m1config.Config, am *auth.Manager, playback http.Handler, web fs.FS
 	})
 	root.Handle("/", spa(web))
-	return securityHeaders(root), nil
+	return observe(log, securityHeaders(root)), nil
 }
 
@@ -119,9 +119,8 @@ func hostPort(scheme, host string) string {
 }
 
-// apiHeaders sets the request ID that contracts.WriteError reuses, marks
-// every API response private, and caps request bodies.
+// apiHeaders marks every API response private and caps request bodies.
+// observe has already set the request ID that contracts.WriteError reuses.
 func apiHeaders(next http.Handler) http.Handler {
 	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
-		w.Header().Set("X-Request-Id", "req-"+rand.Text())
 		w.Header().Set("Cache-Control", "no-store")
 		r.Body = http.MaxBytesReader(w, r.Body, maxAPIBodyBytes)
```

`server/internal/app/app_test.go`: the existing `New` calls pass `nil` for the logger.
```diff
--- a/server/internal/app/app_test.go
+++ b/server/internal/app/app_test.go
@@ -88,5 +88,5 @@ func newEnv(t *testing.T, origin string) *env {
 		defer e.mu.Unlock()
 		return e.ready
-	})
+	}, nil)
 	if err != nil {
 		t.Fatal(err)
@@ -326,14 +326,14 @@ func TestNewRejectsInconsistentInputs(t *testing.T) {
 	ready := func(context.Context) error { return nil }
 	play := &fakePlayback{}
-	if _, err := New(cfg, secureAM, play, web, ready); err == nil {
+	if _, err := New(cfg, secureAM, play, web, ready, nil); err == nil {
 		t.Error("accepted Secure cookies on an http origin")
 	}
 	for name, fn := range map[string]func() error{
-		"nil auth":     func() error { _, err := New(cfg, nil, play, web, ready); return err },
-		"nil playback": func() error { _, err := New(cfg, plainAM, nil, web, ready); return err },
-		"nil web":      func() error { _, err := New(cfg, plainAM, play, nil, ready); return err },
-		"nil ready":    func() error { _, err := New(cfg, plainAM, play, web, nil); return err },
+		"nil auth":     func() error { _, err := New(cfg, nil, play, web, ready, nil); return err },
+		"nil playback": func() error { _, err := New(cfg, plainAM, nil, web, ready, nil); return err },
+		"nil web":      func() error { _, err := New(cfg, plainAM, play, nil, ready, nil); return err },
+		"nil ready":    func() error { _, err := New(cfg, plainAM, play, web, nil, nil); return err },
 		"no index": func() error {
-			_, err := New(cfg, plainAM, play, fstest.MapFS{"a.js": {Data: []byte("x")}}, ready)
+			_, err := New(cfg, plainAM, play, fstest.MapFS{"a.js": {Data: []byte("x")}}, ready, nil)
 			return err
 		},
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go -C server vet ./internal/obs ./internal/app && go -C server test -race ./internal/obs ./internal/app`
Expected: `ok` for both. Every response carries an `X-Request-Id` equal to the context's ID; only the 404 and 500 requests are logged, at INFO and ERROR, without the query string; `SetWriteDeadline` works through the wrapper. (`./cmd/lisimflix` won't compile until Task 4 passes the new argument.)

- [ ] **Step 5: Commit**

```bash
git add server/internal/obs server/internal/app
git commit -m "feat(platform): request IDs on every response and request-error logging (OPS-5)"
```

---

### Task 2: Auth events

**Files:**
- Modify: `server/internal/auth/session.go`, `server/internal/auth/http.go`
- Test: `server/internal/auth/log_test.go`

**Interfaces:**
- Consumes: `obs.Log` (Task 1).
- Produces: `func (m *Manager) SetLogger(l *slog.Logger)`. Internal: `revoke(ctx, reason, ids…)`, and `create`, `lookup` and `remove` now take a `context.Context` (`remove` also takes the reason).

- [ ] **Step 1: Write the failing tests**

`server/internal/auth/log_test.go`:
```go
package auth

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer lets the background sweep and handlers log concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func find(lines []map[string]any, msg string, kv ...string) map[string]any {
	for _, l := range lines {
		if l["msg"] != msg {
			continue
		}
		ok := true
		for i := 0; i+1 < len(kv); i += 2 {
			if l[kv[i]] != kv[i+1] {
				ok = false
			}
		}
		if ok {
			return l
		}
	}
	return nil
}

func TestAuthEventsAreLogged(t *testing.T) {
	h := newHarness(t, false)
	var buf syncBuffer
	h.m.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	c := h.client()
	c.session()
	c.login("viewer", "wrong password here")
	c.login("viewer", testPassword)
	first := sessionCookie(t, c)
	_, p := c.principal()
	c.login("viewer", testPassword) // rotation ends the first session
	c.do("POST", "/api/v1/auth/logout", "", nil)

	other := h.client()
	other.session()
	other.login("viewer", testPassword)
	_, op := other.principal()
	h.clock.Advance(idleTimeout + time.Second)
	h.m.Sweep()

	lines := buf.lines(t)
	fail := find(lines, "auth.login", "result", "fail", "reason", "bad_credentials")
	if fail == nil || fail["peer"] == nil || fail["username"] != nil {
		t.Fatalf("failed login event = %v; it needs reason and peer, and never the username", fail)
	}
	if ok := find(lines, "auth.login", "result", "ok", "username", "viewer"); ok == nil || ok["session_id"] == nil || ok["peer"] == nil {
		t.Fatalf("successful login event = %v", ok)
	}
	for reason, id := range map[string]string{"rotated": p["session"], "expired": op["session"]} {
		if find(lines, "auth.session_end", "reason", reason, "session_id", id) == nil {
			t.Errorf("no auth.session_end with reason %s for %s: %s", reason, id, buf.String())
		}
	}
	if find(lines, "auth.session_end", "reason", "logout") == nil {
		t.Errorf("no auth.session_end with reason logout: %s", buf.String())
	}
	for name, secret := range map[string]string{"password": testPassword, "session cookie": first, "CSRF token": c.token} {
		if secret != "" && strings.Contains(buf.String(), secret) {
			t.Errorf("the %s appears in the log", name)
		}
	}
}

func TestRateLimitedLoginIsLogged(t *testing.T) {
	var calls int
	defer stubArgon(t, &calls)()
	h := newHarness(t, false)
	var buf syncBuffer
	h.m.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	c := h.client()
	c.session()
	for i := 0; i <= loginLimit; i++ {
		c.login("viewer", "wrong password here")
	}
	if find(buf.lines(t), "auth.login", "result", "fail", "reason", "rate_limited") == nil {
		t.Fatalf("no rate_limited event: %s", buf.String())
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go -C server test ./internal/auth`
Expected: FAIL, `h.m.SetLogger undefined`.

- [ ] **Step 3: Implement**

`server/internal/auth/session.go`:
```diff
--- a/server/internal/auth/session.go
+++ b/server/internal/auth/session.go
@@ -8,4 +8,5 @@ import (
 	"encoding/base64"
 	"errors"
+	"log/slog"
 	"strings"
 	"sync"
@@ -13,4 +14,5 @@ import (
 
 	"github.com/Lisimflix/lisimflix/server/internal/m1config"
+	"github.com/Lisimflix/lisimflix/server/internal/obs"
 )
 
@@ -49,4 +51,5 @@ type Manager struct {
 	onRevoke func(authSessionID string)
 	now      func() time.Time
+	log      *slog.Logger // events (obs); discards until SetLogger
 	// Two argon2 slots: anySlot serves every login; viewerSlot serves only the
 	// configured username, so a spray of fake usernames can't starve the
@@ -78,5 +81,5 @@ func NewManager(viewer m1config.Viewer, secure bool, onRevoke func(string)) (*Ma
 	dummy := params{memory: p.memory, time: p.time, threads: p.threads, salt: salt, key: make([]byte, len(p.key))}
 	return &Manager{
-		viewer: viewer, hash: p, dummy: dummy, secure: secure, onRevoke: onRevoke, now: time.Now,
+		viewer: viewer, hash: p, dummy: dummy, secure: secure, onRevoke: onRevoke, now: time.Now, log: slog.New(slog.DiscardHandler),
 		anySlot: make(chan struct{}, 1), viewerSlot: make(chan struct{}, 1), checking: map[string]bool{},
 		sessions: map[[sha256.Size]byte]*session{}, limits: map[string]*attempts{},
@@ -205,5 +208,13 @@ func (m *Manager) succeeded(key string) {
 // create stores a new session and returns its cookie token. If the store is
 // full, the least recently used session is evicted and revoked.
-func (m *Manager) create() (token string, s *session, err error) {
+// SetLogger sets where auth events go (login, session end). Call it before
+// serving. Events never include passwords, tokens or cookies.
+func (m *Manager) SetLogger(l *slog.Logger) {
+	if l != nil {
+		m.log = l
+	}
+}
+
+func (m *Manager) create(ctx context.Context) (token string, s *session, err error) {
 	raw := make([]byte, tokenBytes)
 	if _, err := rand.Read(raw); err != nil {
@@ -215,5 +226,5 @@ func (m *Manager) create() (token string, s *session, err error) {
 	var evicted []string
 	m.mu.Lock()
-	evicted = m.sweepLocked(now)
+	expiredIDs := m.sweepLocked(now)
 	for len(m.sessions) >= maxSessions {
 		var oldest [sha256.Size]byte
@@ -229,5 +240,6 @@ func (m *Manager) create() (token string, s *session, err error) {
 	m.sessions[sha256.Sum256([]byte(token))] = s
 	m.mu.Unlock()
-	m.revoke(evicted...)
+	m.revoke(ctx, "expired", expiredIDs...)
+	m.revoke(ctx, "evicted", evicted...)
 	return token, s, nil
 }
@@ -235,5 +247,5 @@ func (m *Manager) create() (token string, s *session, err error) {
 // lookup returns the live session for token and marks it used. An expired
 // session is removed and revoked.
-func (m *Manager) lookup(token string) (*session, bool) {
+func (m *Manager) lookup(ctx context.Context, token string) (*session, bool) {
 	if len(token) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
 		return nil, false
@@ -246,5 +258,5 @@ func (m *Manager) lookup(token string) (*session, bool) {
 		delete(m.sessions, key)
 		m.mu.Unlock()
-		m.revoke(s.id)
+		m.revoke(ctx, "expired", s.id)
 		return nil, false
 	}
@@ -257,5 +269,6 @@ func (m *Manager) lookup(token string) (*session, bool) {
 
 // remove deletes the session for token, if any, and revokes it.
-func (m *Manager) remove(token string) bool {
+// remove ends the session for token, if any, with reason "logout" or "rotated".
+func (m *Manager) remove(ctx context.Context, token, reason string) bool {
 	if len(token) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
 		return false
@@ -267,5 +280,5 @@ func (m *Manager) remove(token string) bool {
 	m.mu.Unlock()
 	if ok {
-		m.revoke(s.id)
+		m.revoke(ctx, reason, s.id)
 	}
 	return ok
@@ -293,5 +306,5 @@ func (m *Manager) Sweep() {
 	ids := m.sweepLocked(m.now())
 	m.mu.Unlock()
-	m.revoke(ids...)
+	m.revoke(context.Background(), "expired", ids...)
 }
 
@@ -310,6 +323,9 @@ func (m *Manager) Run(ctx context.Context, every time.Duration) {
 }
 
-func (m *Manager) revoke(ids ...string) {
+// revoke logs each ended session with its reason (logout, rotated, expired
+// or evicted) and calls onRevoke.
+func (m *Manager) revoke(ctx context.Context, reason string, ids ...string) {
 	for _, id := range ids {
+		obs.Log(ctx, m.log).Info("auth.session_end", "session_id", id, "reason", reason)
 		m.onRevoke(id)
 	}
```

`server/internal/auth/http.go`:
```diff
--- a/server/internal/auth/http.go
+++ b/server/internal/auth/http.go
@@ -17,4 +17,5 @@ import (
 
 	"github.com/Lisimflix/lisimflix/server/internal/contracts"
+	"github.com/Lisimflix/lisimflix/server/internal/obs"
 )
 
@@ -80,5 +81,5 @@ func (m *Manager) fromRequest(r *http.Request) (*session, bool) {
 		return nil, false
 	}
-	return m.lookup(c.Value)
+	return m.lookup(r.Context(), c.Value)
 }
 
@@ -98,17 +99,24 @@ func (m *Manager) handleSession(w http.ResponseWriter, r *http.Request) {
 
 func (m *Manager) handleLogin(w http.ResponseWriter, r *http.Request) {
+	log := obs.Log(r.Context(), m.log)
+	peer := peerKey(r.RemoteAddr)
+	// Failures never log the username: a mistyped password often lands there.
+	fail := func(reason string) { log.Info("auth.login", "result", "fail", "reason", reason, "peer", peer) }
 	req, ok := decodeLogin(w, r)
 	if !ok {
+		fail("invalid_request")
 		contracts.WriteError(w, r, contracts.CodeInvalidRequest, "Send a JSON username and password.")
 		return
 	}
-	key := strings.ToLower(req.Username) + "|" + peerKey(r.RemoteAddr)
+	key := strings.ToLower(req.Username) + "|" + peer
 	if limited, wait := m.limited(key); limited {
+		fail("rate_limited")
 		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
 		contracts.WriteError(w, r, contracts.CodeRateLimited, "Too many sign-in attempts. Try again later.")
 		return
 	}
-	good, err := m.checkPassword(r.Context(), peerKey(r.RemoteAddr), req.Username, []byte(req.Password))
+	good, err := m.checkPassword(r.Context(), peer, req.Username, []byte(req.Password))
 	if err != nil {
+		fail("busy")
 		w.Header().Set("Retry-After", "1")
 		contracts.WriteError(w, r, contracts.CodeRateLimited, "Sign-in is busy. Try again in a moment.")
@@ -116,4 +124,5 @@ func (m *Manager) handleLogin(w http.ResponseWriter, r *http.Request) {
 	}
 	if !good {
+		fail("bad_credentials")
 		m.failed(key)
 		contracts.WriteError(w, r, contracts.CodeUnauthenticated, "Invalid username or password.")
@@ -122,11 +131,12 @@ func (m *Manager) handleLogin(w http.ResponseWriter, r *http.Request) {
 	m.succeeded(key)
 	if c, err := r.Cookie(CookieName); err == nil {
-		m.remove(c.Value) // rotate: the old session ends
+		m.remove(r.Context(), c.Value, "rotated") // the old session ends
 	}
-	token, s, err := m.create()
+	token, s, err := m.create(r.Context())
 	if err != nil {
 		contracts.WriteError(w, r, contracts.CodeInternalError, "Internal error.")
 		return
 	}
+	log.Info("auth.login", "result", "ok", "username", req.Username, "session_id", s.id, "peer", peer)
 	http.SetCookie(w, &http.Cookie{
 		Name: CookieName, Value: token, Path: "/", Expires: s.created.Add(absoluteTimeout),
@@ -138,5 +148,5 @@ func (m *Manager) handleLogin(w http.ResponseWriter, r *http.Request) {
 func (m *Manager) handleLogout(w http.ResponseWriter, r *http.Request) {
 	c, err := r.Cookie(CookieName)
-	if err != nil || !m.remove(c.Value) {
+	if err != nil || !m.remove(r.Context(), c.Value, "logout") {
 		contracts.WriteError(w, r, contracts.CodeUnauthenticated, "Sign in to continue.")
 		return
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go -C server vet ./internal/auth && go -C server test -race ./internal/auth`
Expected: `ok`. All earlier auth tests still pass. A failed sign-in logs its reason and peer but never the username. Rotation, logout and expiry each log `auth.session_end` with their reason. The password, session cookie and CSRF token never appear in the captured log.

- [ ] **Step 5: Commit**

```bash
git add server/internal/auth
git commit -m "feat(auth): log sign-in results and session ends (OPS-5)"
```

---

### Task 3: Playback events

**Files:**
- Modify: `server/internal/playback/sessions.go`, `server/internal/playback/http.go`
- Test: `server/internal/playback/log_test.go`

**Interfaces:**
- Consumes: `obs.Log` (Task 1).
- Produces: `playback.Options.Log *slog.Logger` (nil discards). Internal: `endLocked(sess, now, reason)`, `(*Server).sourceError(w, r, err, sessionID)`.

- [ ] **Step 1: Write the failing test**

`server/internal/playback/log_test.go`:
```go
package playback

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) has(msg string, kv ...string) bool {
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) != nil || m["msg"] != msg {
			continue
		}
		ok := true
		for i := 0; i+1 < len(kv); i += 2 {
			if m[kv[i]] != kv[i+1] {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func TestPlaybackEventsAreLogged(t *testing.T) {
	var buf syncBuffer
	e := newEnv(t, 4096, func(o *Options) { o.Log = slog.New(slog.NewJSONHandler(&buf, nil)) })

	stopped := e.mustStart(alice, "k1")
	e.req(&alice, "DELETE", "/api/v1/playback/sessions/"+stopped.ID, "", nil)
	revoked := e.mustStart(alice, "k2")
	e.s.RevokeAuthSession(alice.AuthSessionID)
	expired := e.mustStart(alice2, "k3")
	e.clock.Advance(idleTimeout + time.Second)
	waitFor(t, func() bool { return buf.has("playback.end", "session_id", expired.ID, "reason", "expired") })

	if !buf.has("playback.start", "session_id", stopped.ID, "auth_session", alice.AuthSessionID, "movie_id", movieID) {
		t.Errorf("no playback.start for %s: %s", stopped.ID, buf.String())
	}
	for id, reason := range map[string]string{stopped.ID: "stopped", revoked.ID: "revoked"} {
		if !buf.has("playback.end", "session_id", id, "reason", reason) {
			t.Errorf("no playback.end %s for %s: %s", reason, id, buf.String())
		}
	}

	live := e.mustStart(alice2, "k4")
	repl := filepath.Join(e.dir, "Movies", "new.mp4")
	os.WriteFile(repl, bytes.Repeat([]byte{1}, 4096), 0o644)
	later := time.Now().Add(time.Hour)
	os.Chtimes(repl, later, later)
	os.Rename(repl, e.path)
	e.req(&alice2, "GET", live.ContentURL, "", map[string]string{"Range": "bytes=0-9"})
	if !buf.has("playback.source_error", "session_id", live.ID, "code", "source_changed") {
		t.Errorf("no source_changed event: %s", buf.String())
	}
	e.s.Close()
	if !buf.has("playback.end", "session_id", live.ID, "reason", "closed") {
		t.Errorf("no playback.end closed: %s", buf.String())
	}

	for _, ps := range []string{stopped.ContentURL, revoked.ContentURL, live.ContentURL} {
		grant := ps[strings.Index(ps, "grant=")+len("grant="):]
		if strings.Contains(buf.String(), grant) {
			t.Fatal("a content grant appears in the log")
		}
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go -C server test ./internal/playback`
Expected: FAIL, `o.Log undefined`.

- [ ] **Step 3: Implement**

`server/internal/playback/sessions.go`:
```diff
--- a/server/internal/playback/sessions.go
+++ b/server/internal/playback/sessions.go
@@ -2,4 +2,5 @@ package playback
 
 import (
+	"cmp"
 	"context"
 	"crypto/hmac"
@@ -9,4 +10,5 @@ import (
 	"errors"
 	"io/fs"
+	"log/slog"
 	"os"
 	"strings"
@@ -35,4 +37,5 @@ type Options struct {
 	MaxSessions int
 	Authorize   func(contracts.Principal, string) bool
+	Log         *slog.Logger // session events and source errors; nil discards
 }
 
@@ -66,4 +69,5 @@ type Server struct {
 	authorize func(contracts.Principal, string) bool
 	now       func() time.Time
+	log       *slog.Logger
 
 	inflight atomic.Int64 // content handlers still running, revoked or not
@@ -99,5 +103,5 @@ func New(root *os.Root, opts Options) (*Server, error) {
 	s := &Server{
 		root: root, src: opts.Source, grantKey: append([]byte(nil), opts.GrantKey...), max: opts.MaxSessions,
-		authorize: opts.Authorize, now: time.Now,
+		authorize: opts.Authorize, now: time.Now, log: cmp.Or(opts.Log, slog.New(slog.DiscardHandler)),
 		sessions: map[string]*session{}, byKey: map[string]*session{}, done: make(chan struct{}),
 	}
@@ -143,10 +147,12 @@ func (sess *session) public() contracts.PlaybackSession {
 
 // endLocked turns an active session into a tombstone and cuts off its
-// deliveries. s.mu must be held.
-func (s *Server) endLocked(sess *session, now time.Time) {
+// deliveries, logging why (stopped, expired, revoked or closed). s.mu must
+// be held.
+func (s *Server) endLocked(sess *session, now time.Time, reason string) {
 	if !sess.ended.IsZero() {
 		return
 	}
 	sess.ended = now
+	s.log.Info("playback.end", "session_id", sess.id, "auth_session", sess.owner.AuthSessionID, "reason", reason)
 	// Cancel first, then set the deadline: a write that set its own deadline
 	// before this one is unblocked by it, and a write that sets its deadline
@@ -185,5 +191,5 @@ func (s *Server) boundTombstonesLocked() {
 func (s *Server) liveLocked(sess *session, now time.Time) bool {
 	if sess.ended.IsZero() && now.After(sess.deadline) {
-		s.endLocked(sess, now)
+		s.endLocked(sess, now, "expired")
 	}
 	return sess.ended.IsZero()
@@ -225,5 +231,5 @@ func (s *Server) RevokeAuthSession(authSessionID string) {
 	for _, sess := range s.sessions {
 		if sess.owner.AuthSessionID == authSessionID {
-			s.endLocked(sess, now)
+			s.endLocked(sess, now, "revoked")
 		}
 	}
@@ -239,5 +245,5 @@ func (s *Server) Close() error {
 	now := s.now()
 	for _, sess := range s.sessions {
-		s.endLocked(sess, now)
+		s.endLocked(sess, now, "closed")
 	}
 	return nil
```

`server/internal/playback/http.go`:
```diff
--- a/server/internal/playback/http.go
+++ b/server/internal/playback/http.go
@@ -14,4 +14,5 @@ import (
 
 	"github.com/Lisimflix/lisimflix/server/internal/contracts"
+	"github.com/Lisimflix/lisimflix/server/internal/obs"
 )
 
@@ -99,5 +100,5 @@ func (s *Server) start(w http.ResponseWriter, r *http.Request, p contracts.Princ
 	info, err := s.statSource()
 	if err != nil {
-		sourceError(w, r, err)
+		s.sourceError(w, r, err, "")
 		return
 	}
@@ -124,4 +125,5 @@ func (s *Server) start(w http.ResponseWriter, r *http.Request, p contracts.Princ
 	out := sess.public()
 	s.mu.Unlock()
+	obs.Log(r.Context(), s.log).Info("playback.start", "session_id", sess.id, "auth_session", p.AuthSessionID, "movie_id", req.MovieID)
 	writeJSON(w, http.StatusCreated, out)
 }
@@ -206,5 +208,5 @@ func (s *Server) stop(w http.ResponseWriter, r *http.Request, p contracts.Princi
 		return
 	}
-	s.endLocked(sess, s.now())
+	s.endLocked(sess, s.now(), "stopped")
 	w.WriteHeader(http.StatusNoContent)
 }
@@ -235,5 +237,5 @@ func (s *Server) content(w http.ResponseWriter, r *http.Request, p contracts.Pri
 	f, info, err := s.open(sess)
 	if err != nil {
-		sourceError(w, r, err)
+		s.sourceError(w, r, err, sess.id)
 		return
 	}
@@ -303,10 +305,13 @@ func (d *deliveryWriter) Write(p []byte) (int, error) {
 }
 
-func sourceError(w http.ResponseWriter, r *http.Request, err error) {
+// sourceError answers and logs a missing or changed source. sessionID is ""
+// for a start.
+func (s *Server) sourceError(w http.ResponseWriter, r *http.Request, err error, sessionID string) {
+	code, msg := contracts.CodeSourceUnavailable, "The movie file is not available."
 	if errors.Is(err, errSourceChanged) {
-		contracts.WriteError(w, r, contracts.CodeSourceChanged, "The movie file changed. Start playback again.")
-		return
+		code, msg = contracts.CodeSourceChanged, "The movie file changed. Start playback again."
 	}
-	contracts.WriteError(w, r, contracts.CodeSourceUnavailable, "The movie file is not available.")
+	obs.Log(r.Context(), s.log).Warn("playback.source_error", "code", code, "session_id", sessionID, "movie_id", s.src.Movie.ID)
+	contracts.WriteError(w, r, code, msg)
 }
 
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go -C server vet ./internal/playback && go -C server test -race ./internal/playback`
Expected: `ok`, including the delivery-abort tests. `playback.start` and `playback.end` with `stopped`, `revoked`, `expired` (by the sweeper) and `closed`, plus `playback.source_error` for a replaced file. No content grant appears in the log.

- [ ] **Step 5: Commit**

```bash
git add server/internal/playback
git commit -m "feat(playback): log session starts, ends and source errors (OPS-5)"
```

---

### Task 4: Wire it in the command, end-to-end test, deploy docs

**Files:**
- Modify: `server/cmd/lisimflix/serve.go`, `server/cmd/lisimflix/main_test.go`, `deploy/README.md`
- Test: `server/cmd/lisimflix/log_test.go`

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces: the composed server logs every event in the catalogue through the command's JSON logger.

- [ ] **Step 1: Write the failing test**

`server/cmd/lisimflix/main_test.go`: add `newFixtureWith`, which takes the logger:
```diff
--- a/server/cmd/lisimflix/main_test.go
+++ b/server/cmd/lisimflix/main_test.go
@@ -55,4 +55,13 @@ func newFixture(t *testing.T, extra ...storage.Source) *fixture {
 func newFixtureSized(t *testing.T, size int, extra ...storage.Source) *fixture {
 	t.Helper()
+	return newFixtureWith(t, size, nil, extra...)
+}
+
+// newFixtureWith also takes the server's logger (nil discards).
+func newFixtureWith(t *testing.T, size int, log *slog.Logger, extra ...storage.Source) *fixture {
+	t.Helper()
+	if log == nil {
+		log = slog.New(slog.DiscardHandler)
+	}
 	f := &fixture{t: t, dir: t.TempDir()}
 	media := filepath.Join(f.dir, "media")
@@ -79,5 +88,5 @@ func newFixtureSized(t *testing.T, size int, extra ...storage.Source) *fixture {
 		t.Fatal(err)
 	}
-	s, err := compose(context.Background(), f.cfgPath, "", "", testWeb, extra, slog.New(slog.NewTextHandler(io.Discard, nil)))
+	s, err := compose(context.Background(), f.cfgPath, "", "", testWeb, extra, log)
 	if err != nil {
 		t.Fatal(err)
```

`server/cmd/lisimflix/log_test.go`:
```go
package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) events() []map[string]any {
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// #37: one sign-in → play → logout run leaves a server-side story with
// correlation IDs, and none of its secrets.
func TestLogsTellTheStoryWithoutSecrets(t *testing.T) {
	var buf syncBuffer
	f := newFixtureWith(t, 2<<20, slog.New(slog.NewJSONHandler(&buf, nil)))
	b := f.browser()
	_, out := b.do("GET", "/api/v1/auth/session", "", nil)
	b.token = jsonField(out, "csrfToken")
	failed, _ := b.do("POST", "/api/v1/auth/login", `{"username":"viewer","password":"wrong password!"}`, nil)
	b.signIn()
	ps := b.start("k1")
	if resp, _ := b.do("GET", ps.ContentURL, "", map[string]string{"Range": "bytes=0-99"}); resp.StatusCode != 206 {
		t.Fatalf("content = %d", resp.StatusCode)
	}
	b.do("GET", ps.ContentURL[:len(ps.ContentURL)-2]+"AA", "", nil) // tampered grant: 404
	cookie := ""
	for _, c := range b.c.Jar.Cookies(mustParse(f.ts.URL).URL) {
		if c.Name == "lisimflix_session" {
			cookie = c.Value
		}
	}
	b.do("POST", "/api/v1/auth/logout", "", nil)
	afterLogout, _ := b.do("GET", ps.ContentURL, "", nil)

	ev := buf.events()
	has := func(msg string, kv ...any) map[string]any {
		for _, e := range ev {
			if e["msg"] != msg {
				continue
			}
			ok := true
			for i := 0; i+1 < len(kv); i += 2 {
				if e[kv[i].(string)] != kv[i+1] {
					ok = false
				}
			}
			if ok {
				return e
			}
		}
		t.Errorf("no %s %v in:\n%s", msg, kv, buf.String())
		return nil
	}
	if e := has("auth.login", "result", "fail", "reason", "bad_credentials"); e != nil && e["request_id"] != failed.Header.Get("X-Request-Id") {
		t.Errorf("failed login request_id %v, response header %q", e["request_id"], failed.Header.Get("X-Request-Id"))
	}
	has("auth.login", "result", "ok", "username", "viewer")
	has("playback.start", "session_id", ps.ID)
	has("http.request", "status", float64(404), "path", "/api/v1/playback/sessions/"+ps.ID+"/content")
	has("auth.session_end", "reason", "logout")
	has("playback.end", "session_id", ps.ID, "reason", "revoked")
	if e := has("http.request", "status", float64(401), "path", "/api/v1/playback/sessions/"+ps.ID+"/content"); e != nil && e["request_id"] != afterLogout.Header.Get("X-Request-Id") {
		t.Errorf("401 request_id %v, response header %q", e["request_id"], afterLogout.Header.Get("X-Request-Id"))
	}

	grant := ps.ContentURL[strings.Index(ps.ContentURL, "grant=")+len("grant="):]
	for name, secret := range map[string]string{"password": password, "session cookie": cookie, "CSRF token": b.token, "grant": grant, "query": "grant="} {
		if secret != "" && strings.Contains(buf.String(), secret) {
			t.Errorf("the %s appears in the log", name)
		}
	}
}

func jsonField(b []byte, k string) string {
	var m map[string]any
	json.Unmarshal(b, &m)
	s, _ := m[k].(string)
	return s
}

func mustParse(s string) *http.Request {
	r, err := http.NewRequest("GET", s, nil)
	if err != nil {
		panic(err)
	}
	return r
}
```

- [ ] **Step 2: Run it and confirm it fails**

First make the package compile with logging not yet wired: in `serve.go`, change the `app.New(…, ready)` call to `app.New(…, ready, nil)`.
Run: `go -C server test -run LogsTellTheStory ./cmd/lisimflix`
Expected: FAIL, with every expected event reported missing (`no auth.login …`, `no playback.start …`, `no http.request …`).

- [ ] **Step 3: Implement**

`server/cmd/lisimflix/serve.go`:
```diff
--- a/server/cmd/lisimflix/serve.go
+++ b/server/cmd/lisimflix/serve.go
@@ -62,5 +62,5 @@ func compose(ctx context.Context, cfgPath, dbPath, backups string, web fs.FS, ex
 	pb, err := playback.New(root, playback.Options{
 		Source: playback.Source{Movie: cfg.Movie(), RelativePath: cfg.RelativePath}, GrantKey: grantKey,
-		MaxSessions: cfg.MaxPlaybackSessions,
+		MaxSessions: cfg.MaxPlaybackSessions, Log: log,
 		Authorize: func(p contracts.Principal, movieID string) bool {
 			return p.AccountID == v.AccountID && p.ProfileID == v.ProfileID && movieID == cfg.MovieID
@@ -77,4 +77,5 @@ func compose(ctx context.Context, cfgPath, dbPath, backups string, web fs.FS, ex
 		return nil, err
 	}
+	am.SetLogger(log)
 
 	s := &server{cfg: cfg, pb: pb, root: root, nightlyDone: make(chan struct{})}
@@ -107,5 +108,5 @@ func compose(ctx context.Context, cfgPath, dbPath, backups string, web fs.FS, ex
 		return pb.Check()
 	}
-	s.handler, err = app.New(cfg, am, pb.Handler(), web, ready)
+	s.handler, err = app.New(cfg, am, pb.Handler(), web, ready, log)
 	if err != nil {
 		s.shutdown(ctx, nil)
```

`deploy/README.md`: add this paragraph at the end of section 4 ("Run"):
```markdown
- **Logs:** the server writes JSON lines to stderr (`docker compose -f deploy/compose.m1.yml logs`):
  sign-in results, session ends, playback starts and ends, source errors, and requests that
  ended in an error, each with a `request_id` that matches the response's `X-Request-Id`.
  Passwords, cookies, CSRF tokens, content grants and query strings are never logged.
```

- [ ] **Step 4: Run every check**

Run: `go -C server vet ./... && go -C server test -race ./... && go -C server test -count=3 -run 'Shutdown|LogsTell' ./cmd/lisimflix`, then `make check`.
Expected: all `ok`, and `make check` exits 0. Then build the image (`make image`), sign in, play and sign out once, and confirm `docker logs` shows the catalogue events with no secrets. Record that in the PR.

- [ ] **Step 5: Commit**

```bash
git add server/cmd/lisimflix deploy/README.md
git commit -m "feat(platform): wire event logging into the server (OPS-5)"
```

Open the PR `feat(platform): baseline event and error logging (OPS-5)`, `Closes #37`, reviewer @lisim.

---

## Done when

- [ ] All four tasks are committed, and `make check` and Actions pass.
- [ ] One run of the image shows the events in `docker logs`, with no password, cookie, CSRF token, grant or query string. Recorded in the PR.
- [ ] #37 records "module accepted" with the merge.

## Self-review

- **Spec coverage:**
  - §6 security events with actor/action/result and correlation ID: `auth.login` and `auth.session_end`, each with `request_id`.
  - Redaction: Global Constraints, the redaction assertions in three tests, and the query-string mutation below.
  - §11 request/session correlation IDs: `request_id`, `session_id` and `auth_session`.
  - OPS-5 baseline errors: `http.request` for 4xx/5xx, and `playback.source_error`.

  #37's proposed baseline is covered event by event. Job correlation IDs arrive with the first background job feature (LIB-2's scans), not here.
- **Mutation checks** (each change made to the prototype, then reverted; every one fails at least one test):
  1. `path` logged with the query string → `TestErrorRequestsAreLoggedWithoutQuery`, `TestLogsTellTheStoryWithoutSecrets`
  2. `recorder` without `Unwrap` → `TestWriteDeadlinesReachThroughTheLogger`. That's the only test guarding it: the command's shutdown test still passes without it.
  3. Request ID not put in the context → `TestEveryResponseHasARequestID`, `TestLogsTellTheStoryWithoutSecrets`
  4. Username added to failed sign-ins → `TestAuthEventsAreLogged`
  5. Rotation logged as `logout` → `TestAuthEventsAreLogged`
  6. Revocation logged as `closed` → `TestPlaybackEventsAreLogged`
  7. `playback.source_error` not logged → `TestPlaybackEventsAreLogged`
  8. `am.SetLogger` not called in `compose` → `TestLogsTellTheStoryWithoutSecrets`
- **Placeholders:** none. Every code step has the full file or an exact diff against `main`.
- **Type consistency:**
  - `obs.Log(ctx, base)` is used the same way in `app`, `auth` and `playback`.
  - `revoke(ctx, reason, ids…)` and `endLocked(sess, now, reason)` are the only callers of the reason strings in the event catalogue.
  - `app.New`'s new parameter is passed in `serve.go` and as `nil` in `app_test.go`.

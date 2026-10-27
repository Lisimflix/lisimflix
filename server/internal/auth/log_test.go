package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/obs"
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

// Sessions that merely expire while someone logs in are logged without that
// login's request ID; an eviction, which the login caused, keeps it (#86).
func TestSweptSessionsDontBorrowTheLoginsRequestID(t *testing.T) {
	h := newHarness(t, false)
	var buf syncBuffer
	h.m.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	ctx := context.Background()
	_, old, err := h.m.create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(idleTimeout + time.Second)
	if _, _, err := h.m.create(obs.WithRequestID(ctx, "req-login")); err != nil {
		t.Fatal(err)
	}
	if l := find(buf.lines(t), "auth.session_end", "reason", "expired", "session_id", old.id); l == nil || l["request_id"] != nil {
		t.Fatalf("swept session logged as %v; it must have no request_id", l)
	}

	for h.m.sessionCount() < maxSessions {
		if _, _, err := h.m.create(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := h.m.create(obs.WithRequestID(ctx, "req-evict")); err != nil {
		t.Fatal(err)
	}
	if l := find(buf.lines(t), "auth.session_end", "reason", "evicted"); l == nil || l["request_id"] != "req-evict" {
		t.Fatalf("eviction logged as %v; it should carry the login's request_id", l)
	}
}

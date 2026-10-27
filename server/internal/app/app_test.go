package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/Lisimflix/lisimflix/server/internal/auth"
	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/m1config"
)

const (
	password = "correct horse battery"
	// The app is configured for a LAN address; tests reach it on loopback
	// and send this Host/Origin, as a browser on the LAN would.
	lanOrigin = "http://192.168.1.20:8420"
	lanHost   = "192.168.1.20:8420"
)

var viewerHash string

func TestMain(m *testing.M) {
	h, err := auth.HashPassword([]byte(password))
	if err != nil {
		panic(err)
	}
	viewerHash = h
	os.Exit(m.Run())
}

// fakePlayback stands in for PLY-2 in these module tests only. It proves
// routing and authentication, not playback.
type fakePlayback struct {
	mu   sync.Mutex
	seen []contracts.Principal
}

func (f *fakePlayback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := contracts.PrincipalFrom(r.Context())
	if !ok {
		http.Error(w, "no principal", 500)
		return
	}
	f.mu.Lock()
	f.seen = append(f.seen, p)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"items":[],"nextCursor":null}`)
}

var web = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>Lisimflix</title>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
}

type env struct {
	t     *testing.T
	srv   *httptest.Server
	play  *fakePlayback
	ready error
	mu    sync.Mutex
}

func newEnv(t *testing.T, origin string) *env {
	t.Helper()
	cfg := m1config.Config{
		PublicOrigin: origin, CSRFKey: bytes.Repeat([]byte{3}, 32), MediaRoot: "/media/SECRET-ROOT",
		Viewer: m1config.Viewer{AccountID: "acct-1", ProfileID: "prof-1", Username: "viewer", PasswordHash: viewerHash},
	}
	am, err := auth.NewManager(cfg.Viewer, strings.HasPrefix(origin, "https:"), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, play: &fakePlayback{}}
	h, err := New(cfg, am, e.play, web, func(context.Context) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.ready
	})
	if err != nil {
		t.Fatal(err)
	}
	e.srv = httptest.NewServer(h)
	t.Cleanup(e.srv.Close)
	return e
}

type browser struct {
	e      *env
	c      *http.Client
	origin string
	token  string
}

func (e *env) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{e: e, c: &http.Client{Jar: jar}, origin: lanOrigin}
}

func (b *browser) do(method, path, body string, hdr map[string]string) (*http.Response, []byte) {
	b.e.t.Helper()
	req, _ := http.NewRequest(method, b.e.srv.URL+path, strings.NewReader(body))
	req.Host = lanHost
	if b.origin != "" {
		req.Header.Set("Origin", b.origin)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.token != "" {
		req.Header.Set("X-CSRF-Token", b.token)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.e.t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, out
}

func (b *browser) signIn() {
	b.e.t.Helper()
	_, out := b.do("GET", "/api/v1/auth/session", "", nil)
	var s contracts.AuthState
	json.Unmarshal(out, &s)
	b.token = s.CSRFToken
	resp, out := b.do("POST", "/api/v1/auth/login", `{"username":"viewer","password":"`+password+`"}`, nil)
	if resp.StatusCode != 200 {
		b.e.t.Fatalf("login = %d %s", resp.StatusCode, out)
	}
	json.Unmarshal(out, &s)
	b.token = s.CSRFToken
}

func code(t *testing.T, out []byte) string {
	t.Helper()
	var e contracts.APIError
	if err := json.Unmarshal(out, &e); err != nil || e.RequestID == "" {
		t.Fatalf("not an APIError: %s", out)
	}
	return e.Code
}

func TestProtectedAPIRoutesRequireLogin(t *testing.T) {
	e := newEnv(t, lanOrigin)
	b := e.browser()
	b.do("GET", "/api/v1/auth/session", "", nil)
	for _, rt := range []struct{ method, path string }{
		{"GET", "/api/v1/movies"},
		{"POST", "/api/v1/playback/sessions"},
		{"GET", "/api/v1/playback/sessions/x/content"},
	} {
		hdr := map[string]string{"X-CSRF-Token": "", "Origin": ""}
		if rt.method == "GET" {
			hdr = nil
		}
		resp, out := b.do(rt.method, rt.path, "", hdr)
		if rt.method == "GET" && (resp.StatusCode != 401 || code(t, out) != contracts.CodeUnauthenticated) {
			t.Errorf("%s %s anonymous = %d %s", rt.method, rt.path, resp.StatusCode, out)
		}
		if rt.method != "GET" && resp.StatusCode < 400 {
			t.Errorf("%s %s anonymous = %d", rt.method, rt.path, resp.StatusCode)
		}
	}
	if len(e.play.seen) != 0 {
		t.Fatal("playback handler reached without a session")
	}
	b.signIn()
	if resp, out := b.do("GET", "/api/v1/movies", "", nil); resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("movies = %d %s, Cache-Control %q", resp.StatusCode, out, resp.Header.Get("Cache-Control"))
	}
	if p := e.play.seen; len(p) != 1 || p[0].AccountID != "acct-1" || p[0].AuthSessionID == "" {
		t.Fatalf("playback saw %+v", p)
	}
}

func TestLocalHTTPLoginRoundTrip(t *testing.T) {
	e := newEnv(t, lanOrigin)
	b := e.browser()
	b.signIn()
	if resp, _ := b.do("GET", "/api/v1/movies", "", nil); resp.StatusCode != 200 {
		t.Fatalf("movies after login = %d", resp.StatusCode)
	}
	if resp, out := b.do("POST", "/api/v1/auth/logout", "", nil); resp.StatusCode != 204 {
		t.Fatalf("logout = %d %s", resp.StatusCode, out)
	}
	if resp, _ := b.do("GET", "/api/v1/movies", "", nil); resp.StatusCode != 401 {
		t.Fatalf("movies after logout = %d", resp.StatusCode)
	}
}

func TestCrossOriginWriteRejected(t *testing.T) {
	e := newEnv(t, lanOrigin)
	b := e.browser()
	b.signIn()
	for name, hdr := range map[string]map[string]string{
		"foreign origin":      {"Origin": "http://evil.example"},
		"no origin":           {"Origin": ""},
		"https downgrade":     {"Origin": "https://192.168.1.20:8420"},
		"spoofed host":        {"Host": "evil.example", "Origin": "http://evil.example"},
		"referer only, wrong": {"Origin": "", "Referer": "http://evil.example/page"},
	} {
		resp, out := b.do("POST", "/api/v1/auth/logout", "", hdr)
		if resp.StatusCode != 403 || code(t, out) != contracts.CodeCSRFFailed {
			t.Errorf("%s: %d %s", name, resp.StatusCode, out)
		}
	}
	if resp, _ := b.do("POST", "/api/v1/auth/logout", "", map[string]string{"Origin": "", "Referer": lanOrigin + "/watch"}); resp.StatusCode != 204 {
		t.Fatalf("same-origin Referer fallback = %d", resp.StatusCode)
	}
}

func TestProxyHeadersDoNotChangeScheme(t *testing.T) {
	e := newEnv(t, lanOrigin)
	b := e.browser()
	b.do("GET", "/api/v1/auth/session", "", nil)
	_, out := b.do("GET", "/api/v1/auth/session", "", map[string]string{"X-Forwarded-Proto": "https"})
	var s contracts.AuthState
	json.Unmarshal(out, &s)
	b.token = s.CSRFToken
	resp, out := b.do("POST", "/api/v1/auth/login", `{"username":"viewer","password":"`+password+`"}`,
		map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "evil.example", "Forwarded": "proto=https"})
	if resp.StatusCode != 200 {
		t.Fatalf("login with forwarded headers = %d %s", resp.StatusCode, out)
	}
	for _, c := range resp.Cookies() {
		if c.Secure {
			t.Fatalf("cookie %s became Secure from a forwarded header", c.Name)
		}
	}
}

func TestHealthDoesNotLeakConfig(t *testing.T) {
	e := newEnv(t, lanOrigin)
	b := e.browser()
	for _, p := range []string{"/health/live", "/health/ready"} {
		resp, out := b.do("GET", p, "", map[string]string{"Origin": ""})
		if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s = %d", p, resp.StatusCode)
		}
		for _, s := range []string{"SECRET-ROOT", "viewer", "acct-1", "192.168"} {
			if bytes.Contains(out, []byte(s)) {
				t.Fatalf("%s leaks %q: %s", p, s, out)
			}
		}
	}
}

func TestReadinessReflectsInitFailure(t *testing.T) {
	e := newEnv(t, lanOrigin)
	b := e.browser()
	e.mu.Lock()
	e.ready = errors.New("open /media/SECRET-ROOT/movie.mp4: permission denied")
	e.mu.Unlock()
	resp, out := b.do("GET", "/health/ready", "", nil)
	if resp.StatusCode != 503 || bytes.Contains(out, []byte("SECRET")) {
		t.Fatalf("not-ready = %d %s", resp.StatusCode, out)
	}
	if resp, _ := b.do("GET", "/health/live", "", nil); resp.StatusCode != 200 {
		t.Fatalf("liveness must not depend on readiness: %d", resp.StatusCode)
	}
	e.mu.Lock()
	e.ready = nil
	e.mu.Unlock()
	if resp, _ := b.do("GET", "/health/ready", "", nil); resp.StatusCode != 200 {
		t.Fatalf("ready again = %d", resp.StatusCode)
	}
}

func TestStaticFallbackDoesNotSwallowAPI404(t *testing.T) {
	e := newEnv(t, lanOrigin)
	b := e.browser()
	for _, p := range []string{"/api/v1/nope", "/api", "/api/v2/movies"} {
		resp, out := b.do("GET", p, "", nil)
		if resp.StatusCode != 404 || code(t, out) != contracts.CodeNotFound {
			t.Errorf("GET %s = %d %s", p, resp.StatusCode, out)
		}
	}
	if resp, out := b.do("GET", "/health/nope", "", nil); resp.StatusCode != 404 || bytes.Contains(out, []byte("<!doctype")) {
		t.Errorf("GET /health/nope = %d %s", resp.StatusCode, out)
	}
	resp, out := b.do("GET", "/watch/mov-1", "", nil)
	if resp.StatusCode != 200 || !bytes.Contains(out, []byte("<title>Lisimflix")) {
		t.Fatalf("SPA route = %d %s", resp.StatusCode, out)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q", csp)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
	if resp, out := b.do("GET", "/assets/app.js", "", nil); resp.StatusCode != 200 || string(out) != "console.log(1)" {
		t.Fatalf("asset = %d %s", resp.StatusCode, out)
	}
	if resp, _ := b.do("POST", "/watch/mov-1", "", nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST to a page = %d", resp.StatusCode)
	}
}

func TestNewRejectsInconsistentInputs(t *testing.T) {
	cfg := m1config.Config{PublicOrigin: lanOrigin, CSRFKey: bytes.Repeat([]byte{3}, 32),
		Viewer: m1config.Viewer{AccountID: "a", ProfileID: "p", Username: "viewer", PasswordHash: viewerHash}}
	secureAM, _ := auth.NewManager(cfg.Viewer, true, func(string) {})
	plainAM, _ := auth.NewManager(cfg.Viewer, false, func(string) {})
	ready := func(context.Context) error { return nil }
	play := &fakePlayback{}
	if _, err := New(cfg, secureAM, play, web, ready); err == nil {
		t.Error("accepted Secure cookies on an http origin")
	}
	for name, fn := range map[string]func() error{
		"nil auth":     func() error { _, err := New(cfg, nil, play, web, ready); return err },
		"nil playback": func() error { _, err := New(cfg, plainAM, nil, web, ready); return err },
		"nil web":      func() error { _, err := New(cfg, plainAM, play, nil, ready); return err },
		"nil ready":    func() error { _, err := New(cfg, plainAM, play, web, nil); return err },
		"no index": func() error {
			_, err := New(cfg, plainAM, play, fstest.MapFS{"a.js": {Data: []byte("x")}}, ready)
			return err
		},
	} {
		if fn() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

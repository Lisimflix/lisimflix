package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
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

// The container healthcheck polls /health/ready every 30 s. A readiness
// failure is logged once, with its reason, when the state changes, and the
// polls themselves are not logged (#75).
func TestReadinessChangesAreLoggedOnce(t *testing.T) {
	var buf bytes.Buffer
	e := newEnvWith(t, lanOrigin, slog.New(slog.NewJSONHandler(&buf, nil)))
	b := e.browser()
	setReady := func(err error) {
		e.mu.Lock()
		e.ready = err
		e.mu.Unlock()
	}
	poll := func(n, want int) {
		for range n {
			if resp, _ := b.do("GET", "/health/ready", "", nil); resp.StatusCode != want {
				t.Fatalf("/health/ready = %d, want %d", resp.StatusCode, want)
			}
		}
	}
	poll(2, 200)
	setReady(errors.New("movie missing"))
	poll(3, 503)
	setReady(nil)
	poll(2, 200)
	b.do("GET", "/health/live", "", nil)
	b.do("GET", "/health/nope", "", nil)

	var got []string
	for _, l := range logLines(t, &buf) {
		switch l["msg"] {
		case "http.request":
			if p, _ := l["path"].(string); strings.HasPrefix(p, "/health/") {
				t.Errorf("health poll logged as a request: %v", l)
			}
		case "health.ready":
			got = append(got, fmt.Sprintf("%v %v %v", l["level"], l["ready"], l["reason"]))
		}
	}
	want := []string{"INFO true <nil>", "ERROR false movie missing", "INFO true <nil>"}
	if !slices.Equal(got, want) {
		t.Fatalf("health.ready lines = %q, want %q", got, want)
	}
}

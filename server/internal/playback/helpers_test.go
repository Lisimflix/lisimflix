package playback

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

const movieID = "mov-1"

// Test-only principals: production principals come from auth.Require.
var (
	alice  = contracts.Principal{AccountID: "acct-a", ProfileID: "prof-a", AuthSessionID: "as-a1"}
	alice2 = contracts.Principal{AccountID: "acct-a", ProfileID: "prof-a", AuthSessionID: "as-a2"}
	bob    = contracts.Principal{AccountID: "acct-b", ProfileID: "prof-b", AuthSessionID: "as-b1"}
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	t      *testing.T
	dir    string
	path   string
	data   []byte
	root   *os.Root
	s      *Server
	srv    *httptest.Server
	clock  *clock
	policy func(contracts.Principal, string) bool
	single *M1
}

type envOpt func(*Options)

// newEnv writes a random "movie" of size bytes under a temporary root and
// serves the playback handler with the principal taken from a test header.
func newEnv(t *testing.T, size int, opts ...envOpt) *env {
	t.Helper()
	e := &env{t: t, dir: t.TempDir(), clock: &clock{t: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}}
	e.path = filepath.Join(e.dir, "Movies", "movie.mp4")
	if err := os.MkdirAll(filepath.Dir(e.path), 0o755); err != nil {
		t.Fatal(err)
	}
	e.data = make([]byte, size)
	rand.Read(e.data)
	if err := os.WriteFile(e.path, e.data, 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	e.root = root
	info, err := os.Stat(e.path)
	if err != nil {
		t.Fatal(err)
	}
	e.policy = func(p contracts.Principal, id string) bool { return p.AccountID == "acct-a" && id == movieID }
	single := Single(contracts.Movie{ID: movieID, SourceRevision: Revision(movieID, info), Title: "T", DurationMs: 1000,
		Container: "mp4", VideoCodec: "h264", Width: 1920, Height: 800, AudioCodec: "mp3"}, "Movies/movie.mp4",
		func(p contracts.Principal, id string) bool { return e.policy(p, id) })
	e.single = single
	o := Options{Resolver: single, Movies: single.Movies, GrantKey: bytes.Repeat([]byte{5}, 32), MaxSessions: 4}
	for _, fn := range opts {
		fn(&o)
	}
	s, err := New(root, o)
	if err != nil {
		t.Fatal(err)
	}
	s.now = e.clock.Now
	e.s = s
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Test-Principal"); v != "" {
			f := strings.Split(v, "|")
			r = r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{AccountID: f[0], ProfileID: f[1], AuthSessionID: f[2]}))
		}
		s.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		e.srv.Close()
		s.Close()
		root.Close()
	})
	return e
}

func header(p contracts.Principal) string {
	return p.AccountID + "|" + p.ProfileID + "|" + p.AuthSessionID
}

func (e *env) req(p *contracts.Principal, method, path, body string, hdr map[string]string) (*http.Response, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if p != nil {
		req.Header.Set("X-Test-Principal", header(*p))
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, b
}

func (e *env) start(p contracts.Principal, key string) (int, contracts.PlaybackSession, []byte) {
	e.t.Helper()
	resp, b := e.req(&p, "POST", "/api/v1/playback/sessions", `{"itemId":"`+movieID+`","versionId":"`+movieID+`"}`, map[string]string{"Idempotency-Key": key})
	var ps contracts.PlaybackSession
	json.Unmarshal(b, &ps)
	return resp.StatusCode, ps, b
}

func (e *env) mustStart(p contracts.Principal, key string) contracts.PlaybackSession {
	e.t.Helper()
	code, ps, b := e.start(p, key)
	if code != 201 {
		e.t.Fatalf("start = %d %s", code, b)
	}
	return ps
}

func errCode(t *testing.T, b []byte) string {
	t.Helper()
	var e contracts.APIError
	if err := json.Unmarshal(b, &e); err != nil || e.RequestID == "" {
		t.Fatalf("not an APIError: %q", b)
	}
	return e.Code
}

func (e *env) check() error { return e.single.Check(e.root) }

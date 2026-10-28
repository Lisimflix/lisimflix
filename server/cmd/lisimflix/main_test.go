package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

const password = "correct horse battery"

var testWeb = fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Lisimflix</title>")}}

// testProbe is an ffprobe record shaped like the M1 source.
const testProbe = `{"streams":[
 {"index":0,"codec_name":"h264","codec_type":"video","profile":"Main","width":1920,"height":800,"pix_fmt":"yuv420p"},
 {"index":1,"codec_name":"mp3","codec_type":"audio","sample_rate":"44100","channels":2}],
 "format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"12.5"}}`

type fixture struct {
	t       *testing.T
	dir     string
	cfgPath string
	movie   []byte
	ts      *httptest.Server
	s       *server
}

// newFixture writes a media root with a random "movie", provisions a config
// through cmdInit and composes the real modules behind an httptest server.
func newFixture(t *testing.T, extra ...storage.Source) *fixture {
	t.Helper()
	return newFixtureSized(t, 2<<20, extra...)
}

// newFixtureSized uses a movie of size bytes; streaming tests need one far
// larger than the socket buffers, or the whole file is sent before they act.
func newFixtureSized(t *testing.T, size int, extra ...storage.Source) *fixture {
	t.Helper()
	return newFixtureWith(t, size, nil, extra...)
}

// newFixtureWith also takes the server's logger (nil discards).
func newFixtureWith(t *testing.T, size int, log *slog.Logger, extra ...storage.Source) *fixture {
	t.Helper()
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	f := &fixture{t: t, dir: t.TempDir()}
	media := filepath.Join(f.dir, "media")
	cfgDir := filepath.Join(f.dir, "config")
	for _, d := range []string{filepath.Join(media, "Movies"), cfgDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.movie = make([]byte, size)
	rand.Read(f.movie)
	if err := os.WriteFile(filepath.Join(media, "Movies", "movie.mp4"), f.movie, 0o644); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(f.dir, "probe.json")
	os.WriteFile(probe, []byte(testProbe), 0o600)
	f.cfgPath = filepath.Join(cfgDir, "m1.json")

	f.ts = httptest.NewUnstartedServer(nil)
	origin := "http://" + f.ts.Listener.Addr().String()
	var out bytes.Buffer
	if err := cmdInit([]string{"--config", f.cfgPath, "--origin", origin, "--media-root", media, "--movie", "Movies/movie.mp4",
		"--probe-json", probe, "--username", "viewer", "--title", "Test Movie"}, strings.NewReader(password+"\n"+password+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	s, err := compose(context.Background(), f.cfgPath, "", "", testWeb, extra, log)
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	f.ts.Config.Handler = s.handler
	f.ts.Start()
	t.Cleanup(func() {
		f.ts.Close()
		s.shutdown(context.Background(), nil)
	})
	return f
}

type browser struct {
	f     *fixture
	c     *http.Client
	token string
}

func (f *fixture) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{f: f, c: &http.Client{Jar: jar}}
}

func (b *browser) do(method, path, body string, hdr map[string]string) (*http.Response, []byte) {
	b.f.t.Helper()
	req, _ := http.NewRequest(method, b.f.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Origin", b.f.ts.URL)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.token != "" {
		req.Header.Set("X-CSRF-Token", b.token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.f.t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, out
}

func (b *browser) signIn() {
	b.f.t.Helper()
	_, out := b.do("GET", "/api/v1/auth/session", "", nil)
	var st contracts.AuthState
	json.Unmarshal(out, &st)
	b.token = st.CSRFToken
	resp, out := b.do("POST", "/api/v1/auth/login", `{"username":"viewer","password":"`+password+`"}`, nil)
	if resp.StatusCode != 200 {
		b.f.t.Fatalf("login = %d %s", resp.StatusCode, out)
	}
	json.Unmarshal(out, &st)
	b.token = st.CSRFToken
}

func (b *browser) start(key string) contracts.PlaybackSession {
	b.f.t.Helper()
	_, out := b.do("GET", "/api/v1/movies", "", nil)
	var list contracts.MovieList
	json.Unmarshal(out, &list)
	if len(list.Items) != 1 {
		b.f.t.Fatalf("movies = %s", out)
	}
	resp, out := b.do("POST", "/api/v1/playback/sessions", `{"itemId":"`+list.Items[0].ID+`","versionId":"`+list.Items[0].ID+`"}`, map[string]string{"Idempotency-Key": key})
	if resp.StatusCode != 201 {
		b.f.t.Fatalf("start = %d %s", resp.StatusCode, out)
	}
	var ps contracts.PlaybackSession
	json.Unmarshal(out, &ps)
	return ps
}

func TestInitRefusesOverwriteAndPlaintextOutput(t *testing.T) {
	f := newFixture(t)
	raw, _ := os.ReadFile(f.cfgPath)
	if bytes.Contains(raw, []byte(password)) {
		t.Fatal("config contains the plaintext password")
	}
	before := raw
	var out bytes.Buffer
	err := cmdInit([]string{"--config", f.cfgPath, "--origin", "http://127.0.0.1:1", "--media-root", filepath.Join(f.dir, "media"),
		"--movie", "Movies/movie.mp4", "--probe-json", filepath.Join(f.dir, "probe.json"), "--username", "other"},
		strings.NewReader(password+"\n"+password+"\n"), &out)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second init = %v, want fs.ErrExist", err)
	}
	if after, _ := os.ReadFile(f.cfgPath); !bytes.Equal(before, after) {
		t.Fatal("refused init changed the config")
	}
	if strings.Contains(out.String(), password) {
		t.Fatal("init printed the password")
	}
	// Mismatched or weak passwords are refused before anything is written.
	for name, in := range map[string]string{"mismatch": password + "\nsomething else!!\n", "short": "short\nshort\n"} {
		p := filepath.Join(t.TempDir(), "m1.json")
		err := cmdInit([]string{"--config", p, "--origin", "http://127.0.0.1:1", "--media-root", filepath.Join(f.dir, "media"),
			"--movie", "Movies/movie.mp4", "--probe-json", filepath.Join(f.dir, "probe.json"), "--username", "viewer"}, strings.NewReader(in), io.Discard)
		if _, statErr := os.Stat(p); err == nil || statErr == nil {
			t.Errorf("%s: init = %v and wrote a config", name, err)
		}
	}
}

// Pete's note on #31: a probe record for another file is refused.
func TestInitRejectsAProbeForAnotherFile(t *testing.T) {
	f := newFixture(t)
	probe := filepath.Join(t.TempDir(), "other.json")
	os.WriteFile(probe, []byte(strings.Replace(testProbe, `"duration":"12.5"`, `"duration":"12.5","size":"999"`, 1)), 0o600)
	cfg := filepath.Join(t.TempDir(), "m1.json")
	err := cmdInit([]string{"--config", cfg, "--origin", "http://127.0.0.1:1", "--media-root", filepath.Join(f.dir, "media"),
		"--movie", "Movies/movie.mp4", "--probe-json", probe, "--username", "viewer"}, strings.NewReader(password+"\n"+password+"\n"), io.Discard)
	if _, statErr := os.Stat(cfg); err == nil || statErr == nil {
		t.Fatalf("init with a probe of another size = %v; it must refuse and write nothing", err)
	}
}

// healthcheck reads the listen address from the config (#31): an empty host
// means loopback.
func TestHealthcheckUsesTheConfiguredListenAddress(t *testing.T) {
	f := newFixture(t)
	_, port, _ := net.SplitHostPort(f.ts.Listener.Addr().String())
	for name, listen := range map[string]string{"host and port": "127.0.0.1:" + port, "port only": ":" + port} {
		t.Run(name, func(t *testing.T) {
			cfg := filepath.Join(t.TempDir(), "m1.json")
			if err := cmdInit([]string{"--config", cfg, "--origin", f.ts.URL, "--media-root", filepath.Join(f.dir, "media"),
				"--movie", "Movies/movie.mp4", "--probe-json", filepath.Join(f.dir, "probe.json"), "--username", "viewer", "--listen", listen},
				strings.NewReader(password+"\n"+password+"\n"), io.Discard); err != nil {
				t.Fatal(err)
			}
			if err := cmdHealthcheck([]string{"--config", cfg}); err != nil {
				t.Fatalf("healthcheck = %v", err)
			}
		})
	}
	f.ts.Close()
	cfg := filepath.Join(t.TempDir(), "m1.json")
	cmdInit([]string{"--config", cfg, "--origin", f.ts.URL, "--media-root", filepath.Join(f.dir, "media"),
		"--movie", "Movies/movie.mp4", "--probe-json", filepath.Join(f.dir, "probe.json"), "--username", "viewer", "--listen", "127.0.0.1:" + port},
		strings.NewReader(password+"\n"+password+"\n"), io.Discard)
	if err := cmdHealthcheck([]string{"--config", cfg}); err == nil {
		t.Fatal("healthcheck passed with the server stopped")
	}
}

func TestCompositionRejectsAnonymousContent(t *testing.T) {
	f := newFixture(t)
	owner := f.browser()
	owner.signIn()
	ps := owner.start("k1")
	anon := f.browser()
	for _, p := range []string{"/api/v1/movies", ps.ContentURL} {
		if resp, _ := anon.do("GET", p, "", nil); resp.StatusCode != 401 {
			t.Errorf("anonymous GET %s = %d", p, resp.StatusCode)
		}
	}
	if resp, _ := f.browser().do("GET", "/health/ready", "", nil); resp.StatusCode != 200 {
		t.Fatalf("ready = %d", resp.StatusCode)
	}
}

func TestLoginToContentAndLogoutRevocation(t *testing.T) {
	f := newFixture(t)
	b := f.browser()
	b.signIn()
	ps := b.start("k1")
	resp, out := b.do("GET", ps.ContentURL, "", map[string]string{"Range": "bytes=1000-1999"})
	if resp.StatusCode != 206 || !bytes.Equal(out, f.movie[1000:2000]) {
		t.Fatalf("range = %d, %d bytes", resp.StatusCode, len(out))
	}
	if resp, _ := b.do("POST", "/api/v1/playback/sessions/"+ps.ID+"/heartbeat", `{"sequence":1}`, nil); resp.StatusCode != 204 {
		t.Fatalf("heartbeat = %d", resp.StatusCode)
	}
	if resp, _ := b.do("POST", "/api/v1/auth/logout", "", nil); resp.StatusCode != 204 {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	if resp, _ := b.do("GET", ps.ContentURL, "", nil); resp.StatusCode != 401 {
		t.Fatalf("content after logout = %d", resp.StatusCode)
	}
	b.signIn()
	if resp, _ := b.do("GET", ps.ContentURL, "", nil); resp.StatusCode != 404 {
		t.Fatalf("old content URL from a new sign-in = %d", resp.StatusCode)
	}
}

func TestAPINotFoundIsNotSPA(t *testing.T) {
	f := newFixture(t)
	b := f.browser()
	resp, out := b.do("GET", "/api/v1/nope", "", nil)
	if resp.StatusCode != 404 || !bytes.Contains(out, []byte(`"code":"not_found"`)) {
		t.Fatalf("API 404 = %d %s", resp.StatusCode, out)
	}
	if resp, out := b.do("GET", "/watch", "", nil); resp.StatusCode != 200 || !bytes.Contains(out, []byte("<title>Lisimflix")) {
		t.Fatalf("SPA route = %d %s", resp.StatusCode, out)
	}
}

func TestShutdownClosesPlaybackAndRoot(t *testing.T) {
	f := newFixtureSized(t, 64<<20)
	b := f.browser()
	b.signIn()
	ps := b.start("k1")
	req, _ := http.NewRequest("GET", f.ts.URL+ps.ContentURL, nil)
	resp, err := b.c.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("stream = %v %v", resp, err)
	}
	io.ReadFull(resp.Body, make([]byte, 1024))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	if err := f.s.shutdown(ctx, f.ts.Config); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("shutdown with an open stream took %v", took)
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if n+1024 >= int64(len(f.movie)) {
		t.Fatal("the stream was not cut off by shutdown")
	}
	if _, err := f.s.root.Stat("Movies/movie.mp4"); err == nil {
		t.Fatal("media root still open after shutdown")
	}
	if err := f.s.db.PingContext(context.Background()); err == nil {
		t.Fatal("database still open after shutdown")
	}
}

func TestStoragePathsFollowTheConfigFolder(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Dir(f.cfgPath)
	for _, p := range []string{"lisimflix.db", "m1.json"} {
		st, err := os.Stat(filepath.Join(dir, p))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s = %v %v, want a 0600 file", p, st, err)
		}
	}
	applied, err := f.s.db.Applied(context.Background())
	if err != nil || len(applied) == 0 {
		t.Fatalf("migrations applied = %v %v", applied, err)
	}
}

// An image with a newer migration takes a pre-upgrade snapshot that holds
// the old schema and the config (OPS-2 handoff).
func TestUpgradeTakesPreUpgradeSnapshot(t *testing.T) {
	f := newFixture(t)
	f.s.shutdown(context.Background(), nil)
	newer := storage.Source{Module: "itest", FS: fstest.MapFS{
		"20261027120000_itest_marker.sql": {Data: []byte("CREATE TABLE itest_marker (id INTEGER PRIMARY KEY) STRICT;")},
	}}
	s, err := compose(context.Background(), f.cfgPath, "", "", testWeb, []storage.Source{newer}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || s.storageErr != nil {
		t.Fatalf("compose after upgrade = %v / %v", err, s.storageErr)
	}
	defer s.shutdown(context.Background(), nil)
	m, found, err := storage.Latest(filepath.Join(filepath.Dir(f.cfgPath), "backups"))
	if err != nil || !found || m.Reason != "pre-upgrade" {
		t.Fatalf("latest snapshot = %+v %v %v", m, found, err)
	}
	if len(m.Schema) != 1 {
		t.Fatalf("pre-upgrade snapshot schema = %v, want the old one", m.Schema)
	}
	var hasConfig bool
	for _, file := range m.Files {
		hasConfig = hasConfig || file.Name == "m1.json"
	}
	if !hasConfig {
		t.Fatal("pre-upgrade snapshot lacks the config file")
	}
}

// A database from a newer version keeps serving but reports not-ready.
func TestSchemaTooNewIsNotReady(t *testing.T) {
	f := newFixture(t)
	f.s.shutdown(context.Background(), nil)
	db, err := storage.Open(context.Background(), filepath.Join(filepath.Dir(f.cfgPath), "lisimflix.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.ExecContext(context.Background(), `INSERT INTO schema_migrations(version, checksum, applied_at) VALUES('29990101000000_future_x', 'x', 'now')`)
	db.Close()
	s, err := compose(context.Background(), f.cfgPath, "", "", testWeb, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.shutdown(context.Background(), nil)
	if !errors.Is(s.storageErr, storage.ErrSchemaTooNew) {
		t.Fatalf("storageErr = %v", s.storageErr)
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, httptest.NewRequest("GET", "/health/ready", nil))
	if rec.Code != 503 {
		t.Fatalf("ready = %d, want 503", rec.Code)
	}
}

// #99: with the movie deleted after init, serve still starts; readiness and
// playback report the movie as unavailable instead.
func TestServeStartsWithoutTheMovie(t *testing.T) {
	f := newFixture(t)
	f.s.shutdown(context.Background(), nil)
	if err := os.Remove(filepath.Join(f.dir, "media", "Movies", "movie.mp4")); err != nil {
		t.Fatal(err)
	}
	s, err := compose(context.Background(), f.cfgPath, "", "", testWeb, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("compose without the movie: %v", err)
	}
	defer s.shutdown(context.Background(), nil)
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, httptest.NewRequest("GET", "/health/ready", nil))
	if rec.Code != 503 {
		t.Fatalf("ready = %d, want 503 while the movie is missing", rec.Code)
	}
	// Other commands load the config too (healthcheck reads the listen
	// address from it): no server is listening here, but the config loads.
	var out bytes.Buffer
	run([]string{"healthcheck", "--config", f.cfgPath}, nil, &out, &out)
	if strings.Contains(out.String(), "m1config") {
		t.Fatalf("healthcheck failed on the config: %s", out.String())
	}
}

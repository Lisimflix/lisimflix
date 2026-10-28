package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

func TestSessionNeedsPrincipalAndPolicy(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k1")
	for _, rt := range []struct{ method, path, body string }{
		{"GET", "/api/v1/movies", ""},
		{"POST", "/api/v1/playback/sessions", `{"itemId":"mov-1","versionId":"mov-1"}`},
		{"POST", "/api/v1/playback/sessions/" + ps.ID + "/heartbeat", `{"sequence":1}`},
		{"DELETE", "/api/v1/playback/sessions/" + ps.ID, ""},
		{"GET", ps.ContentURL, ""},
	} {
		resp, b := e.req(nil, rt.method, rt.path, rt.body, map[string]string{"Idempotency-Key": "k9"})
		if resp.StatusCode != 401 || errCode(t, b) != contracts.CodeUnauthenticated {
			t.Errorf("%s %s without a principal = %d %s", rt.method, rt.path, resp.StatusCode, b)
		}
	}
	resp, b := e.req(&alice, "GET", "/api/v1/movies", "", nil)
	var list contracts.MovieList
	json.Unmarshal(b, &list)
	if resp.StatusCode != 200 || len(list.Items) != 1 || list.Items[0].ID != movieID || !bytes.Contains(b, []byte(`"nextCursor":null`)) {
		t.Fatalf("permitted list = %d %s", resp.StatusCode, b)
	}
	if bytes.Contains(b, []byte("movie.mp4")) || bytes.Contains(b, []byte(e.dir)) {
		t.Fatal("movie list leaks the source path")
	}
	_, b = e.req(&bob, "GET", "/api/v1/movies", "", nil)
	if !bytes.Contains(b, []byte(`"items":[]`)) {
		t.Fatalf("unpermitted list = %s", b)
	}
	if code, _, b := e.start(bob, "k1"); code != 404 || errCode(t, b) != contracts.CodeNotFound {
		t.Fatalf("unpermitted start = %d %s", code, b)
	}
	resp, b = e.req(&alice, "POST", "/api/v1/playback/sessions", `{"itemId":"mov-other","versionId":"mov-other"}`, map[string]string{"Idempotency-Key": "k3"})
	if resp.StatusCode != 404 {
		t.Fatalf("unknown movie = %d %s", resp.StatusCode, b)
	}
	// Policy is rechecked on every content request.
	e.policy = func(contracts.Principal, string) bool { return false }
	if resp, _ := e.req(&alice, "GET", ps.ContentURL, "", nil); resp.StatusCode != 404 {
		t.Fatalf("content after policy revoked = %d", resp.StatusCode)
	}
}

func TestGrantCannotCrossAccountOrSession(t *testing.T) {
	e := newEnv(t, 4096)
	e.policy = func(contracts.Principal, string) bool { return true }
	a := e.mustStart(alice, "k1")
	b := e.mustStart(bob, "k1")
	for name, tc := range map[string]struct {
		p   contracts.Principal
		url string
	}{
		"other account":            {bob, a.ContentURL},
		"same account, other auth": {alice2, a.ContentURL},
		"tampered grant":           {alice, a.ContentURL[:len(a.ContentURL)-2] + "AA"},
		"no grant":                 {alice, strings.Split(a.ContentURL, "?")[0]},
		"grant moved to session b": {bob, strings.Split(b.ContentURL, "?")[0] + "?" + strings.Split(a.ContentURL, "?")[1]},
	} {
		resp, body := e.req(&tc.p, "GET", tc.url, "", nil)
		if resp.StatusCode != 404 || errCode(t, body) != contracts.CodeNotFound {
			t.Errorf("%s = %d %.80q", name, resp.StatusCode, body)
		}
	}
	for _, act := range []struct{ method, path, body string }{
		{"POST", "/api/v1/playback/sessions/" + a.ID + "/heartbeat", `{"sequence":1}`},
		{"DELETE", "/api/v1/playback/sessions/" + a.ID, ""},
	} {
		if resp, _ := e.req(&bob, act.method, act.path, act.body, nil); resp.StatusCode != 404 {
			t.Errorf("bob %s on alice's session = %d", act.method, resp.StatusCode)
		}
	}
	if resp, _ := e.req(&alice, "GET", a.ContentURL, "", map[string]string{"Range": "bytes=0-0"}); resp.StatusCode != 206 {
		t.Fatalf("owner lost access after the attempts: %d", resp.StatusCode)
	}
}

func TestIdempotentStartAndCapacity(t *testing.T) {
	e := newEnv(t, 4096, func(o *Options) { o.MaxSessions = 2 })
	first := e.mustStart(alice, "k1")
	code, again, b := e.start(alice, "k1")
	if code != 200 || again.ID != first.ID || again.ContentURL != first.ContentURL {
		t.Fatalf("replay = %d %s", code, b)
	}
	resp, b := e.req(&alice, "POST", "/api/v1/playback/sessions", `{"itemId":"mov-2","versionId":"mov-2"}`, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != 409 || errCode(t, b) != contracts.CodeIdempotencyConflict {
		t.Fatalf("changed payload = %d %s", resp.StatusCode, b)
	}
	for name, key := range map[string]string{"missing": "", "too long": strings.Repeat("k", 129), "space": "k 1", "non-ascii": "k\u00e9"} {
		resp, b := e.req(&alice, "POST", "/api/v1/playback/sessions", `{"itemId":"mov-1","versionId":"mov-1"}`, map[string]string{"Idempotency-Key": key})
		if resp.StatusCode != 400 || errCode(t, b) != contracts.CodeInvalidRequest {
			t.Errorf("%s key = %d %s", name, resp.StatusCode, b)
		}
	}
	e.mustStart(alice, "k2")
	if code, _, b := e.start(alice, "k3"); code != 429 || errCode(t, b) != contracts.CodeCapacityUnavailable {
		t.Fatalf("over capacity = %d %s", code, b)
	}
	if code, _, _ := e.start(alice, "k1"); code != 200 {
		t.Fatalf("replay at capacity = %d; a replay must not count again", code)
	}
	// Point 1 on #20: a key whose session ended is a conflict, not a new session.
	if resp, _ := e.req(&alice, "DELETE", "/api/v1/playback/sessions/"+first.ID, "", nil); resp.StatusCode != 204 {
		t.Fatalf("stop = %d", resp.StatusCode)
	}
	if code, _, b := e.start(alice, "k1"); code != 409 || errCode(t, b) != contracts.CodeIdempotencyConflict || !bytes.Contains(b, []byte("Press Play to start again")) {
		t.Fatalf("replay after stop = %d %s", code, b)
	}
	e.clock.Advance(5*time.Minute + time.Second)
	if code, ps, b := e.start(alice, "k1"); code != 201 || ps.ID == first.ID {
		t.Fatalf("key after its tombstone expired = %d %s", code, b)
	}
}

func TestHeartbeatKeepsLongSessionAlive(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k1")
	hb := func(body string) (*http.Response, []byte) {
		return e.req(&alice, "POST", "/api/v1/playback/sessions/"+ps.ID+"/heartbeat", body, nil)
	}
	for i := 1; i <= 2*3600/10; i++ { // two hours of 10-second heartbeats
		e.clock.Advance(10 * time.Second)
		if resp, b := hb(fmt.Sprintf(`{"sequence":%d}`, i)); resp.StatusCode != 204 {
			t.Fatalf("heartbeat %d = %d %s", i, resp.StatusCode, b)
		}
	}
	if resp, _ := e.req(&alice, "GET", ps.ContentURL, "", map[string]string{"Range": "bytes=0-9"}); resp.StatusCode != 206 {
		t.Fatalf("content after two hours = %d", resp.StatusCode)
	}
	// Point 2 on #20: a repeat is a no-op, a lower sequence is rejected.
	if resp, _ := hb(`{"sequence":720}`); resp.StatusCode != 204 {
		t.Fatalf("repeated sequence = %d", resp.StatusCode)
	}
	for _, bad := range []string{`{"sequence":719}`, `{"sequence":-1}`, `{"sequence":1.5}`, `{"seq":800}`, `{"sequence":800,"x":1}`, `not json`} {
		if resp, b := hb(bad); resp.StatusCode != 400 || errCode(t, b) != contracts.CodeInvalidRequest {
			t.Errorf("heartbeat %s = %d %s", bad, resp.StatusCode, b)
		}
	}
	// A repeated sequence doesn't renew: the session still ends 60 s after the last new one.
	e.clock.Advance(55 * time.Second)
	hb(`{"sequence":720}`)
	e.clock.Advance(6 * time.Second)
	if resp, _ := hb(`{"sequence":721}`); resp.StatusCode != 410 {
		t.Fatalf("a repeated sequence renewed the session: %d", resp.StatusCode)
	}
}

func TestExpiredSessionCannotRevive(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k1")
	e.clock.Advance(61 * time.Second)
	resp, b := e.req(&alice, "POST", "/api/v1/playback/sessions/"+ps.ID+"/heartbeat", `{"sequence":1}`, nil)
	if resp.StatusCode != 410 || errCode(t, b) != contracts.CodePlaybackExpired {
		t.Fatalf("heartbeat after expiry = %d %s", resp.StatusCode, b)
	}
	if resp, b := e.req(&alice, "GET", ps.ContentURL, "", nil); resp.StatusCode != 410 || errCode(t, b) != contracts.CodePlaybackExpired {
		t.Fatalf("content after expiry = %d %s", resp.StatusCode, b)
	}
	if code, _, _ := e.start(alice, "k1"); code != 409 {
		t.Fatalf("restart with the expired session's key = %d", code)
	}
	if resp, _ := e.req(&alice, "DELETE", "/api/v1/playback/sessions/"+ps.ID, "", nil); resp.StatusCode != 204 {
		t.Fatalf("stopping an expired session = %d; stop is idempotent", resp.StatusCode)
	}
}

// streaming starts a whole-file GET and reads only the first 64 KiB, so the
// server blocks on a full socket buffer. It returns a func that reads the
// rest and reports how many bytes arrived in total.
func (e *env) streaming(ps contracts.PlaybackSession) func() (int, error) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+ps.ContentURL, nil)
	req.Header.Set("X-Test-Principal", header(alice))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		e.t.Fatalf("stream = %v %v", resp, err)
	}
	n, _ := io.ReadFull(resp.Body, make([]byte, 64<<10))
	waitFor(e.t, func() bool { return e.s.activeDeliveries() == 1 })
	// Wait until the server's write is stuck: no progress for 300 ms.
	last, still := int64(-1), 0
	waitFor(e.t, func() bool {
		time.Sleep(100 * time.Millisecond)
		if w := e.s.written.Load(); w == last {
			still++
		} else {
			last, still = w, 0
		}
		return still >= 3
	})
	return func() (int, error) {
		defer resp.Body.Close()
		m, err := io.Copy(io.Discard, resp.Body)
		return n + int(m), err
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("condition not reached within 5 s")
}

func TestStopAndLogoutAbortDelivery(t *testing.T) {
	const size = 48 << 20 // larger than the socket buffers
	for name, revoke := range map[string]func(e *env, ps contracts.PlaybackSession){
		"stop": func(e *env, ps contracts.PlaybackSession) {
			e.req(&alice, "DELETE", "/api/v1/playback/sessions/"+ps.ID, "", nil)
		},
		"logout": func(e *env, ps contracts.PlaybackSession) { e.s.RevokeAuthSession(alice.AuthSessionID) },
		"close":  func(e *env, ps contracts.PlaybackSession) { e.s.Close() },
		"expiry": func(e *env, ps contracts.PlaybackSession) { e.clock.Advance(61 * time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, size)
			ps := e.mustStart(alice, "k1")
			finish := e.streaming(ps)
			revoke(e, ps)
			// The client is not reading, so only revocation can end the server's blocked write.
			waitFor(t, func() bool { return e.s.activeDeliveries() == 0 })
			done := make(chan struct{})
			var got int
			var err error
			go func() { got, err = finish(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("delivery kept running after revocation")
			}
			if got >= size || err == nil {
				t.Fatalf("received %d of %d bytes, err %v; the response must be cut short", got, size, err)
			}
			if name != "close" {
				if resp, _ := e.req(&alice, "GET", ps.ContentURL, "", map[string]string{"Range": "bytes=0-0"}); resp.StatusCode != 410 {
					t.Fatalf("content after %s = %d", name, resp.StatusCode)
				}
			}
		})
	}
}

func TestTombstonesAreBounded(t *testing.T) {
	e := newEnv(t, 16, func(o *Options) { o.MaxSessions = 1 })
	for i := 0; i < 200; i++ {
		ps := e.mustStart(alice, fmt.Sprintf("k%d", i))
		e.req(&alice, "DELETE", "/api/v1/playback/sessions/"+ps.ID, "", nil)
	}
	if n := e.s.tombstoneCount(); n > maxTombstones {
		t.Fatalf("%d tombstones retained, want at most %d", n, maxTombstones)
	}
	e.clock.Advance(5*time.Minute + time.Second)
	e.mustStart(alice, "fresh")
	if n := e.s.tombstoneCount(); n != 0 {
		t.Fatalf("%d tombstones survived their 5 minutes", n)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	e := newEnv(t, 16)
	good := Options{Resolver: Single(contracts.Movie{ID: movieID, SourceRevision: "r"}, "Movies/movie.mp4", func(contracts.Principal, string) bool { return true }),
		GrantKey: bytes.Repeat([]byte{1}, 32), MaxSessions: 1}
	for name, fn := range map[string]func(*Options){
		"short grant key": func(o *Options) { o.GrantKey = o.GrantKey[:16] },
		"no sessions":     func(o *Options) { o.MaxSessions = 0 },
		"no resolver":     func(o *Options) { o.Resolver = nil },
	} {
		o := good
		fn(&o)
		if _, err := New(e.root, o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := New(nil, good); err == nil {
		t.Error("nil root accepted")
	}
}

// #20 (Pete, point 1): an Idempotency-Key is scoped to the auth session. A
// replay never returns another principal's session, or one from before a logout.
func TestIdempotencyKeyIsScopedToAuthSession(t *testing.T) {
	e := newEnv(t, 4096)
	e.policy = func(contracts.Principal, string) bool { return true }
	a := e.mustStart(alice, "same-key")
	for name, p := range map[string]contracts.Principal{"other account": bob, "same account, new sign-in": alice2} {
		code, ps, b := e.start(p, "same-key")
		if code != 201 || ps.ID == a.ID || ps.ContentURL == a.ContentURL {
			t.Fatalf("%s reusing the key = %d %s; it must get its own session", name, code, b)
		}
	}
	e.s.RevokeAuthSession(alice.AuthSessionID)
	// The same auth session ID can't sign in again after logout, but even if
	// it did, the old key would not hand back the revoked session.
	code, ps, b := e.start(alice, "same-key")
	if code == 200 || code == 201 && ps.ID == a.ID {
		t.Fatalf("replay after logout = %d %s", code, b)
	}
}

// fakeDeadline records the write deadlines a delivery sets.
type fakeDeadline struct {
	mu   sync.Mutex
	last time.Time
}

func (f *fakeDeadline) SetWriteDeadline(t time.Time) error {
	f.mu.Lock()
	f.last = t
	f.mu.Unlock()
	return nil
}

// #27 (Pete): a revoke that lands between a write's liveness check and its
// deadline must still stop that write. Otherwise the write's 30 s deadline
// replaces the revoke's "now" and a stalled client holds the stream.
func TestRevokeBeatsInFlightWriteDeadline(t *testing.T) {
	e := newEnv(t, 16)
	ps := e.mustStart(alice, "k1")
	e.s.mu.Lock()
	sess := e.s.sessions[ps.ID]
	e.s.mu.Unlock()

	fd := &fakeDeadline{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &delivery{cancel: cancel, rc: fd}
	e.s.mu.Lock()
	sess.deliveries[d] = struct{}{}
	e.s.mu.Unlock()
	rec := httptest.NewRecorder()
	dw := &deliveryWriter{ResponseWriter: rec, ctx: ctx, rc: fd, written: &e.s.written, alive: func() bool {
		e.s.mu.Lock()
		defer e.s.mu.Unlock()
		return e.s.liveLocked(sess, e.s.now())
	}}

	paused, release := make(chan struct{}), make(chan struct{})
	testHookAfterAlive = func() { close(paused); <-release }
	defer func() { testHookAfterAlive = nil }()
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() { n, err := dw.Write([]byte("bytes after revoke")); done <- result{n, err} }()

	<-paused
	e.s.RevokeAuthSession(alice.AuthSessionID) // revoke inside the window
	close(release)
	r := <-done
	if r.err != errRevoked || r.n != 0 || rec.Body.Len() != 0 {
		t.Fatalf("write after an in-window revoke = %d, %v, %d bytes sent; it must stop without writing", r.n, r.err, rec.Body.Len())
	}
}

func (e *env) post(p contracts.Principal, key, body string) (int, []byte) {
	e.t.Helper()
	resp, b := e.req(&p, "POST", "/api/v1/playback/sessions", body, map[string]string{"Idempotency-Key": key})
	return resp.StatusCode, b
}

func TestStartByItemAndVersion(t *testing.T) {
	e := newEnv(t, 4096)
	code, b := e.post(alice, "k1", `{"itemId":"`+movieID+`","versionId":"`+movieID+`"}`)
	var ps contracts.PlaybackSession
	json.Unmarshal(b, &ps)
	if code != 201 || ps.ItemID != movieID || ps.VersionID != movieID || !bytes.Contains(b, []byte(`"itemId"`)) || bytes.Contains(b, []byte("movieId")) {
		t.Fatalf("start = %d %s", code, b)
	}
}

func TestStartUnknownVersionIs404(t *testing.T) {
	e := newEnv(t, 16)
	code, b := e.post(alice, "k1", `{"itemId":"`+movieID+`","versionId":"other"}`)
	if code != 404 || errCode(t, b) != contracts.CodeNotFound {
		t.Fatalf("unknown version = %d %s", code, b)
	}
}

func TestStartForbiddenItemIs404LikeUnknown(t *testing.T) {
	e := newEnv(t, 16)
	body := `{"itemId":"` + movieID + `","versionId":"` + movieID + `"}`
	fc, fb := e.post(bob, "k1", body) // bob fails the policy
	uc, ub := e.post(alice, "k2", `{"itemId":"nope","versionId":"nope"}`)
	var f, u contracts.APIError
	json.Unmarshal(fb, &f)
	json.Unmarshal(ub, &u)
	if fc != 404 || uc != 404 || f.Code != u.Code || f.Message != u.Message {
		t.Fatalf("forbidden = %d %s, unknown = %d %s", fc, fb, uc, ub)
	}
}

func TestStartMissingVersionIs400(t *testing.T) {
	e := newEnv(t, 16)
	code, b := e.post(alice, "k1", `{"itemId":"x"}`)
	if code != 400 || errCode(t, b) != contracts.CodeInvalidRequest {
		t.Fatalf("missing version = %d %s", code, b)
	}
}

func TestStartRejectsLegacyMovieID(t *testing.T) {
	e := newEnv(t, 16)
	code, b := e.post(alice, "k1", `{"movieId":"x"}`)
	if code != 400 || errCode(t, b) != contracts.CodeInvalidRequest {
		t.Fatalf("legacy movieId = %d %s", code, b)
	}
}

// multi resolves two items, each its own file.
type multi map[string]Source

func (m multi) Resolve(_ contracts.Principal, item, version string) (Source, error) {
	if s, ok := m[item]; ok && s.VersionID == version {
		return s, nil
	}
	return Source{}, ErrNotFound
}

func TestReplaySameKeyDifferentVersionIs409(t *testing.T) {
	e := newEnv(t, 4096)
	e.mustStart(alice, "k1")
	code, b := e.post(alice, "k1", `{"itemId":"`+movieID+`","versionId":"other"}`)
	if code != 409 || errCode(t, b) != contracts.CodeIdempotencyConflict {
		t.Fatalf("same key, other version = %d %s", code, b)
	}
}

func TestGrantBindsVersion(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k1")
	e.s.mu.Lock()
	sess := e.s.sessions[ps.ID]
	other := *sess
	other.src.VersionID = "other"
	grant := e.s.sign(&other)
	e.s.mu.Unlock()
	if grant == sess.grant {
		t.Fatal("grant ignores the version")
	}
	resp, b := e.req(&alice, "GET", "/api/v1/playback/sessions/"+ps.ID+"/content?grant="+grant, "", nil)
	if resp.StatusCode != 404 || errCode(t, b) != contracts.CodeNotFound {
		t.Fatalf("content with another version's grant = %d %s", resp.StatusCode, b)
	}
}

func TestSessionsOfTwoSourcesStayApart(t *testing.T) {
	e := newEnv(t, 16)
	a, b := bytes.Repeat([]byte("A"), 2048), bytes.Repeat([]byte("B"), 3072)
	srcs := multi{}
	for id, data := range map[string][]byte{"a": a, "b": b} {
		path := filepath.Join(e.dir, "Movies", id+".mp4")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(path)
		srcs[id] = Source{ItemID: id, VersionID: id + "v", FileID: "file-" + id, RelativePath: "Movies/" + id + ".mp4", SourceRevision: Revision("file-"+id, info)}
	}
	s2, err := New(e.root, Options{Resolver: srcs, GrantKey: bytes.Repeat([]byte{7}, 32), MaxSessions: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(contracts.WithPrincipal(r.Context(), alice))
		s2.Handler().ServeHTTP(w, r)
	}))
	defer srv.Close()
	for _, id := range []string{"a", "b"} {
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/playback/sessions", strings.NewReader(`{"itemId":"`+id+`","versionId":"`+id+`v"}`))
		req.Header.Set("Idempotency-Key", "k-"+id)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var ps contracts.PlaybackSession
		json.NewDecoder(resp.Body).Decode(&ps)
		resp.Body.Close()
		if resp.StatusCode != 201 || ps.ItemID != id || ps.VersionID != id+"v" {
			t.Fatalf("start %s = %d %+v", id, resp.StatusCode, ps)
		}
		got, err := http.Get(srv.URL + ps.ContentURL)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(got.Body)
		got.Body.Close()
		want := map[string][]byte{"a": a, "b": b}[id]
		if !bytes.Equal(body, want) || ps.SourceRevision != srcs[id].SourceRevision {
			t.Fatalf("session %s served %d bytes, want %d", id, len(body), len(want))
		}
	}
}

type failing struct{}

func (failing) Resolve(contracts.Principal, string, string) (Source, error) {
	return Source{}, errors.New("catalogue down")
}

func TestStartResolverErrorIs500(t *testing.T) {
	var buf syncBuffer
	e := newEnv(t, 16, func(o *Options) { o.Resolver = failing{}; o.Log = slog.New(slog.NewJSONHandler(&buf, nil)) })
	code, b := e.post(alice, "k1", `{"itemId":"x","versionId":"y"}`)
	if code != 500 || errCode(t, b) != contracts.CodeInternalError {
		t.Fatalf("resolver error = %d %s", code, b)
	}
	if !buf.has("playback.resolve_error", "item_id", "x", "version_id", "y") {
		t.Fatalf("no playback.resolve_error: %s", buf.String())
	}
}

// failAfterFirst resolves once (so start succeeds), then fails.
type failAfterFirst struct {
	inner Resolver
	n     atomic.Int32
}

func (f *failAfterFirst) Resolve(p contracts.Principal, item, version string) (Source, error) {
	if f.n.Add(1) > 1 {
		return Source{}, errors.New("catalogue down")
	}
	return f.inner.Resolve(p, item, version)
}

func TestContentResolverErrorIs500(t *testing.T) {
	var buf syncBuffer
	e := newEnv(t, 4096, func(o *Options) {
		o.Resolver = &failAfterFirst{inner: o.Resolver}
		o.Log = slog.New(slog.NewJSONHandler(&buf, nil))
	})
	ps := e.mustStart(alice, "k1")
	resp, b := e.req(&alice, "GET", ps.ContentURL, "", nil)
	if resp.StatusCode != 500 || errCode(t, b) != contracts.CodeInternalError {
		t.Fatalf("content with failing resolver = %d %s", resp.StatusCode, b)
	}
	if !buf.has("playback.resolve_error", "item_id", movieID, "version_id", movieID) {
		t.Fatalf("no playback.resolve_error: %s", buf.String())
	}
}

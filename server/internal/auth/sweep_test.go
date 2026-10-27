package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/playback"
)

func TestSweepRevokesExpiredWithoutARequest(t *testing.T) {
	h := newHarness(t, false)
	c := h.client()
	c.session()
	c.login("viewer", testPassword)
	_, p := c.principal()
	h.m.Sweep()
	if len(h.revocations()) != 0 {
		t.Fatal("Sweep revoked a live session")
	}
	h.clock.Advance(idleTimeout + time.Second)
	h.m.Sweep() // no request from the client
	if r := h.revocations(); len(r) != 1 || r[0] != p["session"] {
		t.Fatalf("revoked = %v, want the expired session", r)
	}
	if n := h.m.sessionCount(); n != 0 {
		t.Fatalf("%d sessions left after the sweep", n)
	}
}

func TestRunSweepsPeriodically(t *testing.T) {
	h := newHarness(t, false)
	c := h.client()
	c.session()
	c.login("viewer", testPassword)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.m.Run(ctx, 10*time.Millisecond); close(done) }()
	h.clock.Advance(absoluteTimeout + time.Second)
	waitUntil(t, func() bool { return len(h.revocations()) == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept going after its context ended")
	}
}

// #30 (Pete): an auth session that expires mid-stream has its delivery cut
// by the sweep, without the client sending another request.
func TestSweepCutsAnExpiredSessionsStream(t *testing.T) {
	dir := t.TempDir()
	movie := make([]byte, 32<<20)
	rand.Read(movie)
	if err := os.WriteFile(filepath.Join(dir, "movie.mp4"), movie, 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	info, _ := os.Stat(filepath.Join(dir, "movie.mp4"))
	var pb *playback.Server
	h := newHarnessWith(t, false, func(id string) { pb.RevokeAuthSession(id) }, func(mux *http.ServeMux, m *Manager) {
		var err error
		pb, err = playback.New(root, playback.Options{
			Source:   playback.Source{Movie: contracts.Movie{ID: "mov-1", SourceRevision: playback.Revision("mov-1", info)}, RelativePath: "movie.mp4"},
			GrantKey: make([]byte, 32), MaxSessions: 2,
			Authorize: func(contracts.Principal, string) bool { return true },
		})
		if err != nil {
			t.Fatal(err)
		}
		mux.Handle("/api/v1/playback/", m.Require(pb.Handler()))
	})
	defer pb.Close()
	c := h.client()
	c.session()
	c.login("viewer", testPassword)
	resp, b := c.do("POST", "/api/v1/playback/sessions", `{"movieId":"mov-1"}`, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != 201 {
		t.Fatalf("start = %d %s", resp.StatusCode, b)
	}
	var ps contracts.PlaybackSession
	json.Unmarshal(b, &ps)
	req, _ := http.NewRequest("GET", h.srv.URL+ps.ContentURL, nil)
	req.Header.Set("Origin", h.srv.URL)
	stream, err := c.c.Do(req)
	if err != nil || stream.StatusCode != 200 {
		t.Fatalf("stream = %v %v", stream, err)
	}
	defer stream.Body.Close()
	io.ReadFull(stream.Body, make([]byte, 64<<10))

	h.clock.Advance(idleTimeout + time.Second)
	h.m.Sweep() // the only trigger: the client sends nothing more
	got, err := io.Copy(io.Discard, stream.Body)
	if err == nil || got+64<<10 >= int64(len(movie)) {
		t.Fatalf("stream after the sweep: %d more bytes, err %v; it must be cut short", got, err)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("condition not reached")
}

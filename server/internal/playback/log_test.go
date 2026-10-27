package playback

import (
	"bytes"
	"context"
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

// lockProbe is a log handler that checks playback's lock is free whenever a
// line is written (#74): logging under the lock would let a blocked stderr
// stall every playback request.
type lockProbe struct {
	s       **Server
	mu      sync.Mutex
	underMu []string
}

func (h *lockProbe) Enabled(context.Context, slog.Level) bool { return true }
func (h *lockProbe) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *lockProbe) WithGroup(string) slog.Handler            { return h }
func (h *lockProbe) Handle(_ context.Context, r slog.Record) error {
	if s := *h.s; s != nil {
		if s.mu.TryLock() {
			s.mu.Unlock()
		} else {
			h.mu.Lock()
			h.underMu = append(h.underMu, r.Message)
			h.mu.Unlock()
		}
	}
	return nil
}

func TestEventsAreLoggedOutsideTheLock(t *testing.T) {
	var srv *Server
	probe := &lockProbe{s: &srv}
	e := newEnv(t, 4096, func(o *Options) { o.Log = slog.New(probe) })
	srv = e.s
	a := e.mustStart(alice, "k1")
	e.req(&alice, "DELETE", "/api/v1/playback/sessions/"+a.ID, "", nil) // stopped
	e.mustStart(alice, "k2")
	e.s.RevokeAuthSession(alice.AuthSessionID) // revoked
	e.mustStart(alice2, "k3")
	e.clock.Advance(idleTimeout + time.Second) // expired, by the sweeper
	time.Sleep(1500 * time.Millisecond)
	e.mustStart(alice2, "k4")
	e.s.Close() // closed
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if len(probe.underMu) > 0 {
		t.Fatalf("logged while holding playback's lock: %v", probe.underMu)
	}
}

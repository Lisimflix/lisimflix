package playback

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

func TestSessionLookupReturnsOwnedLiveSession(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-1")
	ref, err := e.s.Session(alice, ps.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := SessionRef{ID: ps.ID, ItemID: ps.ItemID, VersionID: ps.VersionID, SourceRevision: ps.SourceRevision}
	if ref != want || ref.ItemID != movieID || ref.SourceRevision == "" {
		t.Fatalf("ref = %+v, want %+v", ref, want)
	}
}

func TestSessionLookupRefusesOtherPrincipal(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-2")
	for _, c := range []struct {
		name string
		p    contracts.Principal
		id   string
	}{
		{"other account", bob, ps.ID},
		{"same profile, other sign-in", alice2, ps.ID},
		{"unknown id", alice, "ps-AAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{"malformed id", alice, "nope"},
	} {
		if _, err := e.s.Session(c.p, c.id); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("%s: err = %v, want ErrSessionNotFound", c.name, err)
		}
	}
}

func TestSessionLookupEnded(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-3")
	if resp, _ := e.req(&alice, "DELETE", "/api/v1/playback/sessions/"+ps.ID, "", nil); resp.StatusCode != 204 {
		t.Fatalf("stop = %d", resp.StatusCode)
	}
	if _, err := e.s.Session(alice, ps.ID); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("err = %v, want ErrSessionEnded", err)
	}
	ps2 := e.mustStart(alice, "k-lookup-3b")
	e.clock.Advance(idleTimeout + time.Second)
	if _, err := e.s.Session(alice, ps2.ID); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("idle: err = %v, want ErrSessionEnded", err)
	}
}

func TestSessionLookupSourceChanged(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-4")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(e.path, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Session(alice, ps.ID); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("err = %v, want ErrSourceChanged", err)
	}
}

func TestSessionLookupSourceMissing(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-5")
	if err := os.Remove(e.path); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Session(alice, ps.ID); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("err = %v, want ErrSourceChanged", err)
	}
}

func TestSessionLookupSameMetadataDifferentFile(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-8")
	info, _ := os.Stat(e.path)
	repl := filepath.Join(e.dir, "Movies", "new.mp4")
	os.WriteFile(repl, bytes.Repeat([]byte{2}, 4096), 0o644)
	os.Chtimes(repl, info.ModTime(), info.ModTime()) // same size and mtime: same revision
	if err := os.Rename(repl, e.path); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Session(alice, ps.ID); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("err = %v, want ErrSourceChanged: file identity must be checked", err)
	}
}

func TestSessionLookupKeepsDeadline(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-6")
	e.s.mu.Lock()
	before := e.s.sessions[ps.ID].deadline
	e.s.mu.Unlock()
	e.clock.Advance(5 * time.Second)
	if _, err := e.s.Session(alice, ps.ID); err != nil {
		t.Fatal(err)
	}
	e.s.mu.Lock()
	after := e.s.sessions[ps.ID].deadline
	e.s.mu.Unlock()
	if !after.Equal(before) {
		t.Fatalf("deadline moved from %v to %v", before, after)
	}
}

func TestSessionLookupDoesNotStatUnderLock(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-7")
	other := e.mustStart(alice, "k-lookup-7b")
	entered, release := make(chan struct{}), make(chan struct{})
	testHookStat = func() { close(entered); <-release }
	t.Cleanup(func() { testHookStat = nil })
	done := make(chan error, 1)
	go func() { _, err := e.s.Session(alice, ps.ID); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("lookup returned %v before the stat", err)
	}
	beat := make(chan int, 1)
	go func() {
		resp, _ := e.req(&alice, "POST", "/api/v1/playback/sessions/"+other.ID+"/heartbeat", `{"sequence":1}`, nil)
		beat <- resp.StatusCode
	}()
	select {
	case code := <-beat:
		if code != 204 {
			t.Errorf("heartbeat = %d, want 204", code)
		}
	case <-time.After(5 * time.Second):
		t.Error("heartbeat blocked while the lookup was stat-ing")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A file that can't be read for another reason (here: permissions) is not
// "changed": the lookup returns a plain error (a 500 the player ignores), not
// ErrSourceChanged, which ends playback in the browser.
func TestSessionLookupUnreadableIsNotSourceChanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-9")
	dir := filepath.Dir(e.path)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	_, err := e.s.Session(alice, ps.ID)
	if err == nil || errors.Is(err, ErrSourceChanged) || errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrSessionEnded) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}

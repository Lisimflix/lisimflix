package playback

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestSessionLookupReturnsOwnedLiveSession(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-1")
	ref, err := e.s.Session(alice, ps.ID)
	if err != nil {
		t.Fatal(err)
	}
	src := e.s.sessions[ps.ID].src
	want := SessionRef{ID: ps.ID, ItemID: src.ItemID, VersionID: src.VersionID, SourceRevision: src.SourceRevision}
	if ref != want || ref.ItemID != movieID || ref.SourceRevision == "" {
		t.Fatalf("ref = %+v, want %+v", ref, want)
	}
}

func TestSessionLookupRefusesOtherPrincipal(t *testing.T) {
	e := newEnv(t, 4096)
	ps := e.mustStart(alice, "k-lookup-2")
	for _, c := range []struct {
		name string
		id   string
		err  error
	}{
		{"other principal", ps.ID, nil},
		{"unknown id", "ps-AAAAAAAAAAAAAAAAAAAAAAAAAA", nil},
		{"malformed id", "nope", nil},
	} {
		p := alice
		if c.name == "other principal" {
			p = bob
		}
		if _, err := e.s.Session(p, c.id); !errors.Is(err, ErrSessionNotFound) {
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
	<-entered
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

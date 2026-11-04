package userdata

import (
	"errors"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

func TestRoundTripKeepsMeaning(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	e.ticks(s, 0, 0, 90_000) // covers 90% of 100 s: auto watched on the last tick
	at := e.clock
	e.hide(0)

	st := e.get()
	if st.Resume == nil || st.Resume.PositionMs != 90_000 || st.Resume.DurationMs != 100_000 ||
		st.Resume.VersionID != "v1" || st.Resume.SourceRevision != "r1" || !st.Resume.UpdatedAt.Equal(at) || st.Resume.UpdatedAt.Location() != time.UTC {
		t.Fatalf("resume = %+v", st.Resume)
	}
	if st.Watched == nil || !st.Watched.Value || st.Watched.Origin != contracts.WatchedAuto || !st.Watched.At.Equal(at) || st.Watched.At.Location() != time.UTC {
		t.Fatalf("watched = %+v", st.Watched)
	}
	if st.PlayCount == nil || *st.PlayCount != 1 {
		t.Fatalf("play count = %v", st.PlayCount)
	}
	if st.LastPlayedAt == nil || !st.LastPlayedAt.Equal(at) || st.LastPlayedAt.Location() != time.UTC {
		t.Fatalf("last played = %v", st.LastPlayedAt)
	}
	if !st.HiddenFromContinueWatching {
		t.Fatal("hidden flag lost")
	}

	// Only hidden set: everything else unknown, not zero values.
	h, err := e.st.Hide(ctx, "p1", "other", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !h.HiddenFromContinueWatching || h.Resume != nil || h.Watched != nil || h.PlayCount != nil || h.LastPlayedAt != nil {
		t.Fatalf("hidden-only state = %+v", h)
	}
	got, err := e.st.Get(ctx, "p1", "other")
	if err != nil {
		t.Fatal(err)
	}
	if got.Resume != nil || got.Watched != nil || got.PlayCount != nil || got.LastPlayedAt != nil {
		t.Fatalf("hidden-only Get = %+v", got)
	}
}

func TestLastPlayedNeverMovesBackwards(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	e.play(s, tick, 0, 0)
	want := e.clock
	e.advance(-time.Hour)
	if r := e.play(s, tick, 1, 10_000); !r.Accepted {
		t.Fatalf("second tick = %s", reason(r))
	}
	if got := e.get().LastPlayedAt; got == nil || !got.Equal(want) {
		t.Fatalf("LastPlayedAt = %v, want %v", got, want)
	}
}

func TestImportFixturesKeepIdentityAndOrigin(t *testing.T) {
	e := newEnv(t)
	// Demonstration only: 1a has no importer, so the source keys map to local items by hand.
	local := map[string]string{"plex:srvA:1001": "itemA", "plex:srvB:1001": "itemB"}
	if len(local) != 2 || local["plex:srvA:1001"] == local["plex:srvB:1001"] {
		t.Fatal("fixture keys must map to distinct items")
	}
	importedAt := e.clock.Add(-time.Hour)
	for _, item := range local {
		if _, err := e.db.Exec(`INSERT INTO userdata_state (profile_id, item_id, watched_value, watched_origin, watched_at, last_played_at)
			VALUES ('p1', ?, 1, 'import', ?, ?)`, item, importedAt.UnixMilli(), importedAt.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}

	for _, item := range local {
		st, err := e.st.Get(ctx, "p1", item)
		if err != nil {
			t.Fatal(err)
		}
		if st.Watched == nil || !st.Watched.Value || st.Watched.Origin != contracts.WatchedImport || !st.Watched.At.Equal(importedAt) ||
			st.Resume != nil || st.PlayCount != nil || st.LastPlayedAt == nil || !st.LastPlayedAt.Equal(importedAt) {
			t.Fatalf("%s imported state = %+v", item, st)
		}
	}

	// A local tick on itemA moves LastPlayedAt and Resume only; itemB is untouched.
	s := Session{ID: "s1", ItemID: "itemA", VersionID: "v1", SourceRevision: "r1", DurationMs: 100_000}
	res, err := e.st.Apply(ctx, "p1", s, contracts.ProgressEvent{Sequence: 0, Kind: tick, PositionMs: 5_000})
	if err != nil || !res.Accepted {
		t.Fatalf("tick: %+v %v", res, err)
	}
	a := res.State
	if a.LastPlayedAt == nil || !a.LastPlayedAt.Equal(e.clock) || a.Resume == nil || a.Resume.PositionMs != 5_000 || !a.Resume.UpdatedAt.Equal(e.clock) {
		t.Fatalf("itemA after tick = %+v", a)
	}
	if a.Watched == nil || a.Watched.Origin != contracts.WatchedImport || !a.Watched.At.Equal(importedAt) || a.PlayCount != nil {
		t.Fatalf("itemA watched/count changed: %+v", a)
	}
	b, err := e.st.Get(ctx, "p1", "itemB")
	if err != nil {
		t.Fatal(err)
	}
	if b.Resume != nil || b.LastPlayedAt == nil || !b.LastPlayedAt.Equal(importedAt) {
		t.Fatalf("itemB changed: %+v", b)
	}
}

func TestExpectedRevisionZeroAfterExplicitWrite(t *testing.T) {
	e := newEnv(t)
	first := e.mustSetWatched(true, 0)
	if first.Revision != 1 {
		t.Fatalf("revision = %d", first.Revision)
	}
	if _, err := e.setWatched("item", false, 0); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("err = %v, want ErrRevisionConflict", err)
	}
	st := e.get()
	if st.Revision != 1 || st.Watched == nil || !st.Watched.Value || st.Watched.Origin != contracts.WatchedManual {
		t.Fatalf("state changed: %+v", st)
	}
}

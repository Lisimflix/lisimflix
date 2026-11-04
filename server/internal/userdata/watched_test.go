package userdata

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

var ctx = context.Background()

func (e *env) setWatched(item string, v bool, rev int64) (contracts.UserItemState, error) {
	return e.st.SetWatched(ctx, "p1", item, v, rev)
}

func (e *env) mustSetWatched(v bool, rev int64) contracts.UserItemState {
	e.t.Helper()
	st, err := e.setWatched("item", v, rev)
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func (e *env) hide(rev int64) contracts.UserItemState {
	e.t.Helper()
	st, err := e.st.Hide(ctx, "p1", "item", rev)
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

// playItem plays one session on the given item up to coveredMs (10 s ticks from 0).
func (e *env) playItem(item, session string, coveredMs int64) {
	e.t.Helper()
	s := Session{ID: session, ItemID: item, VersionID: "v1", SourceRevision: "r1", DurationMs: 100_000}
	for seq, p := int64(0), int64(0); p <= coveredMs; seq, p = seq+1, p+10_000 {
		if r, err := e.st.Apply(ctx, "p1", s, contracts.ProgressEvent{Sequence: seq, Kind: tick, PositionMs: p}); err != nil || !r.Accepted {
			e.t.Fatalf("%s tick %d: %+v %v", item, seq, r, err)
		}
	}
}

func (e *env) list(limit int) []string {
	e.t.Helper()
	got, err := e.st.ContinueWatching(ctx, "p1", limit)
	if err != nil {
		e.t.Fatal(err)
	}
	ids := make([]string, len(got))
	for i, st := range got {
		ids[i] = st.ItemID
	}
	return ids
}

func TestManualWatchedSurvivesLateEvents(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	e.ticks(s, 0, 0, 40_000)
	marked := e.mustSetWatched(true, 0)
	if marked.Watched == nil || !marked.Watched.Value || marked.Watched.Origin != contracts.WatchedManual || marked.PlayCount != nil {
		t.Fatalf("marked = %+v", marked)
	}
	at := marked.Watched.At
	e.advance(time.Minute)
	e.ticks(s, 5, 50_000, 100_000)
	r := e.play(s, tick, 5, 50_000)
	if reason(r) != "duplicate" {
		t.Fatalf("replay = %s", reason(r))
	}
	st := e.get()
	if st.Watched == nil || !st.Watched.Value || st.Watched.Origin != "manual" || !st.Watched.At.Equal(at) || st.PlayCount != nil {
		t.Fatalf("state = %+v", st)
	}
}

func TestUnwatchedIsAReset(t *testing.T) {
	e := newEnv(t)
	a := sess("a", "r1")
	e.ticks(a, 0, 0, 90_000)
	st := e.get()
	if st.Watched == nil || st.Watched.Origin != contracts.WatchedAuto || *st.PlayCount != 1 {
		t.Fatalf("not auto-watched: %+v", st)
	}
	st = e.mustSetWatched(false, 0)
	if st.Watched != nil || st.Resume != nil || *st.PlayCount != 1 || st.Revision != 1 {
		t.Fatalf("after reset: %+v", st)
	}
	if n := e.count(`SELECT COUNT(*) FROM userdata_intervals`); n != 0 || e.covered() != 0 {
		t.Fatalf("coverage left: %d rows, covered %d", n, e.covered())
	}
	if r := e.play(a, tick, 10, 95_000); reason(r) != "stale" {
		t.Fatalf("old session = %s", reason(r))
	}
	e.ticks(sess("b", "r1"), 0, 0, 100_000)
	st = e.get()
	if st.Watched == nil || st.Watched.Origin != contracts.WatchedAuto || *st.PlayCount != 2 {
		t.Fatalf("not re-watched: %+v", st)
	}
}

func TestLateSessionAfterResetIsStale(t *testing.T) {
	e := newEnv(t)
	a := sess("a", "r1")
	e.play(a, tick, 0, 0)
	e.play(a, tick, 1, 10_000)
	e.mustSetWatched(false, 0)
	if r := e.play(a, tick, 2, 20_000); reason(r) != "stale" {
		t.Fatalf("late tick = %s", reason(r))
	}
	if r := e.play(sess("b", "r1"), tick, 0, 0); !r.Accepted {
		t.Fatalf("new session = %s", reason(r))
	}
}

func TestProgressDoesNotBumpRevision(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	s.DurationMs = 40_000
	e.ticks(s, 0, 0, 40_000)
	st := e.get()
	if st.Revision != 0 || st.Watched == nil {
		t.Fatalf("after progress: %+v", st)
	}
	if st = e.mustSetWatched(true, 0); st.Revision != 1 {
		t.Fatalf("revision = %d", st.Revision)
	}
}

func TestRevisionConflict(t *testing.T) {
	e := newEnv(t)
	if st := e.mustSetWatched(true, 0); st.Revision != 1 {
		t.Fatalf("revision = %d", st.Revision)
	}
	before := e.get()
	if _, err := e.setWatched("item", false, 0); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.st.Hide(ctx, "p1", "item", 0); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("hide err = %v", err)
	}
	if after := e.get(); !reflect.DeepEqual(after, before) || after.Watched == nil || after.HiddenFromContinueWatching {
		t.Fatalf("changed: %+v -> %+v", before, after)
	}
}

func TestContinueWatching(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		item string
		ms   int64
	}{{"A", 40_000}, {"B", 10_000}, {"C", 60_000}, {"D", 60_000}, {"E", 60_000}} {
		e.playItem(c.item, "s"+c.item, c.ms)
		e.advance(time.Second)
	}
	if _, err := e.setWatched("C", true, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Hide(ctx, "p1", "D", 0); err != nil {
		t.Fatal(err)
	}
	if got := e.list(10); !slices.Equal(got, []string{"E", "A"}) {
		t.Fatalf("list = %v", got)
	}
	got, _ := e.st.ContinueWatching(ctx, "p1", 10)
	if got[0].Resume == nil || got[0].Resume.PositionMs != 60_000 {
		t.Fatalf("state not mapped: %+v", got[0])
	}
	if other, _ := e.st.ContinueWatching(ctx, "p2", 10); len(other) != 0 {
		t.Fatalf("other profile sees %v", other)
	}
}

func TestContinueWatchingLimit(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 201; i++ {
		e.playItem(fmt.Sprintf("i%03d", i), fmt.Sprint("s", i), 30_000)
		e.advance(time.Second)
	}
	for limit, want := range map[int]int{1000: 200, 0: 50, -1: 50, 201: 200} {
		if got := e.list(limit); len(got) != want {
			t.Errorf("limit %d: got %d, want %d", limit, len(got), want)
		}
	}
	if got := e.list(3); !slices.Equal(got, []string{"i201", "i200", "i199"}) {
		t.Fatalf("limit 3 = %v", got)
	}
}

func TestContinueWatchingTieBreak(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{"b", "c", "a"} {
		e.playItem(id, "s"+id, 30_000) // clock frozen: equal last_played_at
	}
	if got := e.list(10); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("list = %v", got)
	}
}

func TestHideThenPlayAgainRestores(t *testing.T) {
	e := newEnv(t)
	a := sess("a", "r1")
	e.ticks(a, 0, 0, 40_000)
	if st := e.hide(0); !st.HiddenFromContinueWatching || st.Revision != 1 {
		t.Fatalf("hide: %+v", st)
	}
	if got := e.list(10); len(got) != 0 {
		t.Fatalf("hidden item listed: %v", got)
	}
	e.advance(time.Second)
	e.play(sess("b", "r1"), tick, 0, 40_000)
	if st := e.get(); st.HiddenFromContinueWatching {
		t.Fatalf("still hidden: %+v", st)
	}
	if got := e.list(10); !slices.Equal(got, []string{"item"}) {
		t.Fatalf("list = %v", got)
	}
}

func TestLateTickDoesNotUnhide(t *testing.T) {
	e := newEnv(t)
	a := sess("a", "r1")
	e.ticks(a, 0, 0, 40_000)
	e.hide(0)
	if r := e.play(a, tick, 5, 50_000); !r.Accepted {
		t.Fatalf("late tick = %s", reason(r))
	}
	if st := e.get(); !st.HiddenFromContinueWatching {
		t.Fatalf("unhidden by a late tick: %+v", st)
	}
	if got := e.list(10); len(got) != 0 {
		t.Fatalf("listed: %v", got)
	}
}

func TestMarkOrderingUsesEventIDsNotTime(t *testing.T) {
	e := newEnv(t) // clock never advances
	a := sess("a", "r1")
	e.play(a, tick, 0, 0)
	e.mustSetWatched(false, 0)
	if r := e.play(a, tick, 1, 10_000); reason(r) != "stale" {
		t.Fatalf("second tick = %s", reason(r))
	}
	if r := e.play(sess("b", "r1"), tick, 0, 0); !r.Accepted {
		t.Fatalf("new session = %s", reason(r))
	}
}

func TestHideKeepsHistory(t *testing.T) {
	e := newEnv(t)
	e.ticks(sess("a", "r1"), 0, 0, 40_000)
	before := e.count(`SELECT COUNT(*) FROM userdata_events`)
	e.hide(0)
	e.mustSetWatched(true, 1)
	if after := e.count(`SELECT COUNT(*) FROM userdata_events`); after != before || before != 5 {
		t.Fatalf("events %d -> %d", before, after)
	}
}

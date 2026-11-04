package userdata

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

const (
	tick  = contracts.ProgressTick
	pause = contracts.ProgressPause
	seek  = contracts.ProgressSeek
	stop  = contracts.ProgressStop
)

func (e *env) get() contracts.UserItemState {
	e.t.Helper()
	st, err := e.st.Get(context.Background(), "p1", "item")
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

// ticks plays every 10 s from `from` to `to` inclusive, starting at sequence seq.
func (e *env) ticks(s Session, seq, from, to int64) int64 {
	for p := from; p <= to; p += 10_000 {
		if r := e.play(s, tick, seq, p); !r.Accepted {
			e.t.Fatalf("tick %d at %d rejected: %v", seq, p, *r.Reason)
		}
		seq++
	}
	return seq
}

func reason(r contracts.ProgressResult) string {
	if r.Accepted {
		return "accepted"
	}
	return *r.Reason
}

func TestFirstTickRecordsResume(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	r := e.play(s, tick, 1, 10_000)
	st := r.State
	if !r.Accepted || r.Reason != nil || st.Revision != 0 || st.Watched != nil || st.Resume == nil ||
		st.Resume.PositionMs != 10_000 || st.Resume.DurationMs != 100_000 || st.Resume.VersionID != "v1" ||
		st.Resume.SourceRevision != "r1" || !st.Resume.UpdatedAt.Equal(e.clock) {
		t.Fatalf("result = %+v, state %+v", r, st)
	}
}

func TestSequenceStartsAtZero(t *testing.T) {
	e := newEnv(t)
	if r := e.play(sess("s1", "r1"), tick, 0, 0); !r.Accepted {
		t.Fatal("sequence 0 rejected")
	}
}

func TestDuplicateAndStale(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	e.play(s, tick, 1, 10_000)
	e.play(s, tick, 3, 30_000)
	before := e.get()
	from := int64(30_000)
	check := func(label string, ev contracts.ProgressEvent, want string) {
		t.Helper()
		r, err := e.st.Apply(context.Background(), "p1", s, ev)
		if err != nil {
			t.Fatal(err)
		}
		if reason(r) != want {
			t.Fatalf("%s: %s, want %s", label, reason(r), want)
		}
		if r.State.Revision != before.Revision || *r.State.Resume != *before.Resume {
			t.Fatalf("%s changed state: %+v", label, r.State)
		}
	}
	// Each is rejected by the sequence rule or the position rule: a tick at or before the last accepted position, plus a seek (exempt from the position rule, so only its sequence rejects it).
	check("seq 2 ahead in position", contracts.ProgressEvent{Sequence: 2, Kind: tick, PositionMs: 40_000}, "stale")
	check("old seek, lower sequence", contracts.ProgressEvent{Sequence: 2, Kind: seek, PositionMs: 5_000, FromPositionMs: &from}, "stale")
	check("seq 2 behind in position", contracts.ProgressEvent{Sequence: 2, Kind: tick, PositionMs: 20_000}, "stale")
	check("replay 3", contracts.ProgressEvent{Sequence: 3, Kind: tick, PositionMs: 20_000}, "duplicate")
	check("replay 1", contracts.ProgressEvent{Sequence: 1, Kind: tick, PositionMs: 20_000}, "duplicate")
}

func TestReplayedBatchInAnyOrder(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	var got []string
	for _, seq := range []int64{3, 1, 2, 2, 3, 1} {
		got = append(got, reason(e.play(s, tick, seq, seq*10_000)))
	}
	want := []string{"accepted", "stale", "stale", "stale", "duplicate", "stale"}
	if !slices.Equal(got, want) {
		t.Fatalf("reasons = %v, want %v", got, want)
	}
	if p := e.get().Resume.PositionMs; p != 30_000 {
		t.Fatalf("resume = %d, want sequence 3's 30000", p)
	}
	if n := e.count(`SELECT COUNT(*) FROM userdata_events`); n != 6 {
		t.Fatalf("%d event rows, want 6", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM userdata_events WHERE accepted = 1`); n != 1 {
		t.Fatalf("%d accepted rows, want 1", n)
	}
}

func TestBackwardsTickIsStale(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	e.play(s, tick, 1, 20_000)
	if r := e.play(s, tick, 2, 5_000); reason(r) != "stale" {
		t.Fatalf("backwards tick: %s", reason(r))
	}
	from := int64(20_000)
	r, err := e.st.Apply(context.Background(), "p1", s, contracts.ProgressEvent{Sequence: 3, Kind: seek, PositionMs: 5_000, FromPositionMs: &from})
	if err != nil || !r.Accepted || r.State.Resume.PositionMs != 5_000 {
		t.Fatalf("seek back: %+v, %v", r, err)
	}
}

func TestSeekOverIntervalDoesNotCount(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	e.play(s, tick, 1, 0)
	e.play(s, tick, 2, 10_000)
	from := int64(10_000)
	if _, err := e.st.Apply(context.Background(), "p1", s, contracts.ProgressEvent{Sequence: 3, Kind: seek, PositionMs: 80_000, FromPositionMs: &from}); err != nil {
		t.Fatal(err)
	}
	r := e.play(s, tick, 4, 90_000)
	if c := e.covered(); c != 20_000 || r.State.Watched != nil {
		t.Fatalf("covered = %d, watched %v", c, r.State.Watched)
	}
}

func TestStopCountsLikePause(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	e.play(s, tick, 1, 0)
	e.play(s, tick, 2, 10_000)
	e.play(s, stop, 3, 18_000)
	if c := e.covered(); c != 18_000 {
		t.Fatalf("covered = %d", c)
	}
	e.play(sess("s2", "r1"), tick, 1, 0)
	e.play(sess("s2", "r1"), pause, 2, 5_000)
	if c := e.covered(); c != 18_000 {
		t.Fatalf("covered = %d (pause in a second session over seen ground)", c)
	}
}

func TestWatchedAt90PercentCoverage(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	seq := e.ticks(s, 1, 0, 80_000)
	if w := e.get().Watched; w != nil {
		t.Fatalf("watched early: %+v", w)
	}
	e.advance(time.Minute)
	r := e.play(s, tick, seq, 90_000)
	w := r.State.Watched
	if w == nil || !w.Value || w.Origin != contracts.WatchedAuto || !w.At.Equal(e.clock) || *r.State.PlayCount != 1 {
		t.Fatalf("state = %+v", r.State)
	}
	e.advance(time.Minute)
	r = e.play(s, tick, seq+1, 100_000)
	if *r.State.PlayCount != 1 || !r.State.Watched.At.Before(e.clock) {
		t.Fatalf("watched changed again: %+v", r.State)
	}
}

func TestReplayDoesNotDoubleCount(t *testing.T) {
	e := newEnv(t)
	e.ticks(sess("s1", "r1"), 1, 0, 50_000)
	e.ticks(sess("s2", "r1"), 1, 0, 50_000)
	if c := e.covered(); c != 50_000 || e.get().Watched != nil {
		t.Fatalf("covered = %d", c)
	}
}

func TestImplausibleJumpAddsNothing(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	e.play(s, tick, 1, 0)
	r := e.play(s, tick, 2, 60_000)
	if c := e.covered(); c != 0 || r.State.Resume.PositionMs != 60_000 {
		t.Fatalf("covered = %d, resume %+v", c, r.State.Resume)
	}
}

func TestUnknownDuration(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	s.DurationMs = 0
	e.advance(time.Second)
	e.play(s, tick, 1, 0)
	r := e.play(s, tick, 2, 10_000)
	if !r.Accepted || r.State.Watched != nil || r.State.Resume != nil || r.State.LastPlayedAt == nil ||
		!r.State.LastPlayedAt.Equal(e.clock) || e.covered() != 10_000 {
		t.Fatalf("state = %+v, covered %d", r.State, e.covered())
	}
}

func TestPositionPastDurationIsClamped(t *testing.T) {
	e := newEnv(t)
	r := e.play(sess("s1", "r1"), tick, 1, 100_400)
	if p := r.State.Resume.PositionMs; p != 100_000 {
		t.Fatalf("resume = %d", p)
	}
	if n := e.count(`SELECT COUNT(*) FROM userdata_events WHERE position_ms = 100000`); n != 1 {
		t.Fatal("event row not stored clamped")
	}
}

func TestInvalidEvents(t *testing.T) {
	neg := int64(-1)
	zero := int64(0)
	base := contracts.ProgressEvent{Sequence: 1, Kind: tick, PositionMs: 1000}
	with := func(f func(*Session, *contracts.ProgressEvent)) func() (Session, contracts.ProgressEvent) {
		return func() (Session, contracts.ProgressEvent) {
			s, ev := sess("s1", "r1"), base
			f(&s, &ev)
			return s, ev
		}
	}
	cases := map[string]func() (Session, contracts.ProgressEvent){
		"no session":        with(func(s *Session, _ *contracts.ProgressEvent) { s.ID = "" }),
		"no item":           with(func(s *Session, _ *contracts.ProgressEvent) { s.ItemID = "" }),
		"no version":        with(func(s *Session, _ *contracts.ProgressEvent) { s.VersionID = "" }),
		"no source":         with(func(s *Session, _ *contracts.ProgressEvent) { s.SourceRevision = "" }),
		"bad kind":          with(func(_ *Session, e *contracts.ProgressEvent) { e.Kind = "play" }),
		"negative seq":      with(func(_ *Session, e *contracts.ProgressEvent) { e.Sequence = -1 }),
		"negative pos":      with(func(_ *Session, e *contracts.ProgressEvent) { e.PositionMs = -1 }),
		"negative dur":      with(func(s *Session, _ *contracts.ProgressEvent) { s.DurationMs = -1 }),
		"negative from":     with(func(_ *Session, e *contracts.ProgressEvent) { e.Kind, e.FromPositionMs = seek, &neg }),
		"seek without from": with(func(_ *Session, e *contracts.ProgressEvent) { e.Kind = seek }),
		"from on tick":      with(func(_ *Session, e *contracts.ProgressEvent) { e.FromPositionMs = &zero }),
	}
	e := newEnv(t)
	for name, mk := range cases {
		s, ev := mk()
		if _, err := e.st.Apply(context.Background(), "p1", s, ev); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if n := e.count(`SELECT COUNT(*) FROM userdata_events`) + e.count(`SELECT COUNT(*) FROM userdata_state`); n != 0 {
		t.Fatalf("%d rows written", n)
	}
}

func TestTwoSessionsLatestWins(t *testing.T) {
	e := newEnv(t)
	a, b := sess("a", "r1"), sess("b", "r1")
	e.play(a, tick, 1, 30_000)
	e.advance(time.Minute)
	e.play(b, tick, 1, 5_000)
	e.play(b, tick, 2, 10_000)
	st := e.get()
	if st.Resume.PositionMs != 10_000 || !st.LastPlayedAt.Equal(e.clock) {
		t.Fatalf("state = %+v", st)
	}
	if n := e.count(`SELECT COUNT(DISTINCT session_id) FROM userdata_events`); n != 2 {
		t.Fatalf("%d sessions in events", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM userdata_events`); n != 3 {
		t.Fatalf("%d event rows", n)
	}
}

func TestSourceChangeStartsEmptyCoverage(t *testing.T) {
	e := newEnv(t)
	e.ticks(sess("a", "r1"), 1, 0, 80_000)
	if e.get().Watched != nil {
		t.Fatal("watched early")
	}
	b := sess("b", "r2")
	e.play(b, tick, 1, 80_000)
	e.ticks(b, 2, 90_000, 100_000)
	st := e.get()
	if c := e.covered(); c != 20_000 || st.Watched != nil || st.Resume.SourceRevision != "r2" {
		t.Fatalf("covered %d, state %+v", c, st)
	}
}

func TestTwoSourcesKeepOwnCoverage(t *testing.T) {
	e := newEnv(t)
	a, b := sess("a", "r1"), sess("b", "r2")
	var seqA, seqB int64 = 1, 1
	for p := int64(0); p <= 50_000; p += 10_000 {
		e.play(a, tick, seqA, p)
		seqA++
		e.play(b, tick, seqB, p)
		seqB++
	}
	if c := e.covered(); c != 50_000 || e.get().Watched != nil {
		t.Fatalf("covered = %d", c)
	}
	e.ticks(a, seqA, 60_000, 90_000)
	st := e.get()
	if st.Watched == nil || *st.PlayCount != 1 {
		t.Fatalf("not watched once: %+v", st)
	}
}

func TestNoAutoWatchedWithoutBoundaries(t *testing.T) {
	e := newEnv(t)
	s := sess("s1", "r1")
	s.NoAutoWatched = true
	e.ticks(s, 1, 0, 100_000)
	st := e.get()
	if st.Watched != nil || st.Resume == nil || e.covered() != 100_000 {
		t.Fatalf("state = %+v, covered %d", st, e.covered())
	}
}

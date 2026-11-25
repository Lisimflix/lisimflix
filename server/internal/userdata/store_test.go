package userdata

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

func TestStatesUnknownItem(t *testing.T) {
	e := newEnv(t)
	st, err := getState(e.st, context.Background(), "p1", "item")
	if err != nil {
		t.Fatal(err)
	}
	if st.ItemID != "item" || st.Revision != 0 || st.Resume != nil || st.Watched != nil ||
		st.PlayCount != nil || st.LastPlayedAt != nil || st.HiddenFromContinueWatching {
		t.Fatalf("not the zero state: %+v", st)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.db.Migrate(ctx, []storage.Source{storage.Core(), Migrations()}, time.Now); err != nil {
		t.Fatal(err)
	}
	rows, err := e.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'userdata_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if want := []string{"userdata_events", "userdata_intervals", "userdata_state"}; !slices.Equal(got, want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
}

func TestProfilesAreSeparate(t *testing.T) {
	e := newEnv(t)
	e.play(sess("s1", r1), contracts.ProgressTick, 1, 10_000)
	if p1, err := getState(e.st, context.Background(), "p1", "item"); err != nil || p1.Resume == nil {
		t.Fatalf("p1 has no state: %+v, %v", p1, err)
	}
	st, err := getState(e.st, context.Background(), "p2", "item")
	if err != nil {
		t.Fatal(err)
	}
	if st.Revision != 0 || st.Resume != nil {
		t.Fatalf("p2 is not the zero state: %+v", st)
	}
}

func TestBump(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	step := func(expected int64) (int64, error) {
		tx, err := e.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		rev, err := bump(ctx, tx, "p1", "item", expected)
		if err == nil {
			err = tx.Commit()
		}
		return rev, err
	}
	for i, expected := range []int64{0, 1} {
		if rev, err := step(expected); err != nil || rev != int64(i+1) {
			t.Fatalf("bump from %d = %d, %v", expected, rev, err)
		}
	}
	if st, _ := getState(e.st, ctx, "p1", "item"); st.Revision != 2 {
		t.Fatalf("revision = %d, want 2", st.Revision)
	}
	for _, stale := range []int64{0, 1, 5} {
		if _, err := step(stale); !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("stale %d: err = %v, want ErrRevisionConflict", stale, err)
		}
	}
}

func TestBumpFromProgressOnlyRow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.play(sess("s1", r1), contracts.ProgressTick, 1, 10_000) // creates the row at revision 0
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if rev, err := bump(ctx, tx, "p1", "item", 0); err != nil || rev != 1 {
		t.Fatalf("bump = %d, %v", rev, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM userdata_state WHERE profile_id = 'p1' AND item_id = 'item'`); n != 1 {
		t.Fatalf("%d state rows, want 1", n)
	}
	if st, _ := getState(e.st, ctx, "p1", "item"); st.Revision != 1 {
		t.Fatalf("revision = %d, want 1", st.Revision)
	}
}

func TestEmptyProfileIDIsInvalid(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ev := contracts.ProgressEvent{Sequence: 1, Kind: contracts.ProgressTick, PositionMs: 10_000}
	checks := map[string]error{}
	_, checks["Apply"] = e.st.Apply(ctx, "", sess("s1", r1), ev)
	_, checks["SetWatched"] = e.st.SetWatched(ctx, "", "item", true, 0)
	_, checks["Hide"] = e.st.Hide(ctx, "", "item", 0)
	_, checks["States"] = e.st.States(ctx, "", []string{"item"})
	_, checks["SetWatched item"] = e.st.SetWatched(ctx, "p1", "", true, 0)
	_, checks["Hide item"] = e.st.Hide(ctx, "p1", "", 0)
	for name, err := range checks {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if n := e.count(`SELECT (SELECT COUNT(*) FROM userdata_state) + (SELECT COUNT(*) FROM userdata_events)`); n != 0 {
		t.Fatalf("%d rows written", n)
	}
}

func TestMarkReturnsStateFromItsTransaction(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.st.afterCommit = func() { e.db.Close() }
	st, err := e.st.SetWatched(ctx, "p1", "item", true, 0)
	if err != nil || st.Revision != 1 || st.Watched == nil || st.Watched.Origin != contracts.WatchedManual {
		t.Fatalf("SetWatched = %+v, %v", st, err)
	}

	e = newEnv(t)
	e.st.afterCommit = func() { e.db.Close() }
	res, err := e.st.Apply(ctx, "p1", sess("s1", r1), contracts.ProgressEvent{Sequence: 1, Kind: contracts.ProgressTick, PositionMs: 10_000})
	if err != nil || !res.Accepted || res.State.Resume == nil || res.State.Resume.PositionMs != 10_000 {
		t.Fatalf("Apply = %+v, %v", res, err)
	}
}

func TestStatesBatch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.playItem("A", "sA", 40_000)
	if _, err := e.setWatched("B", true, 0); err != nil {
		t.Fatal(err)
	}
	e.playItemFor("p2", "C", "sC", 20_000)
	got, err := e.st.States(ctx, "p1", []string{"A", "B", "C"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got["A"].Resume == nil || got["A"].Resume.PositionMs != 40_000 || got["A"].ItemID != "A" ||
		got["B"].Watched == nil || got["B"].Revision != 1 {
		t.Fatalf("states = %+v", got)
	}
	if c := got["C"]; c.ItemID != "C" || c.Revision != 0 || c.Resume != nil || c.Watched != nil || c.LastPlayedAt != nil {
		t.Fatalf("C is not the zero state: %+v", c)
	}
	if empty, err := e.st.States(ctx, "p1", nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty = %v, %v", empty, err)
	}
	ids := make([]string, 200)
	for i := range ids {
		ids[i] = "id-" + strconv.Itoa(i)
	}
	ids[0] = "A"
	if big, err := e.st.States(ctx, "p1", ids); err != nil || len(big) != 200 || big["A"].Resume == nil {
		t.Fatalf("200 ids: %d states, %v", len(big), err)
	}
}

func TestContinueWatchingPages(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range 120 {
		e.playItem(fmt.Sprintf("item-%03d", i), fmt.Sprintf("s-%03d", i), 40_000)
		if i%3 == 0 {
			e.advance(time.Second) // groups of three share lastPlayedAt
		}
	}
	seen := map[string]int{}
	var order []contracts.UserItemState
	var after *Cursor
	for page := 0; ; page++ {
		items, next, err := e.st.ContinueWatching(ctx, "p1", 50, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range items {
			seen[st.ItemID]++
			order = append(order, st)
		}
		if page == 0 {
			e.advance(time.Minute)
			e.play(Session{ID: "late", ItemID: items[0].ItemID, VersionID: "v1", SourceRevision: r1, DurationMs: 100_000}, tick, 0, 45_000)
		}
		if next == nil {
			if len(items) > 50 || page != 2 {
				t.Fatalf("last page %d has %d items", page, len(items))
			}
			break
		}
		after = next
	}
	if len(seen) != 120 {
		t.Fatalf("%d distinct items, want 120", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s listed %d times", id, n)
		}
	}
	for i := 1; i < len(order); i++ {
		a, b := order[i-1], order[i]
		if a.LastPlayedAt.Before(*b.LastPlayedAt) || (a.LastPlayedAt.Equal(*b.LastPlayedAt) && a.ItemID > b.ItemID) {
			t.Fatalf("out of order at %d: %s then %s", i, a.ItemID, b.ItemID)
		}
	}
}

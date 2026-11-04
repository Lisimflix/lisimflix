package userdata

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

func TestGetUnknownItem(t *testing.T) {
	e := newEnv(t)
	st, err := e.st.Get(context.Background(), "p1", "item")
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
	t.Skip("needs Task 3's Apply helper to write state for p1")
	e := newEnv(t)
	st, err := e.st.Get(context.Background(), "p2", "item")
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
	if st, _ := e.st.Get(ctx, "p1", "item"); st.Revision != 2 {
		t.Fatalf("revision = %d, want 2", st.Revision)
	}
	for _, stale := range []int64{0, 1, 5} {
		if _, err := step(stale); err != ErrRevisionConflict {
			t.Fatalf("stale %d: err = %v, want ErrRevisionConflict", stale, err)
		}
	}
}

package userdata

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
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
	e := newEnv(t)
	e.play(sess("s1", "r1"), contracts.ProgressTick, 1, 10_000)
	if p1, err := e.st.Get(context.Background(), "p1", "item"); err != nil || p1.Resume == nil {
		t.Fatalf("p1 has no state: %+v, %v", p1, err)
	}
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
		if _, err := step(stale); !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("stale %d: err = %v, want ErrRevisionConflict", stale, err)
		}
	}
}

func TestBumpFromProgressOnlyRow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.play(sess("s1", "r1"), contracts.ProgressTick, 1, 10_000) // creates the row at revision 0
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
	if st, _ := e.st.Get(ctx, "p1", "item"); st.Revision != 1 {
		t.Fatalf("revision = %d, want 1", st.Revision)
	}
}

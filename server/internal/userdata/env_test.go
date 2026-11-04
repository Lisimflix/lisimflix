package userdata

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

// env is a migrated database with the progress store and a settable clock.
type env struct {
	t     *testing.T
	db    *storage.DB
	st    *Store
	clock time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "lisimflix.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx, []storage.Source{storage.Core(), Migrations()}, time.Now); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, clock: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	e.st = NewStore(db)
	e.st.now = func() time.Time { return e.clock }
	return e
}

func (e *env) advance(d time.Duration) { e.clock = e.clock.Add(d) }

// sess is a session on item "item", version v1, with a 100 s duration.
func sess(id, rev string) Session {
	return Session{ID: id, ItemID: "item", VersionID: "v1", SourceRevision: rev, DurationMs: 100_000}
}

// play applies one event for profile p1 and fails the test on an error.
func (e *env) play(s Session, kind string, seq, posMs int64) contracts.ProgressResult {
	e.t.Helper()
	res, err := e.st.Apply(context.Background(), "p1", s, contracts.ProgressEvent{Sequence: seq, Kind: kind, PositionMs: posMs})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

// covered reads the state's covered_ms for profile p1 and "item".
func (e *env) covered() int64 {
	e.t.Helper()
	var n int64
	if err := e.db.QueryRow(`SELECT covered_ms FROM userdata_state WHERE profile_id = 'p1' AND item_id = 'item'`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) count(query string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(query).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

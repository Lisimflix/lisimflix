package userdata

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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

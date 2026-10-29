package metadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

// env is a real catalogue (LIB-2a) over a temporary media tree, with the
// metadata store and a settable clock.
type env struct {
	t     *testing.T
	dir   string // the media root
	media *os.Root
	db    *storage.DB
	lib   *library.Store
	scan  *library.Scanner
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
	if _, err := db.Migrate(ctx, []storage.Source{storage.Core(), library.Migrations(), Migrations()}, time.Now); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, dir: t.TempDir(), db: db, clock: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	if e.media, err = os.OpenRoot(e.dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.media.Close() })
	e.lib = library.NewStore(db)
	e.scan = &library.Scanner{Store: e.lib, Media: e.media}
	e.st = NewStore(db)
	e.st.now = func() time.Time { return e.clock }
	e.st.jitter = func(d time.Duration) time.Duration { return d } // exact delays in tests
	return e
}

// write creates a media file (or an NFO) under the media root.
func (e *env) write(rel, body string) {
	e.t.Helper()
	p := filepath.Join(e.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// library creates a library over folder and scans it.
func (e *env) library(typ, folder string) {
	e.t.Helper()
	ctx := context.Background()
	lib, err := e.lib.CreateLibrary(ctx, folder, typ, []string{folder})
	if err != nil {
		e.t.Fatal(err)
	}
	if run, err := e.scan.ScanRoot(ctx, lib.Roots[0].ID); err != nil || run.Status != library.RunCompleted {
		e.t.Fatalf("scan %+v %v", run, err)
	}
}

// item returns the ID of the movie or show with the given parsed title.
func (e *env) item(title string) string {
	e.t.Helper()
	var id string
	if err := e.db.QueryRow(`SELECT id FROM library_items WHERE title = ? AND kind IN ('movie', 'show')`, title).Scan(&id); err != nil {
		e.t.Fatalf("item %q: %v", title, err)
	}
	return id
}

func (e *env) get(id string) contracts.ItemMetadata {
	e.t.Helper()
	md, err := e.st.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return md
}

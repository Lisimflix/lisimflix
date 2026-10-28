package library

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

// clock is a settable test time.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "lisimflix.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := &clock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	if _, err := db.Migrate(ctx, []storage.Source{storage.Core(), Migrations()}, c.now); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db)
	s.now = c.now
	return s, c
}

func TestCreateLibraryChecksFolders(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies", "./More Movies/"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Library(ctx, lib.ID)
	if err != nil || got.Name != "Movies" || len(got.Roots) != 2 || got.Roots[0].Path != "More Movies" || got.Roots[0].State != RootAvailable {
		t.Fatalf("Library = %+v, %v", got, err)
	}
	for name, roots := range map[string][]string{
		"escape":           {"../etc"},
		"absolute":         {"/media/Movies"},
		"same folder":      {"Movies"},
		"inside another":   {"Movies/Action"},
		"around another":   {"."},
		"overlap in input": {"TV", "TV/Anime"},
		"none":             {},
	} {
		if _, err := s.CreateLibrary(ctx, "X", "tv", roots); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := s.CreateLibrary(ctx, "X", "music", []string{"Music"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("music type: %v", err)
	}
	if _, err := s.CreateLibrary(ctx, " ", "tv", []string{"TV"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("blank name: %v", err)
	}
	if _, err := s.Library(ctx, "lib-nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown library: %v", err)
	}
}

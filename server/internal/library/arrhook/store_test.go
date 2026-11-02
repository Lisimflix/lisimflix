package arrhook

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

// newStore returns a store on a fresh database with a TV library over
// "TV" and a movie library over "Movies"; it returns their root IDs too.
func newStore(t *testing.T) (*Store, string, string) {
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
	ls := library.NewStore(db)
	tv, err := ls.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	if err != nil {
		t.Fatal(err)
	}
	mv, err := ls.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(db), tv.Roots[0].ID, mv.Roots[0].ID
}

func TestAddAndAuthenticate(t *testing.T) {
	st, tvRoot, _ := newStore(t)
	ctx := context.Background()
	src, token, err := st.Add(ctx, "sonarr", Sonarr, []string{"TV"}, []Mapping{{`/tv/`, "./TV"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "lfh_") || len(token) < 30 || src.Roots[0].ID != tvRoot || src.Mappings[0] != (Mapping{"/tv", "TV"}) {
		t.Fatalf("Add = %+v, %q", src, token)
	}
	got, err := st.Authenticate(ctx, src.ID, token)
	if err != nil || got.Name != "sonarr" || len(got.Roots) != 1 || len(got.Mappings) != 1 {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}
	for name, c := range map[string][2]string{
		"wrong token":    {src.ID, token + "x"},
		"unknown source": {"src-nope", token},
		"empty":          {"", ""},
	} {
		if _, err := st.Authenticate(ctx, c[0], c[1]); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The token isn't stored, only its hash.
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM arrhook_sources WHERE CAST(token_hash AS TEXT) LIKE '%' || ? || '%'`, token[4:]).Scan(&n)
	if n != 0 {
		t.Fatal("token stored in clear")
	}
	if err := st.Revoke(ctx, "sonarr"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Authenticate(ctx, src.ID, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked: %v", err)
	}
	if err := st.Revoke(ctx, "sonarr"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke twice: %v", err)
	}
}

func TestAddChecksInput(t *testing.T) {
	st, _, _ := newStore(t)
	ctx := context.Background()
	ok := []Mapping{{"/tv", "TV"}}
	if _, _, err := st.Add(ctx, "sonarr", Sonarr, []string{"TV"}, ok); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		name, kind string
		roots      []string
		maps       []Mapping
	}{
		"duplicate name":  {"sonarr", Sonarr, []string{"TV"}, ok},
		"bad name":        {"Sonarr Main", Sonarr, []string{"TV"}, ok},
		"bad kind":        {"lidarr", "lidarr", []string{"TV"}, ok},
		"no folders":      {"s2", Sonarr, nil, ok},
		"not a library":   {"s2", Sonarr, []string{"Music"}, ok},
		"no mappings":     {"s2", Sonarr, []string{"TV"}, nil},
		"relative remote": {"s2", Sonarr, []string{"TV"}, []Mapping{{"tv", "TV"}}},
		"local escapes":   {"s2", Sonarr, []string{"TV"}, []Mapping{{"/tv", "../etc"}}},
		"absolute local":  {"s2", Sonarr, []string{"TV"}, []Mapping{{"/tv", "/media/TV"}}},
	} {
		if _, _, err := st.Add(ctx, c.name, c.kind, c.roots, c.maps); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func TestReceiptsAreBounded(t *testing.T) {
	st, tvRoot, _ := newStore(t)
	ctx := context.Background()
	src, _, _ := st.Add(ctx, "sonarr", Sonarr, []string{"TV"}, []Mapping{{"/tv", "TV"}})
	at := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	for i := range keepReceipts + 5 {
		r := Receipt{ID: "rcpt-" + string(rune('a'+i%26)) + time.Duration(i).String(), SourceID: src.ID,
			At: at.Add(time.Duration(i) * time.Second), Event: "Download", App: "Sonarr/4.0.20.3014", Result: ResultQueued,
			Scopes: []Scope{{tvRoot, "Show"}}}
		if err := st.record(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := st.Receipts(ctx, src.ID, 1000)
	if err != nil || len(rs) != keepReceipts {
		t.Fatalf("%d receipts, %v", len(rs), err)
	}
	if !rs[0].At.Equal(at.Add((keepReceipts+4)*time.Second)) || rs[0].Scopes[0] != (Scope{tvRoot, "Show"}) {
		t.Fatalf("newest %+v", rs[0])
	}
}

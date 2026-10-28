package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// Before matching, an item's metadata is its parsed name: revision 1,
// unmatched, no provenance.
func TestGetBeforeMatching(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	md, err := e.st.Get(context.Background(), e.item("Heat"))
	if err != nil {
		t.Fatal(err)
	}
	f := md.Fields
	if md.Revision != 1 || md.MatchStatus != contracts.MatchUnmatched || *f.Title.Value != "Heat" || f.Title.Source != nil ||
		*f.Year.Value != 1995 || f.Overview.Value != nil || len(f.Genres.Value) != 0 || len(md.ExternalIDs) != 0 {
		t.Fatalf("metadata %+v", md)
	}
	if _, err := e.st.Get(context.Background(), "mov-nope"); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("unknown item: %v", err)
	}
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func ptr[T any](v T) *T { return &v }

// A manual edit is "user" provenance and locked; edits are compare-and-set
// on the revision, so a stale form can't overwrite a newer change.
func TestEditLocksAndChecksRevision(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	ctx := context.Background()
	id := e.item("Heat")

	md, err := e.st.Edit(ctx, id, 1, []Edit{{Field: FieldTitle, Value: raw("Heat (Director's Edition)")}, {Field: FieldYear, Lock: ptr(true)}})
	if err != nil {
		t.Fatal(err)
	}
	f := md.Fields
	if md.Revision != 2 || *f.Title.Value != "Heat (Director's Edition)" || !f.Title.Locked || f.Title.Source.Provider != SourceUser ||
		!f.Year.Locked || *f.Year.Value != 1995 || f.Year.Source != nil {
		t.Fatalf("after edit %+v", md)
	}
	if _, err := e.st.Edit(ctx, id, 1, []Edit{{Field: FieldOverview, Value: raw("stale")}}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	// Unlocking keeps the value but lets the next match replace it.
	md, err = e.st.Edit(ctx, id, 2, []Edit{{Field: FieldTitle, Lock: ptr(false)}})
	if err != nil || md.Fields.Title.Locked || *md.Fields.Title.Value != "Heat (Director's Edition)" {
		t.Fatalf("unlock: %+v %v", md, err)
	}
	if _, err := e.st.Edit(ctx, "mov-nope", 1, []Edit{{Field: FieldTitle, Value: raw("x")}}); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("unknown item: %v", err)
	}
}

func TestEditValidates(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	for name, ed := range map[string]Edit{
		"empty title":     {Field: FieldTitle, Value: raw("  ")},
		"null title":      {Field: FieldTitle, Value: raw(nil)},
		"long title":      {Field: FieldTitle, Value: raw(string(make([]byte, 513)))},
		"year as text":    {Field: FieldYear, Value: raw("1995")},
		"year too early":  {Field: FieldYear, Value: raw(1066)},
		"bad region":      {Field: FieldContentRating, Value: raw(map[string]string{"region": "aus", "value": "M"})},
		"too many genres": {Field: FieldGenres, Value: raw(strings.Split("a b c d e f g h i j k l m n o p q r s t u", " "))},
		"empty genre":     {Field: FieldGenres, Value: raw([]string{"Drama", " "})},
		"unknown field":   {Field: "director", Value: raw("x")},
	} {
		if _, err := e.st.Edit(context.Background(), id, 1, []Edit{ed}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if _, err := e.st.Edit(context.Background(), id, 1, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("no edits: %v, want ErrInvalid", err)
	}
	// Clearing an optional field is fine.
	if _, err := e.st.Edit(context.Background(), id, 1, []Edit{{Field: FieldOverview, Value: raw(nil)}}); err != nil {
		t.Fatal(err)
	}
}

// Fix-match records the admin's choice as a locked "user" ID, clears the
// review candidates and queues the item now.
func TestFixMatchLocksTheChosenID(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Dune/Dune.mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Dune")
	// An item in review, with a candidate waiting.
	e.db.Exec(`INSERT INTO metadata_items(item_id, revision, match_status, updated_at) VALUES(?, 3, 'review', 0)`, id)
	e.db.Exec(`INSERT INTO metadata_candidates(item_id, provider, provider_id, title, year, score) VALUES(?, 'tmdb', '438631', 'Dune', 2021, 0.95)`, id)
	md, err := e.st.FixMatch(context.Background(), id, 3, "tmdb", "841")
	if err != nil {
		t.Fatal(err)
	}
	if md.Revision != 4 || md.MatchStatus != contracts.MatchUnmatched || len(md.ExternalIDs) != 1 || md.ExternalIDs[0].ID != "841" ||
		!md.ExternalIDs[0].Locked || md.ExternalIDs[0].Source.Provider != SourceUser {
		t.Fatalf("fix-match %+v", md)
	}
	var cands int
	var due *int64
	e.db.QueryRow(`SELECT COUNT(*) FROM metadata_candidates WHERE item_id = ?`, id).Scan(&cands)
	e.db.QueryRow(`SELECT next_attempt_at FROM metadata_items WHERE item_id = ?`, id).Scan(&due)
	if cands != 0 || due == nil || *due != e.clock.UnixMilli() {
		t.Fatalf("candidates %d, due %v: want none, queued now", cands, due)
	}
	if _, err := e.st.FixMatch(context.Background(), id, 1, "tmdb", "841"); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale fix-match: %v", err)
	}
	if _, err := e.st.FixMatch(context.Background(), id, md.Revision, "TMDB!", "841"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad namespace: %v", err)
	}
}

func TestEnqueueAndPause(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.write("Movies/Dune/Dune.mkv", "x")
	e.library("movies", "Movies")
	ctx := context.Background()
	if n, err := e.st.Enqueue(ctx, 1); err != nil || n != 1 {
		t.Fatalf("first batch %d %v", n, err)
	}
	if n, err := e.st.Enqueue(ctx, 10); err != nil || n != 1 {
		t.Fatalf("second batch %d %v", n, err)
	}
	if n, err := e.st.Enqueue(ctx, 10); err != nil || n != 0 {
		t.Fatalf("nothing left %d %v", n, err)
	}
	if p, _, _ := e.st.Paused(ctx, "tmdb"); p {
		t.Fatal("paused before Pause")
	}
	if err := e.st.Pause(ctx, "tmdb", "bad key"); err != nil {
		t.Fatal(err)
	}
	if p, why, err := e.st.Paused(ctx, "tmdb"); err != nil || !p || why != "bad key" {
		t.Fatalf("paused %v %q %v", p, why, err)
	}
	e.st.Resume(ctx, "tmdb")
	if p, _, _ := e.st.Paused(ctx, "tmdb"); p {
		t.Fatal("still paused after Resume")
	}
}

// Clearing a field with null is an explicit, locked "user" choice: the parsed
// value doesn't come back. A lock-only row still shows the parsed value.
func TestEditClearsParsedFields(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	md, err := e.st.Edit(context.Background(), id, 1, []Edit{{Field: FieldYear, Value: raw(nil)}, {Field: FieldSortTitle, Value: raw(nil)}})
	if err != nil {
		t.Fatal(err)
	}
	f := md.Fields
	if f.Year.Value != nil || f.Year.Source == nil || f.Year.Source.Provider != SourceUser || !f.Year.Locked ||
		f.SortTitle.Value != nil || f.SortTitle.Source == nil || f.SortTitle.Source.Provider != SourceUser || !f.SortTitle.Locked {
		t.Fatalf("after clear %+v", f)
	}
	if got := e.get(id).Fields; got.Year.Value != nil || got.SortTitle.Value != nil {
		t.Fatalf("Get after clear %+v", got)
	}
}

// Fix-match takes only a well-formed ID in a provider namespace: not "user"
// or "nfo" (provenance, not namespaces), not an unknown namespace, and not a
// value its namespace can't hold.
func TestFixMatchValidatesTheID(t *testing.T) {
	e := newEnv(t)
	e.write("Movies/Dune/Dune.mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Dune")
	ctx := context.Background()
	for _, c := range [][2]string{{"user", "1"}, {"nfo", "1"}, {"mystery", "1"}, {"tmdb", "abc"}, {"tmdb", "0841"},
		{"imdb", "841"}, {"imdb", "tt12"}} {
		if _, err := e.st.FixMatch(ctx, id, 1, c[0], c[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("FixMatch(%s, %s) = %v, want ErrInvalid", c[0], c[1], err)
		}
	}
	for _, c := range [][2]string{{"tmdb", "841"}, {"imdb", "tt0087182"}, {"tvdb", "81189"}} {
		if _, err := e.st.FixMatch(ctx, id, e.get(id).Revision, c[0], c[1]); err != nil {
			t.Errorf("FixMatch(%s, %s) = %v", c[0], c[1], err)
		}
	}
}

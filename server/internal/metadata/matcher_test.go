package metadata

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// menv is env plus a matcher over a fake provider.
type menv struct {
	*env
	fake *fakeProvider
	m    *Matcher
}

func newMatchEnv(t *testing.T) *menv {
	e := newEnv(t)
	f := newFake()
	return &menv{env: e, fake: f, m: &Matcher{Store: e.st, Provider: f, Media: e.media}}
}

func (e *menv) match(itemID string) string {
	e.t.Helper()
	out, err := e.m.MatchItem(context.Background(), itemID)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// fakeProvider is an in-memory provider in the "tmdb" namespace that also
// resolves imdb IDs. during runs inside each call when set.
type fakeProvider struct {
	mu      sync.Mutex
	records map[string]Record // by "ns:id"
	results map[string][]Candidate
	err     error
	calls   []string
	during  func()
}

func newFake() *fakeProvider {
	str := func(s string) *string { return &s }
	yr := func(y int) *int { return &y }
	f := &fakeProvider{records: map[string]Record{}, results: map[string][]Candidate{}}
	f.add(Record{ID: "949", Kind: "movie", Title: str("Heat"), OriginalTitle: str("Heat"), Overview: str("A group of high-end professional thieves."),
		Year: yr(1995), ContentRating: &Rating{"AU", "MA15+"}, Genres: []string{"Crime", "Drama"}, ExternalIDs: map[string]string{"imdb": "tt0113277"}})
	f.add(Record{ID: "603", Kind: "movie", Title: str("The Matrix"), Year: yr(1999), ExternalIDs: map[string]string{"imdb": "tt0133093"}})
	f.add(Record{ID: "1396", Kind: "show", Title: str("Breaking Bad"), Year: yr(2008), Genres: []string{"Drama"}})
	f.results["heat"] = []Candidate{{Provider: "tmdb", ID: "949", Kind: "movie", Title: "Heat", Year: 1995},
		{Provider: "tmdb", ID: "1", Kind: "movie", Title: "Heat", Year: 1986}}
	f.results["dune"] = []Candidate{{Provider: "tmdb", ID: "438631", Kind: "movie", Title: "Dune", Year: 2021},
		{Provider: "tmdb", ID: "841", Kind: "movie", Title: "Dune", Year: 1984}}
	f.results["breaking bad"] = []Candidate{{Provider: "tmdb", ID: "1396", Kind: "show", Title: "Breaking Bad", Year: 2008}}
	return f
}

func (f *fakeProvider) add(r Record) {
	f.records["tmdb:"+r.ID] = r
	for ns, id := range r.ExternalIDs {
		f.records[ns+":"+id] = r
	}
}

func (f *fakeProvider) Name() string         { return "tmdb" }
func (f *fakeProvider) Namespaces() []string { return []string{"tmdb", "imdb"} }

func (f *fakeProvider) call(what string) error {
	f.mu.Lock()
	f.calls = append(f.calls, what)
	during, err := f.during, f.err
	f.mu.Unlock()
	if during != nil {
		during()
	}
	return err
}

func (f *fakeProvider) Search(_ context.Context, q Query) ([]Candidate, error) {
	if err := f.call("search " + Normalize(q.Title)); err != nil {
		return nil, err
	}
	return f.results[Normalize(q.Title)], nil
}

func (f *fakeProvider) Lookup(_ context.Context, kind, ns, id string) (Record, error) {
	if err := f.call("lookup " + ns + ":" + id); err != nil {
		return Record{}, err
	}
	r, ok := f.records[ns+":"+id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return r, nil
}

// Fix-match after review: the chosen ID is used and the candidates go.
func TestFixMatchAfterReview(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Dune/Dune.mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Dune")
	if out := e.match(id); out != OutcomeReview {
		t.Fatalf("ambiguous remake: %s", out)
	}
	var cands int
	e.db.QueryRow(`SELECT COUNT(*) FROM metadata_candidates WHERE item_id = ?`, id).Scan(&cands)
	if cands != 2 {
		t.Fatalf("%d candidates", cands)
	}
	if _, err := e.st.FixMatch(context.Background(), id, e.get(id).Revision, "tmdb", "841"); err != nil {
		t.Fatal(err)
	}
	e.db.QueryRow(`SELECT COUNT(*) FROM metadata_candidates WHERE item_id = ?`, id).Scan(&cands)
	if cands != 0 {
		t.Fatal("candidates kept")
	}
}

func extID(md contracts.ItemMetadata, ns string) (contracts.MetadataExternalID, bool) {
	for _, x := range md.ExternalIDs {
		if x.Namespace == ns {
			return x, true
		}
	}
	return contracts.MetadataExternalID{}, false
}

// An ID hint in the folder name (LIB-2a) is validated by a lookup and
// applied: fields with the provider's provenance, and the IDs it reports.
func TestNameHintMatches(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	if out := e.match(id); out != OutcomeMatched {
		t.Fatalf("outcome %s", out)
	}
	md := e.get(id)
	f := md.Fields
	if md.MatchStatus != contracts.MatchMatched || *f.Overview.Value != "A group of high-end professional thieves." ||
		f.Overview.Source.Provider != "tmdb" || *f.Overview.Source.ProviderID != "949" || !slices.Equal(f.Genres.Value, []string{"Crime", "Drama"}) ||
		f.ContentRating.Value.Region != "AU" {
		t.Fatalf("metadata %+v", md)
	}
	if x, ok := extID(md, "imdb"); !ok || x.ID != "tt0113277" || x.Locked {
		t.Fatalf("imdb %+v", x)
	}
	if !slices.Equal(e.fake.calls, []string{"lookup tmdb:949"}) {
		t.Fatalf("calls %v: a valid hint needs no search", e.fake.calls)
	}
}

// A Kodi NFO beside the file supplies the ID when the name has none.
func TestNFOHintMatches(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/The Matrix (1999)/The Matrix (1999).mkv", "x")
	e.write("Movies/The Matrix (1999)/movie.nfo", `<movie><uniqueid type="imdb">tt0133093</uniqueid></movie>`)
	e.library("movies", "Movies")
	if out := e.match(e.item("The Matrix")); out != OutcomeMatched || !slices.Equal(e.fake.calls, []string{"lookup imdb:tt0133093"}) {
		t.Fatalf("outcome %s, calls %v", out, e.fake.calls)
	}
}

// Without hints, a confident search result is applied; a low-confidence
// one never is: it goes to the review queue with its candidates.
func TestSearchMatchesOnlyWhenConfident(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.write("Movies/Dune/Dune.mkv", "x")
	e.write("Movies/Unknown Film (2001)/Unknown Film (2001).mkv", "x")
	e.library("movies", "Movies")
	if out := e.match(e.item("Heat")); out != OutcomeMatched {
		t.Fatalf("Heat: %s", out)
	}
	if out := e.match(e.item("Dune")); out != OutcomeReview || e.get(e.item("Dune")).MatchStatus != contracts.MatchReview {
		t.Fatalf("Dune without a year: %s", out)
	}
	if out := e.match(e.item("Unknown Film")); out != OutcomeReview {
		t.Fatalf("no results: %s", out)
	}
	if md := e.get(e.item("Dune")); md.Fields.Overview.Value != nil {
		t.Fatal("a review item got provider data")
	}
}

// §3.7: refreshing never overwrites a locked field; unlocked ones update.
func TestLockedFieldsSurviveRefresh(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	e.match(id)
	md := e.get(id)
	md, err := e.st.Edit(context.Background(), id, md.Revision, []Edit{{Field: FieldTitle, Value: raw("Heat (1995 cut)")}, {Field: FieldGenres, Lock: ptr(true)}})
	if err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	r := e.fake.records["tmdb:949"]
	r.Overview, r.Genres = str("Updated overview."), []string{"Thriller"}
	e.fake.add(r)
	e.clock = e.clock.Add(time.Hour)
	if out := e.match(id); out != OutcomeMatched {
		t.Fatalf("refresh: %s", out)
	}
	f := e.get(id).Fields
	if *f.Title.Value != "Heat (1995 cut)" || f.Title.Source.Provider != SourceUser || !slices.Equal(f.Genres.Value, []string{"Crime", "Drama"}) ||
		*f.Overview.Value != "Updated overview." {
		t.Fatalf("after refresh %+v", f)
	}
}

// The admin's fix-match ID wins over every hint, and a locked ID is never
// replaced: a record reporting a different ID makes a conflict instead.
func TestLockedIDWinsAndConflictsAreRaised(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-1}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	if _, err := e.st.FixMatch(context.Background(), id, 1, "tmdb", "949"); err != nil {
		t.Fatal(err)
	}
	if out := e.match(id); out != OutcomeMatched || !slices.Equal(e.fake.calls, []string{"lookup tmdb:949"}) {
		t.Fatalf("outcome %s calls %v", out, e.fake.calls)
	}
	// The locked ID keeps the admin's provenance after the match.
	if x, _ := extID(e.get(id), "tmdb"); x.ID != "949" || !x.Locked || x.Source.Provider != SourceUser {
		t.Fatalf("locked tmdb ID after matching: %+v", x)
	}
	// The admin also locks imdb to another title; the record says otherwise.
	tx, _ := e.db.Begin()
	tx.Exec(`UPDATE metadata_external_ids SET value = 'tt9999999', locked = 1 WHERE item_id = ? AND namespace = 'imdb'`, id)
	tx.Commit()
	if out := e.match(id); out != OutcomeConflict {
		t.Fatalf("outcome %s, want conflict", out)
	}
	after := e.get(id)
	if x, _ := extID(after, "imdb"); after.MatchStatus != contracts.MatchConflict || x.ID != "tt9999999" {
		t.Fatalf("conflict didn't keep the lock: %+v", after)
	}
}

// Two hints that disagree (the folder says one TMDB ID, the NFO another)
// are a conflict for the admin, not a guess.
func TestConflictingHints(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.write("Movies/Heat (1995) {tmdb-949}/movie.nfo", `<movie><tmdbid>1</tmdbid></movie>`)
	e.library("movies", "Movies")
	id := e.item("Heat")
	if out := e.match(id); out != OutcomeConflict || len(e.fake.calls) != 0 {
		t.Fatalf("outcome %s calls %v", out, e.fake.calls)
	}
}

// A hint the provider doesn't know falls back to searching.
func TestInvalidHintFallsBackToSearch(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-404404}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	if out := e.match(e.item("Heat")); out != OutcomeMatched ||
		!slices.Equal(e.fake.calls, []string{"lookup tmdb:404404", "search heat", "lookup tmdb:949"}) {
		t.Fatalf("outcome %s calls %v", out, e.fake.calls)
	}
}

// A hint that names a record of another kind (a show's ID on a movie
// folder) isn't applied: matching falls back to searching.
func TestHintOfAnotherKindFallsBack(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-1396}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	if out := e.match(id); out != OutcomeMatched ||
		!slices.Equal(e.fake.calls, []string{"lookup tmdb:1396", "search heat", "lookup tmdb:949"}) {
		t.Fatalf("outcome %s calls %v", out, e.fake.calls)
	}
	if x, _ := extID(e.get(id), "tmdb"); x.ID != "949" {
		t.Fatalf("matched %+v", x)
	}
}

// Compare-and-set: an admin edit made while the provider was being asked
// wins; the provider's result is dropped and the item retried.
func TestEditDuringMatchWins(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	e.fake.during = func() {
		e.fake.during = nil
		if _, err := e.st.Edit(context.Background(), id, 1, []Edit{{Field: FieldOverview, Value: raw("Mine.")}}); err != nil {
			t.Error(err)
		}
	}
	if out := e.match(id); out != OutcomeRetry {
		t.Fatalf("outcome %s, want retry", out)
	}
	md := e.get(id)
	if *md.Fields.Overview.Value != "Mine." || md.MatchStatus != contracts.MatchUnmatched || md.Fields.Genres.Source != nil {
		t.Fatalf("the provider result overwrote the edit: %+v", md)
	}
}

// An outage keeps existing metadata, and retries back off (doubling,
// capped at a day) instead of hammering the provider.
func TestOutageKeepsMetadataAndBacksOff(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	e.match(id)
	before := e.get(id)
	e.fake.err = fmt.Errorf("%w: 503", ErrUnavailable)
	var next []int64
	for range 13 {
		if out := e.match(id); out != OutcomeRetry {
			t.Fatalf("outcome %s", out)
		}
		var at int64
		e.db.QueryRow(`SELECT next_attempt_at FROM metadata_items WHERE item_id = ?`, id).Scan(&at)
		next = append(next, at-e.clock.UnixMilli())
	}
	if next[0] != time.Minute.Milliseconds() || next[1] != 2*time.Minute.Milliseconds() || next[12] != retryCap.Milliseconds() {
		t.Fatalf("backoff %v", next)
	}
	after := e.get(id)
	if *after.Fields.Overview.Value != *before.Fields.Overview.Value || after.MatchStatus != contracts.MatchMatched {
		t.Fatalf("outage changed metadata: %+v", after)
	}
}

// An auth error (a revoked key) pauses the provider: the batch stops, the
// item stays queued, and nothing runs until Resume.
func TestAuthErrorPausesProvider(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.write("Movies/The Matrix (1999)/The Matrix (1999).mkv", "x")
	e.library("movies", "Movies")
	ctx := context.Background()
	if n, err := e.st.Enqueue(ctx, 10); n != 2 || err != nil {
		t.Fatalf("Enqueue = %d, %v", n, err)
	}
	e.fake.err = fmt.Errorf("%w: 401", ErrAuth)
	if b, err := e.m.RunDue(ctx, 10); !errors.Is(err, ErrPaused) || b.Matched != 0 {
		t.Fatalf("RunDue = %+v, %v", b, err)
	}
	if paused, _, _ := e.st.Paused(ctx, "tmdb"); !paused {
		t.Fatal("not paused")
	}
	if len(e.fake.calls) != 1 {
		t.Fatalf("%d calls: a paused provider must not be called again", len(e.fake.calls))
	}
	e.fake.err = nil
	if _, err := e.m.RunDue(ctx, 10); !errors.Is(err, ErrPaused) {
		t.Fatalf("still paused: %v", err)
	}
	e.st.Resume(ctx, "tmdb")
	if b, err := e.m.RunDue(ctx, 10); err != nil || b.Matched != 1 || b.Review != 1 {
		t.Fatalf("after resume %+v %v", b, err)
	}
}

// Work is bounded (SCALE-03): Enqueue and RunDue take a limit, each item is
// its own transaction, and a later batch picks up the rest.
func TestBatchesAreBounded(t *testing.T) {
	e := newMatchEnv(t)
	for i := range 5 {
		e.write(fmt.Sprintf("Movies/Film %d (2001)/Film %d (2001).mkv", i, i), "x")
	}
	e.library("movies", "Movies")
	ctx := context.Background()
	if n, _ := e.st.Enqueue(ctx, 3); n != 3 {
		t.Fatalf("queued %d, want 3", n)
	}
	if n, _ := e.st.Enqueue(ctx, 10); n != 2 {
		t.Fatalf("queued %d more, want 2", n)
	}
	b, err := e.m.RunDue(ctx, 2)
	if err != nil || b.Review != 2 {
		t.Fatalf("first batch %+v %v", b, err)
	}
	b, err = e.m.RunDue(ctx, 10)
	if err != nil || b.Review != 3 {
		t.Fatalf("second batch %+v %v", b, err)
	}
	if b, _ := e.m.RunDue(ctx, 10); b.Review != 0 {
		t.Fatal("review items were queued again")
	}
}

// Matching only writes metadata tables: the catalogue the player and
// browsing read is untouched (offline playback unaffected).
func TestMatchingLeavesTheCatalogueAlone(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	snap := func() string {
		var s string
		e.db.QueryRow(`SELECT group_concat(id || title || sort_title || coalesce(year, ''), ';') FROM
			(SELECT * FROM library_items ORDER BY id)`).Scan(&s)
		var f string
		e.db.QueryRow(`SELECT group_concat(path || state || size, ';') FROM (SELECT * FROM library_files ORDER BY path)`).Scan(&f)
		return s + "|" + f
	}
	before := snap()
	e.match(e.item("Heat"))
	if after := snap(); after != before {
		t.Fatalf("catalogue changed:\n%s\n%s", before, after)
	}
}

// Shows match like movies, with tvshow.nfo in the show folder.
func TestShowMatchesFromTvshowNFO(t *testing.T) {
	e := newMatchEnv(t)
	e.write("TV/Breaking Bad/Season 01/Breaking Bad - S01E01.mkv", "x")
	e.write("TV/Breaking Bad/tvshow.nfo", `<tvshow><uniqueid type="tmdb">1396</uniqueid></tvshow>`)
	e.library("tv", "TV")
	id := e.item("Breaking Bad")
	if out := e.match(id); out != OutcomeMatched || !slices.Equal(e.fake.calls, []string{"lookup tmdb:1396"}) {
		t.Fatalf("outcome %s calls %v", out, e.fake.calls)
	}
	var season string
	e.db.QueryRow(`SELECT id FROM library_items WHERE kind = 'season'`).Scan(&season)
	if _, err := e.m.MatchItem(context.Background(), season); !errors.Is(err, ErrInvalid) {
		t.Fatalf("season: %v (seasons come from the show's record, META-1b)", err)
	}
}

// #121 review: the backoff never overflows, however long an item has been
// failing. It stays at the cap (plus at most 10% jitter).
func TestBackoffStaysCappedAfterManyFailures(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	e.match(id)
	e.fake.err = fmt.Errorf("%w: 503", ErrUnavailable)
	for _, attempts := range []int{28, 29, 63, 64, 1000} {
		e.db.Exec(`UPDATE metadata_items SET attempts = ? WHERE item_id = ?`, attempts, id)
		if out := e.match(id); out != OutcomeRetry {
			t.Fatalf("outcome %s", out)
		}
		var at int64
		e.db.QueryRow(`SELECT next_attempt_at FROM metadata_items WHERE item_id = ?`, id).Scan(&at)
		if d := at - e.clock.UnixMilli(); d != retryCap.Milliseconds() {
			t.Fatalf("after %d attempts the next try is in %v, want the %v cap", attempts, time.Duration(d)*time.Millisecond, retryCap)
		}
	}
}

// Retries are jittered by up to 10% so items that failed together don't
// all retry in the same instant (§3.7: bounded retries with jitter).
func TestRetryJitterIsBounded(t *testing.T) {
	for _, d := range []time.Duration{time.Minute, retryCap} {
		lo, hi := d, d
		for range 200 {
			j := defaultJitter(d)
			lo, hi = min(lo, j), max(hi, j)
		}
		if lo < d || hi > d+d/10 || hi == lo {
			t.Fatalf("jitter of %v ranged %v..%v, want within [d, 1.1d] and not constant", d, lo, hi)
		}
	}
}

// #121 review: an item the admin edits before matching reaches it is still
// queued, so it gets matched (keeping the edit).
func TestEditBeforeFirstMatchStillMatches(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	ctx := context.Background()
	id := e.item("Heat")
	if _, err := e.st.Edit(ctx, id, 1, []Edit{{Field: FieldYear, Lock: ptr(true)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Enqueue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if b, err := e.m.RunDue(ctx, 10); err != nil || b.Matched != 1 {
		t.Fatalf("RunDue %+v %v: the edited item wasn't matched", b, err)
	}
	if md := e.get(id); !md.Fields.Year.Locked || md.Fields.Overview.Value == nil {
		t.Fatalf("after matching %+v", md)
	}
}

// #121 review: a second fix-match in another namespace replaces the first
// cleanly: the old film's IDs and unlocked fields go, the admin's own
// edits stay, and the item ends matched, not in a permanent conflict.
func TestSecondFixMatchReplacesTheFirst(t *testing.T) {
	e := newMatchEnv(t)
	str := func(s string) *string { return &s }
	r := e.fake.records["tmdb:603"]
	r.Overview, r.Genres, r.SortTitle = str("Neo learns the truth."), []string{"Science Fiction"}, str("Matrix, The")
	e.fake.add(r)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	ctx := context.Background()
	id := e.item("Heat")
	md, _ := e.st.Edit(ctx, id, 1, []Edit{{Field: FieldTitle, Value: raw("Heat (my copy)")}})
	// The wrong film, by IMDb ID.
	md, err := e.st.FixMatch(ctx, id, md.Revision, "imdb", "tt0133093")
	if err != nil {
		t.Fatal(err)
	}
	if out := e.match(id); out != OutcomeMatched {
		t.Fatalf("first fix-match: %s", out)
	}
	// The right film, by TMDB ID.
	if _, err := e.st.FixMatch(ctx, id, e.get(id).Revision, "tmdb", "949"); err != nil {
		t.Fatal(err)
	}
	if out := e.match(id); out != OutcomeMatched {
		t.Fatalf("second fix-match: %s, want matched", out)
	}
	md = e.get(id)
	f := md.Fields
	if x, _ := extID(md, "imdb"); x.ID != "tt0113277" || x.Locked {
		t.Fatalf("imdb %+v: the first fix-match's ID is still there", x)
	}
	if x, _ := extID(md, "tmdb"); x.ID != "949" || !x.Locked {
		t.Fatalf("tmdb %+v", x)
	}
	if *f.Overview.Value != "A group of high-end professional thieves." || !slices.Equal(f.Genres.Value, []string{"Crime", "Drama"}) {
		t.Fatalf("the wrong film's fields stayed: %+v", f)
	}
	// Heat's record has no sort title, so the wrong film's must be gone,
	// not just overwritten: the parsed one shows again.
	if *f.SortTitle.Value != "Heat" || f.SortTitle.Source != nil {
		t.Fatalf("sort title %q from %+v: the wrong film's value stayed", *f.SortTitle.Value, f.SortTitle.Source)
	}
	if *f.Title.Value != "Heat (my copy)" || !f.Title.Locked {
		t.Fatalf("the admin's edit was lost: %+v", f.Title)
	}
}

// #121 review: hints in different namespaces that point at different films
// (a {tmdb-…} folder and an NFO's IMDb ID) are a conflict, caught by
// comparing the looked-up record's IDs with the other hints.
func TestHintsDisagreeingAcrossNamespaces(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.write("Movies/Heat (1995) {tmdb-949}/movie.nfo", `<movie><uniqueid type="imdb">tt0133093</uniqueid></movie>`)
	e.library("movies", "Movies")
	id := e.item("Heat")
	if out := e.match(id); out != OutcomeConflict {
		t.Fatalf("outcome %s, want conflict", out)
	}
	if md := e.get(id); md.MatchStatus != contracts.MatchConflict || md.Fields.Overview.Value != nil || len(md.ExternalIDs) != 0 {
		t.Fatalf("a conflicting record was applied: %+v", md)
	}
	// Hints that agree across namespaces match as usual.
	e2 := newMatchEnv(t)
	e2.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e2.write("Movies/Heat (1995) {tmdb-949}/movie.nfo", `<movie><uniqueid type="imdb">tt0113277</uniqueid></movie>`)
	e2.library("movies", "Movies")
	if out := e2.match(e2.item("Heat")); out != OutcomeMatched {
		t.Fatalf("agreeing hints: %s", out)
	}
}

// A field the admin cleared and locked stays cleared after a match: a match
// never overwrites a locked field, including a locked clear.
func TestLockedClearSurvivesMatch(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
	e.library("movies", "Movies")
	id := e.item("Heat")
	if _, err := e.st.Edit(context.Background(), id, 1, []Edit{{Field: FieldOverview, Value: raw(nil)}}); err != nil {
		t.Fatal(err)
	}
	if out := e.match(id); out != OutcomeMatched {
		t.Fatalf("outcome %s", out)
	}
	f := e.get(id).Fields
	if f.Overview.Value != nil || !f.Overview.Locked || f.Overview.Source.Provider != SourceUser {
		t.Fatalf("overview after matching %+v", f.Overview)
	}
	if !slices.Equal(f.Genres.Value, []string{"Crime", "Drama"}) {
		t.Fatalf("unlocked genres weren't applied: %+v", f.Genres)
	}
}

// A locked ID the provider doesn't know, or that names another kind, makes
// the item a conflict: the admin's choice is never replaced by a hint or a
// search.
func TestUnknownLockedIDIsAConflict(t *testing.T) {
	for _, locked := range []string{"404404", "1396"} { // unknown; a show on a movie
		e := newMatchEnv(t)
		e.write("Movies/Heat (1995) {tmdb-949}/Heat (1995).mkv", "x")
		e.library("movies", "Movies")
		id := e.item("Heat")
		if _, err := e.st.FixMatch(context.Background(), id, 1, "tmdb", locked); err != nil {
			t.Fatal(err)
		}
		if out := e.match(id); out != OutcomeConflict || !slices.Equal(e.fake.calls, []string{"lookup tmdb:" + locked}) {
			t.Fatalf("locked %s: outcome %s calls %v", locked, out, e.fake.calls)
		}
		var reason string
		e.db.QueryRow(`SELECT coalesce(last_error, '') FROM metadata_items WHERE item_id = ?`, id).Scan(&reason)
		if md := e.get(id); md.MatchStatus != contracts.MatchConflict || reason == "" || md.Fields.Overview.Value != nil {
			t.Fatalf("locked %s: status %s reason %q", locked, md.MatchStatus, reason)
		}
	}
}

// A season queued by an edit is taken off the queue by the first batch that
// meets it, so it doesn't fail every later batch.
func TestQueuedSeasonLeavesTheQueue(t *testing.T) {
	e := newMatchEnv(t)
	e.write("TV/Breaking Bad/Season 01/Breaking Bad - S01E01.mkv", "x")
	e.library("tv", "TV")
	ctx := context.Background()
	var season string
	e.db.QueryRow(`SELECT id FROM library_items WHERE kind = 'season'`).Scan(&season)
	if _, err := e.st.Edit(ctx, season, 1, []Edit{{Field: FieldOverview, Value: raw("Season one.")}}); err != nil {
		t.Fatal(err)
	}
	if b, err := e.m.RunDue(ctx, 10); err != nil || len(b.Failed) != 1 || b.Failed[0].ItemID != season {
		t.Fatalf("first batch %+v %v", b, err)
	}
	var queued bool
	e.db.QueryRow(`SELECT next_attempt_at IS NOT NULL FROM metadata_items WHERE item_id = ?`, season).Scan(&queued)
	if queued {
		t.Fatal("the season is still queued")
	}
	if b, err := e.m.RunDue(ctx, 10); err != nil || len(b.Failed) != 0 {
		t.Fatalf("later batch %+v %v", b, err)
	}
}

// If pausing after an auth error fails, the batch stops with that error: the
// provider isn't called again for the next item.
func TestFailedPauseStopsTheBatch(t *testing.T) {
	e := newMatchEnv(t)
	e.write("Movies/Heat (1995)/Heat (1995).mkv", "x")
	e.write("Movies/The Matrix (1999)/The Matrix (1999).mkv", "x")
	e.library("movies", "Movies")
	ctx := context.Background()
	e.st.Enqueue(ctx, 10)
	if _, err := e.db.Exec(`CREATE TRIGGER pause_broken BEFORE INSERT ON metadata_providers BEGIN SELECT RAISE(ABORT, 'pause broken'); END`); err != nil {
		t.Fatal(err)
	}
	e.fake.err = fmt.Errorf("%w: 401", ErrAuth)
	b, err := e.m.RunDue(ctx, 10)
	if err == nil || len(e.fake.calls) != 1 {
		t.Fatalf("RunDue = %+v, %v after %d calls: want the pause error and one call", b, err, len(e.fake.calls))
	}
}

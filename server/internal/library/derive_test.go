package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

func sortKeyOf(t *testing.T, s *Store, title string) (key string, group int) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT sort_key, sort_group FROM library_items WHERE title = ?`, title).Scan(&key, &group); err != nil {
		t.Fatal(err)
	}
	return
}

func TestPlacedItemsGetSortKeys(t *testing.T) {
	s, _, _, _, _ := setupMovies(t, "The Matrix (1999)/The Matrix (1999).mkv", "Élite (2018)/Élite (2018).mkv",
		"2001 A Space Odyssey (1968)/2001 A Space Odyssey (1968).mkv")
	for title, want := range map[string]struct {
		key   string
		group int
	}{
		"The Matrix":           {"matrix", 1},
		"Élite":                {"elite", 1},
		"2001 A Space Odyssey": {"2001 a space odyssey", 0},
	} {
		key, group := sortKeyOf(t, s, title)
		if key != want.key || group != want.group {
			t.Errorf("%s: sort_key %q group %d, want %q %d", title, key, group, want.key, want.group)
		}
	}
}

type factsProber struct{}

func (factsProber) Probe(_ context.Context, _ *os.Root, rel string) (probe.Result, error) {
	return probe.Result{Container: "matroska", DurationMs: 7_200_000,
		Video: []probe.VideoStream{{Index: 0, Codec: "hevc", Width: 1920, Height: 800, HDR: probe.HDR10, Default: true}},
		Audio: []probe.AudioStream{{Index: 1, Codec: "aac", Channels: 2, Language: "eng"}, {Index: 2, Codec: "aac", Channels: 2, Language: "ger"},
			{Index: 3, Codec: "aac", Channels: 2, Language: "und"}}}, nil
}

type fileRow struct {
	Res   *string
	HDR   *int
	Dur   *int64
	Audio []string
}

func fileFactsOf(t *testing.T, s *Store) fileRow {
	t.Helper()
	var r fileRow
	var id string
	if err := s.db.QueryRow(`SELECT id, resolution, hdr, duration_ms FROM library_files`).Scan(&id, &r.Res, &r.HDR, &r.Dur); err != nil {
		t.Fatal(err)
	}
	r.Audio, _ = queryStrings(context.Background(), s.db, `SELECT lang FROM library_file_audio WHERE file_id = ? ORDER BY lang`, id)
	return r
}

func TestStoredProbeFillsFileFacts(t *testing.T) {
	s, _, _, sc, root := setupMovies(t, "Heat (1995)/Heat (1995).mkv")
	probeDue(t, sc, root, factsProber{})
	r := fileFactsOf(t, s)
	if r.Res == nil || *r.Res != "1080p" || r.HDR == nil || *r.HDR != 1 || r.Dur == nil || *r.Dur != 7_200_000 {
		t.Fatalf("facts = %+v", r)
	}
	if !reflect.DeepEqual(r.Audio, []string{"deu", "eng"}) {
		t.Fatalf("audio = %v", r.Audio)
	}
}

func TestSetDisplayRefreshesSortKey(t *testing.T) {
	s, _, _, _, _ := setupMovies(t, "Heat (1995)/Heat (1995).mkv", "Alien (1979)/Alien (1979).mkv")
	ctx := context.Background()
	var id string
	if err := s.db.QueryRow(`SELECT id FROM library_items WHERE title = 'Heat'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	z := "Zzz"
	if err := s.SetDisplay(ctx, id, Display{SortTitle: &z}); err != nil {
		t.Fatal(err)
	}
	if k, g := sortKeyOf(t, s, "Heat"); k != "zzz" || g != 1 {
		t.Fatalf("key %q group %d", k, g)
	}
	if err := s.SetDisplay(ctx, id, Display{Genres: []string{"Drama", "Crime"}}); err != nil {
		t.Fatal(err)
	}
	g, _ := queryStrings(ctx, s.db, `SELECT genre FROM library_item_genres WHERE item_id = ? ORDER BY genre`, id)
	if !reflect.DeepEqual(g, []string{"Crime", "Drama"}) {
		t.Fatalf("genres %v", g)
	}
	if k, _ := sortKeyOf(t, s, "Heat"); k != "zzz" {
		t.Fatalf("nil SortTitle changed key to %q", k)
	}
	if err := s.SetDisplay(ctx, id, Display{Genres: []string{}}); err != nil {
		t.Fatal(err)
	}
	if g, _ = queryStrings(ctx, s.db, `SELECT genre FROM library_item_genres WHERE item_id = ?`, id); len(g) != 0 {
		t.Fatalf("genres not cleared: %v", g)
	}
	if err := s.SetDisplay(ctx, "nope", Display{Genres: []string{"x"}}); err != ErrNotFound {
		t.Fatalf("unknown item: %v", err)
	}
}

func TestBackfillFillsOldRows(t *testing.T) {
	s, _, _, sc, root := setupMovies(t, "The Matrix (1999)/The Matrix (1999).mkv", "Élite (2018)/Élite (2018).mkv")
	probeDue(t, sc, root, factsProber{})
	take := func() (out []string) {
		rows, err := s.db.Query(`SELECT title, sort_key, sort_group FROM library_items ORDER BY title`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a, b string
			var g int
			if err := rows.Scan(&a, &b, &g); err != nil {
				t.Fatal(err)
			}
			out = append(out, a+"|"+b+"|"+string(rune('0'+g)))
		}
		return
	}
	wantItems := take()
	rows, _ := s.db.Query(`SELECT id FROM library_files ORDER BY path`)
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	wantAudio := map[string][]string{}
	for _, id := range ids {
		wantAudio[id], _ = queryStrings(context.Background(), s.db, `SELECT lang FROM library_file_audio WHERE file_id = ? ORDER BY lang`, id)
	}
	for _, q := range []string{`UPDATE library_items SET sort_key = NULL, sort_group = NULL`,
		`UPDATE library_files SET resolution = NULL, hdr = NULL, duration_ms = NULL`, `DELETE FROM library_file_audio`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	items, files, err := s.BackfillDerived(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if items != 2 || files != 2 {
		t.Fatalf("counts items %d files %d", items, files)
	}
	if got := take(); !reflect.DeepEqual(got, wantItems) {
		t.Fatalf("items %v, want %v", got, wantItems)
	}
	for _, id := range ids {
		got, _ := queryStrings(context.Background(), s.db, `SELECT lang FROM library_file_audio WHERE file_id = ? ORDER BY lang`, id)
		if !reflect.DeepEqual(got, wantAudio[id]) || len(got) != 2 {
			t.Fatalf("audio %v want %v", got, wantAudio[id])
		}
	}
	var res string
	var hdr int
	var dur int64
	if err := s.db.QueryRow(`SELECT resolution, hdr, duration_ms FROM library_files LIMIT 1`).Scan(&res, &hdr, &dur); err != nil {
		t.Fatal(err)
	}
	if res != "1080p" || hdr != 1 || dur != 7_200_000 {
		t.Fatalf("file facts %s %d %d", res, hdr, dur)
	}
	if i, f, _ := s.BackfillDerived(context.Background()); i != 0 || f != 0 {
		t.Fatalf("second backfill did %d %d", i, f)
	}
}

func assertNoFacts(t *testing.T, s *Store) {
	t.Helper()
	if r := fileFactsOf(t, s); r.Res != nil || r.HDR != nil || r.Dur != nil || len(r.Audio) != 0 {
		t.Fatalf("facts not cleared: %+v", r)
	}
}

func TestProbeFailureClearsFileFacts(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "Heat (1995)/Heat (1995).mkv")
	probeDue(t, sc, root, factsProber{})
	if r := fileFactsOf(t, s); r.Res == nil || len(r.Audio) != 2 {
		t.Fatalf("facts not set: %+v", r)
	}
	m.write("Movies/Heat (1995)/Heat (1995).mkv", 20)
	scan(t, sc, root)
	p := &scriptedProber{fail: map[string]error{"Heat (1995)/Heat (1995).mkv": errors.New("corrupt")}}
	if run := probeDue(t, sc, root, p); run.Failed != 1 {
		t.Fatalf("run = %+v", run)
	}
	assertNoFacts(t, s)
	// A stale failure writes nothing: BackfillDerived must not pick failed files.
	if i, f, err := s.BackfillDerived(context.Background()); err != nil || f != 0 {
		t.Fatalf("backfill %d %d %v", i, f, err)
	}
}

func TestRetryProbeClearsFileFacts(t *testing.T) {
	s, _, _, sc, root := setupMovies(t, "Heat (1995)/Heat (1995).mkv")
	probeDue(t, sc, root, factsProber{})
	var id string
	if err := s.db.QueryRow(`SELECT id FROM library_files`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryProbe(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	assertNoFacts(t, s)
}

// served is what the API shows of the one movie file's facts.
type served struct {
	res, hdr, eng        bool // the 1080p, HDR and eng filters match it
	facetRes, facetAudio int  // resolutions and audio languages in the facets
	dur, summaryDur      *int64
	container            string
	video, audio         int
}

func servedFacts(t *testing.T, s *Store) served {
	t.Helper()
	ctx := context.Background()
	var libID, itemID string
	if err := s.db.QueryRow(`SELECT library_id, id FROM library_items WHERE kind = 'movie'`).Scan(&libID, &itemID); err != nil {
		t.Fatal(err)
	}
	matches := func(q ItemQuery) bool {
		q.LibraryID = libID
		p, err := s.Items(ctx, grantAll, q)
		if err != nil {
			t.Fatal(err)
		}
		return len(p.Items) == 1
	}
	yes := true
	sv := served{res: matches(ItemQuery{Resolution: "1080p"}), hdr: matches(ItemQuery{HDR: &yes}), eng: matches(ItemQuery{AudioLanguage: "eng"})}
	f, err := s.Facets(ctx, grantAll, libID)
	if err != nil {
		t.Fatal(err)
	}
	sv.facetRes, sv.facetAudio = len(f.Resolutions), len(f.AudioLanguages)
	d, err := s.Item(ctx, grantAll, itemID)
	if err != nil || len(d.Versions) != 1 {
		t.Fatalf("detail %+v %v", d, err)
	}
	v := d.Versions[0]
	sv.dur, sv.summaryDur, sv.container, sv.video, sv.audio = v.DurationMs, d.Item.DurationMs, v.Container, len(v.Video), len(v.Audio)
	return sv
}

// assertFactsServed: the current probe's facts reach filters, facets and detail.
func assertFactsServed(t *testing.T, s *Store) {
	t.Helper()
	sv := servedFacts(t, s)
	if !sv.res || !sv.hdr || !sv.eng || sv.facetRes != 1 || sv.facetAudio != 2 || sv.dur == nil || sv.summaryDur == nil ||
		sv.container != "matroska" || sv.video != 1 || sv.audio != 3 {
		t.Fatalf("facts not served: %+v", sv)
	}
}

// assertFactsHidden: an older revision's probe reaches none of them.
func assertFactsHidden(t *testing.T, s *Store) {
	t.Helper()
	sv := servedFacts(t, s)
	if sv.res || sv.hdr || sv.eng || sv.facetRes != 0 || sv.facetAudio != 0 || sv.dur != nil || sv.summaryDur != nil ||
		sv.container != "" || sv.video != 0 || sv.audio != 0 {
		t.Fatalf("old facts served: %+v", sv)
	}
}

// #168, #181 review: facts and the stored probe count only while they
// describe the file's current revision, whoever moved size/mtime_ns. A file
// restored to its probed revision gets them back without a new probe.
func TestOldProbeFactsCountOnlyForTheirRevision(t *testing.T) {
	const rel = "Movies/Heat (1995)/Heat (1995).mkv"
	s, _, m, sc, root := setupMovies(t, "Heat (1995)/Heat (1995).mkv")
	probeDue(t, sc, root, factsProber{})
	assertFactsServed(t, s)
	scan(t, sc, root) // unchanged: still served
	assertFactsServed(t, s)
	info, err := os.Stat(filepath.Join(m.dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	m.write(rel, 20)
	scan(t, sc, root)
	assertFactsHidden(t, s)
	m.write(rel, 10)
	if err := os.Chtimes(filepath.Join(m.dir, rel), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	scan(t, sc, root)
	assertFactsServed(t, s)
	if run := probeDue(t, sc, root, factsProber{}); run.Queued != 0 {
		t.Fatalf("restored file probed again: %+v", run)
	}
}

// #168: the start-up backfill derives facts only from a probe of the file's
// current revision, so it can't bring back facts a change cleared.
func TestBackfillSkipsProbeOfOlderRevision(t *testing.T) {
	s, _, m, sc, root := setupMovies(t, "Heat (1995)/Heat (1995).mkv")
	probeDue(t, sc, root, factsProber{})
	m.write("Movies/Heat (1995)/Heat (1995).mkv", 20)
	scan(t, sc, root)
	if _, f, err := s.BackfillDerived(context.Background()); err != nil || f != 0 {
		t.Fatalf("backfill files %d, err %v", f, err)
	}
	assertFactsHidden(t, s)
}

// #168, #181 review: a probe of the changed file that times out or is
// deferred moves size/mtime_ns on (retryTimeout, the ErrChanged branch)
// without storing facts; the old revision's facts still don't count.
func TestTimedOutOrDeferredProbeKeepsNoOldFacts(t *testing.T) {
	const rel = "Heat (1995)/Heat (1995).mkv"
	for name, p := range map[string]func(m *media) Prober{
		"timeout":  func(*media) Prober { return &scriptedProber{fail: map[string]error{rel: probe.ErrTimeout}} },
		"deferred": func(m *media) Prober { return &scriptedProber{during: func(r string) { m.write("Movies/"+r, 99) }} },
	} {
		t.Run(name, func(t *testing.T) {
			s, _, m, sc, root := setupMovies(t, rel)
			probeDue(t, sc, root, factsProber{})
			m.write("Movies/"+rel, 20)
			scan(t, sc, root)
			if run := probeDue(t, sc, root, p(m)); run.TimedOut+run.Deferred != 1 {
				t.Fatalf("%+v", run)
			}
			assertFactsHidden(t, s)
		})
	}
}

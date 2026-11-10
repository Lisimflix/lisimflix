//go:build scale

package library

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// TestCatalogueAtScale is LIB-2c Task 8 on a synthetic library: 5,000 movies
// (1,000 with two versions, 200 with three) and 1,000 shows (999 with 3
// seasons of 10 episodes, one with 30 seasons). It times every catalogue
// route server-side (the handler, no network) at idle, during a full rescan
// and during a probe turn, and reports p50/p95. Run it by hand:
//
//	go -C server test -tags scale -run TestCatalogueAtScale -v -timeout 30m ./internal/library/
//
// It asserts only correctness (every response 200, no Resolve error
// under load); the numbers go in docs/testing/.
func TestCatalogueAtScale(t *testing.T) {
	e := newBrowse(t)
	ctx := context.Background()
	var movieFiles, tvFiles []string
	for i := range 5000 {
		dir := fmt.Sprintf("Movies/%c Movie %04d (%d)", 'A'+i%26, i, 1950+i%70)
		base := filepath.Base(dir)
		movieFiles = append(movieFiles, dir+"/"+base+".mkv")
		if i < 1000 {
			movieFiles = append(movieFiles, dir+"/"+base+" - Director's Cut.mkv")
		}
		if i < 200 {
			movieFiles = append(movieFiles, dir+"/"+base+" - Extended.mkv")
		}
	}
	for s := range 1000 {
		seasons := 3
		if s == 0 {
			seasons = 30
		}
		for se := 1; se <= seasons; se++ {
			for ep := 1; ep <= 10; ep++ {
				tvFiles = append(tvFiles, fmt.Sprintf("TV/%c Show %04d (2010)/Season %02d/%c Show %04d - S%02dE%02d.mkv", 'A'+s%26, s, se, 'A'+s%26, s, se, ep))
			}
		}
	}
	for _, f := range append(movieFiles, tvFiles...) {
		e.m.write(f, 10)
	}
	movies, err := e.s.CreateLibrary(ctx, "Movies", "movies", []string{"Movies"})
	if err != nil {
		t.Fatal(err)
	}
	tv, err := e.s.CreateLibrary(ctx, "TV", "tv", []string{"TV"})
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{movies.Roots[0].ID, tv.Roots[0].ID}

	// SCALE-01: first full scan and probe, then a repeat that probes nothing.
	for _, r := range roots {
		start := time.Now()
		run := e.scan(r)
		scanT := time.Since(start)
		start = time.Now()
		pr, perr := e.sc.ProbeDue(ctx, r, benchProber{}, 4, 0, 0)
		if perr != nil {
			t.Fatal(perr)
		}
		t.Logf("SCALE-01 first %s: scan %v (seen %d, added %d, unplaced %d), probe %v (queued %d, stored %d, failed %d)",
			r, scanT.Round(time.Millisecond), run.Seen, run.Added, run.Unplaced, time.Since(start).Round(time.Millisecond), pr.Queued, pr.Stored, pr.Failed)
		start = time.Now()
		run = e.scan(r)
		scanT = time.Since(start)
		if pr, err = e.sc.ProbeDue(ctx, r, benchProber{}, 4, 0, 0); err != nil {
			t.Fatal(err)
		}
		t.Logf("SCALE-01 repeat %s: scan %v (seen %d, changed %d), probe queued %d", r, scanT.Round(time.Millisecond), run.Seen, run.Changed, pr.Queued)
		if pr.Queued != 0 {
			t.Errorf("repeat probe queued %d unchanged files", pr.Queued)
		}
	}
	var items, files int
	e.s.db.QueryRow(`SELECT count(*) FROM library_items`).Scan(&items)
	e.s.db.QueryRow(`SELECT count(*) FROM library_files`).Scan(&files)
	t.Logf("catalogue: %d items, %d files", items, files)

	// Genres on 500 movies, so the genre filter has work to do.
	some, err := queryStrings(ctx, e.s.db, `SELECT id FROM library_items WHERE library_id = ? ORDER BY id LIMIT 500`, movies.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range some {
		if err := e.s.SetDisplay(ctx, id, Display{Genres: []string{"Drama"}}); err != nil {
			t.Fatal(err)
		}
	}

	grant := func(contracts.Principal) Grant { return Grant{All: true, Admin: true} }
	h := NewAPI(e.s, grant, nil).Handler()
	res := NewResolver(e.s, grant)
	get := func(target string) ([]byte, time.Duration) {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r = r.WithContext(contracts.WithPrincipal(r.Context(), *adminP))
		w := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(w, r)
		d := time.Since(start)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: %d %s", target, w.Code, w.Body)
		}
		return w.Body.Bytes(), d
	}

	m, tvItems := "/api/v1/items?libraryId="+movies.ID, "/api/v1/items?libraryId="+tv.ID
	lastPage := m + "&limit=200"
	for {
		var p contracts.ItemPage
		body, _ := get(lastPage)
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatal(err)
		}
		if p.NextCursor == nil {
			break
		}
		lastPage = m + "&limit=200&cursor=" + url.QueryEscape(*p.NextCursor)
	}
	var threeFiles, threeVersion, bigShow, aSeason string
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.s.db.QueryRow(`SELECT item_id FROM library_versions GROUP BY item_id HAVING COUNT(*) = 3 LIMIT 1`).Scan(&threeFiles))
	must(e.s.db.QueryRow(`SELECT id FROM library_versions WHERE item_id = ? LIMIT 1`, threeFiles).Scan(&threeVersion))
	must(e.s.db.QueryRow(`SELECT parent_id FROM library_items WHERE kind = 'season' GROUP BY parent_id HAVING COUNT(*) = 30`).Scan(&bigShow))
	must(e.s.db.QueryRow(`SELECT id FROM library_items WHERE kind = 'season' AND parent_id = ? LIMIT 1`, bigShow).Scan(&aSeason))

	routes := []struct{ name, target string }{
		{"libraries", "/api/v1/libraries"},
		{"facets/movies", "/api/v1/libraries/" + movies.ID + "/facets"},
		{"facets/tv", "/api/v1/libraries/" + tv.ID + "/facets"},
		{"items/title", m},
		{"items/from=M", m + "&from=M"},
		{"items/last-page", lastPage},
		{"items/added", m + "&sort=added"},
		{"items/year", m + "&sort=year"},
		{"items/genre", m + "&genre=Drama"},
		{"items/years", m + "&yearFrom=1980&yearTo=1990"},
		{"items/resolution", m + "&resolution=4k"},
		{"items/hdr", m + "&hdr=true"},
		{"items/sdr", m + "&hdr=false"},
		{"items/audio", m + "&audioLanguage=jpn"},
		{"items/res+audio", m + "&resolution=4k&audioLanguage=jpn"},
		{"items/tv-title", tvItems},
		{"items/tv-resolution", tvItems + "&resolution=720p"},
		{"items/tv-res+audio", tvItems + "&resolution=4k&audioLanguage=fra"},
		{"items/30-seasons", "/api/v1/items?parentId=" + bigShow},
		{"items/episodes", "/api/v1/items?parentId=" + aSeason},
		{"getItem/3-version-movie", "/api/v1/items/" + threeFiles},
		{"getItem/30-season-show", "/api/v1/items/" + bigShow},
	}

	// measure runs every route (and Resolve) round after round until stop
	// closes and at least min rounds are done; it returns durations by name.
	measure := func(min int, stop <-chan struct{}) (map[string][]time.Duration, int) {
		out := map[string][]time.Duration{}
		resolveErrs := 0
		for round := 0; ; round++ {
			if round >= min {
				select {
				case <-stop:
					return out, resolveErrs
				default:
				}
			}
			for _, rt := range routes {
				_, d := get(rt.target)
				out[rt.name] = append(out[rt.name], d)
			}
			start := time.Now()
			if _, err := res.Resolve(*adminP, threeFiles, threeVersion); err != nil {
				resolveErrs++
			}
			out["Resolve"] = append(out["Resolve"], time.Since(start))
		}
	}
	report := func(phase string, d map[string][]time.Duration, resolveErrs int) {
		names := make([]string, 0, len(d))
		for n := range d {
			names = append(names, n)
		}
		slices.Sort(names)
		var b strings.Builder
		fmt.Fprintf(&b, "\n%s (Resolve errors %d)\n%-26s %6s %10s %10s %10s\n", phase, resolveErrs, "route", "n", "p50", "p95", "max")
		for _, n := range names {
			v := slices.Clone(d[n])
			slices.Sort(v)
			p := func(q float64) time.Duration { return v[min(len(v)-1, int(q*float64(len(v))))] }
			fmt.Fprintf(&b, "%-26s %6d %10v %10v %10v\n", n, len(v), p(0.50).Round(10*time.Microsecond), p(0.95).Round(10*time.Microsecond), v[len(v)-1].Round(10*time.Microsecond))
		}
		t.Log(b.String())
		if resolveErrs > 0 {
			t.Errorf("%s: %d Resolve errors (the M1 fallback would not run)", phase, resolveErrs)
		}
	}

	closed := make(chan struct{})
	close(closed)
	get(m) // warm the page cache
	d, re := measure(30, closed)
	report("idle", d, re)

	// During a full rescan of both roots (every file's row is rewritten).
	underLoad := func(phase string, load func()) {
		stop := make(chan struct{})
		var wg sync.WaitGroup
		start := time.Now()
		wg.Go(func() { load(); close(stop) })
		d, re := measure(5, stop)
		wg.Wait()
		t.Logf("%s load took %v", phase, time.Since(start).Round(time.Millisecond))
		report(phase, d, re)
	}
	underLoad("full rescan", func() {
		for _, r := range roots {
			if _, err := e.sc.ScanRoot(ctx, r); err != nil {
				t.Error(err)
			}
		}
	})
	// During a probe turn: every file's probe is stale, so every file's
	// result and facts are written again.
	if _, err := e.s.db.Exec(`UPDATE library_files SET probe_size = NULL`); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("SCALE_EXPLAIN") != "" {
		explainSlow(t, e.s, tv.ID)
	}
	underLoad("probe turn", func() {
		for _, r := range roots {
			if _, err := e.sc.ProbeDue(ctx, r, benchProber{}, 4, 0, 0); err != nil {
				t.Error(err)
			}
		}
	})

}

// explainSlow times and prints EXPLAIN QUERY PLAN for the parts of the routes
// over 100 ms: the TV facets queries and a show-level file filter (its COUNT
// and its first page), built from the same SQL pieces Items and Facets use.
func explainSlow(t *testing.T, s *Store, tvID string) {
	ctx := context.Background()
	desc := descendantFiles + " AND f.resolution = ?)"
	own := ownFiles + " AND f.resolution = ?)"
	filter := " WHERE i.library_id = ? AND i.parent_id IS NULL AND (CASE WHEN i.kind IN ('movie', 'episode') THEN " + own + " ELSE " + desc + " END)"
	qs := []struct {
		name, q string
		args    []any
	}{
		{"facets genres", `SELECT DISTINCT g.genre FROM library_item_genres g JOIN library_items i ON i.id = g.item_id
			WHERE i.library_id = ? AND i.parent_id IS NULL ORDER BY g.genre`, []any{tvID}},
		{"facets years", `SELECT MIN(y), MAX(y) FROM (SELECT COALESCE(display_year, year) AS y FROM library_items
			WHERE library_id = ? AND parent_id IS NULL) WHERE y BETWEEN 1800 AND 9999`, []any{tvID}},
		{"facets audio", `SELECT DISTINCT a.lang FROM library_file_audio a
			JOIN library_files f ON f.id = a.file_id AND f.state = 'present'
			JOIN library_versions v ON v.file_id = f.id JOIN library_items i ON i.id = v.item_id
			WHERE i.library_id = ? ORDER BY a.lang`, []any{tvID}},
		{"facets resolution", `SELECT DISTINCT f.resolution, f.hdr FROM library_files f
			JOIN library_versions v ON v.file_id = f.id JOIN library_items i ON i.id = v.item_id
			WHERE i.library_id = ? AND f.state = 'present' AND f.resolution IS NOT NULL`, []any{tvID}},
		{"tv-resolution COUNT", `SELECT COUNT(*) FROM library_items i` + filter, []any{tvID, "720p", "720p"}},
		{"tv-resolution page", `SELECT ` + itemColumns + `, i.sort_group, i.sort_key` + itemJoins + filter +
			` ORDER BY i.sort_group, i.sort_key, i.id LIMIT 51`, []any{tvID, "720p", "720p"}},
	}
	for _, q := range qs {
		start := time.Now()
		rows, err := s.db.QueryContext(ctx, q.q, q.args...)
		if err != nil {
			t.Fatalf("%s: %v", q.name, err)
		}
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		took := time.Since(start)
		plan, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q.q, q.args...)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for plan.Next() {
			var id, parent, notused int
			var detail string
			plan.Scan(&id, &parent, &notused, &detail)
			fmt.Fprintf(&b, "\n    %d<-%d %s", id, parent, detail)
		}
		plan.Close()
		t.Logf("%s: %v, %d rows%s", q.name, took.Round(100*time.Microsecond), n, b.String())
	}
}

package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// ErrBadRequest marks a request the caller got wrong (a bad limit or cursor).
// It is wrapped with a message the API sends as invalid_request.
var ErrBadRequest = errors.New("library: bad request")

// Page sizes (contract: limit 1-200, default 50).
const (
	defaultLimit = 50
	maxLimit     = 200
)

// pageLimit returns the effective page size; 0 means the default.
func pageLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultLimit, nil
	}
	if limit < 1 || limit > maxLimit {
		return 0, fmt.Errorf("%w: limit must be 1 to %d", ErrBadRequest, maxLimit)
	}
	return limit, nil
}

// read runs fn in one read transaction, so a response never mixes two states
// of the catalogue.
func (s *Store) read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}

const librariesQuery = "libraries"

// Libraries lists the libraries the grant covers, by name then ID, one page at
// a time. A library's availability and first-scan progress come from its roots.
// Planned: LIB-2c (the catalogue API serves it).
func (s *Store) Libraries(ctx context.Context, g Grant, cursor string, limit int) (contracts.LibraryList, error) {
	limit, err := pageLimit(limit)
	if err != nil {
		return contracts.LibraryList{}, err
	}
	var after []any
	if cursor != "" {
		if after, err = decodeCursor(cursor, librariesQuery); err != nil {
			return contracts.LibraryList{}, err
		}
		if len(after) != 2 {
			return contracts.LibraryList{}, fmt.Errorf("%w: cursor is not valid", ErrBadRequest)
		}
	}
	var out contracts.LibraryList
	err = s.read(ctx, func(tx *sql.Tx) error {
		q := `SELECT id, name, type FROM library_libraries`
		var args []any
		var conds []string
		if !g.All {
			if len(g.LibraryIDs) == 0 {
				return nil
			}
			conds = append(conds, "id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(g.LibraryIDs)), ",")+")")
			for _, id := range g.LibraryIDs {
				args = append(args, id)
			}
		}
		if after != nil {
			name, ok1 := after[0].(string)
			id, ok2 := after[1].(string)
			if !ok1 || !ok2 {
				return fmt.Errorf("%w: cursor is not valid", ErrBadRequest)
			}
			conds = append(conds, "(name > ? OR (name = ? AND id > ?))")
			args = append(args, name, name, id)
		}
		if len(conds) > 0 {
			q += " WHERE " + strings.Join(conds, " AND ")
		}
		q += " ORDER BY name, id LIMIT ?"
		args = append(args, limit+1)
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l contracts.Library
			if err := rows.Scan(&l.ID, &l.Name, &l.Type); err != nil {
				return err
			}
			out.Items = append(out.Items, l)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			c := encodeCursor(librariesQuery, last.Name, last.ID)
			out.NextCursor = &c
		}
		for i := range out.Items {
			l := &out.Items[i]
			if l.Availability, l.Scan, err = libraryStatus(ctx, tx, l.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// libraryStatus derives a library's availability and first-scan progress from
// its roots (spec §3.6). degraded reads as available to viewers; the library
// is scanning only while its first whole-root scan runs.
func libraryStatus(ctx context.Context, tx *sql.Tx, libraryID string) (string, contracts.LibraryScan, error) {
	var roots, offline, scanned int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(state = 'offline'), 0), COALESCE(SUM(last_ok_at IS NOT NULL), 0)
		FROM library_roots WHERE library_id = ?`, libraryID).Scan(&roots, &offline, &scanned); err != nil {
		return "", contracts.LibraryScan{}, err
	}
	avail := contracts.AvailabilityPartial
	switch {
	case offline == 0:
		avail = contracts.AvailabilityAvailable
	case offline == roots:
		avail = contracts.AvailabilityOffline
	}
	scan := contracts.LibraryScan{State: contracts.ScanIdle}
	if scanned == 0 {
		var running, seen int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(seen), 0) FROM library_scan_runs
			WHERE status = 'running' AND root_id IN (SELECT id FROM library_roots WHERE library_id = ?)`, libraryID).
			Scan(&running, &seen); err != nil {
			return "", contracts.LibraryScan{}, err
		}
		if running > 0 {
			scan = contracts.LibraryScan{State: contracts.ScanScanning, FilesSeen: seen}
		}
	}
	return avail, scan, nil
}

// resolutionOrder is the order facets list resolutions in: best first.
var resolutionOrder = []string{contracts.Resolution4K, contracts.Resolution1080, contracts.Resolution720, contracts.ResolutionSD}

// Facets returns the filter-menu values present in a library: genres and years
// of its top-level items, and the resolutions, audio languages and HDR/SDR of
// its present files (a file counts while it is present, wherever its root is
// right now, because its item stays listed offline). A library the grant
// doesn't cover is ErrNotFound, the same as an unknown one.
// Planned: LIB-2c (the catalogue API serves it).
func (s *Store) Facets(ctx context.Context, g Grant, libraryID string) (contracts.LibraryFacets, error) {
	f := contracts.LibraryFacets{Genres: []string{}, AudioLanguages: []string{}, Resolutions: []string{}}
	if !g.allows(libraryID) {
		return f, ErrNotFound
	}
	err := s.read(ctx, func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM library_libraries WHERE id = ?`, libraryID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var err error
		if f.Genres, err = txStrings(ctx, tx, `SELECT DISTINCT g.genre FROM library_item_genres g JOIN library_items i ON i.id = g.item_id
			WHERE i.library_id = ? AND i.parent_id IS NULL ORDER BY g.genre`, libraryID); err != nil {
			return err
		}
		var minY, maxY sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MIN(y), MAX(y) FROM (SELECT COALESCE(display_year, year) AS y FROM library_items
			WHERE library_id = ? AND parent_id IS NULL) WHERE y BETWEEN 1800 AND 9999`, libraryID).Scan(&minY, &maxY); err != nil {
			return err
		}
		if minY.Valid {
			f.Years = &contracts.YearRange{Min: minY.Int64, Max: maxY.Int64}
		}
		if f.AudioLanguages, err = txStrings(ctx, tx, `SELECT DISTINCT a.lang FROM library_file_audio a
			JOIN library_files f ON f.id = a.file_id AND f.state = 'present'
			JOIN library_versions v ON v.file_id = f.id JOIN library_items i ON i.id = v.item_id
			WHERE i.library_id = ? ORDER BY a.lang`, libraryID); err != nil {
			return err
		}
		// A probe with no video stores hdr = 0 and no resolution, so only
		// files with a resolution say anything about SDR or HDR.
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT f.resolution, f.hdr FROM library_files f
			JOIN library_versions v ON v.file_id = f.id JOIN library_items i ON i.id = v.item_id
			WHERE i.library_id = ? AND f.state = 'present' AND f.resolution IS NOT NULL`, libraryID)
		if err != nil {
			return err
		}
		defer rows.Close()
		seen := map[string]bool{}
		for rows.Next() {
			var res string
			var hdr sql.NullInt64
			if err := rows.Scan(&res, &hdr); err != nil {
				return err
			}
			seen[res] = true
			if hdr.Valid && hdr.Int64 == 1 {
				f.HDR = true
			} else {
				f.SDR = true
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range resolutionOrder {
			if seen[r] {
				f.Resolutions = append(f.Resolutions, r)
			}
		}
		return nil
	})
	if err != nil {
		return contracts.LibraryFacets{Genres: []string{}, AudioLanguages: []string{}, Resolutions: []string{}}, err
	}
	if f.Genres == nil {
		f.Genres = []string{}
	}
	if f.AudioLanguages == nil {
		f.AudioLanguages = []string{}
	}
	slices.Sort(f.AudioLanguages)
	return f, nil
}

func txStrings(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	return queryStrings(ctx, tx, query, args...)
}

// ItemQuery is one listItems request: exactly one of LibraryID (the library's
// top-level movies or shows) and ParentID (a show's seasons or a season's
// episodes). Sort is title, added or year ("" is title); SortGiven says the
// caller sent it. From is the A–Z jump: one letter A–Z or #. The filters
// apply to libraryId listings only. Limit 0 is the default page size.
type ItemQuery struct {
	LibraryID, ParentID, Sort, From, Genre, Resolution, AudioLanguage string
	YearFrom, YearTo                                                  *int
	HDR, Watched                                                      *bool
	Cursor                                                            string
	Limit                                                             int
	SortGiven                                                         bool
}

// itemOrder is one listing order: a tuple of SQL expressions over item i,
// never NULL, compared in order with the item ID last. The cursor carries the
// tuple of the last item returned. kinds says each key's type: i for an
// integer, s for text.
type itemOrder struct {
	keys    []string
	kinds   string
	orderBy string // "" is the keys ascending
	after   string // keyset predicate; "" is the keys' row value > the cursor's
	args    func(k []any) []any
}

// itemYear is the displayed year, NULL outside the contract's 1800-9999, so a
// year that can't be served sorts and filters as unknown too.
const itemYear = "(CASE WHEN COALESCE(i.display_year, i.year) BETWEEN 1800 AND 9999 THEN COALESCE(i.display_year, i.year) END)"

var (
	// The # group (0) before a–z (1), then the folded sort title.
	orderTitle = itemOrder{keys: []string{"i.sort_group", "i.sort_key", "i.id"}, kinds: "iss"}
	// Newest first; the order the library_items_added index reads.
	orderAdded = itemOrder{keys: []string{"i.created_at", "i.id"}, kinds: "is",
		orderBy: "i.created_at DESC, i.id",
		after:   "(i.created_at < ? OR (i.created_at = ? AND i.id > ?))",
		args:    func(k []any) []any { return []any{k[0], k[0], k[1]} }}
	// Newest year first, no year last.
	orderYear = itemOrder{keys: []string{"(" + itemYear + " IS NULL)", "-COALESCE(" + itemYear + ", 0)", "i.id"}, kinds: "iis"}
	// Seasons by number, specials (0) and unknown numbers last.
	orderSeasons = itemOrder{keys: []string{"CASE WHEN i.idx IS NULL THEN 2 WHEN i.idx = 0 THEN 1 ELSE 0 END", "COALESCE(i.idx, 0)", "i.id"}, kinds: "iis"}
	// Episodes by number, then air date; either unknown sorts last.
	orderEpisodes = itemOrder{keys: []string{"(i.idx IS NULL)", "COALESCE(i.idx, 0)", "(i.air_date IS NULL)", "COALESCE(i.air_date, '')", "i.id"}, kinds: "iiiss"}
)

func (o itemOrder) order() string {
	if o.orderBy != "" {
		return o.orderBy
	}
	return strings.Join(o.keys, ", ")
}

func (o itemOrder) predicate(k []any) (string, []any) {
	if o.after != "" {
		return o.after, o.args(k)
	}
	return "(" + strings.Join(o.keys, ", ") + ") > (" + placeholders(len(k)) + ")", k
}

// valid reports whether cursor keys have the order's shape.
func (o itemOrder) valid(k []any) bool {
	if len(k) != len(o.kinds) {
		return false
	}
	for i, c := range o.kinds {
		switch k[i].(type) {
		case int64:
			if c != 'i' {
				return false
			}
		case string:
			if c != 's' {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

// versionOrderSQL orders an item's versions (v, its file f, f's root r) as
// LIB-2c decision 10 does: available first, then unlabelled, then by
// resolution (4k first), then by version ID. The first is the default.
const versionOrderSQL = `(f.state = 'present' AND r.state != 'offline') DESC, v.label IS NOT NULL,
	CASE f.resolution WHEN '4k' THEN 0 WHEN '1080p' THEN 1 WHEN '720p' THEN 2 WHEN 'sd' THEN 3 ELSE 4 END, v.id`

// itemColumns are the ItemSummary columns of item i, with its parent p and
// grandparent gp joined. available: a movie or episode is available when any
// version's file is present on a root that isn't offline (decision 9).
const itemColumns = `i.id, i.kind, i.library_id, i.parent_id, COALESCE(i.display_title, i.title),
	COALESCE(i.display_sort_title, i.sort_title), ` + itemYear + `, i.idx, i.created_at,
	(SELECT CASE WHEN f.duration_ms > 0 THEN f.duration_ms END FROM library_versions v
		JOIN library_files f ON f.id = v.file_id JOIN library_roots r ON r.id = f.root_id
		WHERE v.item_id = i.id ORDER BY ` + versionOrderSQL + ` LIMIT 1),
	CASE WHEN i.kind IN ('show', 'season') THEN (SELECT COUNT(*) FROM library_items c WHERE c.parent_id = i.id) END,
	CASE i.kind WHEN 'season' THEN COALESCE(p.display_title, p.title) WHEN 'episode' THEN COALESCE(gp.display_title, gp.title) END,
	CASE WHEN i.kind = 'episode' THEN p.idx END,
	CASE WHEN i.kind IN ('movie', 'episode') THEN EXISTS (SELECT 1 FROM library_versions v
		JOIN library_files f ON f.id = v.file_id JOIN library_roots r ON r.id = f.root_id
		WHERE v.item_id = i.id AND f.state = 'present' AND r.state != 'offline') END`

const itemJoins = ` FROM library_items i LEFT JOIN library_items p ON p.id = i.parent_id
	LEFT JOIN library_items gp ON gp.id = p.parent_id`

// itemFiles is the present files of item i and of its descendants (a show's
// seasons' episodes: two levels at most). Each file filter gets its own
// EXISTS: it matches when any one of them matches it (decision 6), so two
// filters may be met by different files or episodes.
const itemFiles = `EXISTS (SELECT 1 FROM library_versions v JOIN library_files f ON f.id = v.file_id
	WHERE v.item_id IN (SELECT i.id
		UNION ALL SELECT c.id FROM library_items c WHERE c.parent_id = i.id
		UNION ALL SELECT e.id FROM library_items c JOIN library_items e ON e.parent_id = c.id WHERE c.parent_id = i.id)
	AND f.state = 'present'`

// canonicalQuery is the query a cursor is bound to: every ItemQuery field
// except Cursor and Limit, with the effective sort.
func canonicalQuery(q ItemQuery, sort string) string {
	b, err := json.Marshal(struct {
		L, P, S, F, G, R, A string
		YF, YT              *int
		H                   *bool
	}{q.LibraryID, q.ParentID, sort, q.From, q.Genre, q.Resolution, q.AudioLanguage, q.YearFrom, q.YearTo, q.HDR})
	if err != nil {
		panic(err) // strings, ints and bools
	}
	return "items" + string(b)
}

func badRequest(msg string) error { return fmt.Errorf("%w: %s", ErrBadRequest, msg) }

// checkItemQuery applies the listItems 400 rules and returns the effective
// sort ("" for a parentId listing, which has its chosen order).
func checkItemQuery(q ItemQuery) (string, error) {
	if (q.LibraryID == "") == (q.ParentID == "") {
		return "", badRequest("pass exactly one of libraryId and parentId")
	}
	if q.Watched != nil {
		return "", badRequest("watched isn't available yet")
	}
	if q.ParentID != "" {
		switch {
		case q.SortGiven:
			return "", badRequest("sort doesn't apply with parentId")
		case q.From != "":
			return "", badRequest("from doesn't apply with parentId")
		case q.Genre != "" || q.Resolution != "" || q.AudioLanguage != "" || q.YearFrom != nil || q.YearTo != nil || q.HDR != nil:
			return "", badRequest("filters don't apply with parentId")
		}
		return "", nil
	}
	sort := q.Sort
	if sort == "" {
		sort = contracts.SortTitle
	}
	if sort != contracts.SortTitle && sort != contracts.SortAdded && sort != contracts.SortYear {
		return "", badRequest("sort must be title, added or year")
	}
	if q.From != "" {
		if sort != contracts.SortTitle {
			return "", badRequest("from needs sort=title")
		}
		if len(q.From) != 1 || (q.From != "#" && (q.From[0] < 'A' || q.From[0] > 'Z')) {
			return "", badRequest("from must be one letter A-Z or #")
		}
	}
	for _, y := range []*int{q.YearFrom, q.YearTo} {
		if y != nil && (*y < 1800 || *y > 9999) {
			return "", badRequest("years are 1800 to 9999")
		}
	}
	if q.YearFrom != nil && q.YearTo != nil && *q.YearFrom > *q.YearTo {
		return "", badRequest("yearFrom is after yearTo")
	}
	if q.Resolution != "" && !slices.Contains(resolutionOrder, q.Resolution) {
		return "", badRequest("resolution must be sd, 720p, 1080p or 4k")
	}
	if a := q.AudioLanguage; a != "" && (len(a) != 3 || strings.Trim(a, "abcdefghijklmnopqrstuvwxyz") != "") {
		return "", badRequest("audioLanguage must be an ISO 639-2/T code")
	}
	if n := utf8.RuneCountInString(q.Genre); n > 64 {
		return "", badRequest("genre is too long")
	}
	return sort, nil
}

// Items lists one page of a library's movies or shows, or of a parent's
// children, in the requested order. Total is set on the first page only. A
// library or parent the grant doesn't cover is ErrNotFound, the same as an
// unknown one; a request listItems can't apply is ErrBadRequest.
// Planned: LIB-2c (the catalogue API serves it).
func (s *Store) Items(ctx context.Context, g Grant, q ItemQuery) (contracts.ItemPage, error) {
	sort, err := checkItemQuery(q)
	if err != nil {
		return contracts.ItemPage{}, err
	}
	limit, err := pageLimit(q.Limit)
	if err != nil {
		return contracts.ItemPage{}, err
	}
	canon := canonicalQuery(q, sort)
	var keys []any
	if q.Cursor != "" {
		if keys, err = decodeCursor(q.Cursor, canon); err != nil {
			return contracts.ItemPage{}, err
		}
	}
	if q.LibraryID != "" && !g.allows(q.LibraryID) {
		return contracts.ItemPage{}, ErrNotFound
	}
	var out contracts.ItemPage
	err = s.read(ctx, func(tx *sql.Tx) error {
		// Scope: which items, in which order.
		var scope []string
		var scopeArgs []any
		var ord itemOrder
		if q.LibraryID != "" {
			var one int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM library_libraries WHERE id = ?`, q.LibraryID).Scan(&one); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrNotFound
				}
				return err
			}
			scope, scopeArgs = []string{"i.library_id = ?", "i.parent_id IS NULL"}, []any{q.LibraryID}
			ord = map[string]itemOrder{contracts.SortTitle: orderTitle, contracts.SortAdded: orderAdded, contracts.SortYear: orderYear}[sort]
		} else {
			var libID, kind string
			if err := tx.QueryRowContext(ctx, `SELECT library_id, kind FROM library_items WHERE id = ?`, q.ParentID).Scan(&libID, &kind); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrNotFound
				}
				return err
			}
			if !g.allows(libID) {
				return ErrNotFound
			}
			// Only after the grant check, so a hidden movie is the same 404 as an unknown ID.
			switch kind {
			case contracts.KindShow:
				ord = orderSeasons
			case contracts.KindSeason:
				ord = orderEpisodes
			default:
				return badRequest("parentId must be a show or a season")
			}
			scope, scopeArgs = []string{"i.parent_id = ?"}, []any{q.ParentID}
		}

		// Filters: bound parameters only.
		conds, args := slices.Clone(scope), slices.Clone(scopeArgs)
		if q.From != "" && q.From != "#" {
			// Group 1 keys start with a-z, so >= the letter is "that letter or later".
			conds = append(conds, "i.sort_group = 1", "i.sort_key >= ?")
			args = append(args, strings.ToLower(q.From))
		}
		if q.Genre != "" {
			conds = append(conds, "EXISTS (SELECT 1 FROM library_item_genres g WHERE g.item_id = i.id AND g.genre = ?)")
			args = append(args, q.Genre)
		}
		if q.YearFrom != nil {
			conds = append(conds, itemYear+" >= ?")
			args = append(args, *q.YearFrom)
		}
		if q.YearTo != nil {
			conds = append(conds, itemYear+" <= ?")
			args = append(args, *q.YearTo)
		}
		var fileConds []string
		var fileArgs [][]any
		if q.Resolution != "" {
			fileConds = append(fileConds, "f.resolution = ?")
			fileArgs = append(fileArgs, []any{q.Resolution})
		}
		if q.HDR != nil {
			if *q.HDR {
				fileConds = append(fileConds, "f.hdr = 1")
			} else {
				// hdr = 0 without a resolution is a file with no video: not SDR.
				fileConds = append(fileConds, "f.hdr = 0 AND f.resolution IS NOT NULL")
			}
			fileArgs = append(fileArgs, nil)
		}
		if q.AudioLanguage != "" {
			fileConds = append(fileConds, "EXISTS (SELECT 1 FROM library_file_audio a WHERE a.file_id = f.id AND a.lang = ?)")
			fileArgs = append(fileArgs, []any{q.AudioLanguage})
		}
		for i, fc := range fileConds {
			conds = append(conds, itemFiles+" AND "+fc+")")
			args = append(args, fileArgs[i]...)
		}
		where := " WHERE " + strings.Join(conds, " AND ")

		if q.Cursor == "" {
			var total int64
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM library_items i`+where, args...).Scan(&total); err != nil {
				return err
			}
			out.Total = &total
		}

		pageConds, pageArgs := where, slices.Clone(args)
		if keys != nil {
			if len(keys) == 1 && !ord.valid(keys) {
				// A long cursor carries only the last item's ID: re-read its keys.
				// Accepted: if that item's sort values changed since the last page
				// (SetDisplay), paging resumes from its new position and may repeat
				// or skip items. Only titles too long for a 512-byte cursor do this.
				id, ok := keys[0].(string)
				if !ok {
					return badRequest("cursor is not valid")
				}
				if keys, err = itemKeys(ctx, tx, ord, scope, append(slices.Clone(scopeArgs), id)); err != nil {
					return err
				}
			}
			if !ord.valid(keys) {
				return badRequest("cursor is not valid")
			}
			p, pa := ord.predicate(keys)
			pageConds += " AND " + p
			pageArgs = append(pageArgs, pa...)
		}
		pageArgs = append(pageArgs, limit+1)
		rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+`, `+strings.Join(ord.keys, ", ")+itemJoins+pageConds+
			` ORDER BY `+ord.order()+` LIMIT ?`, pageArgs...)
		if err != nil {
			return err
		}
		defer rows.Close()
		var last []any
		for rows.Next() {
			if len(out.Items) == limit {
				c := encodeCursor(canon, last...)
				if len(c) > maxCursor {
					c = encodeCursor(canon, out.Items[limit-1].ID)
				}
				out.NextCursor = &c
				break
			}
			it, k, err := scanItem(rows, len(ord.keys))
			if err != nil {
				return err
			}
			out.Items, last = append(out.Items, it), k
		}
		return rows.Err()
	})
	if err != nil {
		return contracts.ItemPage{}, err
	}
	if out.Items == nil {
		out.Items = []contracts.ItemSummary{}
	}
	return out, nil
}

// itemKeys reads an order's keys for the item a short cursor names; an item
// that's gone or outside the listing makes the cursor invalid.
func itemKeys(ctx context.Context, tx *sql.Tx, ord itemOrder, scope []string, args []any) ([]any, error) {
	dest := make([]any, len(ord.keys))
	ptrs := make([]any, len(ord.keys))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	err := tx.QueryRowContext(ctx, `SELECT `+strings.Join(ord.keys, ", ")+` FROM library_items i WHERE `+
		strings.Join(scope, " AND ")+` AND i.id = ?`, args...).Scan(ptrs...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, badRequest("cursor is not valid")
	}
	if err != nil {
		return nil, err
	}
	return normKeys(dest), nil
}

// normKeys makes scanned key values the types a decoded cursor has.
func normKeys(k []any) []any {
	for i, v := range k {
		if b, ok := v.([]byte); ok {
			k[i] = string(b)
		}
	}
	return k
}

func scanItem(rows *sql.Rows, nKeys int) (contracts.ItemSummary, []any, error) {
	var it contracts.ItemSummary
	var parent, showTitle sql.NullString
	var year, idx, duration, children, seasonIdx, avail sql.NullInt64
	var created int64
	keys := make([]any, nKeys)
	dest := []any{&it.ID, &it.Kind, &it.LibraryID, &parent, &it.Title, &it.SortTitle, &year, &idx, &created,
		&duration, &children, &showTitle, &seasonIdx, &avail}
	for i := range keys {
		dest = append(dest, &keys[i])
	}
	if err := rows.Scan(dest...); err != nil {
		return it, nil, err
	}
	if parent.Valid {
		it.ParentID = &parent.String
	}
	if year.Valid { // itemYear is NULL outside 1800-9999
		it.Year = ptrTo(int(year.Int64))
	}
	if idx.Valid && idx.Int64 >= 0 && (it.Kind == contracts.KindSeason || it.Kind == contracts.KindEpisode) {
		it.Index = ptrTo(int(idx.Int64))
	}
	it.AddedAt = time.UnixMilli(created).UTC()
	if duration.Valid {
		it.DurationMs = &duration.Int64
	}
	if children.Valid {
		it.ChildCount = &children.Int64
	}
	if showTitle.Valid {
		it.ShowTitle = &showTitle.String
	}
	if seasonIdx.Valid && seasonIdx.Int64 >= 0 {
		it.SeasonIndex = &seasonIdx.Int64
	}
	if avail.Valid {
		it.Available = ptrTo(avail.Int64 == 1)
	}
	return it, normKeys(keys), nil
}

func ptrTo[T any](v T) *T { return &v }

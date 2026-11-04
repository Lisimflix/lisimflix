package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

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

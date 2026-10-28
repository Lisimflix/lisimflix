package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// Outcomes of matching one item.
const (
	OutcomeMatched  = "matched"  // a record was applied
	OutcomeReview   = "review"   // no confident match: candidates wait for the admin
	OutcomeConflict = "conflict" // hints or a locked ID disagree: the admin decides
	OutcomeRetry    = "retry"    // the provider failed or the item changed: queued again
	OutcomeSkipped  = "skipped"  // the provider is paused: nothing was done
)

// Matcher matches catalogue items to Provider's records under §3.7's
// precedence (Decision 4): a locked ID, then ID hints, then a confident
// search result, else the review queue. No transaction spans a provider call.
// Planned: META-1c (serve's job loop and the fix-match endpoint build it).
type Matcher struct {
	Store    *Store
	Provider Provider
	Media    *os.Root // the media root, for NFO files
}

// Batch counts the outcomes of a RunDue.
type Batch struct {
	Matched, Review, Conflict, Retry int
	Failed                           []ItemError
}

// ItemError is an item whose matching failed with a database error.
type ItemError struct{ ItemID, Err string }

// itemInfo is what matching reads about an item before asking the provider.
type itemInfo struct {
	kind, title string
	year        int
	rev         int64
	attempts    int
	locked      map[string]string   // locked IDs by namespace
	hints       map[string][]string // name hints by namespace (LIB-2a)
	nfos        map[string][]string // NFO paths by root path
}

// MatchItem matches one movie or show and returns its outcome. A non-nil
// error takes precedence over the outcome: it is ErrItemNotFound, ErrInvalid
// (not a movie or show), or a database error (with OutcomeSkipped if the
// provider refused the credentials but couldn't be paused). Provider failures
// are outcomes, not errors: OutcomeRetry (backed off), or (OutcomeSkipped,
// nil) when the provider is paused, now or by this call.
// Planned: META-1c (the metadata job loop and the fix-match endpoint run them).
func (m *Matcher) MatchItem(ctx context.Context, itemID string) (string, error) {
	prov := m.Provider.Name()
	if paused, _, err := m.Store.Paused(ctx, prov); err != nil {
		return "", err
	} else if paused {
		return OutcomeSkipped, nil
	}
	info, err := m.Store.itemInfo(ctx, itemID)
	if err != nil {
		return "", err
	}
	namespaces := m.Provider.Namespaces()

	// 1. The admin's locked ID wins; hints are ignored.
	for _, ns := range namespaces {
		if id, ok := info.locked[ns]; ok {
			out, err := m.lookup(ctx, itemID, info, ns, id, nil, nil)
			if out != "" || err != nil {
				return out, err
			}
			return m.Store.setStatus(ctx, itemID, info.rev, contracts.MatchConflict,
				fmt.Sprintf("the locked %s ID %s isn't a %s at %s", ns, id, info.kind, prov))
		}
	}

	// 2. ID hints from names and NFO files.
	hints, conflicts := m.hints(info)
	for _, ns := range namespaces {
		if vs := hints[ns]; len(vs) > 1 {
			return m.Store.setStatus(ctx, itemID, info.rev, contracts.MatchConflict,
				fmt.Sprintf("the hints disagree on the %s ID: %s", ns, strings.Join(vs, ", ")))
		}
		if slices.Contains(conflicts, ns) {
			return m.Store.setStatus(ctx, itemID, info.rev, contracts.MatchConflict,
				fmt.Sprintf("the NFO files disagree on the %s ID", ns))
		}
	}
	check := map[string]string{}
	for ns, vs := range hints {
		if len(vs) == 1 && !slices.Contains(conflicts, ns) {
			check[ns] = vs[0]
		}
	}
	conflict := func(reason string) (string, error) {
		return m.Store.setStatus(ctx, itemID, info.rev, contracts.MatchConflict, reason)
	}
	for _, ns := range namespaces {
		if id, ok := check[ns]; ok {
			out, err := m.lookup(ctx, itemID, info, ns, id, check, conflict)
			if out != "" || err != nil {
				return out, err
			}
			delete(check, ns) // unknown, or another kind: try the next hint
		}
	}

	// 3. A confident search result that agrees with the remaining hints,
	// else 4. the review queue.
	q := Query{Kind: info.kind, Title: info.title, Year: info.year}
	cands, err := m.Provider.Search(ctx, q)
	if err != nil {
		return m.failed(ctx, itemID, info, err)
	}
	ranked, auto := Rank(q, cands)
	if len(ranked) == 0 {
		return m.Store.review(ctx, itemID, info.rev, nil, "no search results")
	}
	if auto {
		toReview := func(reason string) (string, error) { return m.Store.review(ctx, itemID, info.rev, ranked, reason) }
		out, err := m.lookup(ctx, itemID, info, ranked[0].Provider, ranked[0].ID, check, toReview)
		if out != "" || err != nil {
			return out, err
		}
	}
	return m.Store.review(ctx, itemID, info.rev, ranked, "")
}

// hints merges the item's name hints with the IDs in its NFO files, with
// numeric IDs written without leading zeros ({tmdb-0949} is 949). An NFO that
// can't be read is skipped. conflicts lists namespaces the NFO files disagree
// on.
func (m *Matcher) hints(info itemInfo) (map[string][]string, []string) {
	hints := map[string][]string{}
	add := func(ns, v string) {
		if strings.Trim(v, "0123456789") == "" {
			if v = strings.TrimLeft(v, "0"); v == "" {
				return
			}
		}
		if !slices.Contains(hints[ns], v) {
			hints[ns] = append(hints[ns], v)
		}
	}
	for ns, vs := range info.hints {
		for _, v := range vs {
			add(ns, v)
		}
	}
	var conflicts []string
	if m.Media == nil {
		return hints, nil
	}
	for root, paths := range info.nfos {
		r, err := m.Media.OpenRoot(root)
		if err != nil {
			continue
		}
		ids, bad := readNFOs(r, paths)
		r.Close()
		conflicts = append(conflicts, bad...)
		for ns, v := range ids {
			add(ns, v)
		}
	}
	return hints, conflicts
}

// lookup fetches the record for ns:id and applies it. It returns "" with no
// error when the provider doesn't know it or it's another kind, so the
// caller falls back. A record reporting an ID that differs from a hint in
// check is never applied: clash settles the item with the reason instead (a
// conflict for a hint's record, review for a search result's).
func (m *Matcher) lookup(ctx context.Context, itemID string, info itemInfo, ns, id string, check map[string]string,
	clash func(reason string) (string, error)) (string, error) {
	prov := m.Provider.Name()
	rec, err := m.Provider.Lookup(ctx, info.kind, ns, id)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return m.failed(ctx, itemID, info, err)
	}
	if rec.Kind != info.kind {
		return "", nil
	}
	for hns, hint := range check {
		reported := rec.ExternalIDs[hns]
		if hns == prov {
			reported = rec.ID
		}
		if reported != "" && reported != hint {
			return clash(fmt.Sprintf("%s %s is %s %s, but the hints say %s %s", ns, id, hns, reported, hns, hint))
		}
	}
	out, err := m.Store.applyRecord(ctx, itemID, info.rev, prov, rec)
	if errors.Is(err, errStale) {
		return OutcomeRetry, m.Store.retryLater(ctx, itemID, 0, 0, err.Error())
	}
	return out, err
}

// failed handles a provider error: an auth error pauses the provider and
// leaves the item queued as it was (if pausing fails, the error comes with
// OutcomeSkipped so RunDue stops); anything else retries with backoff, unless
// the item was edited during the call.
func (m *Matcher) failed(ctx context.Context, itemID string, info itemInfo, err error) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if errors.Is(err, ErrAuth) {
		if perr := m.Store.Pause(ctx, m.Provider.Name(), err.Error()); perr != nil {
			return OutcomeSkipped, fmt.Errorf("metadata: pausing %s after %v: %w", m.Provider.Name(), err, perr)
		}
		return OutcomeSkipped, nil
	}
	return OutcomeRetry, m.Store.retryLater(ctx, itemID, info.rev, info.attempts+1, err.Error())
}

// RunDue matches up to limit due items, oldest first, each in its own
// transactions. A database error on one item goes in Batch.Failed, the item
// is backed off like a provider failure, and the batch goes on; a paused
// provider stops it with ErrPaused, a failure to pause it stops it with that
// error, and so does ctx.
// Planned: META-1c (the metadata job loop and the fix-match endpoint run them).
func (m *Matcher) RunDue(ctx context.Context, limit int) (Batch, error) {
	var b Batch
	if limit <= 0 {
		return b, nil
	}
	rows, err := m.Store.db.QueryContext(ctx, `SELECT item_id, attempts FROM metadata_items WHERE next_attempt_at <= ?
		ORDER BY next_attempt_at, item_id LIMIT ?`, ms(m.Store.now()), limit)
	if err != nil {
		return b, err
	}
	type dueItem struct {
		id       string
		attempts int
	}
	var due []dueItem
	for rows.Next() {
		var d dueItem
		if err := rows.Scan(&d.id, &d.attempts); err != nil {
			rows.Close()
			return b, err
		}
		due = append(due, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return b, err
	}
	for _, d := range due {
		if err := ctx.Err(); err != nil {
			return b, err
		}
		id := d.id
		out, err := m.MatchItem(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return b, ctx.Err()
			}
			if out == OutcomeSkipped { // the provider refused us but couldn't be paused
				return b, err
			}
			b.Failed = append(b.Failed, ItemError{ItemID: id, Err: err.Error()})
			if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrItemNotFound) {
				// Best effort, so the item isn't first in every batch.
				_ = m.Store.retryLater(ctx, id, 0, d.attempts+1, err.Error())
			}
			continue
		}
		switch out {
		case OutcomeMatched:
			b.Matched++
		case OutcomeReview:
			b.Review++
		case OutcomeConflict:
			b.Conflict++
		case OutcomeRetry:
			b.Retry++
		case OutcomeSkipped:
			return b, ErrPaused
		}
	}
	return b, nil
}

// itemInfo reads the item, creating its metadata row (queued) if missing.
// Only movies and shows are matched: another kind gives ErrInvalid and is
// taken off the queue if an edit put it there.
func (s *Store) itemInfo(ctx context.Context, itemID string) (itemInfo, error) {
	info := itemInfo{locked: map[string]string{}, hints: map[string][]string{}, nfos: map[string][]string{}}
	now := ms(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return info, err
	}
	defer tx.Rollback()
	var year sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT kind, title, year FROM library_items WHERE id = ?`, itemID).Scan(&info.kind, &info.title, &year)
	if errors.Is(err, sql.ErrNoRows) {
		return info, ErrItemNotFound
	}
	if err != nil {
		return info, err
	}
	info.year = int(year.Int64)
	if info.kind != "movie" && info.kind != "show" {
		if _, err := tx.ExecContext(ctx, `UPDATE metadata_items SET next_attempt_at = NULL WHERE item_id = ?`, itemID); err != nil {
			return info, err
		}
		if err := tx.Commit(); err != nil {
			return info, err
		}
		return info, invalid("%s is a %s: only movies and shows are matched", itemID, info.kind)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO metadata_items(item_id, revision, match_status, next_attempt_at, updated_at)
		VALUES(?, 1, 'unmatched', ?, ?)`, itemID, now, now); err != nil {
		return info, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT revision, attempts FROM metadata_items WHERE item_id = ?`, itemID).Scan(&info.rev, &info.attempts); err != nil {
		return info, err
	}
	if err := scanPairs(ctx, tx, func(ns, v string) { info.locked[ns] = v },
		`SELECT namespace, value FROM metadata_external_ids WHERE item_id = ? AND locked = 1`, itemID); err != nil {
		return info, err
	}
	if err := scanPairs(ctx, tx, func(ns, v string) { info.hints[ns] = append(info.hints[ns], v) },
		`SELECT namespace, value FROM library_id_hints WHERE item_id = ? ORDER BY namespace, value`, itemID); err != nil {
		return info, err
	}
	// NFO files (Decision 11): beside each video, and movie.nfo or
	// tvshow.nfo in the item's own folders. A folder shared with other items
	// (several loose films) names none of them, so its movie.nfo is ignored.
	addNFO := func(root, p string) {
		if !slices.Contains(info.nfos[root], p) {
			info.nfos[root] = append(info.nfos[root], p)
		}
	}
	if info.kind == "movie" {
		if err := scanPairs(ctx, tx, func(root, file string) { addNFO(root, strings.TrimSuffix(file, path.Ext(file))+".nfo") },
			`SELECT r.path, f.path FROM library_versions v JOIN library_files f ON f.id = v.file_id
			JOIN library_roots r ON r.id = f.root_id WHERE v.item_id = ? AND f.state = 'present' ORDER BY r.path, f.path`, itemID); err != nil {
			return info, err
		}
	}
	folderNFO := map[string]string{"movie": "movie.nfo", "show": "tvshow.nfo"}[info.kind]
	if err := scanPairs(ctx, tx, func(root, folder string) { addNFO(root, path.Join(folder, folderNFO)) },
		`SELECT r.path, f.folder FROM library_folders f JOIN library_roots r ON r.id = f.root_id
		WHERE f.item_id = ? AND f.folder != '' AND NOT EXISTS (SELECT 1 FROM library_folders o
			WHERE o.root_id = f.root_id AND o.folder = f.folder AND o.item_id != f.item_id)
		ORDER BY r.path, f.folder`, itemID); err != nil {
		return info, err
	}
	return info, tx.Commit()
}

// scanPairs runs a query returning two text columns and calls each per row.
func scanPairs(ctx context.Context, tx *sql.Tx, each func(a, b string), query string, args ...any) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return err
		}
		each(a, b)
	}
	return rows.Err()
}

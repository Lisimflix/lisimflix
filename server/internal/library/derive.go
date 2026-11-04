package library

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
	"github.com/Lisimflix/lisimflix/server/internal/textfold"
)

// backfillBatch bounds the rows one backfill transaction touches (§10.2).
const backfillBatch = 500

// sortKey is the stored browse order of an item's sort title.
func sortKey(sortTitle string) (key string, group int) {
	key = textfold.Title(sortTitle)
	return key, textfold.Group(key)
}

// storeFacts writes a file's derived columns and audio languages. It runs in
// the transaction that stores the probe result, so they never disagree. A
// probe with no video stores hdr = 0 and a NULL resolution, so hdr = 0 alone
// doesn't mean "has SDR video": facet code must also require a resolution.
func storeFacts(ctx context.Context, tx *sql.Tx, fileID string, f fileFacts) error {
	hdr := 0
	if f.HDR {
		hdr = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_files SET resolution = NULLIF(?, ''), hdr = ?, duration_ms = ? WHERE id = ?`,
		f.Resolution, hdr, f.DurationMs, fileID); err != nil {
		return err
	}
	return replaceAudio(ctx, tx, fileID, f.AudioLangs)
}

// clearFacts removes a file's derived facts when its probe is gone or failed:
// they described a probe that no longer applies.
func clearFacts(ctx context.Context, tx *sql.Tx, fileID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE library_files SET resolution = NULL, hdr = NULL, duration_ms = NULL WHERE id = ?`, fileID); err != nil {
		return err
	}
	return replaceAudio(ctx, tx, fileID, nil)
}

func replaceAudio(ctx context.Context, tx *sql.Tx, fileID string, langs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_file_audio WHERE file_id = ?`, fileID); err != nil {
		return err
	}
	for _, l := range langs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_file_audio(file_id, lang) VALUES(?, ?)`, fileID, l); err != nil {
			return err
		}
	}
	return nil
}

// Display holds the values META-1c applies to an item. A nil field leaves the
// stored value as it is; an empty non-nil Genres clears them.
type Display struct {
	Title, SortTitle *string
	Year             *int
	Genres           []string
}

// SetDisplay applies display values to an item and refreshes its sort key.
// Planned: META-1c (applies matched titles, years and genres).
func (s *Store) SetDisplay(ctx context.Context, itemID string, d Display) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE library_items SET display_title = COALESCE(?, display_title),
		display_sort_title = COALESCE(?, display_sort_title), display_year = COALESCE(?, display_year) WHERE id = ?`,
		d.Title, d.SortTitle, d.Year, itemID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	var st string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(display_sort_title, sort_title) FROM library_items WHERE id = ?`, itemID).Scan(&st); err != nil {
		return err
	}
	key, group := sortKey(st)
	if _, err := tx.ExecContext(ctx, `UPDATE library_items SET sort_key = ?, sort_group = ? WHERE id = ?`, key, group, itemID); err != nil {
		return err
	}
	if d.Genres != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM library_item_genres WHERE item_id = ?`, itemID); err != nil {
			return err
		}
		for _, g := range d.Genres {
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_item_genres(item_id, genre) VALUES(?, ?) ON CONFLICT DO NOTHING`, itemID, g); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// BackfillSortKeys fills the sort key and group of items that predate them,
// 500 per transaction, and reports how many it filled. Serve runs it before
// serving the catalogue: listItems pages on these keys, and a NULL key would
// sort first and break the next page's cursor.
func (s *Store) BackfillSortKeys(ctx context.Context) (int, error) {
	items := 0
	for {
		n, err := s.backfillItems(ctx)
		items += n
		if err != nil || n < backfillBatch {
			return items, err
		}
	}
}

// BackfillDerived fills the derived columns of rows that predate them: items
// without a sort key and probed files without file facts, 500 rows per
// transaction. It reports how many of each it filled.
func (s *Store) BackfillDerived(ctx context.Context) (items, files int, err error) {
	items, err = s.BackfillSortKeys(ctx)
	if err != nil {
		return items, files, err
	}
	for {
		n, err := s.backfillFiles(ctx)
		files += n
		if err != nil {
			return items, files, err
		}
		if n < backfillBatch {
			return items, files, nil
		}
	}
}

func (s *Store) backfillItems(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	type row struct{ id, title string }
	var batch []row
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(display_sort_title, sort_title) FROM library_items WHERE sort_key IS NULL LIMIT ?`, backfillBatch)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.title); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, r)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, r := range batch {
		key, group := sortKey(r.title)
		if _, err := tx.ExecContext(ctx, `UPDATE library_items SET sort_key = ?, sort_group = ? WHERE id = ?`, key, group, r.id); err != nil {
			return 0, err
		}
	}
	return len(batch), tx.Commit()
}

// backfillFiles keys on hdr, which a stored probe always sets, so a probe with
// no video stream (NULL resolution) isn't picked again.
func (s *Store) backfillFiles(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	type row struct{ id, js string }
	var batch []row
	rows, err := tx.QueryContext(ctx, `SELECT id, probe_json FROM library_files WHERE probe_json IS NOT NULL AND hdr IS NULL LIMIT ?`, backfillBatch)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.js); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, r)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, r := range batch {
		var res probe.Result
		// An unreadable stored result gets empty facts rather than being retried forever.
		_ = json.Unmarshal([]byte(r.js), &res)
		if err := storeFacts(ctx, tx, r.id, deriveFile(res)); err != nil {
			return 0, err
		}
	}
	return len(batch), tx.Commit()
}

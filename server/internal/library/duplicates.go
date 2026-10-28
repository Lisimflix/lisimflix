package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/library/naming"
)

// groupItem returns the movie or show item for e's folder, creating it if
// needed. A folder already mapped to an item always uses it. A new folder
// whose key another folder's item already has gets a provisional item of its
// own and a pending duplicate pair, which settle resolves after a completed
// scan (Pete's rule on #90: two folders that exist at once are never merged
// automatically).
func (s *Store) groupItem(ctx context.Context, tx *sql.Tx, root Root, e naming.Entry, it item) (id, key string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT f.item_id, i.scan_key FROM library_folders f JOIN library_items i ON i.id = f.item_id
		WHERE f.root_id = ? AND f.folder = ? AND f.scan_key = ?`, root.ID, e.Folder, e.Key).Scan(&id, &key)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return id, key, err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM library_items WHERE library_id = ? AND scan_key = ?`, root.LibraryID, e.Key).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		it.key = e.Key
	case err != nil:
		return "", "", err
	default:
		// Provisional until settle decides. It must not start with e.Key + "|",
		// or it would look like one of the existing item's seasons.
		it.key = "dup:" + root.ID + ":" + e.Folder + ":" + e.Key
	}
	if id, err = s.item(ctx, tx, root.LibraryID, it); err != nil {
		return "", "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO library_folders(root_id, folder, scan_key, item_id) VALUES(?, ?, ?, ?)`,
		root.ID, e.Folder, e.Key, id); err != nil {
		return "", "", err
	}
	if existing != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO library_duplicates(id, root_id, item_id, other_id, state) VALUES(?, ?, ?, ?, 'pending')`,
			newID("dup"), root.ID, existing, id); err != nil {
			return "", "", err
		}
	}
	return id, it.key, nil
}

// settle resolves the root's pending duplicate pairs after a completed scan,
// once files that weren't found have been marked missing. If the new folder
// replaced a folder that is gone (a rename or move), it is folded into that
// folder's item, keeping its identity; see renamed. Otherwise both folders
// exist and the pair is flagged for the admin. A scoped scan (flag false)
// only folds: the old folder may simply not have been rescanned yet, so a
// live pair stays pending until a whole-root scan.
func (s *Store) settle(ctx context.Context, rootID string, flag bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, item_id, other_id FROM library_duplicates WHERE root_id = ? AND state = 'pending'`, rootID)
	if err != nil {
		return err
	}
	type pair struct{ id, item, other string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.item, &p.other); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	for _, p := range pairs {
		into, err := renamed(ctx, tx, p.other)
		if err != nil {
			return err
		}
		switch {
		case into != "":
			err = dropGoneFolders(ctx, tx, into)
			if err == nil {
				err = fold(ctx, tx, into, p.other)
			}
		case flag:
			_, err = tx.ExecContext(ctx, `UPDATE library_duplicates SET state = 'flagged' WHERE id = ?`, p.id)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// renamed returns the item whose folder the new folder of item other
// replaced, or "" if there is none or more than one. The candidates are every
// item of the library mapped from the same naming key, not only the one
// whose key it clashed with: after an admin's "keep separate" or "merge",
// either folder can be renamed later. A candidate qualifies if one of its
// folders has no present file left (that folder is gone), or, for loose
// files, if it has no present file at all.
func renamed(ctx context.Context, tx *sql.Tx, other string) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT c.item_id FROM library_folders o
		JOIN library_roots orr ON orr.id = o.root_id
		JOIN library_folders c ON c.scan_key = o.scan_key
		JOIN library_roots cr ON cr.id = c.root_id AND cr.library_id = orr.library_id
		WHERE o.item_id = ? AND c.item_id != ?
		ORDER BY c.item_id`, other, other)
	if err != nil {
		return "", err
	}
	var cands []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		cands = append(cands, id)
	}
	rows.Close()
	var found []string
	for _, c := range cands {
		var gone bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM library_folders f WHERE f.item_id = ? AND f.folder != ''
			AND NOT EXISTS (SELECT 1 FROM library_files x WHERE x.root_id = f.root_id AND x.state = 'present'
				AND x.path >= f.folder || '/' AND x.path < f.folder || '0'))`, c).Scan(&gone); err != nil {
			return "", err
		}
		if !gone {
			alive, err := hasPresentFile(ctx, tx, c)
			if err != nil {
				return "", err
			}
			gone = !alive
		}
		if gone {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		return "", nil // none, or ambiguous: the admin decides
	}
	return found[0], nil
}

// dropGoneFolders removes the item's mappings for folders with no present
// file left: the folder a rename replaced. Kept, it would make every later
// folder with the same naming key look like another rename of it, and a
// coexisting copy would be folded in silently (Pete, #111). If the old
// folder comes back, it is a new folder and is flagged.
func dropGoneFolders(ctx context.Context, tx *sql.Tx, itemID string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM library_folders AS f WHERE f.item_id = ? AND f.folder != ''
		AND NOT EXISTS (SELECT 1 FROM library_files x WHERE x.root_id = f.root_id AND x.state = 'present'
			AND x.path >= f.folder || '/' AND x.path < f.folder || '0')`, itemID)
	return err
}

// hasPresentFile reports whether any version under the item (or, for a
// show, under its seasons and episodes) is on a present file. Scan keys are
// unique per library only, so children are matched within the item's own
// library (#103).
func hasPresentFile(ctx context.Context, tx *sql.Tx, itemID string) (bool, error) {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM library_versions v
		JOIN library_files f ON f.id = v.file_id JOIN library_items i ON i.id = v.item_id
		JOIN library_items top ON top.id = ?
		WHERE f.state = 'present' AND i.library_id = top.library_id
			AND (i.id = top.id OR substr(i.scan_key, 1, length(top.scan_key) + 1) = top.scan_key || '|'))`, itemID).Scan(&ok)
	return ok, err
}

// fold moves everything under item other into item into: versions, ID hints,
// folder mappings and, for a show, its seasons and episodes (merged with ones
// into already has, by key). Then other is deleted, with its pair.
func fold(ctx context.Context, tx *sql.Tx, into, other string) error {
	var intoKey, otherKey string
	if err := tx.QueryRowContext(ctx, `SELECT scan_key FROM library_items WHERE id = ?`, into).Scan(&intoKey); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT scan_key FROM library_items WHERE id = ?`, other).Scan(&otherKey); err != nil {
		return err
	}
	// Children in key order, so a season is handled before its episodes.
	// Scan keys are unique per library only: stay inside other's library.
	rows, err := tx.QueryContext(ctx, `SELECT id, scan_key FROM library_items
		WHERE library_id = (SELECT library_id FROM library_items WHERE id = ?)
			AND substr(scan_key, 1, length(?) + 1) = ? || '|' ORDER BY length(scan_key)`, other, otherKey, otherKey)
	if err != nil {
		return err
	}
	type child struct{ id, key string }
	var children []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.id, &c.key); err != nil {
			rows.Close()
			return err
		}
		children = append(children, c)
	}
	rows.Close()
	for _, c := range children {
		target := intoKey + strings.TrimPrefix(c.key, otherKey)
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM library_items WHERE scan_key = ? AND library_id = (SELECT library_id FROM library_items WHERE id = ?)`,
			target, into).Scan(&existing)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Move it across: new key, parent = the item now at its parent key.
			parentKey := target[:strings.LastIndex(target, "|")]
			// The alias matters: inside the subquery, library_items is the
			// subquery's own table, so the parent must be matched to c.
			if _, err := tx.ExecContext(ctx, `UPDATE library_items AS c SET scan_key = ?, parent_id = (SELECT p.id FROM library_items p
				WHERE p.scan_key = ? AND p.library_id = c.library_id) WHERE c.id = ?`, target, parentKey, c.id); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if err := moveInto(ctx, tx, existing, c.id); err != nil {
				return err
			}
		}
	}
	// Move the folder mappings before moveInto deletes other: they cascade.
	if _, err := tx.ExecContext(ctx, `UPDATE library_folders SET item_id = ? WHERE item_id = ?`, into, other); err != nil {
		return err
	}
	return moveInto(ctx, tx, into, other)
}

// moveInto gives item into the versions and ID hints of item from, then
// deletes from (its children have already been moved).
func moveInto(ctx context.Context, tx *sql.Tx, into, from string) error {
	for _, q := range []string{
		`UPDATE OR IGNORE library_versions SET item_id = ? WHERE item_id = ?`,
		`INSERT OR IGNORE INTO library_id_hints(item_id, namespace, value) SELECT ?, namespace, value FROM library_id_hints WHERE item_id = ?`,
		`UPDATE library_items SET parent_id = ? WHERE parent_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, into, from); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM library_items WHERE id = ?`, from)
	return err
}

// Duplicate is a possible duplicate the admin decides: two folders of the
// library whose names give the same movie or show.
type Duplicate struct {
	ID, ItemID, OtherID, Title string
}

// Duplicates lists the library's flagged pairs.
// Planned: LIB-2c (the admin duplicates screen).
func (s *Store) Duplicates(ctx context.Context, libID string) ([]Duplicate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.item_id, d.other_id, i.title FROM library_duplicates d
		JOIN library_items i ON i.id = d.item_id WHERE i.library_id = ? AND d.state = 'flagged' ORDER BY i.sort_title, d.id`, libID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Duplicate
	for rows.Next() {
		var d Duplicate
		if err := rows.Scan(&d.ID, &d.ItemID, &d.OtherID, &d.Title); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Decide records the admin's decision on a flagged pair. Merge folds the
// other folder's item into the existing one (one item, both folders'
// versions); otherwise both stay separate. Either way the folders keep their
// mapping, so the pair isn't flagged again.
// Planned: LIB-2c (the admin duplicates screen).
func (s *Store) Decide(ctx context.Context, dupID string, merge bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var item, other string
	err = tx.QueryRowContext(ctx, `SELECT item_id, other_id FROM library_duplicates WHERE id = ? AND state = 'flagged'`, dupID).Scan(&item, &other)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: no flagged duplicate %s", ErrNotFound, dupID)
	}
	if err != nil {
		return err
	}
	if merge {
		err = fold(ctx, tx, item, other)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE library_duplicates SET state = 'separate' WHERE id = ?`, dupID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

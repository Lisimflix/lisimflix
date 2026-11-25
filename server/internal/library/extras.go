package library

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/library/naming"
)

// placeExtra attaches an extra to its movie or show and clears the file's
// unplaced reason. It tries naming.Extra's folders in order, each an exact
// library_folders match for the root, never a parent: a movie's folder is its
// full path, so climbing would reach other movies. The first folder with
// matching rows decides; it must give exactly one item. A suffix name with a
// year counts only the row with its own key, so "Marvel/Thor (2011)-trailer"
// never attaches to Iron Man. Any other extra counts only an item the folder
// itself names (naming.NamesFolder), so "Marvel/Thor-trailer" stays unplaced
// next to a loose "Marvel/Iron Man (2008).mkv" (#238 review); so does
// "Marvel/Iron Man-trailer.mkv", which the admin places. Residual risk: a trailer titled differently from
// its folder's movie ("Star Wars (1977)-trailer" in "Star Wars Episode IV -
// A New Hope (1977)/") stays unplaced; the admin sees it in the unplaced list.
func (s *Store) placeExtra(ctx context.Context, tx *sql.Tx, root Root, fileID, rel string) (bool, error) {
	x, ok := naming.Extra(rel)
	if !ok {
		return false, nil
	}
	for _, folder := range x.Folders {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT item_id, scan_key FROM library_folders WHERE root_id = ? AND folder = ?`, root.ID, folder)
		if err != nil {
			return false, err
		}
		var items []string
		for rows.Next() {
			var id, k string
			if err := rows.Scan(&id, &k); err != nil {
				rows.Close()
				return false, err
			}
			// A name with a year must match the item's key; one without must be
			// in the item's own folder, not a collection folder it's loose in.
			if x.Key != "" && (k == "movie:"+x.Key || k == "show:"+x.Key) || x.Key == "" && naming.NamesFolder(folder, k) {
				if !slices.Contains(items, id) {
					items = append(items, id)
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, err
		}
		switch len(items) {
		case 0:
			continue // a season folder without a match never stops the climb
		case 1:
		default:
			return false, nil // ambiguous: the admin sees it unplaced
		}
		title := strings.TrimSpace(x.Title)
		if r := []rune(title); len(r) > 200 {
			title = string(r[:200])
		}
		if title == "" {
			title = string(x.Type)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_extras(id, item_id, file_id, type, title) VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(file_id) DO UPDATE SET item_id = excluded.item_id, type = excluded.type, title = excluded.title`,
			newID("ext"), items[0], fileID, string(x.Type), title); err != nil {
			return false, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE library_files SET unplaced = NULL WHERE id = ?`, fileID)
		return err == nil, err
	}
	return false, nil
}

// adopt is the post-walk pass of every scan, scoped ones included: it runs
// after settle so a file walked before its movie or show is attached in the
// same scan. Each present file still unplaced as 'extra' goes back through
// place, so Classify decides again (decision 6c): an episode or movie misread
// before LIB-3 is placed normally and a real extra reaches placeExtra. A
// whole-root scan ("." scope) tries every such file of the root; a scoped scan
// only those under its scope's top folder, the widest folder an extra there
// could attach to, so stuck extras elsewhere aren't classified again on every
// file event (#238 review). A file healed here goes through grouping after
// settle, so a duplicate pair it opens stays pending until the next scan
// (accepted). It returns how many files it tried, for tests.
//
// shortcut: Run.Unplaced counts a file when first seen unplaced and isn't decremented by adoption, fine because the file's own unplaced column is the truth; if the admin view ever shows the run counter, count the files adopt heals for Scan to subtract.
func (s *Store) adopt(ctx context.Context, rootID, scope string) (int, error) {
	root, libType, err := s.root(ctx, rootID)
	if err != nil {
		return 0, err
	}
	q := `SELECT id, path, size, mtime_ns FROM library_files WHERE root_id = ? AND state = 'present' AND unplaced = 'extra'`
	args := []any{rootID}
	if scope != "." {
		top, _, _ := strings.Cut(scope, "/")
		q += ` AND path >= ? AND path < ?` // under top ('0' sorts right after '/')
		args = append(args, top+"/", top+"0")
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY path`, args...)
	if err != nil {
		return 0, err
	}
	type file struct {
		id string
		f  File
	}
	var files []file
	for rows.Next() {
		var x file
		if err := rows.Scan(&x.id, &x.f.Path, &x.f.Rev.Size, &x.f.Rev.ModTime); err != nil {
			rows.Close()
			return 0, err
		}
		files = append(files, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := len(files)
	for len(files) > 0 {
		batch := files[:min(500, len(files))]
		files = files[len(batch):]
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return 0, err
		}
		var scratch Run // adoption is not counted again
		for _, x := range batch {
			if err := s.place(ctx, tx, root, libType, x.id, x.f, &scratch); err != nil {
				tx.Rollback()
				return 0, err
			}
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
	}
	// After healing, so a healed file's fresh version can pair in this scan.
	return n, s.adoptVersions(ctx, rootID, scope == ".")
}

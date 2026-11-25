package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Lisimflix/lisimflix/server/internal/library/naming"
)

// Version identity (LIB-3 Task 3). seq is the placement order within an item,
// never reused, so the newer file has the higher seq. ordinal numbers the
// versions that share a label (decision 6e's fold, null equals null); it is
// stored for ordering and flags, never shown. A version placed since the
// root's last whole-root scan is fresh: adoptVersions may move an older,
// missing version of the same item onto its file, so a rename or upgrade
// keeps the version ID (decision 2).

// ver is one version row as the version rules read it; label is "" for NULL.
type ver struct {
	id, fileID, label string
	seq, ord          int
	fresh, present    bool
}

// loadVersions reads every version of the item, in seq order.
func loadVersions(ctx context.Context, tx *sql.Tx, itemID string) ([]ver, error) {
	rows, err := tx.QueryContext(ctx, `SELECT v.id, v.file_id, COALESCE(v.label, ''), v.seq, v.ordinal, v.fresh, f.state = 'present'
		FROM library_versions v JOIN library_files f ON f.id = v.file_id WHERE v.item_id = ? ORDER BY v.seq, v.id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ver
	for rows.Next() {
		var v ver
		if err := rows.Scan(&v.id, &v.fileID, &v.label, &v.seq, &v.ord, &v.fresh, &v.present); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// sameLabel compares labels with decision 6e's ASCII fold, as SQLite lower() does.
func sameLabel(a, b string) bool { return naming.FoldLabel(a) == naming.FoldLabel(b) }

// sameClass reports whether two labels are in one adoption class (decision 2):
// resolution-only labels are one class; any other label must match.
func sameClass(a, b string) bool {
	ra, rb := naming.IsResolutionLabel(a), naming.IsResolutionLabel(b)
	if ra || rb {
		return ra && rb
	}
	return sameLabel(a, b)
}

// flagTarget is the version a version of label with ordinal below is flagged
// against: the group's lowest-ordinal version below it whose file is present,
// else the lowest-ordinal one below it. skip is left out; "" means none.
// "Below" keeps flags acyclic.
func flagTarget(vs []ver, label string, below int, skip string) string {
	var best *ver
	for i := range vs {
		v := &vs[i]
		if v.id == skip || v.ord >= below || !sameLabel(v.label, label) {
			continue
		}
		if best == nil || v.present && !best.present ||
			v.present == best.present && (v.ord < best.ord || v.ord == best.ord && v.id < best.id) {
			best = v
		}
	}
	if best == nil {
		return ""
	}
	return best.id
}

// setFlag flags versionID against ofID. It is an upsert: a version that was
// kept and is numbered again is flagged again.
func setFlag(ctx context.Context, tx *sql.Tx, versionID, ofID string) error {
	if ofID == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO library_version_flags(version_id, of_id, state) VALUES(?, ?, 'flagged')
		ON CONFLICT(version_id) DO UPDATE SET of_id = excluded.of_id, state = 'flagged'`, versionID, ofID)
	return err
}

// version places a file on an item. A file already on the item keeps its
// version (a file replaced in place). Otherwise it gets a fresh version with
// the next seq and the next ordinal of its label group, flagged when that
// ordinal is above 1. Adoption never happens here: during the walk the
// replaced file is still present.
func (s *Store) version(ctx context.Context, tx *sql.Tx, itemID, fileID, label string) error {
	vs, err := loadVersions(ctx, tx, itemID)
	if err != nil {
		return err
	}
	seq, ord := 1, 1
	for _, v := range vs {
		if v.fileID == fileID {
			return nil
		}
		seq = max(seq, v.seq+1)
		if sameLabel(v.label, label) {
			ord = max(ord, v.ord+1)
		}
	}
	id := newID("ver")
	if _, err := tx.ExecContext(ctx, `INSERT INTO library_versions(id, item_id, file_id, label, ordinal, seq, fresh)
		VALUES(?, ?, ?, NULLIF(?, ''), ?, ?, 1)`, id, itemID, fileID, label, ord, seq); err != nil {
		return err
	}
	if ord > 1 {
		return setFlag(ctx, tx, id, flagTarget(vs, label, ord, ""))
	}
	return nil
}

// adoptVersions runs after the walk, once files are marked missing and settle
// has folded renames. A fresh version F on a present file takes over N, an
// older (lower seq) version of the same item whose file isn't present, when
// the pairing is one-to-one: pass 1 by exact label, pass 2 by adoption class
// among the versions still unpaired. N keeps its ID on F's file. A whole-root
// scan then clears fresh for the root's versions; a scoped scan keeps it, so
// adoption doesn't depend on which scope a watcher visits first. Items are
// handled 500 per transaction.
func (s *Store) adoptVersions(ctx context.Context, rootID string, full bool) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT v.item_id FROM library_versions v JOIN library_files f ON f.id = v.file_id
		WHERE v.fresh = 1 AND f.root_id = ? AND f.state = 'present'`, rootID)
	if err != nil {
		return err
	}
	var items []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		items = append(items, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for len(items) > 0 {
		batch := items[:min(batchSize, len(items))]
		items = items[len(batch):]
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, id := range batch {
			if err := adoptItem(ctx, tx, id); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if !full {
		return nil
	}
	// One statement, not batched by 500 rows: an exception to spec §10.2's
	// bound, to measure in LIB-2c's scale run (#110).
	_, err = s.db.ExecContext(ctx, `UPDATE library_versions SET fresh = 0
		WHERE fresh = 1 AND file_id IN (SELECT id FROM library_files WHERE root_id = ?)`, rootID)
	return err
}

// adoptItem pairs and adopts the item's versions.
func adoptItem(ctx context.Context, tx *sql.Tx, itemID string) error {
	vs, err := loadVersions(ctx, tx, itemID)
	if err != nil {
		return err
	}
	paired := map[string]bool{}
	var pairs [][2]string // F, N
	for _, match := range []func(a, b string) bool{sameLabel, sameClass} {
		fc, nc := map[string][]string{}, map[string][]string{}
		for _, f := range vs {
			if !f.fresh || !f.present || paired[f.id] {
				continue
			}
			for _, n := range vs {
				if n.present || paired[n.id] || n.seq >= f.seq || !match(f.label, n.label) {
					continue
				}
				fc[f.id] = append(fc[f.id], n.id)
				nc[n.id] = append(nc[n.id], f.id)
			}
		}
		for _, f := range vs {
			if ns := fc[f.id]; len(ns) == 1 && len(nc[ns[0]]) == 1 {
				pairs = append(pairs, [2]string{f.id, ns[0]})
				paired[f.id], paired[ns[0]] = true, true
			}
		}
	}
	for _, p := range pairs {
		if err := adoptOne(ctx, tx, itemID, p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}

// adoptOne moves version N onto version F's file and deletes F. F is deleted
// before N takes its file, or UNIQUE (item_id, file_id) would fail. N always
// takes F's seq and fresh, so the newer file keeps the higher seq.
func adoptOne(ctx context.Context, tx *sql.Tx, itemID, fID, nID string) error {
	vs, err := loadVersions(ctx, tx, itemID) // an earlier adoption may have renumbered them
	if err != nil {
		return err
	}
	var f, n ver
	for _, v := range vs {
		switch v.id {
		case fID:
			f = v
		case nID:
			n = v
		}
	}
	var err2 error
	exec := func(q string, args ...any) {
		if err2 == nil {
			_, err2 = tx.ExecContext(ctx, q, args...)
		}
	}
	if sameLabel(n.label, f.label) {
		exec(`UPDATE library_version_flags SET of_id = ? WHERE of_id = ?`, n.id, f.id)
		exec(`DELETE FROM library_versions WHERE id = ?`, f.id) // F's own flag cascades away
		exec(`UPDATE library_versions SET file_id = ?, ordinal = ?, seq = ?, fresh = ? WHERE id = ?`,
			f.fileID, min(n.ord, f.ord), f.seq, f.fresh, n.id)
	} else {
		err2 = leaveGroup(ctx, tx, vs, n)
		exec(`DELETE FROM library_version_flags WHERE version_id = ?`, n.id)
		exec(`UPDATE library_version_flags SET version_id = ? WHERE version_id = ?`, n.id, f.id)
		exec(`UPDATE library_version_flags SET of_id = ? WHERE of_id = ?`, n.id, f.id)
		exec(`DELETE FROM library_versions WHERE id = ?`, f.id)
		exec(`UPDATE library_versions SET file_id = ?, label = NULLIF(?, ''), ordinal = ?, seq = ?, fresh = ? WHERE id = ?`,
			f.fileID, f.label, f.ord, f.seq, f.fresh, n.id)
	}
	exec(`DELETE FROM library_version_flags WHERE version_id = ? AND of_id = ?`, n.id, n.id)
	return err2
}

// leaveGroup applies the leave-group rule when version n leaves its label
// group: H, the group's lowest-ordinal version other than n, becomes the head
// (ordinal 1, no flag), and every other flag against n is pointed at that
// version's own flag target, n excluded. vs is updated in place.
func leaveGroup(ctx context.Context, tx *sql.Tx, vs []ver, n ver) error {
	var h *ver
	for i := range vs {
		v := &vs[i]
		if v.id != n.id && sameLabel(v.label, n.label) && (h == nil || v.ord < h.ord || v.ord == h.ord && v.id < h.id) {
			h = v
		}
	}
	if h == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_version_flags WHERE version_id = ?`, h.id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_versions SET ordinal = 1 WHERE id = ?`, h.id); err != nil {
		return err
	}
	h.ord = 1
	for _, v := range vs {
		if v.id == n.id || v.id == h.id || !sameLabel(v.label, n.label) {
			continue
		}
		t := flagTarget(vs, n.label, v.ord, n.id)
		q, args := `UPDATE library_version_flags SET of_id = ? WHERE version_id = ? AND of_id = ?`, []any{t, v.id, n.id}
		if t == "" {
			q, args = `DELETE FROM library_version_flags WHERE version_id = ? AND of_id = ?`, []any{v.id, n.id}
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

// renumber gives the versions moved into item into by a merge or rename fold
// (moved, IDs) the next seqs, in their seq order, and a new ordinal when
// theirs is already taken in their label group; any ordinal above 1 is
// flagged again at its target, so flags stay below. into's existing
// versions never change; a rename then finishes in adoptVersions.
func renumber(ctx context.Context, tx *sql.Tx, into string, moved []string) error {
	vs, err := loadVersions(ctx, tx, into)
	if err != nil {
		return err
	}
	isMoved := map[string]bool{}
	for _, id := range moved {
		isMoved[id] = true
	}
	var settled []ver
	top := 0
	for _, v := range vs {
		top = max(top, v.seq)
		if !isMoved[v.id] {
			settled = append(settled, v)
		}
	}
	for _, m := range vs {
		if !isMoved[m.id] {
			continue
		}
		top++
		m.seq = top
		taken, last := false, 0
		for _, v := range settled {
			if sameLabel(v.label, m.label) {
				last = max(last, v.ord)
				taken = taken || v.ord == m.ord
			}
		}
		if taken {
			m.ord = last + 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_versions SET seq = ?, ordinal = ? WHERE id = ?`, m.seq, m.ord, m.id); err != nil {
			return err
		}
		if m.ord > 1 { // also when free: its old flag may point at a sibling just renumbered above it
			if err := setFlag(ctx, tx, m.id, flagTarget(settled, m.label, m.ord, "")); err != nil {
				return err
			}
		}
		settled = append(settled, m)
	}
	return nil
}

// displayLabel is a version's label as listed: n is its read-time rank among
// the listed versions of its label group, never the stored ordinal. Rank 1
// shows the label as is; later ranks add " (n)", cutting the label so the
// whole stays within 100 characters; a null label shows "Version n".
func displayLabel(label *string, n int) *string {
	if n <= 1 {
		return label
	}
	if label == nil || *label == "" {
		s := "Version " + strconv.Itoa(n)
		return &s
	}
	suffix := " (" + strconv.Itoa(n) + ")"
	r := []rune(*label)
	if keep := 100 - len(suffix); len(r) > keep {
		r = r[:keep]
	}
	s := string(r) + suffix
	return &s
}

// VersionFlag is a version flagged as a possible duplicate of another version
// of the same item. Label is the raw stored label.
type VersionFlag struct {
	VersionID, OfID, ItemID, Title string
	Label                          *string
}

// VersionFlags lists the library's flagged versions while both files are
// present, ordered by the item's sort title.
//
// shortcut: the target is chosen when the flag is written, so if it later goes missing and isn't adopted its live duplicates stay unflagged until it returns or a rename/merge reflags them; compute OfID at read time in LIB-2d if admins report missed duplicates.
// Planned: LIB-2d (the #104 duplicates view).
func (s *Store) VersionFlags(ctx context.Context, libID string) ([]VersionFlag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.version_id, g.of_id, i.id, i.title, v.label FROM library_version_flags g
		JOIN library_versions v ON v.id = g.version_id JOIN library_files f ON f.id = v.file_id
		JOIN library_versions o ON o.id = g.of_id JOIN library_files of ON of.id = o.file_id
		JOIN library_items i ON i.id = v.item_id
		WHERE i.library_id = ? AND g.state = 'flagged' AND f.state = 'present' AND of.state = 'present'
		ORDER BY i.sort_title, i.id, g.version_id`, libID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VersionFlag
	for rows.Next() {
		var f VersionFlag
		if err := rows.Scan(&f.VersionID, &f.OfID, &f.ItemID, &f.Title, &f.Label); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// KeepVersions records the admin's decision to keep a flagged version beside
// the one it was flagged against.
// Planned: LIB-2d (the #104 duplicates view).
func (s *Store) KeepVersions(ctx context.Context, versionID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE library_version_flags SET state = 'kept' WHERE version_id = ? AND state = 'flagged'`, versionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: no flagged version %s", ErrNotFound, versionID)
	}
	return nil
}

// RelabelVersion gives a version a new label (1–90 characters after
// trimming). It leaves its old label group by the leave-group rule and joins
// the new one with the next ordinal, flagged when that is above 1. A label
// equal to the old one (ASCII fold) changes nothing.
//
// shortcut: a relabelled version isn't adopted on a later rename unless the new label is in the same adoption class as the file's parsed label, when an admin-locked label field exists upgrade.
// Planned: LIB-2d (the #104 duplicates view).
func (s *Store) RelabelVersion(ctx context.Context, versionID, label string) error {
	label = strings.TrimSpace(label)
	if n := utf8.RuneCountInString(label); n < 1 || n > maxVersionLabel {
		return fmt.Errorf("%w: a version label must be 1–%d characters", ErrInvalid, maxVersionLabel)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var itemID string
	err = tx.QueryRowContext(ctx, `SELECT item_id FROM library_versions WHERE id = ?`, versionID).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: no version %s", ErrNotFound, versionID)
	}
	if err != nil {
		return err
	}
	vs, err := loadVersions(ctx, tx, itemID)
	if err != nil {
		return err
	}
	var v ver
	for _, x := range vs {
		if x.id == versionID {
			v = x
		}
	}
	if sameLabel(v.label, label) {
		return nil
	}
	if err := leaveGroup(ctx, tx, vs, v); err != nil {
		return err
	}
	ord := 1
	for _, x := range vs {
		if x.id != v.id && sameLabel(x.label, label) {
			ord = max(ord, x.ord+1)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_version_flags WHERE version_id = ?`, v.id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_versions SET label = ?, ordinal = ? WHERE id = ?`, label, ord, v.id); err != nil {
		return err
	}
	if ord > 1 {
		if err := setFlag(ctx, tx, v.id, flagTarget(vs, label, ord, v.id)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// maxVersionLabel leaves room for displayLabel's " (n)" within 100 characters.
const maxVersionLabel = 90

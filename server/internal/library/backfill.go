package library

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/library/naming"
)

// BackfillVersions numbers the versions placed before LIB-3 (seq = 0), once,
// at serve start, 500 items per transaction; a crash resumes where it
// stopped. Per item it recomputes each label from the file's path (placed
// files are never placed again, so stored labels predate the canonical
// ones), numbers seq and ordinal with missing or retired files first, flags
// every ordinal above 1, and marks fresh each present version with an older
// dead version in its adoption class, so the first whole-root scan adopts a
// rename made before LIB-3 and keeps the old version ID. It reports how many
// versions it numbered.
func (s *Store) BackfillVersions(ctx context.Context) (int, error) {
	total := 0
	for {
		n, items, err := s.backfillVersions(ctx)
		total += n
		if err != nil || items < backfillBatch {
			return total, err
		}
	}
}

func (s *Store) backfillVersions(ctx context.Context) (versions, items int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT item_id FROM library_versions WHERE seq = 0 LIMIT ?`, backfillBatch)
	if err != nil {
		return 0, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		n, err := backfillItem(ctx, tx, id)
		if err != nil {
			return 0, 0, err
		}
		versions += n
	}
	return versions, len(ids), tx.Commit()
}

func backfillItem(ctx context.Context, tx *sql.Tx, itemID string) (int, error) {
	vs, err := loadVersions(ctx, tx, itemID)
	if err != nil {
		return 0, err
	}
	// Each version's file, to classify again.
	rows, err := tx.QueryContext(ctx, `SELECT v.id, f.path, f.size, l.type FROM library_versions v
		JOIN library_files f ON f.id = v.file_id JOIN library_roots r ON r.id = f.root_id
		JOIN library_libraries l ON l.id = r.library_id WHERE v.item_id = ? AND v.seq = 0`, itemID)
	if err != nil {
		return 0, err
	}
	labels := map[string]string{}
	for rows.Next() {
		var id, path, typ string
		var size int64
		if err := rows.Scan(&id, &path, &size, &typ); err != nil {
			rows.Close()
			return 0, err
		}
		if e, err := naming.Classify(typ, path, size); err == nil {
			labels[id] = e.Version
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var settled, todo []ver
	top := 0
	for _, v := range vs {
		if v.seq != 0 {
			settled = append(settled, v)
			top = max(top, v.seq)
			continue
		}
		if l, ok := labels[v.id]; ok {
			v.label = l // else the path no longer classifies: keep the stored label
		}
		todo = append(todo, v)
	}
	// Dead files first, so a predecessor gets the lower seq and ordinal.
	slices.SortFunc(todo, func(a, b ver) int {
		if a.present != b.present {
			if a.present {
				return 1
			}
			return -1
		}
		return strings.Compare(a.id, b.id)
	})
	for i := range todo {
		v := &todo[i]
		top++
		v.seq, v.ord = top, 1
		for _, x := range settled {
			if sameLabel(x.label, v.label) {
				v.ord = max(v.ord, x.ord+1)
			}
		}
		for _, x := range settled {
			if x.seq < v.seq && !x.present && sameClass(x.label, v.label) && v.present {
				v.fresh = true
			}
		}
		settled = append(settled, *v)
	}
	for _, v := range todo {
		if _, err := tx.ExecContext(ctx, `UPDATE library_versions SET label = NULLIF(?, ''), seq = ?, ordinal = ?, fresh = ? WHERE id = ?`,
			v.label, v.seq, v.ord, v.fresh, v.id); err != nil {
			return 0, err
		}
		if v.ord > 1 {
			if err := setFlag(ctx, tx, v.id, flagTarget(settled, v.label, v.ord, "")); err != nil {
				return 0, err
			}
		}
	}
	return len(todo), nil
}

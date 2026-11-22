package library

import (
	"context"
	"database/sql"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// minSegmentMs is the shortest chapter taken as a whole episode (decision 1).
const minSegmentMs = 10 * 60 * 1000

// SegmentOf locates episode index (0-based, among the file's count episodes
// ordered by number) in a file shared by several episodes. It trusts the
// chapters only when there is one per episode, each at least 10 minutes, and
// together they tile the file from 0 to within a second of its duration;
// otherwise it returns nil. It never splits a file evenly (spec §3.8).
func SegmentOf(chapters []probe.Chapter, durationMs int64, index, count int) *contracts.Segment {
	if count < 2 || len(chapters) != count || index < 0 || index >= count || durationMs <= 0 {
		return nil
	}
	var at int64
	for _, c := range chapters {
		if c.StartMs != at || c.EndMs-c.StartMs < minSegmentMs {
			return nil
		}
		at = c.EndMs
	}
	if d := at - durationMs; d > 1000 || d < -1000 {
		return nil
	}
	return &contracts.Segment{StartMs: chapters[index].StartMs, EndMs: chapters[index].EndMs}
}

// itemExtras lists a movie's or show's extras (none for a season or episode),
// each played by its own ID (decision 4). A retired file's extra is gone, and
// a missing one is left out unless its root is offline, so a renamed extra
// doesn't list twice.
// Planned: LIB-3 (Store.Item lists extras, Task 7).
func (s *Store) itemExtras(ctx context.Context, tx *sql.Tx, g Grant, itemID string) ([]contracts.Extra, error) {
	rows, err := tx.QueryContext(ctx, `SELECT x.id, x.type, x.title, x.file_id, CASE WHEN f.duration_ms > 0 THEN f.duration_ms END,
		f.size, f.mtime_ns, f.path, f.probe_json, (f.state = 'present' AND r.state != 'offline')
		FROM library_extras x JOIN library_items i ON i.id = x.item_id
		JOIN library_files f ON f.id = x.file_id JOIN library_roots r ON r.id = f.root_id
		WHERE x.item_id = ? AND i.kind IN ('movie', 'show') AND `+liveVersion+` AND (f.state = 'present' OR r.state = 'offline')
		ORDER BY x.type, x.title, x.id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []contracts.Extra{}
	for rows.Next() {
		var x contracts.Extra
		var f fileCols
		if err := rows.Scan(&x.ID, &x.Type, &x.Title, &x.Version.FileID, &f.duration, &f.size, &f.mtime, &f.rel, &f.probeJSON, &x.Version.Available); err != nil {
			return nil, err
		}
		x.Version.ID = x.ID
		s.fileVersion(&x.Version, g, f)
		out = append(out, x)
	}
	return out, rows.Err()
}

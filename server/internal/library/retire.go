package library

import (
	"context"
	"database/sql"
	"time"
)

// RetireAfter is how long a file must stay missing before it can be retired:
// a successful scan at least this long after the one that first missed it
// must still not find it (spec §3.6). Failed scans don't count, so an outage
// suspends retirement.
const RetireAfter = 24 * time.Hour

// Candidate is a missing file that may now be retired.
type Candidate struct {
	FileID, Path string
	MissingSince time.Time
}

const candidateCond = `f.state = 'missing' AND EXISTS (SELECT 1 FROM library_scan_runs r
	WHERE r.root_id = f.root_id AND r.status = 'completed' AND r.scope IS NULL AND r.started_at >= f.missing_since + ?)`

// RetirementCandidates lists the root's files that meet the retirement rule,
// for the admin preview. Nothing is retired automatically.
// Planned: LIB-2c (the admin retirement preview).
func (s *Store) RetirementCandidates(ctx context.Context, rootID string) ([]Candidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, f.path, f.missing_since FROM library_files f
		WHERE f.root_id = ? AND `+candidateCond+` ORDER BY f.path`, rootID, RetireAfter.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var since int64
		if err := rows.Scan(&c.FileID, &c.Path, &since); err != nil {
			return nil, err
		}
		c.MissingSince = time.UnixMilli(since)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Retire retires the given files of the root that are still candidates and
// returns how many it retired. Their items and versions stay, with any user
// data; a retired file that comes back is present again on the next scan.
// Planned: LIB-2c (the admin confirms the preview).
func (s *Store) Retire(ctx context.Context, rootID string, fileIDs []string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, id := range fileIDs {
		var res sql.Result
		if res, err = tx.ExecContext(ctx, `UPDATE library_files AS f SET state = 'retired'
			WHERE f.id = ? AND f.root_id = ? AND `+candidateCond, id, rootID, RetireAfter.Milliseconds()); err != nil {
			return 0, err
		}
		k, _ := res.RowsAffected()
		n += int(k)
	}
	return n, tx.Commit()
}

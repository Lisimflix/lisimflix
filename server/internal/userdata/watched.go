package userdata

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// SetWatched marks the item watched (origin manual) or, with false, resets it:
// watched, resume and coverage are cleared and sessions whose first event id
// is at or below the highest event id at the time of the mark become stale.
// The play count is untouched. A stale expectedRevision is
// ErrRevisionConflict and changes nothing. No event row is written.
//
// Planned: DATA-1b (setWatched handler).
func (s *Store) SetWatched(ctx context.Context, profileID, itemID string, watched bool, expectedRevision int64) (contracts.UserItemState, error) {
	return s.mark(ctx, profileID, itemID, expectedRevision, func(tx *sql.Tx, now, eventID int64) error {
		if watched {
			_, err := tx.ExecContext(ctx, `UPDATE userdata_state SET watched_value = 1, watched_origin = ?, watched_at = ?
				WHERE profile_id = ? AND item_id = ?`, contracts.WatchedManual, now, profileID, itemID)
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM userdata_intervals WHERE profile_id = ? AND item_id = ?`, profileID, itemID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE userdata_state SET watched_value = NULL, watched_origin = NULL, watched_at = NULL,
			resume_position_ms = NULL, resume_duration_ms = NULL, resume_version_id = NULL,
			resume_source_revision = NULL, resume_updated_at = NULL, covered_ms = 0, reset_event_id = ?
			WHERE profile_id = ? AND item_id = ?`, eventID, profileID, itemID)
		return err
	})
}

// Hide removes the item from Continue Watching until a session with no events
// yet at the time of the hide plays it. Events and progress are kept.
// Revision rules as SetWatched.
//
// Planned: DATA-1b (hideContinueWatching handler).
func (s *Store) Hide(ctx context.Context, profileID, itemID string, expectedRevision int64) (contracts.UserItemState, error) {
	return s.mark(ctx, profileID, itemID, expectedRevision, func(tx *sql.Tx, now, eventID int64) error {
		_, err := tx.ExecContext(ctx, `UPDATE userdata_state SET hidden = 1, hide_event_id = ? WHERE profile_id = ? AND item_id = ?`,
			eventID, profileID, itemID)
		return err
	})
}

// mark runs write in one transaction after a revision bump. eventID is the
// highest event id at that moment (0 if none), the mark's place in event order.
func (s *Store) mark(ctx context.Context, profileID, itemID string, expected int64,
	write func(tx *sql.Tx, now, eventID int64) error) (contracts.UserItemState, error) {
	st := contracts.UserItemState{ItemID: itemID}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return st, fmt.Errorf("userdata: mark: %w", err)
	}
	defer tx.Rollback()
	if _, err := bump(ctx, tx, profileID, itemID, expected); err != nil {
		return st, err
	}
	var eventID int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM userdata_events`).Scan(&eventID); err != nil {
		return st, fmt.Errorf("userdata: mark: %w", err)
	}
	if err := write(tx, s.now().UTC().UnixMilli(), eventID); err != nil {
		return st, fmt.Errorf("userdata: mark: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return st, fmt.Errorf("userdata: mark: %w", err)
	}
	return s.Get(ctx, profileID, itemID)
}

// ContinueWatching lists the profile's partly played items, newest first:
// resume present, at least PlayedMinMs covered, not watched, not hidden.
// limit <= 0 means 50 and is capped at 200.
//
// Planned: DATA-1b (listContinueWatching handler).
func (s *Store) ContinueWatching(ctx context.Context, profileID string, limit int) ([]contracts.UserItemState, error) {
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, `+stateCols+` FROM userdata_state
		WHERE profile_id = ? AND hidden = 0 AND resume_position_ms IS NOT NULL AND covered_ms >= ?
		AND COALESCE(watched_value, 0) = 0
		ORDER BY last_played_at DESC, item_id LIMIT ?`, profileID, PlayedMinMs, limit)
	if err != nil {
		return nil, fmt.Errorf("userdata: continue watching: %w", err)
	}
	defer rows.Close()
	out := []contracts.UserItemState{}
	for rows.Next() {
		var st contracts.UserItemState
		if err := scanState(rows, &st, &st.ItemID); err != nil {
			return nil, fmt.Errorf("userdata: continue watching: %w", err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

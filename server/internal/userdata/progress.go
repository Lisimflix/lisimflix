package userdata

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// Apply records one progress event of a playback session for the profile, in
// one transaction. A duplicate or stale event is stored as a rejected row and
// answered Accepted: false with the unchanged state, not an error. Event times
// are the server's receipt time. Progress never bumps the revision.
//
// Planned: DATA-1b (reportProgress handler).
func (s *Store) Apply(ctx context.Context, profileID string, sess Session, e contracts.ProgressEvent) (contracts.ProgressResult, error) {
	var res contracts.ProgressResult
	if profileID == "" {
		return res, ErrInvalid
	}
	if err := validate(sess, e); err != nil {
		return res, err
	}
	if sess.DurationMs > 0 && e.PositionMs > sess.DurationMs {
		e.PositionMs = sess.DurationMs // probe durations are rounded (Decision 13)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("userdata: apply: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().UnixMilli()

	if _, err := tx.ExecContext(ctx, `INSERT INTO userdata_state (profile_id, item_id) VALUES (?, ?)
		ON CONFLICT DO NOTHING`, profileID, sess.ItemID); err != nil {
		return res, fmt.Errorf("userdata: apply: %w", err)
	}
	var (
		hidden                                          bool
		hideEvent, resetEvent, watched, count, lastPlay sql.NullInt64
		origin                                          sql.NullString
	)
	if err := tx.QueryRowContext(ctx, `SELECT hidden, hide_event_id, reset_event_id, watched_value, watched_origin, play_count, last_played_at
		FROM userdata_state WHERE profile_id = ? AND item_id = ?`, profileID, sess.ItemID).
		Scan(&hidden, &hideEvent, &resetEvent, &watched, &origin, &count, &lastPlay); err != nil {
		return res, fmt.Errorf("userdata: apply: %w", err)
	}
	first, hasFirst, err := firstEventID(ctx, tx, profileID, sess.ID)
	if err != nil {
		return res, err
	}
	prev, err := lastAccepted(ctx, tx, profileID, sess.ID)
	if err != nil {
		return res, err
	}

	reason := ""
	switch {
	case prev.exists && prev.seqs[e.Sequence]:
		reason = contracts.ProgressDuplicate
	case prev.exists && e.Sequence <= prev.maxSeq,
		prev.exists && e.Kind == contracts.ProgressTick && e.PositionMs < prev.pos,
		prev.exists && (e.Kind == contracts.ProgressPause || e.Kind == contracts.ProgressStop) &&
			e.PositionMs < prev.pos-StopToleranceMs, // player rounding (DATA-1b Decision 12)
		isLate(first, hasFirst, resetEvent):
		reason = contracts.ProgressStale
	}
	var r any
	if reason != "" {
		r = reason
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO userdata_events (profile_id, item_id, session_id, sequence, kind,
		position_ms, from_position_ms, version_id, source_revision, accepted, reason, received_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, profileID, sess.ItemID, sess.ID, e.Sequence, e.Kind,
		e.PositionMs, e.FromPositionMs, sess.VersionID, sess.SourceRevision, reason == "", r, now); err != nil {
		return res, fmt.Errorf("userdata: apply: %w", err)
	}
	if reason == "" {
		spans, err := loadCoverage(ctx, tx, profileID, sess)
		if err != nil {
			return res, err
		}
		if prev.exists && e.Kind != contracts.ProgressSeek {
			if sp, ok := Advance(prev.pos, e.PositionMs); ok {
				spans = Merge(append(spans, sp))
				if err := saveCoverage(ctx, tx, profileID, sess, spans); err != nil {
					return res, err
				}
			}
		}
		covered := Covered(spans)
		lastPlayed := max(lastPlay.Int64, now)
		clearHidden := hidden && !isLate(first, hasFirst, hideEvent)
		if _, err := tx.ExecContext(ctx, `UPDATE userdata_state SET covered_ms = ?, last_played_at = ?,
			hidden = CASE WHEN ? THEN 0 ELSE hidden END WHERE profile_id = ? AND item_id = ?`,
			covered, lastPlayed, clearHidden, profileID, sess.ItemID); err != nil {
			return res, fmt.Errorf("userdata: apply: %w", err)
		}
		markAuto := !sess.NoAutoWatched && !watched.Valid && IsWatched(e.PositionMs, covered, sess.DurationMs)
		autoWatched := markAuto || (watched.Int64 == 1 && origin.String == contracts.WatchedAuto)
		switch {
		case autoWatched && pastWatchedPosition(e.PositionMs, sess.DurationMs):
			// No "Resume from the credits" on an item watched automatically (DATA-1b Decision 11).
			if _, err := tx.ExecContext(ctx, `UPDATE userdata_state SET resume_position_ms = NULL, resume_duration_ms = NULL,
				resume_version_id = NULL, resume_source_revision = NULL, resume_updated_at = NULL
				WHERE profile_id = ? AND item_id = ?`, profileID, sess.ItemID); err != nil {
				return res, fmt.Errorf("userdata: apply: %w", err)
			}
		case sess.DurationMs > 0:
			if _, err := tx.ExecContext(ctx, `UPDATE userdata_state SET resume_position_ms = ?, resume_duration_ms = ?,
				resume_version_id = ?, resume_source_revision = ?, resume_updated_at = ?
				WHERE profile_id = ? AND item_id = ?`, e.PositionMs, sess.DurationMs, sess.VersionID,
				sess.SourceRevision, now, profileID, sess.ItemID); err != nil {
				return res, fmt.Errorf("userdata: apply: %w", err)
			}
		}
		if markAuto {
			if _, err := tx.ExecContext(ctx, `UPDATE userdata_state SET watched_value = 1, watched_origin = ?, watched_at = ?,
				play_count = COALESCE(play_count, 0) + 1 WHERE profile_id = ? AND item_id = ?`,
				contracts.WatchedAuto, now, profileID, sess.ItemID); err != nil {
				return res, fmt.Errorf("userdata: apply: %w", err)
			}
		}
	}
	if res.State, err = readState(ctx, tx, profileID, sess.ItemID); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.ProgressResult{}, fmt.Errorf("userdata: apply: %w", err)
	}
	s.committed()
	res.Accepted = reason == ""
	if reason != "" {
		res.Reason = &reason
	}
	return res, nil
}

func validate(sess Session, e contracts.ProgressEvent) error {
	switch {
	case sess.ID == "", sess.ItemID == "", sess.VersionID == "", sess.SourceRevision == "":
	case e.Kind != contracts.ProgressTick && e.Kind != contracts.ProgressPause &&
		e.Kind != contracts.ProgressSeek && e.Kind != contracts.ProgressStop:
	case e.Sequence < 0, e.PositionMs < 0, sess.DurationMs < 0:
	case (e.Kind == contracts.ProgressSeek) != (e.FromPositionMs != nil):
	case e.FromPositionMs != nil && *e.FromPositionMs < 0:
	default:
		return nil
	}
	return ErrInvalid
}

// isLate reports whether the session began at or before a reset or hide mark
// (an event id). A session with no events yet is new, and a null mark is none.
func isLate(firstEventID int64, hasEvents bool, mark sql.NullInt64) bool {
	return hasEvents && mark.Valid && firstEventID <= mark.Int64
}

// firstEventID is the session's lowest recorded event id (accepted or not).
func firstEventID(ctx context.Context, tx *sql.Tx, profileID, sessionID string) (int64, bool, error) {
	var id sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MIN(id) FROM userdata_events WHERE profile_id = ? AND session_id = ?`,
		profileID, sessionID).Scan(&id); err != nil {
		return 0, false, fmt.Errorf("userdata: session start: %w", err)
	}
	return id.Int64, id.Valid, nil
}

// accepted summarises a session's accepted events.
type accepted struct {
	exists bool
	maxSeq int64
	pos    int64 // position of the highest accepted sequence
	seqs   map[int64]bool
}

func lastAccepted(ctx context.Context, tx *sql.Tx, profileID, sessionID string) (accepted, error) {
	a := accepted{seqs: map[int64]bool{}}
	rows, err := tx.QueryContext(ctx, `SELECT sequence, position_ms FROM userdata_events
		WHERE profile_id = ? AND session_id = ? AND accepted = 1`, profileID, sessionID)
	if err != nil {
		return a, fmt.Errorf("userdata: session events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq, pos int64
		if err := rows.Scan(&seq, &pos); err != nil {
			return a, fmt.Errorf("userdata: session events: %w", err)
		}
		a.seqs[seq] = true
		if !a.exists || seq > a.maxSeq {
			a.maxSeq, a.pos = seq, pos
		}
		a.exists = true
	}
	if err := rows.Err(); err != nil {
		return a, fmt.Errorf("userdata: session events: %w", err)
	}
	return a, nil
}

func loadCoverage(ctx context.Context, tx *sql.Tx, profileID string, sess Session) ([]Span, error) {
	rows, err := tx.QueryContext(ctx, `SELECT start_ms, end_ms FROM userdata_intervals WHERE profile_id = ?
		AND item_id = ? AND version_id = ? AND source_revision = ?`, profileID, sess.ItemID, sess.VersionID, sess.SourceRevision)
	if err != nil {
		return nil, fmt.Errorf("userdata: coverage: %w", err)
	}
	defer rows.Close()
	var spans []Span
	for rows.Next() {
		var sp Span
		if err := rows.Scan(&sp.StartMs, &sp.EndMs); err != nil {
			return nil, fmt.Errorf("userdata: coverage: %w", err)
		}
		spans = append(spans, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("userdata: coverage: %w", err)
	}
	return spans, nil
}

// saveCoverage replaces one source's coverage rows with spans.
func saveCoverage(ctx context.Context, tx *sql.Tx, profileID string, sess Session, spans []Span) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM userdata_intervals WHERE profile_id = ? AND item_id = ?
		AND version_id = ? AND source_revision = ?`, profileID, sess.ItemID, sess.VersionID, sess.SourceRevision); err != nil {
		return fmt.Errorf("userdata: coverage: %w", err)
	}
	for _, sp := range spans {
		if _, err := tx.ExecContext(ctx, `INSERT INTO userdata_intervals (profile_id, item_id, version_id, source_revision,
			start_ms, end_ms) VALUES (?, ?, ?, ?, ?, ?)`, profileID, sess.ItemID, sess.VersionID, sess.SourceRevision,
			sp.StartMs, sp.EndMs); err != nil {
			return fmt.Errorf("userdata: coverage: %w", err)
		}
	}
	return nil
}

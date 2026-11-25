package userdata

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations is the progress schema, for storage.Migrate.
func Migrations() storage.Source {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err) // embedded at build time
	}
	return storage.Source{Module: "userdata", FS: sub}
}

var (
	ErrRevisionConflict = errors.New("userdata: revision conflict")
	ErrInvalid          = errors.New("userdata: invalid")
)

// Session is a live playback session, which fixes the item, version and
// source revision its events apply to.
type Session struct {
	ID, ItemID, VersionID, SourceRevision string
	DurationMs                            int64
	NoAutoWatched                         bool
}

// Store keeps per-profile progress in the shared database.
type Store struct {
	db          *storage.DB
	now         func() time.Time
	afterCommit func() // test seam: runs after a write commits; nil in production
}

// committed runs the afterCommit test seam.
func (s *Store) committed() {
	if s.afterCommit != nil {
		s.afterCommit()
	}
}

func NewStore(db *storage.DB) *Store { return &Store{db: db, now: time.Now} }

// States returns one state per requested item for the profile, the zero
// state (revision 0, nulls, hidden false) when there is no row. One query for
// the whole batch; callers pass at most a page of IDs.
//
// Planned: DATA-1b (library UserStates hook, Task 6).
func (s *Store) States(ctx context.Context, profileID string, itemIDs []string) (map[string]contracts.UserItemState, error) {
	if profileID == "" {
		return nil, ErrInvalid
	}
	out := make(map[string]contracts.UserItemState, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(itemIDs)+1)
	args = append(args, profileID)
	for _, id := range itemIDs {
		out[id] = contracts.UserItemState{ItemID: id}
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, `+stateCols+` FROM userdata_state WHERE profile_id = ?
		AND item_id IN (?`+strings.Repeat(", ?", len(itemIDs)-1)+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("userdata: states: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var st contracts.UserItemState
		if err := scanState(rows, &st, &st.ItemID); err != nil {
			return nil, fmt.Errorf("userdata: states: %w", err)
		}
		out[st.ItemID] = st
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("userdata: states: %w", err)
	}
	return out, nil
}

// readState reads the profile's state for the item inside tx, or the zero state.
func readState(ctx context.Context, tx *sql.Tx, profileID, itemID string) (contracts.UserItemState, error) {
	st := contracts.UserItemState{ItemID: itemID}
	err := scanState(tx.QueryRowContext(ctx, `SELECT `+stateCols+` FROM userdata_state
		WHERE profile_id = ? AND item_id = ?`, profileID, itemID), &st)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("userdata: read state: %w", err)
	}
	return st, nil
}

// stateCols is the column list scanState reads.
const stateCols = `revision, resume_position_ms, resume_duration_ms, resume_version_id,
	resume_source_revision, resume_updated_at, watched_value, watched_origin, watched_at, play_count,
	last_played_at, hidden`

// scanState maps one stateCols row onto st; lead receives any columns selected before stateCols.
func scanState(row interface{ Scan(...any) error }, st *contracts.UserItemState, lead ...any) error {
	var (
		rev                          int64
		pos, dur, resAt              sql.NullInt64
		verID, srcRev, wOrigin       sql.NullString
		wVal, wAt, count, lastPlayed sql.NullInt64
		hidden                       int64
	)
	dest := append(lead, &rev, &pos, &dur, &verID, &srcRev, &resAt, &wVal, &wOrigin, &wAt, &count, &lastPlayed, &hidden)
	if err := row.Scan(dest...); err != nil {
		return err
	}
	st.Revision = rev
	// Resume and watched are each written as a whole group (Apply and SetWatched always write every column of a group).
	st.HiddenFromContinueWatching = hidden != 0
	if pos.Valid && dur.Valid && verID.Valid && srcRev.Valid && resAt.Valid {
		st.Resume = &contracts.Resume{PositionMs: pos.Int64, DurationMs: dur.Int64, VersionID: verID.String,
			SourceRevision: srcRev.String, UpdatedAt: time.UnixMilli(resAt.Int64).UTC()}
	}
	if wVal.Valid && wOrigin.Valid && wAt.Valid {
		st.Watched = &contracts.Watched{Value: wVal.Int64 != 0, Origin: wOrigin.String, At: time.UnixMilli(wAt.Int64).UTC()}
	}
	if count.Valid {
		c := int(count.Int64)
		st.PlayCount = &c
	}
	if lastPlayed.Valid {
		t := time.UnixMilli(lastPlayed.Int64).UTC()
		st.LastPlayedAt = &t
	}
	return nil
}

// bump advances the item's revision from expected inside tx. A stale expected
// is ErrRevisionConflict; expected 0 also covers an item with no row yet.
func bump(ctx context.Context, tx *sql.Tx, profileID, itemID string, expected int64) (int64, error) {
	res, err := tx.ExecContext(ctx, `UPDATE userdata_state SET revision = revision + 1
		WHERE profile_id = ? AND item_id = ? AND revision = ?`, profileID, itemID, expected)
	if err != nil {
		return 0, fmt.Errorf("userdata: bump: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("userdata: bump: %w", err)
	}
	if n == 0 && expected == 0 {
		res, err = tx.ExecContext(ctx, `INSERT INTO userdata_state (profile_id, item_id, revision) VALUES (?, ?, 1)
			ON CONFLICT DO NOTHING`, profileID, itemID)
		if err != nil {
			return 0, fmt.Errorf("userdata: bump: %w", err)
		}
		if n, err = res.RowsAffected(); err != nil {
			return 0, fmt.Errorf("userdata: bump: %w", err)
		}
	}
	if n == 0 {
		return 0, ErrRevisionConflict
	}
	return expected + 1, nil
}

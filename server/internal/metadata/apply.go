package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// errStale: the item changed (an edit or a fix-match) while the provider was
// being asked, so the result is dropped (Decision 7).
var errStale = errors.New("metadata: item changed during matching")

// Retry backoff for provider failures (Decision 10).
const (
	retryBase = time.Minute
	retryCap  = 24 * time.Hour
	maxShift  = 11 // retryBase << 11 is already past retryCap
)

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// applyRecord applies rec from provider prov if the item is still at
// revision rev. A record whose IDs contradict a locked ID only makes the item
// a conflict. Otherwise unlocked IDs and fields take the record's values with
// the provider's provenance; locked ones, and fields the record doesn't
// provide, stay as they are.
func (s *Store) applyRecord(ctx context.Context, itemID string, rev int64, prov string, rec Record) (string, error) {
	fields, err := recordFields(rec)
	if err != nil {
		return "", err
	}
	ids := map[string]string{}
	for ns, v := range rec.ExternalIDs {
		if v != "" {
			ids[ns] = v
		}
	}
	ids[prov] = rec.ID

	now := ms(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := bump(ctx, tx, itemID, rev, now); err != nil {
		if errors.Is(err, ErrRevisionConflict) {
			return "", errStale
		}
		return "", err
	}

	rows, err := tx.QueryContext(ctx, `SELECT namespace, value FROM metadata_external_ids WHERE item_id = ? AND locked = 1`, itemID)
	if err != nil {
		return "", err
	}
	var reason string
	for rows.Next() {
		var ns, locked string
		if err := rows.Scan(&ns, &locked); err != nil {
			rows.Close()
			return "", err
		}
		if v, ok := ids[ns]; ok && v != locked && reason == "" {
			reason = fmt.Sprintf("%s record %s reports %s %s, but %s %s is locked", prov, rec.ID, ns, v, ns, locked)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if reason != "" {
		if err := finishMatch(ctx, tx, itemID, contracts.MatchConflict, reason, now); err != nil {
			return "", err
		}
		return OutcomeConflict, tx.Commit()
	}

	for ns, v := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_external_ids(item_id, namespace, value, provider, provider_id, fetched_at, locked)
			VALUES(?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT(item_id, namespace) DO UPDATE SET value = excluded.value, provider = excluded.provider,
			provider_id = excluded.provider_id, fetched_at = excluded.fetched_at WHERE metadata_external_ids.locked = 0`,
			itemID, ns, v, prov, rec.ID, now); err != nil {
			return "", err
		}
	}
	for _, f := range fields {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_fields(item_id, field, value, provider, provider_id, fetched_at, locked)
			VALUES(?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT(item_id, field) DO UPDATE SET value = excluded.value, provider = excluded.provider,
			provider_id = excluded.provider_id, fetched_at = excluded.fetched_at WHERE metadata_fields.locked = 0`,
			itemID, f.name, f.value, prov, rec.ID, now); err != nil {
			return "", err
		}
	}
	if err := finishMatch(ctx, tx, itemID, contracts.MatchMatched, "", now); err != nil {
		return "", err
	}
	return OutcomeMatched, tx.Commit()
}

type recordField struct{ name, value string }

// recordFields returns the fields rec provides, as stored JSON. Nil and zero
// values aren't provided, so they never become a stored clear.
func recordFields(rec Record) ([]recordField, error) {
	var out []recordField
	add := func(name string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		out = append(out, recordField{name, string(b)})
		return nil
	}
	for _, t := range []struct {
		name string
		v    *string
	}{{FieldTitle, rec.Title}, {FieldOriginalTitle, rec.OriginalTitle}, {FieldSortTitle, rec.SortTitle}, {FieldOverview, rec.Overview}} {
		if t.v != nil && *t.v != "" {
			if err := add(t.name, *t.v); err != nil {
				return nil, err
			}
		}
	}
	if rec.Year != nil && *rec.Year != 0 {
		if err := add(FieldYear, *rec.Year); err != nil {
			return nil, err
		}
	}
	if r := rec.ContentRating; r != nil && r.Region != "" && r.Value != "" {
		if err := add(FieldContentRating, contracts.ContentRating{Region: r.Region, Value: r.Value}); err != nil {
			return nil, err
		}
	}
	if len(rec.Genres) > 0 {
		if err := add(FieldGenres, rec.Genres); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// finishMatch ends a matching attempt with status: the review candidates go
// and the item leaves the queue. reason ("" for none) is kept for the admin.
// The caller has already bumped the revision.
func finishMatch(ctx context.Context, tx *sql.Tx, itemID, status, reason string, now int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM metadata_candidates WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE metadata_items SET match_status = ?, next_attempt_at = NULL, attempts = 0,
		last_error = ?, updated_at = ? WHERE item_id = ?`, status, nullable(bounded(reason)), now, itemID)
	return err
}

// setStatus ends a matching attempt with status (whose outcome has the same
// name) if the item is still at revision rev; a stale item is retried instead.
func (s *Store) setStatus(ctx context.Context, itemID string, rev int64, status, reason string) (string, error) {
	return s.finish(ctx, itemID, rev, func(tx *sql.Tx, now int64) (string, error) {
		return status, finishMatch(ctx, tx, itemID, status, reason, now)
	})
}

// review puts the item in the review queue with the best reviewKeep
// candidates and why ("" for no reason beyond a weak match), if it is still
// at revision rev; a stale item is retried instead.
func (s *Store) review(ctx context.Context, itemID string, rev int64, ranked []Scored, reason string) (string, error) {
	return s.finish(ctx, itemID, rev, func(tx *sql.Tx, now int64) (string, error) {
		if err := finishMatch(ctx, tx, itemID, contracts.MatchReview, reason, now); err != nil {
			return "", err
		}
		for _, c := range ranked[:min(len(ranked), reviewKeep)] {
			var year sql.NullInt64
			if c.Year != 0 {
				year = sql.NullInt64{Int64: int64(c.Year), Valid: true}
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO metadata_candidates(item_id, provider, provider_id, title, year, score)
				VALUES(?, ?, ?, ?, ?, ?)`, itemID, c.Provider, c.ID, c.Title, year, c.Score); err != nil {
				return "", err
			}
		}
		return OutcomeReview, nil
	})
}

// finish runs write in a transaction after a compare-and-set on the revision.
func (s *Store) finish(ctx context.Context, itemID string, rev int64, write func(tx *sql.Tx, now int64) (string, error)) (string, error) {
	now := ms(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := bump(ctx, tx, itemID, rev, now); err != nil {
		if errors.Is(err, ErrRevisionConflict) {
			tx.Rollback() // before retryLater's own write
			return OutcomeRetry, s.retryLater(ctx, itemID, 0, 0, errStale.Error())
		}
		return "", err
	}
	out, err := write(tx, now)
	if err != nil {
		return "", err
	}
	return out, tx.Commit()
}

// retryDelay is the wait before attempt (1, 2, 4 … minutes, capped at a
// day). The shift is clamped first, so it can't overflow.
func retryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return retryBase
	}
	return min(retryBase<<min(attempt-1, maxShift), retryCap)
}

// retryLater queues the item again after attempt's backoff (with jitter),
// keeping its metadata and recording why. With rev > 0 it is compare-and-set:
// an item edited since rev was read is left alone (the edit already queued
// or settled it).
func (s *Store) retryLater(ctx context.Context, itemID string, rev int64, attempt int, reason string) error {
	now := s.now()
	at := now.Add(s.jitter(retryDelay(attempt)))
	_, err := s.db.ExecContext(ctx, `UPDATE metadata_items SET next_attempt_at = ?, attempts = ?, last_error = ?, updated_at = ?
		WHERE item_id = ? AND (? = 0 OR revision = ?)`,
		ms(at), attempt, nullable(bounded(reason)), ms(now), itemID, rev, rev)
	return err
}

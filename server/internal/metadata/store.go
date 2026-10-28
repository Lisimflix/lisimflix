// Package metadata matches catalogue items to provider records and keeps
// their descriptive metadata with provenance and locks per field (META-1,
// spec §3.2 and §3.7). Matching never blocks browsing or playback: an item
// without metadata shows its parsed name (LIB-2a).
package metadata

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations is the metadata schema, for storage.Migrate.
// Planned: META-1c (serve registers the metadata migrations).
func Migrations() storage.Source {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err) // embedded at build time
	}
	return storage.Source{Module: "metadata", FS: sub}
}

// Editable fields, named as CTR-2's JSON keys.
const (
	FieldTitle         = "title"
	FieldSortTitle     = "sortTitle"
	FieldOriginalTitle = "originalTitle"
	FieldOverview      = "overview"
	FieldYear          = "year"
	FieldContentRating = "contentRating"
	FieldGenres        = "genres"
)

// SourceUser is the provenance of a manual edit or a fix-match.
const SourceUser = "user"

var (
	ErrItemNotFound     = errors.New("metadata: item not found")
	ErrRevisionConflict = errors.New("metadata: revision conflict")
	ErrInvalid          = errors.New("metadata: invalid")
	ErrPaused           = errors.New("metadata: provider paused")
)

// Limits (plan Decision 8).
const (
	maxTitle    = 512
	maxOverview = 10000
	minYear     = 1870
	maxYear     = 2200
	maxRating   = 16
	maxGenres   = 20
	maxGenre    = 64
	maxErrorLen = 500
	maxIDLen    = 64
)

var (
	nsRe     = regexp.MustCompile(`^[a-z][a-z0-9]{1,31}$`)
	regionRe = regexp.MustCompile(`^[A-Z]{2}$`)
)

// Store keeps matched metadata in the shared database. It reads the
// catalogue's tables but never writes them.
type Store struct {
	db     *storage.DB
	now    func() time.Time
	jitter func(time.Duration) time.Duration
}

// NewStore returns a store over db.
// Planned: META-1c (serve and the metadata HTTP API create it).
func NewStore(db *storage.DB) *Store {
	return &Store{db: db, now: time.Now, jitter: defaultJitter}
}

// defaultJitter adds 0-10% to d.
func defaultJitter(d time.Duration) time.Duration {
	if d < 10 {
		return d
	}
	return d + time.Duration(rand.Int64N(int64(d)/10+1))
}

// Edit is one change to a field: a new Value (JSON null clears it), a Lock
// change, or both. A value without Lock is locked.
type Edit struct {
	Field string
	Value json.RawMessage
	Lock  *bool
}

func ms(t time.Time) int64 { return t.UnixMilli() }

// bounded cuts s to at most maxErrorLen bytes on a character boundary.
func bounded(s string) string {
	if len(s) <= maxErrorLen {
		return s
	}
	s = s[:maxErrorLen]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// bump advances the item's revision if it is still expected, creating the
// metadata row (queued) when the item has none. Call it inside a transaction.
func bump(ctx context.Context, tx *sql.Tx, itemID string, expected, now int64) error {
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM library_items WHERE id = ?`, itemID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO metadata_items(item_id, revision, match_status, next_attempt_at, updated_at)
		VALUES(?, 1, 'unmatched', ?, ?)`, itemID, now, now); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE metadata_items SET revision = revision + 1, updated_at = ? WHERE item_id = ? AND revision = ?`,
		now, itemID, expected)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRevisionConflict
	}
	return nil
}

// Get returns the item's metadata: its parsed name until matching or an edit
// supplies values, overlaid with stored fields and provider IDs.
// Planned: META-1c (the metadata HTTP API).
func (s *Store) Get(ctx context.Context, itemID string) (contracts.ItemMetadata, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return contracts.ItemMetadata{}, err
	}
	defer tx.Rollback()
	return get(ctx, tx, itemID)
}

func get(ctx context.Context, q querier, itemID string) (contracts.ItemMetadata, error) {
	var title, sortTitle string
	var year sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT title, sort_title, year FROM library_items WHERE id = ?`, itemID).Scan(&title, &sortTitle, &year)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.ItemMetadata{}, ErrItemNotFound
	}
	if err != nil {
		return contracts.ItemMetadata{}, err
	}
	md := contracts.ItemMetadata{ItemID: itemID, Revision: 1, MatchStatus: contracts.MatchUnmatched, ExternalIDs: []contracts.MetadataExternalID{}}
	md.Fields.Title.Value = &title
	md.Fields.SortTitle.Value = &sortTitle
	if year.Valid {
		y := int(year.Int64)
		md.Fields.Year.Value = &y
	}
	md.Fields.Genres.Value = []string{}
	err = q.QueryRowContext(ctx, `SELECT revision, match_status FROM metadata_items WHERE item_id = ?`, itemID).Scan(&md.Revision, &md.MatchStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return md, err
	}

	rows, err := q.QueryContext(ctx, `SELECT field, value, provider, provider_id, fetched_at, locked FROM metadata_fields WHERE item_id = ?`, itemID)
	if err != nil {
		return md, err
	}
	defer rows.Close()
	for rows.Next() {
		var field string
		var value, provider, providerID sql.NullString
		var fetched sql.NullInt64
		var locked bool
		if err := rows.Scan(&field, &value, &provider, &providerID, &fetched, &locked); err != nil {
			return md, err
		}
		src := provenance(provider, providerID, fetched)
		if err := overlay(&md.Fields, field, value, src, locked); err != nil {
			return md, fmt.Errorf("metadata: field %s of %s: %w", field, itemID, err)
		}
	}
	if err := rows.Err(); err != nil {
		return md, err
	}
	rows.Close()

	ids, err := q.QueryContext(ctx, `SELECT namespace, value, provider, provider_id, fetched_at, locked FROM metadata_external_ids WHERE item_id = ? ORDER BY namespace`, itemID)
	if err != nil {
		return md, err
	}
	defer ids.Close()
	for ids.Next() {
		var x contracts.MetadataExternalID
		var provider, providerID sql.NullString
		var fetched sql.NullInt64
		if err := ids.Scan(&x.Namespace, &x.ID, &provider, &providerID, &fetched, &x.Locked); err != nil {
			return md, err
		}
		x.Source = provenance(provider, providerID, fetched)
		md.ExternalIDs = append(md.ExternalIDs, x)
	}
	return md, ids.Err()
}

func provenance(provider, providerID sql.NullString, fetched sql.NullInt64) *contracts.Provenance {
	if !provider.Valid {
		return nil
	}
	p := &contracts.Provenance{Provider: provider.String}
	if providerID.Valid {
		p.ProviderID = &providerID.String
	}
	if fetched.Valid {
		p.FetchedAt = time.UnixMilli(fetched.Int64).UTC()
	}
	return p
}

// overlay applies a stored row to the field it names. A NULL value with a
// provenance is an explicit clear; without one (a lock-only row) it keeps the
// parsed value.
func overlay(f *contracts.MetadataFields, field string, value sql.NullString, src *contracts.Provenance, locked bool) error {
	set := func(v any) error {
		if !value.Valid {
			if src == nil {
				return nil
			}
			return json.Unmarshal([]byte("null"), v)
		}
		return json.Unmarshal([]byte(value.String), v)
	}
	switch field {
	case FieldTitle:
		f.Title.Source, f.Title.Locked = src, locked
		return set(&f.Title.Value)
	case FieldSortTitle:
		f.SortTitle.Source, f.SortTitle.Locked = src, locked
		return set(&f.SortTitle.Value)
	case FieldOriginalTitle:
		f.OriginalTitle.Source, f.OriginalTitle.Locked = src, locked
		return set(&f.OriginalTitle.Value)
	case FieldOverview:
		f.Overview.Source, f.Overview.Locked = src, locked
		return set(&f.Overview.Value)
	case FieldYear:
		f.Year.Source, f.Year.Locked = src, locked
		return set(&f.Year.Value)
	case FieldContentRating:
		f.ContentRating.Source, f.ContentRating.Locked = src, locked
		return set(&f.ContentRating.Value)
	case FieldGenres:
		f.Genres.Source, f.Genres.Locked = src, locked
		if err := set(&f.Genres.Value); err != nil {
			return err
		}
		if f.Genres.Value == nil {
			f.Genres.Value = []string{}
		}
		return nil
	}
	return fmt.Errorf("unknown field")
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// validate checks one edit and returns the JSON to store (nil clears).
func validate(e Edit) (stored any, hasValue bool, err error) {
	switch e.Field {
	case FieldTitle, FieldSortTitle, FieldOriginalTitle, FieldOverview, FieldYear, FieldContentRating, FieldGenres:
	default:
		return nil, false, invalid("unknown field %q", e.Field)
	}
	if e.Value == nil {
		if e.Lock == nil {
			return nil, false, invalid("%s: no value or lock change", e.Field)
		}
		return nil, false, nil
	}
	isNull := strings.TrimSpace(string(e.Value)) == "null"
	if isNull {
		if e.Field == FieldTitle {
			return nil, false, invalid("title can't be cleared")
		}
		return nil, true, nil
	}
	text := func(max int, nonEmpty bool) (any, bool, error) {
		var v string
		if err := json.Unmarshal(e.Value, &v); err != nil {
			return nil, false, invalid("%s must be text", e.Field)
		}
		if len(v) > max || (nonEmpty && strings.TrimSpace(v) == "") {
			return nil, false, invalid("%s must be 1-%d bytes", e.Field, max)
		}
		return v, true, nil
	}
	switch e.Field {
	case FieldTitle, FieldSortTitle, FieldOriginalTitle:
		return text(maxTitle, true)
	case FieldOverview:
		return text(maxOverview, false)
	case FieldYear:
		var v int
		if err := json.Unmarshal(e.Value, &v); err != nil {
			return nil, false, invalid("year must be a whole number")
		}
		if v < minYear || v > maxYear {
			return nil, false, invalid("year must be %d-%d", minYear, maxYear)
		}
		return v, true, nil
	case FieldContentRating:
		var v contracts.ContentRating
		if err := json.Unmarshal(e.Value, &v); err != nil {
			return nil, false, invalid("contentRating must be {region, value}")
		}
		if !regionRe.MatchString(v.Region) || len(v.Value) < 1 || len(v.Value) > maxRating {
			return nil, false, invalid("contentRating needs an upper-case region and a value of 1-%d bytes", maxRating)
		}
		return v, true, nil
	default: // genres
		var v []string
		if err := json.Unmarshal(e.Value, &v); err != nil {
			return nil, false, invalid("genres must be a list of text")
		}
		if len(v) > maxGenres {
			return nil, false, invalid("at most %d genres", maxGenres)
		}
		for _, g := range v {
			if strings.TrimSpace(g) == "" || len(g) > maxGenre {
				return nil, false, invalid("each genre must be 1-%d bytes", maxGenre)
			}
		}
		return v, true, nil
	}
}

// Edit applies manual changes if expectedRevision is still current. A value
// is recorded with "user" provenance and locked unless Lock is false.
// Planned: META-1c (the metadata HTTP API edit operation).
func (s *Store) Edit(ctx context.Context, itemID string, expectedRevision int64, edits []Edit) (contracts.ItemMetadata, error) {
	type change struct {
		field    string
		json     any
		hasValue bool
		lock     *bool
	}
	if len(edits) == 0 {
		return contracts.ItemMetadata{}, invalid("no edits")
	}
	changes := make([]change, 0, len(edits))
	for _, e := range edits {
		v, has, err := validate(e)
		if err != nil {
			return contracts.ItemMetadata{}, err
		}
		changes = append(changes, change{e.Field, v, has, e.Lock})
	}
	now := ms(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.ItemMetadata{}, err
	}
	defer tx.Rollback()
	if err := bump(ctx, tx, itemID, expectedRevision, now); err != nil {
		return contracts.ItemMetadata{}, err
	}
	for _, c := range changes {
		lock := true
		if c.lock != nil {
			lock = *c.lock
		}
		if !c.hasValue {
			// lock-only: keep value and provenance
			_, err = tx.ExecContext(ctx, `INSERT INTO metadata_fields(item_id, field, locked) VALUES(?, ?, ?)
				ON CONFLICT(item_id, field) DO UPDATE SET locked = excluded.locked`, itemID, c.field, lock)
		} else {
			var val any
			if c.json != nil {
				b, merr := json.Marshal(c.json)
				if merr != nil {
					return contracts.ItemMetadata{}, merr
				}
				val = string(b)
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO metadata_fields(item_id, field, value, provider, fetched_at, locked) VALUES(?, ?, ?, ?, ?, ?)
				ON CONFLICT(item_id, field) DO UPDATE SET value = excluded.value, provider = excluded.provider,
				provider_id = NULL, fetched_at = excluded.fetched_at, locked = excluded.locked`,
				itemID, c.field, val, SourceUser, now, lock)
		}
		if err != nil {
			return contracts.ItemMetadata{}, err
		}
	}
	md, err := get(ctx, tx, itemID)
	if err != nil {
		return md, err
	}
	return md, tx.Commit()
}

// FixMatch records the admin's choice of provider record as a locked "user"
// ID and replaces the previous match: other provider IDs and unlocked fields
// go, review candidates are cleared, and the item is queued now (Decision 9).
// Planned: META-1c (the metadata HTTP API fix-match operation).
func (s *Store) FixMatch(ctx context.Context, itemID string, expectedRevision int64, namespace, id string) (contracts.ItemMetadata, error) {
	if !nsRe.MatchString(namespace) {
		return contracts.ItemMetadata{}, invalid("namespace %q", namespace)
	}
	if len(id) < 1 || len(id) > maxIDLen {
		return contracts.ItemMetadata{}, invalid("id must be 1-%d bytes", maxIDLen)
	}
	now := ms(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.ItemMetadata{}, err
	}
	defer tx.Rollback()
	if err := bump(ctx, tx, itemID, expectedRevision, now); err != nil {
		return contracts.ItemMetadata{}, err
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO metadata_external_ids(item_id, namespace, value, provider, fetched_at, locked) VALUES(?, ?, ?, ?, ?, 1)
			ON CONFLICT(item_id, namespace) DO UPDATE SET value = excluded.value, provider = excluded.provider,
			provider_id = NULL, fetched_at = excluded.fetched_at, locked = 1`, []any{itemID, namespace, id, SourceUser, now}},
		{`DELETE FROM metadata_external_ids WHERE item_id = ? AND namespace <> ?`, []any{itemID, namespace}},
		{`DELETE FROM metadata_fields WHERE item_id = ? AND locked = 0`, []any{itemID}},
		{`DELETE FROM metadata_candidates WHERE item_id = ?`, []any{itemID}},
		{`UPDATE metadata_items SET match_status = 'unmatched', next_attempt_at = ?, attempts = 0, last_error = NULL WHERE item_id = ?`, []any{now, itemID}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return contracts.ItemMetadata{}, err
		}
	}
	md, err := get(ctx, tx, itemID)
	if err != nil {
		return md, err
	}
	return md, tx.Commit()
}

// Enqueue queues up to limit movies and shows that have no metadata row yet,
// oldest first, and returns how many it queued.
// Planned: META-1c (the scan listener and the metadata job loop queue new items).
func (s *Store) Enqueue(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	now := ms(s.now())
	res, err := s.db.ExecContext(ctx, `INSERT INTO metadata_items(item_id, revision, match_status, next_attempt_at, updated_at)
		SELECT id, 1, 'unmatched', ?, ? FROM library_items li
		WHERE kind IN ('movie', 'show') AND NOT EXISTS (SELECT 1 FROM metadata_items m WHERE m.item_id = li.id)
		ORDER BY created_at, id LIMIT ?`, now, now, limit)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// Pause stops calls to provider until Resume, recording why.
// Planned: META-1c (the metadata job loop pauses a provider whose key is refused).
func (s *Store) Pause(ctx context.Context, provider, reason string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO metadata_providers(name, paused_reason, paused_at) VALUES(?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET paused_reason = excluded.paused_reason, paused_at = excluded.paused_at`,
		provider, bounded(reason), ms(s.now()))
	return err
}

// Resume clears a provider's pause.
// Planned: META-1b (the settings screen resumes TMDB when the key changes).
func (s *Store) Resume(ctx context.Context, provider string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM metadata_providers WHERE name = ?`, provider)
	return err
}

// Paused reports whether provider is paused, and why.
// Planned: META-1c (the metadata job loop and the admin screen read it).
func (s *Store) Paused(ctx context.Context, provider string) (bool, string, error) {
	var reason sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT paused_reason FROM metadata_providers WHERE name = ?`, provider).Scan(&reason)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	return err == nil, reason.String, err
}

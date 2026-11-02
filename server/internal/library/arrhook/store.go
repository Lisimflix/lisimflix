// Package arrhook is LIB-4: Sonarr and Radarr import notifications. An
// instance that has a source here may ask for a library scan of the folders
// it was given, with its own paths mapped onto the media root. The
// notifications are hints: the catalogue still decides what is in the library
// by scanning. Nothing here deletes media or catalogue entries.
//
// Planned: LIB-4 (the webhook handler in a later task calls this store).
package arrhook

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations is the import-notification schema, for storage.Migrate.
func Migrations() storage.Source {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err) // embedded at build time
	}
	return storage.Source{Module: "arrhook", FS: sub}
}

// Sonarr and Radarr are the source kinds.
const (
	Sonarr = "sonarr"
	Radarr = "radarr"
)

var (
	ErrNotFound     = errors.New("arrhook: not found")
	ErrInvalid      = errors.New("arrhook: invalid source")
	ErrUnauthorized = errors.New("arrhook: unauthorized")
)

// Receipt results.
const (
	ResultQueued   = "queued"
	ResultTest     = "test"
	ResultIgnored  = "ignored"
	ResultRejected = "rejected"
)

// keepReceipts is how many receipts each source keeps for the admin view.
const keepReceipts = 200

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Mapping turns a folder as the source reports it (Remote, absolute, "/tv" or
// "C:/Media/TV") into the same folder relative to the media root (Local).
type Mapping struct{ Remote, Local string }

// Root is one library folder a source may queue scans in.
type Root struct{ ID, Path string }

// Source is an authorised Sonarr or Radarr instance.
type Source struct {
	ID, Name, Kind string
	Roots          []Root
	Mappings       []Mapping
	Revoked        bool
}

// Scope is a folder (or file) of one library root that a notification queued.
type Scope struct {
	RootID string `json:"rootId"`
	Path   string `json:"path"`
}

// Receipt records what one notification did.
type Receipt struct {
	ID, SourceID string
	At           time.Time
	Event, App   string
	Result       string
	Reason       string
	Scopes       []Scope
	RequestID    string
}

// Store is the import-notification persistence on the shared database.
type Store struct {
	db  *storage.DB
	now func() time.Time
}

func NewStore(db *storage.DB) *Store { return &Store{db: db, now: time.Now} }

// Add creates a source and returns it with its token, shown once: only the
// token's hash is stored.
func (s *Store) Add(ctx context.Context, name, kind string, roots []string, maps []Mapping) (Source, string, error) {
	switch {
	case !nameRe.MatchString(name):
		return Source{}, "", fmt.Errorf("%w: name must be lowercase letters, digits and hyphens", ErrInvalid)
	case kind != Sonarr && kind != Radarr:
		return Source{}, "", fmt.Errorf("%w: kind must be sonarr or radarr", ErrInvalid)
	case len(roots) == 0:
		return Source{}, "", fmt.Errorf("%w: at least one folder is required", ErrInvalid)
	case len(maps) == 0:
		return Source{}, "", fmt.Errorf("%w: at least one path mapping is required", ErrInvalid)
	}
	clean := make([]Mapping, len(maps))
	seen := map[string]bool{}
	for i, m := range maps {
		remote, ok := normRemote(m.Remote)
		local := path.Clean(m.Local)
		switch {
		case !ok:
			return Source{}, "", fmt.Errorf("%w: remote path %q must be absolute", ErrInvalid, m.Remote)
		case !fs.ValidPath(local):
			return Source{}, "", fmt.Errorf("%w: local path %q must be inside the media root", ErrInvalid, m.Local)
		case seen[remote]:
			return Source{}, "", fmt.Errorf("%w: remote path %q is mapped twice", ErrInvalid, remote)
		}
		seen[remote] = true
		clean[i] = Mapping{remote, local}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Source{}, "", err
	}
	defer tx.Rollback()

	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM arrhook_sources WHERE name = ?`, name).Scan(&taken); err != nil {
		return Source{}, "", err
	}
	if taken > 0 {
		return Source{}, "", fmt.Errorf("%w: a source named %q exists", ErrInvalid, name)
	}
	src := Source{ID: "src-" + strings.ToLower(rand.Text()), Name: name, Kind: kind, Mappings: clean}
	token := "lfh_" + rand.Text()
	hash := sha256.Sum256([]byte(token))
	if _, err := tx.ExecContext(ctx, `INSERT INTO arrhook_sources(id, name, kind, token_hash, created_at) VALUES(?, ?, ?, ?, ?)`,
		src.ID, name, kind, hash[:], s.now().UnixMilli()); err != nil {
		return Source{}, "", err
	}
	done := map[string]bool{}
	for _, p := range roots {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM library_roots WHERE path = ?`, p).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return Source{}, "", fmt.Errorf("%w: %q is not a library folder", ErrInvalid, p)
		}
		if err != nil {
			return Source{}, "", err
		}
		if done[id] {
			continue
		}
		done[id] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO arrhook_roots(source_id, root_id) VALUES(?, ?)`, src.ID, id); err != nil {
			return Source{}, "", err
		}
		src.Roots = append(src.Roots, Root{ID: id, Path: p})
	}
	for _, m := range clean {
		if _, err := tx.ExecContext(ctx, `INSERT INTO arrhook_mappings(source_id, remote, local) VALUES(?, ?, ?)`,
			src.ID, m.Remote, m.Local); err != nil {
			return Source{}, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return Source{}, "", err
	}
	return src, token, nil
}

// Authenticate returns the source if token is its live token. Every failure,
// including an unknown ID, returns ErrUnauthorized; an unknown ID is compared
// against a zero hash so the timing doesn't reveal it.
func (s *Store) Authenticate(ctx context.Context, id, token string) (Source, error) {
	var want []byte
	var revoked sql.NullInt64
	found := true
	err := s.db.QueryRowContext(ctx, `SELECT token_hash, revoked_at FROM arrhook_sources WHERE id = ?`, id).Scan(&want, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		found, want, err = false, make([]byte, sha256.Size), nil
	}
	if err != nil {
		return Source{}, err
	}
	sum := sha256.Sum256([]byte(token))
	match := subtle.ConstantTimeCompare(sum[:], want) == 1
	if !found || !match || revoked.Valid {
		return Source{}, ErrUnauthorized
	}
	return s.source(ctx, id)
}

// Sources lists every source by name, revoked ones included.
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM arrhook_sources ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Source, 0, len(ids))
	for _, id := range ids {
		src, err := s.source(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, nil
}

// Revoke refuses every later request from the named source.
func (s *Store) Revoke(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE arrhook_sources SET revoked_at = ? WHERE name = ? AND revoked_at IS NULL`,
		s.now().UnixMilli(), name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// source loads one source with its roots and mappings.
func (s *Store) source(ctx context.Context, id string) (Source, error) {
	var src Source
	var revoked sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id, name, kind, revoked_at FROM arrhook_sources WHERE id = ?`, id).
		Scan(&src.ID, &src.Name, &src.Kind, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, err
	}
	src.Revoked = revoked.Valid

	rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.path FROM arrhook_roots r
		JOIN library_roots l ON l.id = r.root_id WHERE r.source_id = ? ORDER BY l.path`, id)
	if err != nil {
		return Source{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Root
		if err := rows.Scan(&r.ID, &r.Path); err != nil {
			return Source{}, err
		}
		src.Roots = append(src.Roots, r)
	}
	if err := rows.Err(); err != nil {
		return Source{}, err
	}

	mrows, err := s.db.QueryContext(ctx, `SELECT remote, local FROM arrhook_mappings WHERE source_id = ? ORDER BY remote`, id)
	if err != nil {
		return Source{}, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var m Mapping
		if err := mrows.Scan(&m.Remote, &m.Local); err != nil {
			return Source{}, err
		}
		src.Mappings = append(src.Mappings, m)
	}
	return src, mrows.Err()
}

// record stores a receipt and keeps only the newest keepReceipts for its source.
func (s *Store) record(ctx context.Context, r Receipt) error {
	scopes := r.Scopes
	if scopes == nil {
		scopes = []Scope{}
	}
	js, err := json.Marshal(scopes)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO arrhook_receipts(id, source_id, received_at, event, app, result, reason, scopes, request_id)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.SourceID, r.At.UnixMilli(), r.Event, r.App, r.Result, nullable(r.Reason), string(js), nullable(r.RequestID)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM arrhook_receipts WHERE source_id = ? AND rowid NOT IN (
		SELECT rowid FROM arrhook_receipts WHERE source_id = ? ORDER BY received_at DESC, rowid DESC LIMIT ?)`,
		r.SourceID, r.SourceID, keepReceipts); err != nil {
		return err
	}
	return tx.Commit()
}

// Receipts returns the newest n receipts of a source, newest first.
func (s *Store) Receipts(ctx context.Context, sourceID string, n int) ([]Receipt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, received_at, event, app, result, reason, scopes, request_id
		FROM arrhook_receipts WHERE source_id = ? ORDER BY received_at DESC, rowid DESC LIMIT ?`, sourceID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Receipt
	for rows.Next() {
		var r Receipt
		var at int64
		var reason, reqID sql.NullString
		var scopes string
		if err := rows.Scan(&r.ID, &at, &r.Event, &r.App, &r.Result, &reason, &scopes, &reqID); err != nil {
			return nil, err
		}
		r.SourceID = sourceID
		r.At = time.UnixMilli(at)
		r.Reason, r.RequestID = reason.String, reqID.String
		if err := json.Unmarshal([]byte(scopes), &r.Scopes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

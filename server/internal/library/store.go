package library

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/library/naming"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations is the catalogue's schema, for storage.Migrate.
// Planned: LIB-2b (serve registers it with the other modules' migrations).
func Migrations() storage.Source {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err) // embedded at build time
	}
	return storage.Source{Module: "library", FS: sub}
}

var (
	ErrNotFound = errors.New("library: not found")
	ErrInvalid  = errors.New("library: invalid library")
)

// Root states (spec §3.6).
const (
	RootAvailable = "available"
	RootScanning  = "scanning"
	RootDegraded  = "degraded"
	RootOffline   = "offline"
)

type Library struct {
	ID, Name, Type string
	Roots          []Root
}

// Root is one folder of a library, relative to the media root. An offline or
// degraded root keeps its catalogue entries (spec §3.6).
type Root struct {
	ID, LibraryID, Path string
	State               string
	Reason              string    // why the last scan failed; "" after a success
	LastOK              time.Time // zero until a scan succeeds
}

// Store is the catalogue's persistence on the shared database.
type Store struct {
	db  *storage.DB
	now func() time.Time
}

// Planned: LIB-2b (serve builds the store and scanner).
func NewStore(db *storage.DB) *Store { return &Store{db: db, now: time.Now} }

func newID(prefix string) string { return prefix + "-" + rand.Text() }

func ms(t time.Time) int64 { return t.UnixMilli() }

// CreateLibrary adds a library over one or more folders, given relative to the
// media root. A folder can't be the media root's parent, nor overlap a folder
// of any library.
// Planned: LIB-2c (the libraries API).
func (s *Store) CreateLibrary(ctx context.Context, name, typ string, roots []string) (Library, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "" || len(name) > 200:
		return Library{}, fmt.Errorf("%w: name must be 1–200 characters", ErrInvalid)
	case typ != naming.Movies && typ != naming.TV && typ != naming.Anime:
		return Library{}, fmt.Errorf("%w: type must be movies, tv or anime", ErrInvalid)
	case len(roots) == 0:
		return Library{}, fmt.Errorf("%w: at least one folder is required", ErrInvalid)
	}
	clean := make([]string, len(roots))
	for i, r := range roots {
		c := path.Clean(strings.TrimPrefix(r, "./"))
		if !fs.ValidPath(c) {
			return Library{}, fmt.Errorf("%w: folder %q must be inside the media root", ErrInvalid, r)
		}
		clean[i] = c
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	existing, err := queryStrings(ctx, tx, `SELECT path FROM library_roots`)
	if err != nil {
		return Library{}, err
	}
	for i, c := range clean {
		for _, other := range append(existing, clean[:i]...) {
			if overlaps(c, other) {
				return Library{}, fmt.Errorf("%w: folder %q overlaps %q", ErrInvalid, c, other)
			}
		}
	}
	lib := Library{ID: newID("lib"), Name: name, Type: typ}
	if _, err := tx.ExecContext(ctx, `INSERT INTO library_libraries(id, name, type, created_at) VALUES(?, ?, ?, ?)`,
		lib.ID, name, typ, ms(s.now())); err != nil {
		return Library{}, err
	}
	for _, c := range clean {
		r := Root{ID: newID("root"), LibraryID: lib.ID, Path: c, State: RootAvailable}
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_roots(id, library_id, path) VALUES(?, ?, ?)`, r.ID, lib.ID, c); err != nil {
			return Library{}, err
		}
		lib.Roots = append(lib.Roots, r)
	}
	return lib, tx.Commit()
}

// overlaps reports whether one folder is the other or inside it. "." (the
// whole media root) overlaps everything.
func overlaps(a, b string) bool {
	return a == b || a == "." || b == "." || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// Library returns one library and its roots.
// Planned: LIB-2c (the libraries API).
func (s *Store) Library(ctx context.Context, id string) (Library, error) {
	var lib Library
	err := s.db.QueryRowContext(ctx, `SELECT id, name, type FROM library_libraries WHERE id = ?`, id).Scan(&lib.ID, &lib.Name, &lib.Type)
	if errors.Is(err, sql.ErrNoRows) {
		return Library{}, ErrNotFound
	}
	if err != nil {
		return Library{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM library_roots WHERE library_id = ? ORDER BY path`, id)
	if err != nil {
		return Library{}, err
	}
	ids, err := scanStrings(rows)
	if err != nil {
		return Library{}, err
	}
	for _, rid := range ids {
		r, _, err := s.root(ctx, rid)
		if err != nil {
			return Library{}, err
		}
		lib.Roots = append(lib.Roots, r)
	}
	return lib, nil
}

// root returns a root and its library's type.
func (s *Store) root(ctx context.Context, id string) (Root, string, error) {
	var r Root
	var reason sql.NullString
	var lastOK sql.NullInt64
	var typ string
	err := s.db.QueryRowContext(ctx, `SELECT r.id, r.library_id, r.path, r.state, r.reason, r.last_ok_at, l.type
		FROM library_roots r JOIN library_libraries l ON l.id = r.library_id WHERE r.id = ?`, id).
		Scan(&r.ID, &r.LibraryID, &r.Path, &r.State, &reason, &lastOK, &typ)
	if errors.Is(err, sql.ErrNoRows) {
		return Root{}, "", ErrNotFound
	}
	if err != nil {
		return Root{}, "", err
	}
	r.Reason = reason.String
	if lastOK.Valid {
		r.LastOK = time.UnixMilli(lastOK.Int64)
	}
	return r, typ, nil
}

// MarkInterrupted ends runs left "running" by a crash or restart. Scans are
// idempotent, so the next scan of the root simply starts again (spec §3.6).
// Planned: LIB-2b (serve calls it at startup).
func (s *Store) MarkInterrupted(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE library_scan_runs SET status = 'interrupted', finished_at = ? WHERE status = 'running'`, ms(s.now()))
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_roots SET state = 'degraded', reason = 'interrupted' WHERE state = 'scanning'`); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), tx.Commit()
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryStrings(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanStrings(rows)
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Package storage owns Lisimflix's SQLite database: opening it safely on local
// disk, applying module migrations, durable settings, and consistent
// snapshot/restore (spec §§10.2, 11.3–11.4; BASE-03, OPS-01).
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"

	_ "modernc.org/sqlite"
)

// DB is the shared connection pool. Modules own their SQL through it.
type DB struct {
	*sql.DB
	path string
}

// ErrNetworkFilesystem means the database directory is on NFS/SMB/CIFS, where
// SQLite WAL is unsafe (spec §10.2).
var ErrNetworkFilesystem = errors.New("storage: database must be on local disk, not a network filesystem")

// Open opens or creates the database at path with WAL, foreign keys, a busy
// timeout and IMMEDIATE write transactions on every connection.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := checkLocalFilesystem(path); err != nil {
		return nil, err
	}
	// Create the file 0600 before SQLite does: it holds settings and secrets,
	// and SQLite gives -wal/-shm files the database file's mode.
	if err := ensurePrivateFile(path); err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "foreign_keys(1)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	sdb, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}
	var mode string
	if err := sdb.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("storage: open: %w", err)
	}
	if mode != "wal" {
		sdb.Close()
		return nil, fmt.Errorf("storage: open: journal_mode is %q, want wal", mode)
	}
	return &DB{DB: sdb, path: path}, nil
}

// ensurePrivateFile creates path with mode 0600, or tightens an existing file.
func ensurePrivateFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	return os.Chmod(path, 0o600)
}

// Path is the database file path.
// Planned: OPS-5 (storage diagnostics show where the database lives).
func (d *DB) Path() string { return d.path }

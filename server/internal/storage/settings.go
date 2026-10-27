package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GetSetting returns a durable setting; ok is false when it is unset. Values
// may be secrets: callers must not log them.
// Planned: META-1 (provider settings such as API keys).
func (d *DB) GetSetting(ctx context.Context, key string) (value string, ok bool, err error) {
	err = d.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("storage: get setting %s: %w", key, err)
	}
	return value, true, nil
}

// SetSetting inserts or replaces a durable setting.
// Planned: META-1 (provider settings such as API keys).
func (d *DB) SetSetting(ctx context.Context, key, value string, now time.Time) error {
	_, err := d.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("storage: set setting %s: %w", key, err)
	}
	return nil
}

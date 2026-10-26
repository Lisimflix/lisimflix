package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"time"
)

// ReuseWindow is how recent a same-schema pre-upgrade snapshot must be to be
// reused instead of taking a new one. It covers a crash loop (minutes), not a
// later upgrade after a rollback, whose data may have changed since.
const ReuseWindow = time.Hour

// MigrateWithBackup takes a verified "pre-upgrade" snapshot before applying
// pending migrations to an existing database (spec §11.4). A brand-new
// database is migrated without one. If the newest pre-upgrade snapshot has this
// database's schema and is younger than ReuseWindow, it is reused, so a
// container restarting on a failing migration doesn't write a copy every start.
func (d *DB) MigrateWithBackup(ctx context.Context, srcs []Source, o SnapshotOptions, now func() time.Time) ([]string, error) {
	pending, err := d.Pending(ctx, srcs)
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	applied, err := d.Applied(ctx)
	if err != nil {
		return nil, err
	}
	if len(applied) > 0 {
		prev, found, err := latest(o.Dir, "pre-upgrade")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		t := now()
		if !found || !slices.Equal(prev.Schema, applied) || t.Sub(prev.CreatedAt) >= ReuseWindow {
			o.Reason, o.Now = "pre-upgrade", t
			if _, _, err := d.Snapshot(ctx, o); err != nil {
				return nil, fmt.Errorf("storage: pre-upgrade snapshot: %w", err)
			}
			if _, err := Prune(o.Dir); err != nil {
				return nil, fmt.Errorf("storage: prune: %w", err)
			}
		}
	}
	return d.Migrate(ctx, srcs, now)
}

// NextRun is the next local time at hour:00 strictly after now.
func NextRun(now time.Time, hour int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// Nightly takes one snapshot a day and prunes old ones. Failures are logged
// and never prune a usable snapshot.
type Nightly struct {
	DB    *DB
	Opts  SnapshotOptions                      // Dir, AppVersion, ExtraFiles; Reason and Now are set per run
	Hour  int                                  // hour in the container's time zone (TZ; UTC if unset), default 3
	Now   func() time.Time                     // default time.Now
	After func(time.Duration) <-chan time.Time // default time.After
	Log   *slog.Logger                         // default slog.Default()
}

func (n *Nightly) defaults() {
	if n.Hour == 0 {
		n.Hour = 3
	}
	if n.Now == nil {
		n.Now = time.Now
	}
	if n.After == nil {
		n.After = time.After
	}
	if n.Log == nil {
		n.Log = slog.Default()
	}
}

// RunOnce takes a nightly snapshot, then prunes.
func (n *Nightly) RunOnce(ctx context.Context) error {
	n.defaults()
	o := n.Opts
	o.Reason, o.Now = "nightly", n.Now()
	path, _, err := n.DB.Snapshot(ctx, o)
	if err != nil {
		return err
	}
	removed, err := Prune(o.Dir)
	if err != nil {
		return fmt.Errorf("storage: prune: %w", err)
	}
	n.Log.Info("backup snapshot complete", "snapshot", path, "pruned", len(removed))
	return nil
}

// Run loops until ctx is cancelled.
func (n *Nightly) Run(ctx context.Context) {
	n.defaults()
	for {
		now := n.Now()
		wait := NextRun(now, n.Hour).Sub(now)
		select {
		case <-ctx.Done():
			return
		case <-n.After(wait):
		}
		if err := n.RunOnce(ctx); err != nil {
			n.Log.Error("backup snapshot failed", "err", err)
		}
	}
}

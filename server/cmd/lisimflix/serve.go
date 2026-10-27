package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/app"
	"github.com/Lisimflix/lisimflix/server/internal/auth"
	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/m1config"
	"github.com/Lisimflix/lisimflix/server/internal/playback"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
	"github.com/Lisimflix/lisimflix/server/internal/webassets"
)

const shutdownGrace = 15 * time.Second

// authSweepEvery is how often expired auth sessions are ended and their
// streams cut, without waiting for a request (#30).
const authSweepEvery = time.Minute

// server is the composed M1 application and everything shutdown must close.
type server struct {
	cfg        m1config.Config
	handler    http.Handler
	pb         *playback.Server
	root       *os.Root
	db         *storage.DB // nil when storage failed to start
	storageErr error

	stopNightly context.CancelFunc // stops the nightly job and the auth sweep
	nightlyDone chan struct{}
	once        sync.Once
}

// compose wires the approved modules (plan #26): config → media root →
// playback → auth → storage → app. A storage failure keeps serving but
// reports not-ready. extra adds module migration sources.
func compose(ctx context.Context, cfgPath, dbPath, backups string, web fs.FS, extra []storage.Source, log *slog.Logger) (*server, error) {
	cfg, err := m1config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(cfg.MediaRoot)
	if err != nil {
		return nil, errors.New("open the media root failed")
	}
	grantKey := make([]byte, 32) // process-local: grants end with the process
	rand.Read(grantKey)
	v := cfg.Viewer
	pb, err := playback.New(root, playback.Options{
		Source: playback.Source{Movie: cfg.Movie(), RelativePath: cfg.RelativePath}, GrantKey: grantKey,
		MaxSessions: cfg.MaxPlaybackSessions, Log: log,
		Authorize: func(p contracts.Principal, movieID string) bool {
			return p.AccountID == v.AccountID && p.ProfileID == v.ProfileID && movieID == cfg.MovieID
		},
	})
	if err != nil {
		root.Close()
		return nil, err
	}
	am, err := auth.NewManager(v, strings.HasPrefix(cfg.PublicOrigin, "https:"), pb.RevokeAuthSession)
	if err != nil {
		pb.Close()
		root.Close()
		return nil, err
	}
	am.SetLogger(log)

	s := &server{cfg: cfg, pb: pb, root: root, nightlyDone: make(chan struct{})}
	dbPath, backups = storagePaths(cfgPath, dbPath, backups)
	snap := storage.SnapshotOptions{Dir: backups, AppVersion: version, ExtraFiles: []string{cfgPath}}
	s.db, s.storageErr = openStorage(ctx, dbPath, snap, extra)
	switch {
	case errors.Is(s.storageErr, storage.ErrSchemaTooNew):
		log.Error("not ready: the database is newer than this version; restore a compatible snapshot from the backups directory with a matching image", "err", s.storageErr)
	case s.storageErr != nil:
		log.Error("not ready: storage failed to start", "err", s.storageErr)
	}
	nctx, cancel := context.WithCancel(context.Background())
	s.stopNightly = cancel // also stops the auth sweep
	go am.Run(nctx, authSweepEvery)
	if s.db != nil {
		// Hour 0 means the default, 03:00 in the container's TZ (UTC if unset).
		go func() {
			defer close(s.nightlyDone)
			(&storage.Nightly{DB: s.db, Opts: snap, Log: log}).Run(nctx)
		}()
	} else {
		close(s.nightlyDone)
	}

	ready := func(context.Context) error {
		if s.storageErr != nil {
			return s.storageErr
		}
		return pb.Check()
	}
	s.handler, err = app.New(cfg, am, pb.Handler(), web, ready, log)
	if err != nil {
		s.shutdown(ctx, nil)
		return nil, err
	}
	return s, nil
}

func openStorage(ctx context.Context, path string, snap storage.SnapshotOptions, extra []storage.Source) (*storage.DB, error) {
	db, err := storage.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	srcs := append([]storage.Source{storage.Core()}, extra...)
	if _, err := db.MigrateWithBackup(ctx, srcs, snap, time.Now); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// shutdown closes in the approved order: playback first (cutting streams, or
// Shutdown would wait for them), then the HTTP server, the media root, the
// nightly job (a running snapshot finishes, bounded by ctx) and the
// database. srv may be nil. It is safe to call more than once.
func (s *server) shutdown(ctx context.Context, srv *http.Server) error {
	var err error
	s.once.Do(func() {
		s.pb.Close()
		if srv != nil {
			err = srv.Shutdown(ctx)
		}
		s.root.Close()
		s.stopNightly()
		select {
		case <-s.nightlyDone:
		case <-ctx.Done():
		}
		if s.db != nil {
			s.db.Close()
		}
	})
	return err
}

func cmdServe(args []string) error {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fl.String("config", "/config/m1.json", "config file")
	dbPath := fl.String("db", "", "SQLite database (default: lisimflix.db next to --config)")
	backups := fl.String("backups", "", "snapshot directory (default: backups/ next to --config)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	web, err := webassets.FS()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	s, err := compose(ctx, *cfgPath, *dbPath, *backups, web, extraSources(), log)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr: s.cfg.ListenAddr, Handler: s.handler,
		ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10, IdleTimeout: 2 * time.Minute,
		// No WriteTimeout: a movie streams for hours. Playback sets per-write deadlines.
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	if err := s.pb.Check(); err != nil {
		log.Warn("not ready: the movie is missing or changed since init", "err", err)
	}
	log.Info("listening", "addr", s.cfg.ListenAddr, "origin", s.cfg.PublicOrigin, "version", version)
	select {
	case err := <-errc:
		s.shutdown(context.Background(), nil)
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := s.shutdown(sctx, srv); err != nil {
		return err
	}
	log.Info("stopped")
	return nil
}

// cmdBackupNow takes one nightly-style snapshot and prunes (Nightly.RunOnce).
func cmdBackupNow(args []string) error {
	fl := flag.NewFlagSet("backup-now", flag.ContinueOnError)
	cfgPath := fl.String("config", "/config/m1.json", "config file, copied into the snapshot")
	dbPath := fl.String("db", "", "SQLite database (default: lisimflix.db next to --config)")
	backups := fl.String("backups", "", "snapshot directory (default: backups/ next to --config)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	db, dir := storagePaths(*cfgPath, *dbPath, *backups)
	ctx := context.Background()
	d, err := storage.Open(ctx, db)
	if err != nil {
		return err
	}
	defer d.Close()
	n := &storage.Nightly{DB: d, Opts: storage.SnapshotOptions{Dir: dir, AppVersion: version, ExtraFiles: []string{*cfgPath}}}
	return n.RunOnce(ctx)
}

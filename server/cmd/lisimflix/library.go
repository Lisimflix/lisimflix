package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/library"
	"github.com/Lisimflix/lisimflix/server/internal/m1config"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

// defaultFFprobe is where the image installs jellyfin-ffmpeg's ffprobe
// (LIB-1); LISIMFLIX_FFPROBE overrides it for runs outside the image.
const defaultFFprobe = "/usr/lib/jellyfin-ffmpeg/ffprobe"

// newProber returns the probe runner serve uses. Tests replace it.
var newProber = func() library.Prober {
	return probe.Runner{Path: orDefault(os.Getenv("LISIMFLIX_FFPROBE"), defaultFFprobe)}
}

// libraryOptions are the scheduler's settings: the spec defaults. Tests
// shorten them.
var libraryOptions = library.SchedulerOptions{}

func orDefault(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// cmdLibrary adds and lists libraries until the admin screens (LIB-2c)
// exist. A running server picks up a new library within a minute.
func cmdLibrary(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: lisimflix library add|list [flags]")
	}
	fl := flag.NewFlagSet("library "+args[0], flag.ContinueOnError)
	cfgPath := fl.String("config", "/config/m1.json", "config file")
	dbPath := fl.String("db", "", "SQLite database (default: lisimflix.db next to --config)")
	backups := fl.String("backups", "", "snapshot directory (default: backups/ next to --config)")
	name := fl.String("name", "", "library name (add)")
	typ := fl.String("type", "", "movies, tv or anime (add)")
	var folders []string
	fl.Func("folder", "folder inside the media root; repeat for more (add)", func(v string) error {
		folders = append(folders, v)
		return nil
	})
	if err := fl.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := m1config.Load(*cfgPath)
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, dir := storagePaths(*cfgPath, *dbPath, *backups)
	d, err := openStorage(ctx, db, storage.SnapshotOptions{Dir: dir, AppVersion: version, ExtraFiles: []string{*cfgPath}}, nil)
	if err != nil {
		return err
	}
	defer d.Close()
	st := library.NewStore(d)
	switch args[0] {
	case "add":
		media, err := os.OpenRoot(cfg.MediaRoot)
		if err != nil {
			return errors.New("open the media root failed")
		}
		defer media.Close()
		for _, f := range folders {
			if info, err := media.Stat(strings.TrimPrefix(f, "./")); err != nil || !info.IsDir() {
				return fmt.Errorf("folder %q isn't a folder inside the media root", f)
			}
		}
		lib, err := st.CreateLibrary(ctx, *name, *typ, folders)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "added %s (%s)\n", lib.ID, lib.Name)
		return nil
	case "list":
		libs, err := st.Libraries(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "LIBRARY\tNAME\tTYPE\tFOLDER\tSTATE\tLAST SCAN OK")
		for _, l := range libs {
			for _, r := range l.Roots {
				last := "never"
				if !r.LastOK.IsZero() {
					last = r.LastOK.Local().Format(time.DateTime)
				}
				state := r.State
				if r.Reason != "" {
					state += " (" + r.Reason + ")"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", l.ID, l.Name, l.Type, r.Path, state, last)
			}
		}
		return tw.Flush()
	}
	return fmt.Errorf("unknown library command %q", args[0])
}

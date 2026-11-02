package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/Lisimflix/lisimflix/server/internal/library/arrhook"
)

// cmdHook manages Sonarr/Radarr import sources (LIB-4) until the admin
// screens (UI-6) exist. Changes apply to a running server at once: each
// notification reads its source from the database.
func cmdHook(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: lisimflix hook add|list|revoke|receipts [flags]")
	}
	fl := flag.NewFlagSet("hook "+args[0], flag.ContinueOnError)
	cfgPath := fl.String("config", "/config/m1.json", "config file")
	dbPath := fl.String("db", "", "SQLite database (default: lisimflix.db next to --config)")
	backups := fl.String("backups", "", "snapshot directory (default: backups/ next to --config)")
	name := fl.String("name", "", "source name, e.g. sonarr (add, revoke, receipts)")
	kind := fl.String("kind", "", "sonarr or radarr (add)")
	n := fl.Int("n", 20, "how many receipts (receipts)")
	var folders []string
	var maps []arrhook.Mapping
	fl.Func("folder", "library folder the source may scan, as given to library add; repeat for more (add)", func(v string) error {
		folders = append(folders, v)
		return nil
	})
	fl.Func("map", "REMOTE=LOCAL: a folder as Sonarr/Radarr sees it, and the same folder inside the media root; repeat for more (add)", func(v string) error {
		remote, local, ok := strings.Cut(v, "=")
		if !ok {
			return errors.New("want REMOTE=LOCAL, e.g. /tv=TV")
		}
		maps = append(maps, arrhook.Mapping{Remote: remote, Local: local})
		return nil
	})
	if err := fl.Parse(args[1:]); err != nil {
		return err
	}
	ctx := context.Background()
	cfg, d, err := openForCLI(ctx, *cfgPath, *dbPath, *backups)
	if err != nil {
		return err
	}
	defer d.Close()
	st := arrhook.NewStore(d)
	switch args[0] {
	case "add":
		src, token, err := st.Add(ctx, *name, *kind, folders, maps)
		if err != nil {
			return err
		}
		app := map[string]string{arrhook.Sonarr: "Sonarr", arrhook.Radarr: "Radarr"}[src.Kind]
		fmt.Fprintf(stdout, "added %s (%s)\n\n", src.ID, src.Name)
		fmt.Fprintf(stdout, "In %s: Settings > Connect > + > Webhook\n", app)
		unit := map[string]string{arrhook.Sonarr: "Series", arrhook.Radarr: "Movie"}[src.Kind]
		file := map[string]string{arrhook.Sonarr: "Episode File", arrhook.Radarr: "Movie File"}[src.Kind]
		fmt.Fprintf(stdout, "  Triggers:  On Import, On Upgrade, On Rename, On File Delete, On %s Delete For Upgrade, On %s Delete (and On Import Complete)\n",
			file, unit)
		fmt.Fprintf(stdout, "  URL:       %s%s\n", strings.TrimSuffix(cfg.PublicOrigin, "/"), arrhook.Path)
		fmt.Fprintf(stdout, "  Method:    POST\n")
		fmt.Fprintf(stdout, "  Username:  %s\n", src.ID)
		fmt.Fprintf(stdout, "  Password:  %s\n\n", token)
		fmt.Fprintln(stdout, "The password is shown only now. Press Test in "+app+"; `lisimflix hook receipts --name "+src.Name+"` shows the result.")
		return nil
	case "list":
		srcs, err := st.Sources(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "SOURCE\tNAME\tKIND\tFOLDERS\tMAPPINGS\tSTATE")
		for _, s := range srcs {
			var fs, ms []string
			for _, r := range s.Roots {
				fs = append(fs, r.Path)
			}
			for _, m := range s.Mappings {
				ms = append(ms, m.Remote+"="+m.Local)
			}
			state := "active"
			if s.Revoked {
				state = "revoked"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Name, s.Kind, strings.Join(fs, ","), strings.Join(ms, ","), state)
		}
		return tw.Flush()
	case "revoke":
		return st.Revoke(ctx, *name)
	case "receipts":
		srcs, err := st.Sources(ctx)
		if err != nil {
			return err
		}
		for _, s := range srcs {
			if s.Name != *name {
				continue
			}
			rs, err := st.Receipts(ctx, s.ID, *n)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "RECEIVED\tEVENT\tAPP\tRESULT\tSCANS\tREASON")
			for _, r := range rs {
				var sc []string
				for _, x := range r.Scopes {
					sc = append(sc, x.Path)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.At.Local().Format(time.DateTime), printable(r.Event), printable(r.App), r.Result,
					printable(strings.Join(sc, "; ")), printable(r.Reason))
			}
			return tw.Flush()
		}
		return arrhook.ErrNotFound
	}
	return fmt.Errorf("unknown hook command %q", args[0])
}

// printable replaces anything that is not a printable rune (control
// characters, escapes, tabs, newlines) with "?": receipt text is sender-controlled
// and must not drive the admin's terminal or break the table.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return '?'
	}, s)
}

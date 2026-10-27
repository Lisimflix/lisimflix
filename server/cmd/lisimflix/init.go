package main

import (
	"bufio"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/Lisimflix/lisimflix/server/internal/auth"
	"github.com/Lisimflix/lisimflix/server/internal/m1config"
	"github.com/Lisimflix/lisimflix/server/internal/playback"
)

func newID(prefix string) string { return prefix + strings.ToLower(rand.Text()[:16]) }

// cmdInit provisions the owner-only M1 config: one viewer and one source.
// The password never comes from argv: a terminal prompts twice; otherwise
// two lines are read from stdin (`lisimflix init … < pwfile`).
func cmdInit(args []string, stdin io.Reader, stdout io.Writer) error {
	fl := flag.NewFlagSet("init", flag.ContinueOnError)
	cfgPath := fl.String("config", "/config/m1.json", "config file to create")
	origin := fl.String("origin", "", "public origin, e.g. http://192.168.1.20:8420")
	mediaRoot := fl.String("media-root", "/media", "media root (mounted read-only)")
	movie := fl.String("movie", "", "movie path relative to the media root")
	probePath := fl.String("probe-json", "", "ffprobe JSON for the movie (-show_format -show_streams -of json)")
	username := fl.String("username", "", "viewer username")
	title := fl.String("title", "", "movie title (default: the file name)")
	listen := fl.String("listen", ":8420", "listen address")
	if err := fl.Parse(args); err != nil {
		return err
	}
	raw, err := os.ReadFile(*probePath)
	if err != nil {
		return errors.New("read the probe record: " + err.Error())
	}
	pr, err := parseProbe(raw)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(*mediaRoot)
	if err != nil {
		return errors.New("open the media root: " + err.Error())
	}
	defer root.Close()
	info, err := root.Stat(*movie)
	if err != nil {
		return errors.New("the movie is not readable inside the media root")
	}
	pw, err := readPassword(stdin, stdout)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	clear(pw)
	if err != nil {
		return err
	}
	key := make([]byte, 32)
	rand.Read(key)
	movieID := newID("mov-")
	t := *title
	if t == "" {
		t = strings.TrimSuffix(filepath.Base(*movie), filepath.Ext(*movie))
	}
	cfg := m1config.Config{
		ListenAddr: *listen, PublicOrigin: *origin, MediaRoot: *mediaRoot,
		MovieID: movieID, RelativePath: *movie, Title: t, SourceRevision: playback.Revision(movieID, info),
		VideoCodec: pr.VideoCodec, AudioCodec: pr.AudioCodec, Width: pr.Width, Height: pr.Height, DurationMS: pr.DurationMS,
		Viewer:  m1config.Viewer{AccountID: newID("acct-"), ProfileID: newID("prof-"), Username: *username, PasswordHash: hash},
		CSRFKey: key, MaxPlaybackSessions: 2,
	}
	// serve must be able to load what init writes: check the hash the way
	// the runtime will before saving (#30).
	if _, err := auth.NewManager(cfg.Viewer, strings.HasPrefix(cfg.PublicOrigin, "https:"), func(string) {}); err != nil {
		return err
	}
	if err := m1config.SaveNew(*cfgPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s for viewer %q and movie %q (%dx%d %s/%s)\n", *cfgPath, *username, t, pr.Width, pr.Height, pr.VideoCodec, pr.AudioCodec)
	return nil
}

func readPassword(stdin io.Reader, stdout io.Writer) ([]byte, error) {
	var a, b []byte
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(stdout, "Password: ")
		a, _ = term.ReadPassword(int(f.Fd()))
		fmt.Fprint(stdout, "\nAgain: ")
		b, _ = term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stdout)
	} else {
		r := bufio.NewReader(io.LimitReader(stdin, 4096))
		l1, _ := r.ReadString('\n')
		l2, _ := r.ReadString('\n')
		a, b = []byte(strings.TrimRight(l1, "\r\n")), []byte(strings.TrimRight(l2, "\r\n"))
	}
	if string(a) != string(b) {
		return nil, errors.New("the passwords don't match")
	}
	return a, nil
}

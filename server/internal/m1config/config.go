// Package m1config holds the owner-provisioned configuration for the M1
// movie proof: one non-admin viewer and one source file. It is not the G1
// settings store (OPS-2) or admin onboarding (ACC-1).
package m1config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
)

// Viewer is the single M1 account. Only AccountID, ProfileID and Username are
// public; PasswordHash never leaves the server.
type Viewer struct {
	AccountID    string `json:"accountId"`
	ProfileID    string `json:"profileId"`
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
}

// Config is the whole M1 configuration file. Only the fields returned by
// Movie become public.
type Config struct {
	ListenAddr     string `json:"listenAddr"`
	PublicOrigin   string `json:"publicOrigin"`
	MediaRoot      string `json:"mediaRoot"`
	MovieID        string `json:"movieId"`
	RelativePath   string `json:"relativePath"`
	Title          string `json:"title"`
	SourceRevision string `json:"sourceRevision"`
	VideoCodec     string `json:"videoCodec"`
	AudioCodec     string `json:"audioCodec"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	DurationMS     int64  `json:"durationMs"`
	Viewer         Viewer `json:"viewer"`
	// CSRFKey is 32 random bytes, base64 in JSON.
	CSRFKey             []byte `json:"csrfKey"`
	MaxPlaybackSessions int    `json:"maxPlaybackSessions"`
}

const maxFileBytes = 64 << 10

var (
	idPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	userPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	codecPattern = regexp.MustCompile(`^[a-z0-9.]{1,32}$`)
)

// Movie is the configured source's public metadata.
func (c Config) Movie() contracts.Movie {
	return contracts.Movie{
		ID: c.MovieID, SourceRevision: c.SourceRevision, Title: c.Title, DurationMs: c.DurationMS,
		Container: "mp4", VideoCodec: c.VideoCodec, Width: c.Width, Height: c.Height, AudioCodec: c.AudioCodec,
	}
}

// Load reads and validates the config at path. The file must be private (no
// group/other permissions), at most 64 KiB, one JSON object with no unknown
// fields, and must not live under the media root.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("m1config: open: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("m1config: stat: %w", err)
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 {
		return Config{}, errors.New("m1config: config file must be a regular file with mode 0600")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("m1config: read: %w", err)
	}
	if len(raw) > maxFileBytes {
		return Config{}, errors.New("m1config: config file is larger than 64 KiB")
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, errors.New("m1config: config is not valid JSON for this version (unknown or mistyped field)")
	}
	if dec.More() {
		return Config{}, errors.New("m1config: unexpected data after the config object")
	}
	// The movie may be gone since init (#99): playback reports it as not
	// ready at runtime, so it doesn't stop the server or other commands.
	if err := validate(c, path, false); err != nil {
		return Config{}, err
	}
	return c, nil
}

// SaveNew validates c and writes it to path with mode 0600. It never
// replaces an existing file: if path exists it returns an error wrapping
// fs.ErrExist and leaves the file unchanged.
func SaveNew(path string, c Config) error {
	if err := validate(c, path, true); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("m1config: encode: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".m1-*.tmp")
	if err != nil {
		return fmt.Errorf("m1config: create: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("m1config: chmod: %w", err)
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("m1config: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("m1config: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("m1config: close: %w", err)
	}
	// A hard link appears atomically and fails if path already exists.
	if err := os.Link(tmp.Name(), path); err != nil {
		return fmt.Errorf("m1config: save: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// validate checks every field. needMovie requires the movie file to exist,
// as init does; without it a missing movie is accepted, but one that exists
// must still be a regular file inside the media root.
func validate(c Config, cfgPath string, needMovie bool) error {
	bad := func(field, rule string) error { return fmt.Errorf("m1config: %s %s", field, rule) }
	if host, port, err := net.SplitHostPort(c.ListenAddr); err != nil || port == "" || (host != "" && net.ParseIP(host) == nil) {
		return bad("listenAddr", "must be host:port or :port")
	}
	if err := validOrigin(c.PublicOrigin); err != nil {
		return err
	}
	for field, v := range map[string]string{"viewer.accountId": c.Viewer.AccountID, "viewer.profileId": c.Viewer.ProfileID, "movieId": c.MovieID, "sourceRevision": c.SourceRevision} {
		if !idPattern.MatchString(v) {
			return bad(field, "must be 1-64 letters, digits, '-' or '_'")
		}
	}
	if !userPattern.MatchString(c.Viewer.Username) {
		return bad("viewer.username", "must be 1-64 letters, digits, '.', '-' or '_'")
	}
	if !strings.HasPrefix(c.Viewer.PasswordHash, "$argon2id$") || len(c.Viewer.PasswordHash) > 256 {
		return bad("viewer.passwordHash", "must be an Argon2id PHC string")
	}
	if t := strings.TrimSpace(c.Title); t == "" || len(c.Title) > 256 {
		return bad("title", "must be 1-256 bytes")
	}
	if c.VideoCodec != "h264" {
		return bad("videoCodec", "must be h264 (M1 direct play)")
	}
	if !codecPattern.MatchString(c.AudioCodec) {
		return bad("audioCodec", "must be a short lowercase codec name")
	}
	if c.Width < 1 || c.Height < 1 || c.Width > 1920 || c.Height > 1080 {
		return bad("width/height", "must be within 1920x1080")
	}
	if c.DurationMS < 1 {
		return bad("durationMs", "must be positive")
	}
	if len(c.CSRFKey) != 32 {
		return bad("csrfKey", "must be 32 bytes")
	}
	if c.MaxPlaybackSessions < 1 || c.MaxPlaybackSessions > 16 {
		return bad("maxPlaybackSessions", "must be 1-16")
	}
	return validSource(c, cfgPath, needMovie)
}

// validOrigin accepts https://host[:port], and http:// only for loopback,
// private or link-local addresses and local names. M1's plain HTTP mode is
// for a trusted private network (spec §6.6).
func validOrigin(s string) error {
	err := errors.New("m1config: publicOrigin must be https://host[:port], or http:// on a private-network address or local name, with no path")
	u, perr := url.Parse(s)
	if perr != nil || u.User != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return err
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
	default:
		return err
	}
	host := u.Hostname()
	if ip, perr := netip.ParseAddr(host); perr == nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return nil
		}
		return err
	}
	h := strings.ToLower(host)
	if h == "localhost" || !strings.Contains(h, ".") {
		return nil
	}
	for _, suffix := range []string{".local", ".lan", ".home.arpa", ".internal"} {
		if strings.HasSuffix(h, suffix) {
			return nil
		}
	}
	return err
}

// validSource checks the media root and the movie inside it. os.Root keeps
// the check inside the root, including through symlinks. Playback confines
// every open again; this is only a startup check. A missing movie is an
// error only when needMovie is set.
func validSource(c Config, cfgPath string, needMovie bool) error {
	if !filepath.IsAbs(c.MediaRoot) {
		return errors.New("m1config: mediaRoot must be an absolute path")
	}
	if !filepath.IsLocal(c.RelativePath) || !strings.EqualFold(filepath.Ext(c.RelativePath), ".mp4") {
		return errors.New("m1config: relativePath must be a path inside mediaRoot ending in .mp4")
	}
	root := filepath.Clean(c.MediaRoot)
	cfgAbs, err := filepath.Abs(cfgPath)
	if err != nil {
		return fmt.Errorf("m1config: config path: %w", err)
	}
	// Compare real locations, so a symlink on either side can't hide that
	// the config lives under the media root (#30).
	if rel, err := filepath.Rel(resolved(root), resolved(filepath.Dir(cfgAbs))); err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return errors.New("m1config: the config file must not be stored under mediaRoot")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return errors.New("m1config: mediaRoot is not an accessible directory")
	}
	defer r.Close()
	st, err := r.Stat(c.RelativePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if !needMovie {
				return nil
			}
			return errors.New("m1config: the movie file does not exist under mediaRoot")
		}
		return errors.New("m1config: the movie file is not accessible inside mediaRoot")
	}
	if !st.Mode().IsRegular() {
		return errors.New("m1config: the movie must be a regular file")
	}
	return nil
}

// resolved returns p with symlinks resolved, or p itself if it can't be
// resolved (for example a folder that doesn't exist yet).
func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

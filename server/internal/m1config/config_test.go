package m1config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A syntactically valid PHC string; auth.NewManager checks it fully.
const testHash = "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"

type fixture struct {
	cfgPath, mediaRoot string
	cfg                Config
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	base := t.TempDir()
	cfgDir, media := filepath.Join(base, "config"), filepath.Join(base, "media")
	for _, d := range []string{cfgDir, filepath.Join(media, "Movies")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(media, "Movies", "movie.mp4"), []byte("not really a movie"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture{
		cfgPath:   filepath.Join(cfgDir, "m1.json"),
		mediaRoot: media,
		cfg: Config{
			ListenAddr: ":8420", PublicOrigin: "http://192.168.1.20:8420", MediaRoot: media,
			MovieID: "mov-7Qx2", RelativePath: "Movies/movie.mp4", Title: "Tears of Steel",
			SourceRevision: strings.Repeat("0f", 32), VideoCodec: "h264", AudioCodec: "mp3", // revisions are 64 hex characters
			Width: 1920, Height: 800, DurationMS: 734167,
			Viewer:              Viewer{AccountID: "acct-9Kd1", ProfileID: "prof-3Hq8", Username: "viewer", PasswordHash: testHash},
			CSRFKey:             bytes.Repeat([]byte{7}, 32),
			MaxPlaybackSessions: 2,
		},
	}
}

func TestConfigRoundTripPreservesIdentity(t *testing.T) {
	f := newFixture(t)
	if err := SaveNew(f.cfgPath, f.cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Viewer != f.cfg.Viewer || got.MovieID != f.cfg.MovieID || !bytes.Equal(got.CSRFKey, f.cfg.CSRFKey) || got.DurationMS != 734167 {
		t.Fatalf("round trip changed identity: %+v", got)
	}
	m := got.Movie()
	if m.ID != "mov-7Qx2" || m.Container != "mp4" || m.VideoCodec != "h264" || m.Width != 1920 || m.DurationMs != 734167 {
		t.Fatalf("Movie() = %+v", m)
	}
	b, _ := json.Marshal(m)
	for _, secret := range []string{"Movies/movie.mp4", f.mediaRoot, "argon2id", "acct-9Kd1"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("public movie JSON leaks %q: %s", secret, b)
		}
	}
}

func TestSaveNewIsPrivateAndRefusesOverwrite(t *testing.T) {
	f := newFixture(t)
	if err := SaveNew(f.cfgPath, f.cfg); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", st.Mode().Perm())
	}
	before, _ := os.ReadFile(f.cfgPath)
	other := f.cfg
	other.Viewer.AccountID = "acct-other"
	if err := SaveNew(f.cfgPath, other); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second SaveNew = %v, want fs.ErrExist", err)
	}
	after, _ := os.ReadFile(f.cfgPath)
	if !bytes.Equal(before, after) {
		t.Fatal("refused overwrite still changed the file")
	}
	if entries, _ := os.ReadDir(filepath.Dir(f.cfgPath)); len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
	if err := os.Chmod(f.cfgPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(f.cfgPath); err == nil {
		t.Fatal("Load accepted a group/world-readable config")
	}
}

func TestConfigRejectsUnknownFieldsAndEscapingPath(t *testing.T) {
	f := newFixture(t)
	if err := SaveNew(f.cfgPath, f.cfg); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(f.cfgPath)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	write := func(t *testing.T, v any, tail string) string {
		p := filepath.Join(t.TempDir(), "m1.json")
		b, _ := json.Marshal(v)
		if err := os.WriteFile(p, append(b, tail...), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	with := func(k string, v any) map[string]any {
		c := map[string]any{}
		for kk, vv := range m {
			c[kk] = vv
		}
		c[k] = v
		return c
	}
	cases := map[string]string{
		"unknown field":   write(t, with("adminBypass", true), ""),
		"trailing data":   write(t, m, `{"x":1}`),
		"absolute source": write(t, with("relativePath", filepath.Join(f.mediaRoot, "Movies/movie.mp4")), ""),
		"dotdot source":   write(t, with("relativePath", "../media/Movies/movie.mp4"), ""),
		"not mp4":         write(t, with("relativePath", "Movies/movie.mkv"), ""),
		"missing source":  write(t, with("relativePath", "Movies/other.mp4"), ""),
		"relative root":   write(t, with("mediaRoot", "media"), ""),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(p); err == nil {
				t.Fatal("Load accepted it")
			}
		})
	}
	t.Run("symlink escaping the root", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "secret.mp4")
		if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(f.mediaRoot, "Movies", "link.mp4")); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(write(t, with("relativePath", "Movies/link.mp4"), "")); err == nil {
			t.Fatal("Load followed a symlink out of the media root")
		}
	})
	t.Run("oversized file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "m1.json")
		if err := os.WriteFile(p, bytes.Repeat([]byte(" "), 65*1024), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Fatal("Load accepted a file over 64 KiB")
		}
	})
	t.Run("config inside media", func(t *testing.T) {
		p := filepath.Join(f.mediaRoot, "m1.json")
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Fatal("Load accepted a config stored under the media root")
		}
	})
}

func TestConfigRejectsMissingIDsOrInvalidOrigin(t *testing.T) {
	f := newFixture(t)
	mutate := map[string]func(*Config){
		"empty account":         func(c *Config) { c.Viewer.AccountID = "" },
		"empty profile":         func(c *Config) { c.Viewer.ProfileID = "" },
		"bad movie id":          func(c *Config) { c.MovieID = "mov/../x" },
		"empty revision":        func(c *Config) { c.SourceRevision = "" },
		"empty username":        func(c *Config) { c.Viewer.Username = "" },
		"not argon2id":          func(c *Config) { c.Viewer.PasswordHash = "$2a$10$abcdefghijklmnopqrstuv" },
		"zero duration":         func(c *Config) { c.DurationMS = 0 },
		"over 1080p":            func(c *Config) { c.Width, c.Height = 3840, 2160 },
		"not h264":              func(c *Config) { c.VideoCodec = "hevc" },
		"revision too long":     func(c *Config) { c.SourceRevision = strings.Repeat("a", 65) },
		"short csrf key":        func(c *Config) { c.CSRFKey = c.CSRFKey[:16] },
		"no playback sessions":  func(c *Config) { c.MaxPlaybackSessions = 0 },
		"bad listen addr":       func(c *Config) { c.ListenAddr = "8420" },
		"origin with path":      func(c *Config) { c.PublicOrigin = "http://192.168.1.20:8420/app" },
		"origin with userinfo":  func(c *Config) { c.PublicOrigin = "http://u:p@192.168.1.20:8420" },
		"origin not http(s)":    func(c *Config) { c.PublicOrigin = "ftp://192.168.1.20" },
		"public http ip":        func(c *Config) { c.PublicOrigin = "http://8.8.8.8:8420" },
		"public http hostname":  func(c *Config) { c.PublicOrigin = "http://media.example.com" },
		"https downgrade input": func(c *Config) { c.PublicOrigin = "http://media.example.com:443" },
	}
	for name, fn := range mutate {
		t.Run(name, func(t *testing.T) {
			c := f.cfg
			c.Viewer = f.cfg.Viewer
			c.CSRFKey = append([]byte(nil), f.cfg.CSRFKey...)
			fn(&c)
			if err := SaveNew(filepath.Join(t.TempDir(), "m1.json"), c); err == nil {
				t.Fatal("SaveNew accepted it")
			}
		})
	}
	for _, ok := range []string{"http://localhost:8420", "http://127.0.0.1:8420", "http://[fd00::5]:8420", "http://media-server.lan:8420", "http://nas:8420", "https://media.example.com"} {
		c := f.cfg
		c.PublicOrigin = ok
		if err := SaveNew(filepath.Join(t.TempDir(), "m1.json"), c); err != nil {
			t.Errorf("SaveNew(%s) = %v, want ok", ok, err)
		}
	}
}

func TestErrorsDoNotEchoSecrets(t *testing.T) {
	f := newFixture(t)
	c := f.cfg
	c.Viewer.PasswordHash = "$argon2id$not-a-hash-SECRET"
	c.RelativePath = "../SECRETPATH.mp4"
	err := SaveNew(filepath.Join(t.TempDir(), "m1.json"), c)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}

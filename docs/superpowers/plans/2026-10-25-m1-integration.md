# M1: Integration and Original-Quality Movie Verification Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** Approved by @lisim on #19 (2026-10-26) with the changes in [Approved changes](#approved-changes-19), which are written into this plan. Claimed by @Lammy93. Not implemented: no image, playback run or platform qualification has occurred. Closes BASE-01, BASE-02 and BASE-04 together with MOVIE-01–04.
**Goal:** Assemble the accepted modules and obtain MOVIE-01–04 evidence for a complete original-quality movie on all six targets.
**Architecture:** One Linux amd64 container embeds the built web app and composes real configuration/auth, playback and source confinement. M1 uses an owner-provisioned non-admin viewer and one source; it is not the complete first-release onboarding/library product.
**Tech Stack:** Go 1.27.3, Node 24.22.1 build stage, original MP4 delivery, Docker/Compose.
**Spec:** [Design §§9.3, 11, 13](../specs/2026-10-24-lisimflix-design.md). Inputs: [CTR-1](2026-10-25-ctr-1-m1-contracts.md), [runtime](2026-10-25-ops-1-m1-runtime.md), [playback](2026-10-25-ply-2-m1-direct-play.md), [player](2026-10-25-ui-1-m1-player.md).

## Global constraints

- Server target: **Linux x86-64/amd64 containers**. ARM is optional later work.
- M1 source: complete 1080p SDR H.264 MP4 with unchanged compatible audio, tested in Chrome/Firefox × Linux/Windows/macOS.
- Local HTTP and username/password work without a domain/proxy/provider; trusted-LAN use has no transport encryption. No direct internet exposure.
- Media mounted read-only; only config/cache are writable. No media, passwords, sessions or grants enter Git or logs.
- Contributors self-claim this integration slice. Fixture/evidence preparation can start immediately; live composition needs the three module implementations, not all of G1.

## Review focus

1. Real authentication protects real media; no test adapters or permissive fallbacks enter the binary.
2. A movie-length response is not killed by an ordinary short HTTP write timeout.
3. Container UID/GID/mounts and actual LAN IP work without modifying source ownership.
4. Static/API routing, cookie/CSRF rotation and logout revocation work together.
5. Six complete runs and original-byte/source-integrity evidence replace assumptions from short smoke tests.

## Ownership and interfaces

Own `server/cmd/lisimflix/**`, `server/internal/webassets/**`, `Dockerfile`, `deploy/compose.m1.yml`, `.dockerignore`, `testdata/media/README.md`, `docs/testing/m1-evidence-template.md`, and the `server/go.mod`/`go.sum` edit that adds `golang.org/x/term v0.47.0` (moved here from #18); coordinate updates to root `Makefile` and `.gitignore`. Do not rewrite module implementations/schema as a hidden integration fix: send a bounded change back to its feature claimant, or explicitly claim the agreed fix.

Use the module signatures verbatim, as approved on #18 and #20:

```go
// m1config
type Config struct { …; VideoCodec, AudioCodec string; Width, Height int; DurationMS int64; … }
func (c Config) Movie() contracts.Movie // Container fixed "mp4"
// auth
func NewManager(viewer m1config.Viewer, secure bool, onRevoke func(string)) (*Manager, error)
func (m *Manager) Secure() bool
func CSRF(key []byte, secure bool) func(http.Handler) http.Handler
// app
func New(cfg m1config.Config, am *auth.Manager, playback http.Handler, web fs.FS, ready func(context.Context) error) (http.Handler, error)
// playback
func (s *Server) Check() error // source present and unchanged
func (s *Server) Close() error // ends sessions and deliveries, stops the sweeper
```

Composition is: `m1config.Load` → `os.OpenRoot(cfg.MediaRoot)` → `playback.New` with `Source{cfg.Movie(), cfg.RelativePath}`, a fresh process-local 32-byte grant key, `MaxSessions: cfg.MaxPlaybackSessions` and an `Authorize` that checks the exact configured account, profile and movie → `auth.NewManager(cfg.Viewer, origin is https, pb.RevokeAuthSession)` → storage (below) → `app.New(cfg, am, pb.Handler(), web, ready)` with real embedded assets → HTTP server.

**Storage (OPS-2 handoff, #17):** both paths are built from the `--config` folder, so tests and other layouts need no extra settings: `dir := filepath.Dir(cfgPath)`, `storage.Open(ctx, filepath.Join(dir, "lisimflix.db"))`, then `MigrateWithBackup(ctx, []storage.Source{storage.Core()}, storage.SnapshotOptions{Dir: filepath.Join(dir, "backups"), AppVersion: version, ExtraFiles: []string{cfgPath}}, time.Now)`, where `cfgPath` is the actual `--config` value (in the container, `/config/m1.json` gives `/config/lisimflix.db` and `/config/backups`). `--db` and `--backups` may override them. Modules add their `Source`s as they land. On error `serve` keeps running and `ready` reports not-ready; for `ErrSchemaTooNew` the log names the error and the recovery (restore a compatible snapshot with a compatible image). `ready` returns the storage error, else `pb.Check()`. Start `go am.Run(ctx, time.Minute)` on the same shutdown context: the auth manager's sweep ends expired sessions and calls `onRevoke`, so an expiring sign-in cuts its streams without waiting for its next request (#30). Start the nightly job with a pointer, since `Run` has a pointer receiver: `go (&storage.Nightly{DB: db, Opts: snap, Log: log}).Run(ctx)`, stopped by the shutdown context. `Hour` is left zero on purpose: the default, 03:00 in the container's `TZ` (UTC if unset). The deployment docs state the `TZ` used. Generate a fresh process-local grant key; keep durable account/profile/movie IDs and CSRF key in the private config. Set the explicit session limit from config. M1's source resolver is the configured movie, not a fake production catalogue.

`lisimflix init --config PATH --origin URL --media-root PATH --movie RELATIVE_PATH --probe-json PATH --username NAME` is owner-only provisioning for this proof. With a terminal it prompts twice for a password using `x/term`; without one (scripted setup) it reads two lines from stdin. It never accepts or logs a plaintext password argument. Docs show stdin from a private file or redirect, removed afterwards, for example `lisimflix init … < pwfile && rm pwfile`, never `echo pw |`, so the password stays out of shell history. It generates stable IDs/CSRF key, validates the source and probe record and refuses config overwrite. It fills `VideoCodec`, `AudioCodec`, `Width`, `Height` and `DurationMS` (rounded to milliseconds) from the probe, and `SourceRevision` with `playback.Revision(movieID, info)`, the stat taken through the root. `serve --config PATH` runs the app. `healthcheck` checks `GET http://127.0.0.1:8420/health/ready` (address overridable with `--addr`) with a 3 s deadline and exits nonzero unless it gets 200. Full browser admin setup/user management remains separate G1 work, so this CLI proof does not claim the finished out-of-box setup UX.

The probe record is owner-generated ffprobe JSON, outside Git, with the source mounted read-only. Before writing config, qualify the source: the container is MP4 (`format_name` includes `mp4`); **exactly one** video stream, H.264, `yuv420p`/`yuvj420p` (8-bit 4:2:0), within 1920×1080 (record the actual dimensions); **exactly one** audio stream, **AAC or MP3**, the audio Chrome and Firefox both decode from MP4; positive, finite duration. **SDR rule:** an unspecified `color_transfer` counts as SDR; only `smpte2084` (PQ) and `arib-std-b67` (HLG) are rejected as HDR. Anything else is reported as unqualified rather than labelled qualified, and never converted. Use `playback.Revision(movieID, fileInfo)` for the configured revision. Record complete-file SHA-256 separately for acceptance; do not load an entire movie into memory.

## Task 1: Compose the executable and non-root container

**Consumes:** Accepted runtime/playback/player interfaces and built frontend. **Produces:** executable `init`, `serve`, `healthcheck`; actual container/Compose deployment.

- [ ] Write `server/cmd/lisimflix/main_test.go` with `TestInitRefusesOverwriteAndPlaintextOutput`, `TestCompositionRejectsAnonymousContent`, `TestLoginToContentAndLogoutRevocation`, `TestAPINotFoundIsNotSPA` and `TestShutdownClosesPlaybackAndRoot`. Use temporary generated configuration/files and real modules; no production auth stub.
- [ ] Run `go -C server test ./cmd/lisimflix`; expect missing composition/failing assertions.
- [ ] Build the real wiring and `//go:embed dist` loader in `server/internal/webassets/assets.go`. Root build copies `web/dist` into that package before building the command; pure module tests remain independently runnable without embedded assets. Start the server with `ReadHeaderTimeout` 10 s, `MaxHeaderBytes` 16 KiB, `IdleTimeout` 2 min and **no `WriteTimeout`** (playback sets per-write deadlines). On SIGTERM, in this order: `pb.Close()` **before** `http.Server.Shutdown` (Shutdown waits for active connections, and an open stream would otherwise hold it for the whole grace period); `root.Close()` after `pb.Close()`, once no delivery can open a file; wait for the nightly job to return (the shutdown context stops it; a snapshot already running finishes, bounded by the grace period); then `db.Close()`. Exit within the documented 15-second container grace period.
- [ ] Add reproducible amd64 build stages using the pinned Go/Node versions and a minimal non-root runtime image. M1 needs no encoder; do not claim full ffmpeg/transcode support from this image. Runtime base `gcr.io/distroless/static-debian12:nonroot`, both bases pinned by digest; no shell or encoder. Compose maps port 8420 and config/cache/media mounts, sets an explicit non-root `user: ${PUID}:${PGID}` from documented defaults, mounts `/media:ro`, sets `read_only: true`, `cap_drop: [ALL]`, `no-new-privileges`, `stop_grace_period: 15s`, a `tmpfs` for `/tmp` (SQLite spills large sorts and index builds there; the root is read-only) and `TZ`, and never uses privileged mode or recursively chowns media. Docker build context excludes private media/config/evidence and dependencies. Document host directory permissions rather than silently changing them.
- [ ] Run the real `make check` and `make image` targets. The former still performs actual Go lint/tests and frontend generation/lint/typecheck/build; its asset build precedes command-package tests. Run local container smoke checks with the real image settings (user, read-only root, dropped capabilities, tmpfs): readiness, login, range bytes, source-read-only behaviour, first start migrates, a pre-upgrade snapshot is taken by an image with a newer migration, `Nightly.RunOnce` succeeds, and `/config` files are 0600 (directories 0700) owned by `PUID:PGID`. Commit as `feat(platform): assemble M1 direct-play container`.

## Task 2: Reproduce a real authenticated short smoke run

**Files:** Add `server/cmd/lisimflix/integration_test.go`, tagged `integration`; document commands in `testdata/media/README.md`.

- [ ] Add `TestM1RealHTTPRangeAndRevocation` using the actual binary/modules and a locally owned short MP4. Gate it with `LISIMFLIX_M1_MOVIE` and `LISIMFLIX_M1_PROBE` file paths, never a password/token environment dump. Missing fixtures fail this explicitly requested test; normal unit checks do not pretend to run it.
- [ ] Run `go -C server test -tags=integration ./cmd/lisimflix -run TestM1RealHTTPRangeAndRevocation` before wiring is correct; capture its specific failure rather than claiming missing prerequisites are proof.
- [ ] Complete only integration wiring needed for real login → CSRF/session start → original byte range → heartbeat → logout → denied/revoked content. Add source-before/after checksum and process/memory observations. Keep source URLs/grants redacted from artifacts.
- [ ] Rerun this test and the UI Chrome smoke against the real application, with API interception disabled. Verify activity advances and CSRF/login failures remain handled. No full M1 claim yet.
- [ ] Commit as `test(playback): verify real M1 HTTP integration`.

## Task 3: Collect full-duration M1 acceptance evidence

**Files:** Create `docs/testing/m1-evidence-template.md` and `.gitignore` entry for private `.m1-evidence/`; no real media or private evidence committed automatically.

- [ ] Create a six-row results template: Chrome/Linux, Firefox/Linux, Chrome/Windows, Firefox/Windows, Chrome/macOS, Firefox/macOS. Fields: image commit/digest, host/OS/browser versions, anonymised source ID, duration, codecs/profile/resolution, source bitrate, original checksum, timestamps, start result, interruptions, audio sync, actual end reached, range-byte check and final source checksum. Initial status for every row is **not run**.
- [ ] Qualify the same complete authorised movie's audio/video on the real browsers. UI-1 doesn't check codecs (it plays through `<video>`), so this step is where the codec check lives: each run records `canPlayType` for a string **built from the probe**, not a fixed one. Video is `avc1.PPCCLL` from the H.264 profile and level (Main L4.0 is `avc1.4D0028`; High is `64`, Constrained Baseline `42`). Audio is `mp4a.40.2` for AAC-LC (`mp4a.40.5` for HE-AAC) and `mp3` for MP3, because Firefox returns `""` for `mp4a.6B`. Tears of Steel gives `video/mp4; codecs="avc1.4D0028, mp3"`. A short generated clip can qualify the harness but cannot replace a complete movie run. An incompatible fixture is a recorded failure/selection issue, not permission to transcode.
- [ ] Run the complete movie from the beginning to actual end on each combination without seeking to manufacture completion. Confirm no encoder jobs or lower-quality outputs, no crashes/unhandled errors/post-start buffering under the recorded suitable connection, and unchanged source checksums. A failure remains open; repeat the affected row after a correction.
- [ ] Publish only a reviewed redacted result summary, with local evidence retained by its tester. Confirm MOVIE-01–04 for every row. Performance benchmarks, wider formats, persistent resume and the large-library milestone are separate work; do not claim them here.
- [ ] Commit the completed redacted report only after the real runs, using `docs(testing): record M1 playback qualification`. Do not pre-fill or pre-commit a successful result while implementing this plan.

## Handoff

M1 completes only when real integration and all six full runs pass. Module merge/build/smoke is progress, not completion. Later library/home/native-progress features remain independently claimable; nobody is assigned this plan or a browser platform by this document.

## Approved changes (#19)

Items 1–7 were proposed on #19 and approved by @lisim with fixes F1–F3. All are written into the sections above; this list is the record.

| # | Change |
|---|---|
| 1 | Module signatures restated from #18/#20 (`Config` probe fields and `Movie()`, `NewManager` error, `Secure`, `CSRF`, `app.New` `ready`, `playback.Check`/`Close`), the composition order, and what `init` fills |
| 2 | Probe qualification and the SDR rule (unspecified transfer counts as SDR; only PQ/HLG rejected), replacing "reject absent/ambiguous streams" |
| 3, F1 | Storage startup wiring from the OPS-2 handoff; `Nightly` started through a pointer; default 03:00 in `TZ` intended |
| 4 | `server/go.mod`/`go.sum` (`x/term`) in this plan's ownership |
| 5 | Test media attribution: "(CC) Blender Foundation \| mango.blender.org", CC BY 3.0, complete film with credits; container-only remux; CC BY-ND applies only to the separately released soundtrack files |
| 6 | From the prototype run: shutdown order, server timeouts, AAC/MP3 allowlist, `healthcheck` details, `init` stdin for scripted setup, distroless image, compose hardening; UI-1 codec checks use `avc1.4D4028, mp3`; Firefox audio is checked by ear on the real runs |
| 7 | BASE-01/02/04 close here with MOVIE-01–04 |
| F2 | SQLite under a read-only root: `tmpfs` for `/tmp`, and the storage smoke checks under the real image settings |
| F3 | Docs feed the `init` password from a private file or redirect, never `echo` |
| N1 | Approval note: the codec check lives in the Task 3 qualification step, with the string built from the probe |
| N2 | Approval note: database and backup paths come from the `--config` folder |
| N3 | Approval note: shutdown also closes the root after `pb.Close()` and the database after the nightly job stops |
| N4 | #30, point 2 (b): a once-a-minute `auth.Manager` sweep (`Sweep`, `Run`) revokes expired sessions without a request. Test `TestSweepCutsAnExpiredSessionsStream` expires a session mid-stream and the delivery is cut with no further request |

Prototype evidence (local, not module acceptance): with uid 1000, a read-only root, all capabilities dropped and `/media:ro`, the image passed a 31-check smoke run of login, ranges, a byte-identical whole-file GET, revocation, SIGTERM and source integrity. A storage run then covered `init` from `< pwfile`, the first-start migration, `backup-now` (`Nightly.RunOnce`) while serving, an upgrade image taking a pre-upgrade snapshot at the old schema, and every `/config` file 0600 and directory 0700 owned by uid 1000. It passed without a tmpfs on today's tiny database; the tmpfs covers future large migrations.

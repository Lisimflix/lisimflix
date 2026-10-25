# M1: Integration and Original-Quality Movie Verification Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** Draft; unassigned. No image, playback run or platform qualification has occurred.
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

Own `server/cmd/lisimflix/**`, `server/internal/webassets/**`, `Dockerfile`, `deploy/compose.m1.yml`, `.dockerignore`, `testdata/media/README.md`, `docs/testing/m1-evidence-template.md`; coordinate updates to root `Makefile` and `.gitignore`. Do not rewrite module implementations/schema as a hidden integration fix: send a bounded change back to its feature claimant, or explicitly claim the agreed fix.

Use the module signatures verbatim. Composition is: load config → open `os.Root` → construct playback with a real exact account/profile/movie policy → construct auth manager with `playback.RevokeAuthSession` as its hook → `app.New` with real embedded assets → HTTP server. Generate a fresh process-local grant key; keep durable account/profile/movie IDs and CSRF key in the private config. Set the explicit session limit from config. M1's source resolver is the configured movie, not a fake production catalogue.

`lisimflix init --config PATH --origin URL --media-root PATH --movie RELATIVE_PATH --probe-json PATH --username NAME` is owner-only provisioning for this proof. It prompts twice for a password using `x/term`, never accepts/logs a plaintext password argument, generates stable IDs/CSRF key, validates the source and probe record and refuses config overwrite. `serve --config PATH` runs the app. `healthcheck` checks loopback readiness with a bounded HTTP deadline and exits nonzero on failure. Full browser admin setup/user management remains separate G1 work, so this CLI proof does not claim the finished out-of-box setup UX.

The probe record is owner-generated ffprobe JSON, outside Git, with the source mounted read-only. Validate MP4/H.264/1920×1080/SDR, compatible audio and positive duration before writing config; reject absent/ambiguous streams or invalid data rather than labelling it qualified. Use `playback.Revision(movieID, fileInfo)` for the configured revision. Record complete-file SHA-256 separately for acceptance; do not load an entire movie into memory.

## Task 1: Compose the executable and non-root container

**Consumes:** Accepted runtime/playback/player interfaces and built frontend. **Produces:** executable `init`, `serve`, `healthcheck`; actual container/Compose deployment.

- [ ] Write `server/cmd/lisimflix/main_test.go` with `TestInitRefusesOverwriteAndPlaintextOutput`, `TestCompositionRejectsAnonymousContent`, `TestLoginToContentAndLogoutRevocation`, `TestAPINotFoundIsNotSPA` and `TestShutdownClosesPlaybackAndRoot`. Use temporary generated configuration/files and real modules; no production auth stub.
- [ ] Run `go -C server test ./cmd/lisimflix`; expect missing composition/failing assertions.
- [ ] Build the real wiring and `//go:embed dist` loader in `server/internal/webassets/assets.go`. Root build copies `web/dist` into that package before building the command; pure module tests remain independently runnable without embedded assets. Start the server with a short header-read timeout and no short global streaming write timeout, bounded headers and graceful shutdown. SIGTERM stops admission, revokes/closes playback and exits within the documented 15-second container grace period.
- [ ] Add reproducible amd64 build stages using the pinned Go/Node versions and a minimal non-root runtime image. M1 needs no encoder; do not claim full ffmpeg/transcode support from this image. Compose maps port 8420 and config/cache/media mounts, sets an explicit non-root `user` from documented PUID/PGID defaults, mounts `/media:ro`, and never uses privileged mode or recursively chowns media. Docker build context excludes private media/config/evidence and dependencies. Document host directory permissions rather than silently changing them.
- [ ] Run the real `make check` and `make image` targets. The former still performs actual Go lint/tests and frontend generation/lint/typecheck/build; its asset build precedes command-package tests. Run local container smoke checks for readiness, login, range bytes and source-read-only behaviour. Commit as `feat(platform): assemble M1 direct-play container`.

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
- [ ] Qualify the same complete authorised movie's audio/video on the real browsers. A short generated clip can qualify the harness but cannot replace a complete movie run. An incompatible fixture is a recorded failure/selection issue, not permission to transcode.
- [ ] Run the complete movie from the beginning to actual end on each combination without seeking to manufacture completion. Confirm no encoder jobs or lower-quality outputs, no crashes/unhandled errors/post-start buffering under the recorded suitable connection, and unchanged source checksums. A failure remains open; repeat the affected row after a correction.
- [ ] Publish only a reviewed redacted result summary, with local evidence retained by its tester. Confirm MOVIE-01–04 for every row. Performance benchmarks, wider formats, persistent resume and the large-library milestone are separate work; do not claim them here.
- [ ] Commit the completed redacted report only after the real runs, using `docs(testing): record M1 playback qualification`. Do not pre-fill or pre-commit a successful result while implementing this plan.

## Handoff

M1 completes only when real integration and all six full runs pass. Module merge/build/smoke is progress, not completion. Later library/home/native-progress features remain independently claimable; nobody is assigned this plan or a browser platform by this document.

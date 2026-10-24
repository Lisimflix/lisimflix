# Lisimflix: Design Spec

- **Date:** 2026-10-24
- **Status:** Approved in brainstorming, pending written-spec review
- **Repo:** `Lisimflix/lisimflix` (private, GitHub org `Lisimflix`)
- **Image:** `ghcr.io/lisimflix/lisimflix`

## 1. Purpose

Lisimflix is a self-hosted media server and player that covers what Plex and
Jellyfin do as its own independent product, with no dependence on any outside
company's accounts or services. It runs next to the existing Plex server on
zraid until the household is happy to switch.

### Goals

- Every core Plex/Jellyfin function: libraries, metadata (including anime), multiple
  accounts and profiles, personal home screens, watch progress, remote access.
- Smooth streaming regardless of codec, resolution or network speed. This is the main
  reason the earlier Jellyfin attempt was abandoned (lag, unplayable formats).
- One Docker container. Simple to deploy, back up and replicate on any machine.
- Built by an organisation team in parallel through PRs and CI.

### Non-goals

- Compatibility with Plex's official client apps or the plex.tv account system.
- Reverse engineering Plex or any other proprietary software. Everything is built
  from open standards, public APIs and observed product behaviour.
- Watch Together, Live TV/IPTV/DVR, and books/comics/audiobooks (Komga and
  Audiobookshelf already cover these). They may be revisited later and are not in this spec.
- Native PlayStation, Hisense VIDAA, Apple TV, Chromecast or iOS apps. These devices use
  the web app (TV mode in their built-in browsers on PlayStation/Hisense).
- Streaming-service content, ads or "Discover" style promotion. These are never added.

### Known context (zraid, Oct 2026)

- Media: `/mnt/user/dataz/media` (Sonarr/Radarr-managed, anime separated via tags).
- GPU: NVIDIA GTX 1070 (Pascal), driver 580, `nvidia` Docker runtime already in use by Plex.
  It decodes H.264/HEVC (incl. 10-bit)/VP9 and encodes H.264/HEVC. It **cannot decode AV1**.
- CPU: 12 threads, 27 GB RAM.
- Tautulli 90-day usage: 7 users, peak 3 concurrent streams / 2 transcodes, 283 plays
  (218 direct play, 62 transcode). Platforms: Android 199, Windows 20, Opera 11,
  iOS 11, PlayStation 8, VIDAA 7. VIDAA always transcoded and PlayStation half the time.

## 2. Architecture

### 2.1 Single container

```
ghcr.io/lisimflix/lisimflix
├── /app/lisimflix   Go binary: API + embedded web app + streaming + scanner + scheduler
├── /usr/lib/jellyfin-ffmpeg/{ffmpeg,ffprobe}   GPU-enabled ffmpeg build
└── volumes
    /config   SQLite DB, artwork/thumbnail cache, settings, backups, TLS certs
    /media    library root, mounted READ-ONLY
    /cache    transcode segments and temp files (fast SSD)
```

- **Language: Go.** Single static binary. The web app's built assets are embedded via
  `embed`. Good concurrency for many streams and ffmpeg process supervision.
- **Database: SQLite** (WAL mode) behind a repository interface so Postgres can be added
  later without touching handlers. Migrations are versioned and run on startup.
- **ffmpeg: jellyfin-ffmpeg**, a separate open-source GPU-tuned ffmpeg build. It is used
  only as an external process and Jellyfin server code is not used.
- **Web app: React + TypeScript (Vite)**, built to static files and embedded in the binary.
- **Runs as non-root** (PUID/PGID env). `/media` is always read-only.
- **No GPU** works too: it falls back to CPU (libx264) encoding automatically.
- Deploy: one `docker-compose.yml` and one Unraid template. 3 volumes, 1 port (8420
  default, HTTP; optional 443 for built-in TLS), optional `--runtime=nvidia`.
- First run opens a setup wizard: admin account, server name, libraries, metadata keys.
- **Backup/restore:** copy `/config`. Nightly automatic DB snapshot (keep 7) with restore from admin UI.

### 2.2 Internal modules (Go packages)

Each has one job and a documented interface, so team members can own one at a time.

| Package | Responsibility |
|---|---|
| `api` | HTTP routing, auth middleware, JSON API (versioned `/api/v1`), OpenAPI spec |
| `auth` | Accounts, profiles, sessions/tokens, Argon2id, TOTP, OIDC, invites, rate limiting |
| `library` | Library definitions, file scanning, filesystem watching, ffprobe analysis |
| `metadata` | Provider interface and providers (TMDB, TVDB, AniList, AniDB, Jikan/MAL, Fanart.tv, MusicBrainz, NFO), ID mapping, artwork cache |
| `media` | Items domain model: movies, shows/seasons/episodes, versions, extras, collections |
| `userdata` | Progress, watched state, watchlist, ratings, history, per-show track prefs |
| `playback` | Device capability model, playback decision engine, sessions |
| `transcode` | ffmpeg job supervisor, HLS packaging, ABR ladder, HW accel probing, tone-mapping |
| `subtitles` | Extraction, conversion (SRT/VTT), ASS/PGS delivery, burn-in fallback |
| `trickplay` | Seek-bar thumbnails, intro/credits detection |
| `importer` | One-time Plex watch-history/progress import (via the user's own Plex server API/DB) |
| `admin` | Activity monitor, stream kill/message, tasks, logs, GPU self-test, backups |
| `tls` | Optional built-in Let's Encrypt (autocert) |
| `web` | Embedded SPA serving |

API clients (web now, Android later) use only the public `/api/v1`. The OpenAPI spec is
the contract and the TypeScript client is generated from it.

## 3. Libraries, metadata and accounts

### 3.1 Libraries

- Types: **Movies, TV Shows, Anime (TV/Movies), Music, Photos, Home Videos**. One or more folders each.
- Scanner: inotify watch plus a periodic full scan (default 6h) as a safety net. Each file
  gets an ffprobe pass storing container, streams, codecs, profiles/levels, bit depth,
  HDR type (HDR10/HDR10+/HLG/DV profile), audio channels/format, subtitle tracks
  (text/image/ASS), chapters and duration.
- Understands Sonarr/Radarr naming, `{tmdb-123}` / `{tvdb-123}` / `[anidb-123]` ID hints, and `.nfo` files.
- Multiple versions/cuts of a movie are grouped under one item (e.g. Theatrical, Director's
  Cut, 4K vs 1080p), with a "Play version" picker. Extras/special features come from Plex-style
  folders (`Featurettes/`, `Behind The Scenes/`, `Trailers/`, ...).
- Broken/unreadable files are logged and listed in admin. They never abort a scan.
- Missing seasons/episodes can optionally be shown (greyed out) based on metadata.

### 3.2 Metadata

- Provider interface: `Search`, `Lookup(ids)`, `Images`, `Episodes`. Results are merged
  by field priority per library type. Artwork is downloaded and cached locally (resized variants).
- **General:** TMDB (primary, free key), TVDB (optional), IMDb/Rotten Tomatoes ratings
  where available, Fanart.tv (logos, clearart, backdrops), MusicBrainz (music), local NFO.
- **Anime:**
  - AniList (primary: descriptions, art, genres, studios, relations, airing status)
  - AniDB (episode lists, specials/OVAs/episode types; strict rate limiting plus aggressive caching, client registration required)
  - Jikan/MyAnimeList (scores, fallback)
  - TMDB/TVDB (fallback artwork)
  - ID mapping via the community anime-lists datasets (AniDB ↔ TVDB ↔ TMDB ↔ AniList ↔ MAL), refreshed weekly
  - Absolute and seasonal numbering both supported and cross-mapped. Specials/OVAs are placed correctly.
  - Alternate orders: TVDB orders and TMDB episode groups selectable per show.
- Title display language per profile: English / Romaji / Native. Search matches all alternate titles.
- **Fix match** and per-item/bulk metadata editing. Manual edits and locked fields survive rescans.
- "Keep original title" option per library.

### 3.3 Accounts and profiles

- **Admin:** full server control.
- **Users:** own login. Created by admin or via invite link / sign-up page (admin-approved).
- **Home profiles** under an account ("Who's watching?") with optional 4-digit PIN.
- Per user/profile policy:
  - visible libraries
  - max content rating (e.g. kids: G/PG only)
  - max stream quality/bitrate (e.g. remote friends ≤ 1080p 10 Mbps)
  - max simultaneous streams
  - download permission (offline phase)
- Per-profile preferences:
  - layout (A/B/C)
  - colour theme
  - title language
  - default audio and subtitle languages (global and per-library, e.g. anime = JP audio + EN subs)
  - subtitle appearance
  - skip lengths
  - auto-skip intro/credits
  - AniList/MAL account link
- AniList/MAL sync: marking an episode watched updates the linked list.

### 3.4 Per-profile user data

Progress, watched/unwatched, watch history, watchlist, personal ratings, per-show
audio/subtitle choice, "remove from Continue Watching".

### 3.5 Plex import

One-time admin tool. Connects to the user's own Plex server with that server's admin token,
reads per-user watch state and progress, matches items by GUID/TMDB/TVDB IDs and file path,
and writes into Lisimflix profiles. Dry-run report first.

## 4. Streaming engine

### 4.1 Decision ladder (per play request)

Inputs: file analysis (3.1), client capability profile, user/profile limits, measured or estimated bandwidth.

1. **Direct play:** the file is served as-is via HTTP range requests.
2. **Remux:** compatible streams, incompatible container. Repackaged to fMP4/HLS with `-c copy`.
3. **Audio transcode only:** video copied, audio converted (TrueHD/DTS/FLAC → EAC3/AAC as client supports).
4. **Full transcode:** GPU decode (NVDEC/CUVID) → scale/tone-map (CUDA) → NVENC
   H.264 or HEVC, packaged as HLS fMP4. CPU fallback where the GPU can't decode (AV1 on Pascal) or fails.

Every decision is recorded with **reasons** (e.g. "audio TrueHD unsupported by client") and
shown in admin activity and the player's stats overlay.

### 4.2 Client capabilities

- Clients report capabilities on session start:
  - Web: runtime probing via `MediaSource.isTypeSupported`, `MediaCapabilities.decodingInfo`,
    `canPlayType`, HDR media queries (`(dynamic-range: high)`), screen size.
  - Android (later): `MediaCodecList`/Media3.
- Server-side **quirks table** keyed by user-agent/platform (PlayStation browser, Hisense
  VIDAA browser, Safari, etc.) that overrides known false positives.
- If a direct play/remux fails to start or stalls repeatedly, the player reports it, the server
  moves one rung down the ladder and remembers it for that device + codec.

### 4.3 Adaptive streaming

- HLS with fMP4 segments (4s), multi-variant ladder generated **on demand** (only the
  variants actually requested are encoded):
  source/4K, 1080p 20, 1080p 8, 720p 4, 480p 1.5, 360p 0.7 Mbps, each capped by user policy.
- Player: hls.js (web) with ABR, starting from a bandwidth estimate taken at session start.
- Seeking: segments are generated just-in-time. A seek outside the generated window restarts
  ffmpeg at that position (keyframe-aligned) and reuses finished segments.
- Next-episode preloading: the first segments of the next episode are prepared near the end.
- Server bandwidth governance: per-user caps, a global upload cap, LAN vs WAN detection
  (LAN never throttled by WAN rules).
- Transcode cache in `/cache`, LRU-evicted, size cap configurable.

### 4.4 Subtitles

- Text (SRT, VTT, embedded mov_text): extracted and served as WebVTT sidecars. They never force a transcode.
- ASS/SSA: served raw and rendered client-side with **JASSUB** (libass/WASM) so styling
  and typesetting are preserved during direct play. Fonts are extracted from MKV attachments.
- PGS/VobSub: client-side rendering (PGS renderer in web) where possible, burn-in during transcode as a last resort.
- Subtitle offset adjustment, appearance settings, "signs & songs" track detection.

### 4.5 HDR

- HDR10/HLG/DV (profiles with an HDR10 base layer) are direct played on HDR-capable clients.
- On SDR clients: GPU tone-mapping (`tonemap_cuda` / OpenCL in jellyfin-ffmpeg) to BT.709 SDR.
- DV profile 5 (no fallback layer) is tone-mapped with correct handling, never shown in wrong colours.

### 4.6 GPU management

- Startup self-test encodes/decodes short samples for each codec/profile and records
  what the hardware actually supports. Shown in admin ("HEVC 10-bit decode ✓, AV1 decode ✗").
- Concurrency limit for NVENC sessions (default 8, configurable). Overflow falls back to CPU.
- GPU failure at runtime → CPU fallback, with an admin warning.

### 4.7 Trickplay, intros and credits

- Seek thumbnails generated in the background (low priority, GPU-assisted) as tiled sprite sheets.
- Intro detection by audio fingerprint comparison across episodes of a season. Credits
  detection by fingerprint plus black-frame analysis. Chapter markers are used when present.
  Skip buttons, optional auto-skip.

### 4.8 Remote delivery

- Built-in HTTPS via Let's Encrypt (autocert) on the owner's domain and one forwarded port,
  **or** Tailscale. Cloudflare may front the web app/API, but video bypasses it.
- Stream URLs are signed, user-bound and short-lived.

## 5. Web app (UI/UX)

Mockups are in `.superpowers/brainstorm/` (layout options; detail page and player).

### 5.1 Layouts and themes

- Three layouts sharing one component library. Only the shell/navigation differs.
  - **C, Apple TV style (default):** minimal header, filter chips, personal "up next" card, rows.
  - **A, streaming-service style:** top tabs, large featured banner, rows.
  - **B, sidebar library style:** persistent library sidebar, no banner, denser rows.
- Layout is chosen per profile. Colour themes are separate: dark (default), OLED black, light, and accent colours.
- Build order: C fully polished first, then A and B as shells.

### 5.2 Pages

- **Who's watching?:** profile picker with PIN.
- **Home** (per profile, rows can be reordered or hidden):
  - Continue Watching, Next Up
  - Recently Added per library
  - Watchlist, collections
  - admin-defined shared rows
- **Library:** virtualised infinite grid (no pagination); filters (genre, year, rating,
  audio language, resolution, HDR, watched, tags, studio); sorting; A–Z jump; optional folder view.
- **Detail page:**
  - backdrop, title (+ alternate title), ratings (TMDB/IMDb/RT/AniList/MAL)
  - quality chips
  - Play/Resume, Watchlist, Mark watched, version picker
  - seasons incl. Specials, episodes with progress
  - extras, cast, related
  - per-show audio/subtitle choice
- **Player:**
  - play/pause, adjustable skip back/forward, seek with thumbnails
  - Skip Intro/Credits, Next Episode with countdown
  - speed, quality (Auto/fixed)
  - audio & subtitle menu with offset
  - fullscreen and picture-in-picture
  - stats-for-nerds overlay
- **Search:** instant results across titles, alternate/anime titles, people, studios, genres/tags.
- **Person/studio pages, collections (nested), smart playlists, watch history.**
- **Profile settings** (see 3.3).

### 5.3 Admin dashboard

- **Activity:** live sessions with decision + reasons, GPU/CPU/bandwidth, kill stream, message user.
- **Users & invites:** policies (3.3), sessions/devices, 2FA status.
- **Libraries:** scan status, unmatched items, fix-match, bulk edit.
- **System:** GPU self-test, logs, scheduled tasks, backups/restore, metadata provider keys, TLS.
- **Plex import tool.**

### 5.4 TV mode

- Auto-enabled on large screens or on detecting arrow-key/gamepad input. Can be toggled manually.
- Full spatial (D-pad) navigation with visible focus rings, larger targets, no hover-only controls.
- QR-code sign-in: the TV shows a code, the phone approves it.
- Targets: PlayStation browser, Hisense VIDAA browser, Android TV browser (until the native app ships).

### 5.5 Phones

PWA (installable), bottom tab bar, touch gestures in the player (double-tap seek).

### 5.6 Accessibility

Keyboard-complete, ARIA labels, focus management, readable subtitle defaults in every theme. WCAG 2.2 AA target.

## 6. Security

- Passwords: Argon2id. Sessions: opaque tokens stored hashed, per-device, revocable, "sign out everywhere".
- Optional TOTP 2FA (admin can require it). Optional OIDC SSO.
- Login rate limiting and lockout with backoff. Admin alert on repeated failures.
- Authorisation is enforced server-side on every item/stream request against library visibility and rating policy.
- Signed, expiring, user-bound stream/segment URLs.
- Non-root process, read-only media mount, strict CSP on the web app, no third-party analytics.
- Secrets (metadata API keys, TLS) are kept only in `/config` and never logged.

## 7. Testing

- **Go unit tests** for every package. The playback decision engine gets a table-driven
  matrix of device profiles × file profiles × policies → expected rung and reasons.
- **Test media set** (`testdata/media/`, generated by a script with ffmpeg, so no
  copyrighted content): H.264/HEVC 8/10-bit, HDR10, HLG, DV (P5/P8), AV1, VP9, TrueHD,
  DTS-HD, EAC3 Atmos, FLAC, ASS with fonts, PGS, SRT, MKV/MP4/AVI/TS, odd framerates, interlaced.
- **End-to-end tests:** Playwright (`mcr.microsoft.com/playwright` image) runs every test file in
  Chromium/Firefox/WebKit, asserts playback starts, seeks work and subtitles render, under
  throttled networks of 1, 3, 10 Mbps and unthrottled.
- **API contract tests** against the OpenAPI spec.
- **GPU tests** are tagged and run on zraid (self-hosted runner or manual). CI uses CPU paths.
- Before each phase is called done, it is verified against real files from the zraid library
  and known-problem files from the Jellyfin attempt.

## 8. Team workflow (Lisimflix org)

- Private repo `Lisimflix/lisimflix`, monorepo: `server/` (Go), `web/` (React), `docs/`,
  `deploy/` (compose, Unraid template), `testdata/`. Android goes in `Lisimflix/lisimflix-android` (phase 5).
- `main` is protected: PR + 1 review + green CI required.
- GitHub Actions on PRs: lint, unit tests, web build, E2E (CPU). On merge to `main`: build and push
  `ghcr.io/lisimflix/lisimflix:edge`. Tags `vX.Y.Z` push versioned + `latest`.
- Work is tracked as GitHub Issues on an org Project board, grouped by phase/milestone.
  Module boundaries (2.2) map to issue labels (`area:transcode`, `area:web`, ...).
- `CONTRIBUTING.md` and `CLAUDE.md` define conventions for humans and AI agents alike.

## 9. Phases

| # | Phase | Done when |
|---|---|---|
| 1 | **Foundation:** container, DB, auth/accounts/profiles, scanner, metadata incl. anime, Plex import, API skeleton | Real zraid libraries scan and match accurately. Users/profiles work via API. |
| 2 | **Streaming engine:** decision ladder, remux, GPU transcode, ABR HLS, subtitles, HDR tone-map, trickplay, intro detection | Every test-media file plays in all browsers at all throttle levels |
| 3 | **Web app:** layout C complete, then A and B, TV mode, admin dashboard, PWA | Household can browse and play everything from PC, phone and PS/Hisense browsers |
| 4 | **Hardening and trial:** 2FA/OIDC/invites, TLS + remote access, backups, bandwidth governance; trial with household + friends next to Plex | Two weeks' trial with no blocking issues |
| 5 | **Android TV + phone app** (Kotlin, Media3), QR sign-in | Native app plays the test set directly where hardware allows |
| 6 | **Offline downloads:** original or transcoded copies, per-user permission, batch download | A downloaded episode plays offline on Android and the PWA |

Plex keeps running throughout. The cut-over is the owner's decision after phase 4.

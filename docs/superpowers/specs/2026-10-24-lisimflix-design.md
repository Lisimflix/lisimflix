# Lisimflix: Design Spec

- **Date:** 2026-10-24
- **Status:** Expanded review draft; original vision retained, revised scope and defaults require Pete/Lammy approval
- **Revision:** 2026-10-25 — requirements, failure behaviour, contracts and acceptance gates
- **Repo:** `Lisimflix/lisimflix` (private, GitHub org `Lisimflix`)
- **Image:** `ghcr.io/lisimflix/lisimflix`

## 0. How to read and approve this spec

This is a product and engineering contract, not a claim that the application exists.
The repository currently contains design and team workflow documents, not a working server.
The 2026-10-25 additions are proposals for review, including the release boundary,
numeric defaults and performance targets. They must not be described as decisions
already approved by either project maintainer. Pete confirmed on 2026-10-25 that
zraid is Lammy's Unraid server and that the product must serve independent deployments;
the original installation-specific design is not the product boundary.

**MUST** means required within the feature's agreed release. **SHOULD** means the
implementation can differ if its PR records the reason and user impact. **MAY** means
optional. A feature described here is not automatically required for the first trial.
Section 9 assigns proposed delivery gates; section 14 records decisions still needed.
An unresolved item blocks the affected feature, not unrelated work.

### 0.1 Approaches and recommendation

| Approach | Benefit | Cost / risk | Review position |
|---|---|---|---|
| Editorial cleanup of the original scope | Small diff; preserves every original promise | Leaves hard playback assumptions and completion criteria unresolved | Insufficient for this review |
| Detailed contract with a thin working playback slice first | Tests the reason for building early; retains later features explicitly | Changes delivery order; needs owner agreement on first-trial devices | Recommended in this draft |
| Full feature parity before deployment testing | Broad first release | Delays real-device evidence and puts too many independent risks on the critical path | Not recommended |

The recommended approach keeps Go, React, SQLite and a single container. It adds no
microservices, plugin system, distributed workers or speculative Postgres implementation.
Scope is clarified rather than silently deleted. Approval of this draft should name
which section 14 proposals are accepted or changed before implementation plans are written.

### 0.2 User outcomes and critical journeys

| User | Journey | Required outcome |
|---|---|---|
| Owner | Start an empty instance, create admin, add a read-only library | Can browse a sample without editing a database or exposing a public endpoint |
| Household viewer | Choose profile, resume an episode, change tracks | Correct personal position and preferences, with no other profile's history |
| Remote invited viewer | Accept invitation, sign in, select a permitted title | Only approved content and quality; understandable failures on weak networks |
| Anime viewer | Choose Japanese audio and styled English subtitles | Episode identity, ordering, signs, fonts and timing remain correct |
| Administrator | Diagnose a failed play or restore a backup | Sees the decision and failure reason; can recover without modifying media |

### 0.3 Invariants

- Source media is never deleted, renamed, modified, tagged or written to by Lisimflix.
- Access rules apply to discovery, metadata, artwork, subtitles, streams and downloads.
- Local accounts, cached browsing and supported local playback work without metadata
  services; social sign-in and external sync can remain unavailable during outages.
- Work is bounded: requests, scanner queues, subprocesses, memory, disk and retries
  have limits. Exhaustion produces a controlled response, not an unbounded fallback.
- Existing Plex/Jellyfin systems remain independent throughout the trial. Optional
  migration reads from them; Lisimflix never changes their source state.

### 0.4 Evidence and confidence

Product requirements are deployment-neutral. A reference installation is evidence
for its tested configuration, never the definition of all users, host paths, GPU
vendors, library sizes or stream limits. Lammy's original setup is recorded separately
in [Reference installations](../../testing/reference-installations.md). Its ownership
is confirmed by Pete; its hardware, driver, usage and playback claims still need tests.

Each test installation records OS/container runtime, CPU/RAM, optional accelerator and
driver, writable storage, media mounts, catalogue size, network and client versions.
Release claims name the configurations actually tested. Do not equate a contributor's
host with another deployment or require access to their private library for release.

External references in section 15 support technical constraints. Product limits and
acceptance targets are proposals, not measurements obtained from those references.

## 1. Purpose

Lisimflix is a self-hosted media server and player that covers what Plex and
Jellyfin do as its own independent product. Core administration and local playback
do not require a vendor account; external metadata and social sign-in are optional
integrations with their own availability and terms. Anyone in the supported deployment
matrix can run an independent instance for their own local and invited viewers.
Existing Plex/Jellyfin users may trial it alongside their server; new installations
do not need either product, its database or a contributor's infrastructure.

### 1.1 Product audience and deployment independence

The product serves individual self-hosters, households and administrators sharing
their own libraries with invited remote users. One independently administered
instance supports multiple accounts/profiles and libraries; different installations
keep their own identities, configuration, metadata keys, media and user data.
This is a reusable self-hosted application. Federation, hosted multi-tenant SaaS and
distributed transcode clusters are outside this design's current scope.

Hostnames, media paths, usernames, server names, domains and provider credentials
are supplied at setup, never compiled into code, seed data or UI copy. Documentation
uses placeholders and working generic deployment examples. A contributor's private
services, Sonarr/Radarr tags, VPN, GPU, reverse proxy or network layout cannot be a
hidden installation prerequisite. External integrations remain independently optional.

The supported baseline is a Linux container with CPU-only direct play/remux and
bounded software conversion. Hardware acceleration is an optional capability with
separately qualified backends. NVIDIA is the initial candidate already described;
Intel/AMD are additional backend candidates, not advertised support until proven.
Non-Unraid deployments use the generic compose path. Architecture and resource limits
are published per tested configuration rather than claiming every host can transcode
every file. Build/version-specific capability evidence remains mandatory.

### Goals

- **Goal 1 — reliable playback:** a user can select supported media, start it, seek,
  pause, resume and complete it reliably on a declared deployment/client/network
  profile. This priority was confirmed by Pete on 2026-10-25. Section 9.3 separates
  the enabling skeleton from the user-visible proof required to achieve this goal.
  Playback uses a published support matrix, with an explicit
  fallback or clear unsupported result outside it. Lag and unplayable formats were
  the reason the earlier Jellyfin attempt was abandoned; impossible bandwidth or
  hardware combinations must not be presented as guaranteed smooth streaming.
- Long-term coverage of self-hosted media users' core Plex/Jellyfin functions: libraries, metadata (including anime), multiple
  accounts and profiles, personal home screens, watch progress, remote access.
- One Docker container. Simple to deploy, back up and migrate across supported hosts.
- Built by an organisation team in parallel through PRs and CI.

### Non-goals

- Compatibility with Plex's official client apps or the plex.tv account system.
- Reverse engineering Plex or any other proprietary software. Everything is built
  from open standards, public APIs and observed product behaviour.
- Watch Together, Live TV/IPTV/DVR, and books/comics/audiobooks (Komga and
  Audiobookshelf already cover these). They may be revisited later and are not in this spec.
- Native PlayStation, Hisense VIDAA, Apple TV, Chromecast or iOS apps. Web playback
  targets devices with a usable browser; Apple TV/Chromecast must not be assumed to
  provide one. Casting and support for devices without a browser need a separate decision.
- Streaming-service content, ads or "Discover" style promotion. These are never added.

### 1.2 Supported environments and configuration

| Environment | Product contract | Evidence needed |
|---|---|---|
| Generic Linux container, no GPU | Required baseline; browse, direct/remux, supported subtitles and bounded software conversion | Fresh deploy and restore, representative playback, overload behaviour on a declared CPU/RAM profile |
| Generic Linux container, qualified accelerator | Same application/configuration with optional device access and tested pipeline | Per-backend codec/filter/driver tests and measured capacity; GPU absence/failure handled safely |
| Unraid | Deployment adapter for the same image and public configuration contract | Template/mount/permissions checks; no separate Unraid-only product behaviour |
| Untested OS/architecture/accelerator | Explicitly experimental until qualified | Versioned image, runtime, codec and recovery evidence before promotion |

Settings include library roots, public URL/trusted ingress, account policies, job and
stream budgets, cache/free-space limits, metadata providers and accelerator preference.
Reject invalid settings with actionable validation. Show effective configuration and
detected capabilities without exposing secrets. Backup/restore remains portable across
supported hosts: remap mount roots, re-probe devices and preserve item/user identities.

Library discovery works with manually organised files. Sonarr/Radarr naming and NFO
are optional matching aids. Do not depend on a particular host folder, tag collection
or a running Sonarr/Radarr service. Paths in API/config refer to container-visible
roots, with host-to-container mappings documented per installation.

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
- **Database: SQLite** (WAL mode) on local storage. Modules own their SQL; handlers do
  not access tables directly. Add interfaces where consumers need a stable contract,
  rather than implementing a hypothetical Postgres backend. Versioned migrations run
  on startup; incompatible databases fail closed with a recovery instruction.
- **ffmpeg: jellyfin-ffmpeg**, a separate open-source GPU-tuned ffmpeg build. It is used
  only as an external process and Jellyfin server code is not used.
- **Web app: React + TypeScript (Vite)**, built to static files and embedded in the binary.
- **Runs as non-root** (PUID/PGID env). `/media` is always read-only.
- **No GPU:** direct play and remux remain available. CPU encoding (libx264) is a
  bounded fallback for profiles the host can sustain; unsupported real-time workloads
  are refused or offered a lower quality. CPU HDR/DV conversion requires separate proof.
- Deploy: one generic `docker-compose.yml` and one optional Unraid template, 3 volumes
  and HTTP port 8420. Initial setup, local login and playback work on a trusted
  private LAN without a domain, reverse proxy, TLS certificate or external account.
  GPU access is optional. Remote/internet access requires configured HTTPS; see 11.1.
- First run opens a setup wizard: local admin account, server name, libraries and
  optional metadata keys. A domain or provider credential is not needed to complete it.
- **Backup/restore:** use a consistent SQLite snapshot plus durable configuration;
  copying only a running WAL database file is not a backup procedure. A stopped,
  complete `/config` copy is also supported. Nightly snapshots keep the last 7
  successful backups; restore and upgrade behaviour is specified in section 11.

### 2.2 Internal modules (Go packages)

Each has one job and a documented interface, so team members can own one at a time.

| Package | Responsibility |
|---|---|
| `api` | HTTP routing, auth middleware, JSON API (versioned `/api/v1`), OpenAPI spec |
| `auth` | Accounts, profiles, sessions/tokens, OAuth sign-in (Google, Microsoft, Apple, Facebook, generic OIDC), local admin fallback (Argon2id + TOTP), invites, rate limiting |
| `library` | Library definitions, file scanning, filesystem watching, ffprobe analysis |
| `metadata` | Provider interface and providers (TMDB, TVDB, AniList, AniDB, MAL (official API), Fanart.tv, MusicBrainz, NFO), ID mapping, artwork cache |
| `media` | Items domain model: movies, shows/seasons/episodes, versions, extras, collections |
| `userdata` | Progress, watched state, watchlist, ratings, history, per-show track prefs |
| `playback` | Device capability model, playback decision engine, sessions |
| `transcode` | ffmpeg job supervisor, HLS packaging, ABR ladder, HW accel probing, tone-mapping |
| `subtitles` | Extraction, conversion (SRT/VTT), ASS/PGS delivery, burn-in fallback |
| `trickplay` | Seek-bar thumbnails, intro/credits detection |
| `importer` | Later optional Plex/Jellyfin watch-state migration through validated source adapters; no runtime dependency for basic development |
| `admin` | Activity monitor, stream kill/message, tasks, logs, GPU self-test, backups |
| `tls` | Optional built-in Let's Encrypt (autocert) |
| `web` | Embedded SPA serving |

API clients (web now, Android later) use only the public `/api/v1`. The OpenAPI spec is
the contract and the TypeScript client is generated from it.

### 2.3 Optional acceleration and portable configuration

Playback selects a capability-backed execution plan, not a fixed vendor pipeline.
The supervisor accepts software or qualified hardware decode/filter/encode stages
and records their actual backend. Vendor-specific probing/arguments stay in the
transcode implementation; account, library and client APIs do not expose NVIDIA-only
assumptions. Do not add speculative backend frameworks before there are real consumers.

CPU-only startup requires no GPU device nodes, driver libraries or NVIDIA runtime.
An unavailable optional backend leaves supported software/direct paths usable. An
explicit hardware-required operator setting may instead fail readiness with a clear
reason. A different GPU never changes content permissions or source-file identity.
Hardware support is published per backend/build/driver/profile after qualification;
detecting a device is not equivalent to certifying every filter or real-time workload.

## 3. Libraries, metadata and accounts

### 3.1 Libraries

- Types: **Movies, TV Shows, Anime (TV/Movies), Music, Photos, Home Videos**. One or more folders each.
- The first library/interface milestone includes Movies, TV and **Anime as its own
  selectable library type**, not only a tag on TV. Anime has its own provider/order
  settings, with AniList preferred where permitted and suitable under 3.7 rather
  than applying TVDB-first matching universally. Complex cross-provider mapping is
  still qualified separately; provider reliability is validated with actual fixtures.
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
- **General:** candidate providers are TMDB (primary), TVDB (optional), licensed/
  permitted IMDb/Rotten Tomatoes ratings where available, Fanart.tv (logos, clearart,
  backdrops), MusicBrainz (music), and local NFO. Keys, costs and usage permissions
  are validated per provider before implementation; no scraping is implied.
- **Anime:**
  - AniList (candidate primary, subject to 3.7: descriptions, art, genres, studios, relations, airing status)
  - AniDB (episode lists, specials/OVAs/episode types; strict rate limiting plus aggressive caching, client registration required)
  - MyAnimeList scores via the official MAL API only, after its own terms check (Jikan excluded, D06)
  - TMDB/TVDB (fallback artwork)
  - Versioned cross-provider ID/episode mapping, with source coverage and update
    permissions validated under 3.7; no single dataset is assumed to cover all IDs.
  - Absolute and seasonal numbering both supported and cross-mapped. Specials/OVAs are placed correctly.
  - Alternate orders: TVDB orders and TMDB episode groups selectable per show.
- Title display language per profile: English / Romaji / Native. Search matches all alternate titles.
- **Fix match** and per-item/bulk metadata editing. Manual edits and locked fields survive rescans.
- "Keep original title" option per library.

### 3.3 Accounts and profiles

- **Admin:** full server control.
- **Users:** sign in with local username/password accounts from G1, or later with configured providers (6.1), phased
  under section 9. Access requires admin creation, invitation or approved sign-up
  request. A social login on its own never grants access.
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
- AniList/MAL sync is an explicitly enabled later feature. Linking, episode mapping,
  conflict resolution and provider permission must be validated before outbound writes
  are enabled (3.8). A local watched update never waits for an external provider.

### 3.4 Per-profile user data

Progress, watched/unwatched, watch history, watchlist, personal ratings, per-show
audio/subtitle choice, "remove from Continue Watching".

### 3.5 Future Plex and Jellyfin imports

Plex and Jellyfin migration are later optional features. Basic playback, library/
interface development and Continue Watching use Lisimflix's own activity and never
wait for either importer. Keep stable item/profile identities, provider mappings,
progress units and timestamps suitable for future reconciliation from the beginning
(10.3), without implementing migration adapters or source-specific scaffolding now.

Each eventual admin tool reads authorised per-user watch state/progress, explicitly
maps it to Lisimflix profiles and matches media using trusted IDs and confirmed path
mappings. Validate the source/version's actual accessible fields before promising
complete history. Preview changes in a dry-run; apply only after confirmation.

### 3.6 Scan lifecycle and filesystem safety

Each library has an internal ID, type, display name, roots, metadata language/order,
provider settings and scan status. Each root is tracked separately: available,
scanning, degraded or offline. An offline root MUST NOT remove its catalogue entries.

The pipeline is discovery → stable-file check → probe → identity/match → enrichment
→ publish. Local browsing does not wait for remote metadata. Growing/replaced files
are retried after a quiet period (proposed 30 seconds). A size/mtime change invalidates
the probe. Jobs carry a source revision; stale results cannot overwrite newer data.
Watcher events are hints. Overflow, remount and restart trigger reconciliation;
periodic full scans cover missed events and network mounts. Duplicate work coalesces.
Scans are resumable/idempotent. Cancel preserves completed work and stops owned probes.

Discovery supports filesystem hints, scheduled reconciliation, manual **Scan now**
and optional Sonarr/Radarr import notifications. These paths feed the same bounded
scan pipeline. There is **no universal one-minute discovery deadline**: local/network
storage, import method, mount visibility and stable-file checks affect availability.
Measure discovery/probe/enrichment separately per deployment and show queued, waiting
for source, scanning, metadata pending, completed and failed states with reasons.

**LIB-4 — import-triggered scans:** an administrator can configure Sonarr/Radarr to
notify Lisimflix after import/upgrade. A validated event queues only the affected
file/folder/library, not a full-catalogue scan per imported file. Batch/season-pack
events and duplicate notifications coalesce by source/revision. A successful receipt
means queued, not that an item has already become playable or fully matched.

Notifications are optional hints, not proof a file is stable or permission to accept
arbitrary filesystem paths. Authenticate each source with revocable credentials
limited to its configured scan roots; redact credentials and bound payload/event rates.
Apply explicit remote-to-container path mappings before resolving within approved
roots. Reject path escapes/unmapped roots; an absent/offline file is visible retryable
work, never a reason to retire the whole library. Never execute payload commands.
No callback can delete media, grant library access or bypass probe/stability checks.

Adapters validate the supported Sonarr/Radarr version and event schema; setup includes
a connection test. Connection-test events validate reachability/authentication without
launching a full scan. Missed notifications are recovered by normal reconciliation.
Both applications expose webhook notifications in their current official source:
[Sonarr](https://github.com/Sonarr/Sonarr/blob/develop/src/NzbDrone.Core/Notifications/Webhook/Webhook.cs),
[Radarr](https://github.com/Radarr/Radarr/blob/develop/src/NzbDrone.Core/Notifications/Webhook/Webhook.cs).

ffprobe has bounded concurrency, deadlines and output. Proposed defaults: 2 probes,
30 seconds each, 8 MiB captured JSON. Limit violations are visible failures, not
silently truncated successful results. Admin can retry with an explicit override.
Paths resolve within configured roots; symlink escapes are rejected. HTTP requests
use item IDs, never arbitrary host paths. Subprocess arguments never pass through a
shell. Removing a library removes catalogue associations only, after confirmation.

Missing files become unavailable only after successful enumeration of an online root.
Proposed automatic retirement requires two successful scans at least 24 hours apart,
with an admin preview; outages suspend it. Retain identity and user data for returning
files. Moves preserve identity where trusted evidence exists; ambiguous renames go
to review rather than being guessed from size alone. Empty/offline mounts are tested.

### 3.7 Matching, provenance and anime ordering

Matching precedence: explicit user selection/locks → validated provider IDs/NFO →
deterministic provider match → review queue. Conflicting IDs raise a conflict instead
of overwriting a lock. Imported fields retain provider, provider ID, fetch time and
lock status. Refreshing art or language cannot unlock manually edited fields.

Provider outages preserve existing metadata and playable local entries. Calls have
timeouts, shared per-provider rate limits, bounded retries/jitter, caching and
provider-directed backoff. Configuration/auth errors pause a provider. Initial scans
fetch only relevant records, not provider-wide catalogues. Clients never receive keys.
Check attribution, redistribution, caching and commercial-use conditions before shipping
each provider. AniList prohibits mass collection and constrains competing services;
it is not an unrestricted catalogue. See its [terms](https://docs.anilist.co/guide/terms-of-use)
and [rate-limit documentation](https://docs.anilist.co/guide/rate-limiting).

Separate local show, provider series, season/order, logical episode and physical file
identities. A file may contain several episodes; specials may have multiple mappings.
Mapping edges retain source revision, confidence and manual overrides. Equal numeric
episode positions across providers do not prove equal content.

The original all-provider mapping claim needs validation: inspect the selected
[Anime-Lists revision](https://github.com/Anime-Lists/anime-lists) and its actual schema;
additional sources may be required for AniList/MAL IDs. Proposed weekly updates are
subject to permissions. Validate and preview the diff before applying it; keep the
last valid dataset on failure. Never overwrite manual mappings/progress automatically.

Fixtures cover absolute numbering, split cours, offsets, OVAs, recaps, specials,
multi-episode files, alternate cuts and conflicting IDs. Fix-match previews affected
episodes and user data. Changing presentation order does not change watched identity.

### 3.8 Progress, preferences and external sync

Progress belongs to profile + logical item, retaining source/version identity for
different cuts. Proposed watched rule: unique acknowledged media-time intervals cover
90% of the playable duration, or cover 50% and playback advances naturally into a
verified credits marker. Seeking over an interval does not count it; replay counts
only once, and playback speed changes elapsed wall time rather than media coverage.
Record advancing intervals from bounded progress events, excluding explicit seeks
and implausible jumps. Seeking directly to credits cannot mark an item watched.
Explicit mark watched/unwatched overrides automation. Different timelines require
confirmation before copying a resume position between versions.

For multi-episode files, automatic per-episode watched state requires known timeline
boundaries. Without them, retain file resume state and ask for explicit episode-level
watched actions instead of assuming equal-length episodes. This behaviour and the
coverage thresholds are provisional defaults under D11.

Clients send progress every 10 seconds and on pause/seek/stop/exit when possible.
Session ID, monotonic event sequence and source revision reject stale/duplicate events.
A late heartbeat cannot undo an explicit user action. Backwards progress within one
session requires a seek event. For concurrent sessions, latest server-accepted active
session event wins; retain both histories. Client wall clocks are not authoritative.
Proposed crash-resume accuracy: within 15 seconds of the last viewed position.

Continue Watching includes items played at least 30 seconds and not watched.
Remove-from-row hides without deleting history; playing again restores eligibility.
Next Up follows the selected order; unavailable episodes are labelled and specials
are not silently inserted into autoplay. Autoplay still checks current permissions.

Track precedence: session choice → saved show choice → library preference → profile
languages/forced rules → valid default. Save semantic choices (language, role,
commentary/forced/signs), not only indexes that change on replacement. Explain missing
track fallbacks. Heuristics suggest signs/SDH/forced roles without asserting certainty.

External sync is off by default and cannot block local progress. Before delivery,
specify OAuth scopes, permitted usage, unlink/revocation, retry budget and mappings.
Proposed first version: outbound only, never reduce remote progress, preview initial
reconciliation and exclude ambiguous/special mappings. Bidirectional sync is separate.

### 3.9 Future import safety (Plex and Jellyfin)

The source is read-only. Admin explicitly maps source users to destination profiles;
display-name/email matches alone cannot merge histories. Validate which per-user
state the chosen API/access method actually exposes before promising completeness.
Any database-based option uses an owner-provided consistent snapshot with a supported
schema, never writes to either source server's live database, and rejects unknown versions.

Dry-run reports matched, ambiguous, unavailable, unsupported and conflicting records,
target profiles and changed fields. Commit uses a batch ID and is idempotent on rerun.
Newer Lisimflix activity wins unless admin selects another policy. Record before-state
so undo restores only unchanged imported values, preserving subsequent local activity.
Retry partial failures without duplicates. Import tokens never appear in logs/exports
and are discarded when the job ends unless separately authorised for retention.

## 4. Streaming engine

### 4.1 Decision ladder (per play request)

Inputs: file analysis (3.1), client capability profile, user/profile limits, measured or estimated bandwidth.

1. **Direct play:** the file is served as-is via HTTP range requests.
2. **Remux:** compatible streams, incompatible container. Repackaged to fMP4/HLS with `-c copy`.
3. **Audio transcode only:** video copied, audio converted (TrueHD/DTS/FLAC → EAC3/AAC as client supports).
4. **Full transcode:** select a tested decode → filter → encode pipeline, packaged as
   HLS fMP4. Prefer hardware paths where actually supported. Software decode plus
   hardware encode is valid; filter compatibility and frame transfers are explicit.
   CPU fallback is subject to admission limits and sustainable output speed.

Every decision is recorded with **reasons** (e.g. "audio TrueHD unsupported by client") and
shown in admin activity and the player's stats overlay.

### 4.2 Client capabilities

- Clients report capabilities on session start:
  - Web: runtime probing via `MediaSource.isTypeSupported`, `MediaCapabilities.decodingInfo`,
    `canPlayType`, HDR media queries (`(dynamic-range: high)`), screen size.
  - Android (later): `MediaCodecList`/Media3.
- Feature-detect probes and provide compatible fallbacks; an unavailable browser
  API on local HTTP must not block otherwise supported M1 video playback.
- Server-side **quirks table** keyed by user-agent/platform (PlayStation browser, Hisense
  VIDAA browser, Safari, etc.) that overrides known false positives.
- If direct play/remux fails, the client reports evidence. The server distinguishes
  compatibility from network/capacity faults, then selects a bounded alternative
  under 4.9. Only appropriate failures become expiring device overrides.

### 4.3 Adaptive streaming

- HLS with fMP4 segments (nominal 4s), with the following candidate quality presets:
  source/4K, 1080p 20, 1080p 8, 720p 4, 480p 1.5, 360p 0.7 Mbps, each capped by user policy.
- Start with one selected rendition per session and controlled quality changes.
  Seamless ABR is a later playback gate: renditions need aligned timestamps/keyframes
  and an explicit encoding/cache strategy. Do not advertise variants that cannot be
  made available within the measured switching budget (4.9).
- Player: hls.js where supported; native HLS where required and verified. A bandwidth
  estimate is a hint, constrained by policy and observed playback performance.
- Seeking: segments are generated just-in-time. A seek outside the generated window restarts
  ffmpeg at that position (keyframe-aligned) and reuses finished segments.
- Next-episode preloading: the first segments of the next episode are prepared near the end.
- Server bandwidth governance: per-user caps, a global upload cap, LAN vs WAN detection
  (LAN never throttled by WAN rules).
- Transcode cache in `/cache`, LRU-evicted, size cap configurable.

### 4.4 Subtitles

- Text (SRT, VTT, embedded mov_text): prefer extraction to WebVTT sidecars. Verified
  clients avoid video transcode; clients unable to render the selected track need an
  explicit burn-in fallback or unsupported result. Conversion can lose formatting.
- ASS/SSA: served raw and rendered client-side with **JASSUB** (libass/WASM) so styling
  and typesetting are preserved during direct play. Fonts are extracted from MKV attachments.
- PGS/VobSub: proven burn-in first; client rendering is a later optimisation with
  its own renderer/device evidence (4.11).
- Subtitle offset adjustment, appearance settings, "signs & songs" track detection.

### 4.5 HDR

- HDR10/HLG/DV (profiles with an HDR10 base layer) are direct played on HDR-capable clients.
- On SDR clients: use a tone-mapping filter present in the pinned jellyfin-ffmpeg
  build and proven on the selected software or accelerator backend. Do not assume CUDA decoding implies a
  compatible CUDA tone-mapping filter. Record the complete filter graph in diagnostics.
- DV profile 5 (no HDR10 fallback) needs a verified DV-aware conversion path; otherwise
  reject with a clear explanation. P7/P8 base-layer handling is also fixture-tested.
  Wrong-colour output is a failure, not an acceptable fallback.

### 4.6 GPU management

- Startup self-test encodes/decodes short samples for each codec/profile and records
  what the hardware actually supports. Shown in admin ("HEVC 10-bit decode ✓, AV1 decode ✗").
- Concurrency is bounded by a configurable application budget and measured hardware/
  driver support. There is no assumption of 8 useful simultaneous sessions. Encoder
  session limits do not establish real-time capacity for scaling, filters or subtitle burn-in.
- GPU failure opens a temporary circuit breaker. Retry a tested CPU/hybrid path only
  if capacity permits; otherwise report overload without spawning more work.

### 4.7 Trickplay, intros and credits

- Seek thumbnails generated in the background (low priority, optionally accelerated)
  as tiled sprite sheets; no GPU is required for the feature's software path.
- Intro detection by audio fingerprint comparison across episodes of a season. Credits
  detection by fingerprint plus black-frame analysis. Chapter markers are used when present.
  Skip buttons, optional auto-skip.

### 4.8 Remote delivery

- Remote HTTPS through an explicitly configured reverse proxy first; optional
  built-in Let's Encrypt (autocert) later, or a documented Tailscale deployment.
  If Cloudflare fronts the UI/API, video uses a separate verified delivery route;
  routing, cookies, origin/CORS and URL expiry must be tested together.
- Stream URLs are signed, user-bound and short-lived.

### 4.9 Playback contract, admission and lifecycle

A play request includes item/version ID, profile, capability revision, position,
tracks and desired quality. Server authorises, resolves paths/tracks, selects a route,
reserves capacity and returns session ID, effective quality/tracks, delivery URL,
expiry, heartbeat instructions and reasons. Repeating an idempotency key cannot
launch duplicate encoders. Client capability claims never override access policy.

Outcomes: `direct`, `remux`, `audio_transcode`, `video_transcode`, `unsupported`,
`capacity_unavailable`. Reason codes include `container_unsupported`,
`video_profile_unsupported`, `audio_unsupported`, `subtitle_burnin_required`,
`hdr_conversion_required`, `bitrate_policy`, `bandwidth_insufficient`,
`device_failure_override`, `capacity_exhausted`. Tests assert all applicable reasons.

Check profile/level, bit depth, chroma, resolution/framerate, HDR, audio channels,
subtitle renderer and container. An H.264 probe does not prove Hi10P or every level.
SDR H.264 8-bit 4:2:0 + AAC stereo is a candidate compatibility output, requiring
real decoder and HLS/MSE verification per device.

| State | Meaning | Recovery / exit |
|---|---|---|
| Requested / preparing | Authorisation, validation and reservation | Ready, bounded timeout or explicit rejection |
| Ready / playing | Media available and client consuming | Pause, seek, quality change, stall, stop or expiry |
| Paused / recovering | No advancement or bounded fallback | Resume, replacement generation or end |
| Ended / failed / expired | Terminal with reason and final progress | Release owned work; new playback needs a new request |

Reservation/launch are atomic against per-installation capacity budgets. Administrators
configure total streams, hardware/software jobs and expensive-filter weights within
their host's measured budget; no contributor's observed concurrency becomes a global
product limit. Proposed safe uncalibrated start: one software video job and zero
hardware jobs until a backend passes its probe; setup requires an explicit total
stream budget and explains the evidence needed to raise it. No claim that one software
job can convert every file in real time. Qualification runs record the selected limits,
including headroom for other applications sharing the host/GPU.
Direct play still uses disk/network and counts against account stream limits.
Preloads and background work yield to foreground sessions.

Proposed preparation timeout: 20 seconds, visible progress after 2 seconds. If encoding
cannot sustain real time, try one lower permitted quality, then fail clearly. Repeated
GPU-specific failures open a 5-minute circuit breaker until a successful probe; one
corrupt file must not disable the GPU. Maximum two automatic route changes per session;
record each, preserve position and tracks, and never enter an infinite fallback loop.

Capabilities combine runtime probes and a versioned quirks table. Cache negative
results by device/browser version + codec/delivery profile (proposed expiry 7 days),
with reset/retest. Network stalls alone cannot permanently blacklist a codec.
Clients heartbeat every 10 seconds; release jobs/capacity after 60 seconds absent.
A connected paused session can retain bounded cache, not an encoder indefinitely.
Stop/admin kill is idempotent: signal the owned process group, wait a bounded grace
period, terminate if necessary and reap it. Restart marks sessions interrupted;
clients create new sessions at saved positions rather than attaching to stale jobs.

### 4.10 HLS timeline, seeks, quality changes and cache

The server owns the timeline. Init segments, timestamps, sequence numbers, durations
and discontinuities remain coherent. Transcoded switchable renditions align keyframes.
Copy remux uses existing keyframes: segment lengths can differ from the 4-second target.
Use Apple's [HLS authoring specification](https://developer.apple.com/streaming/hls-authoring-specification-for-apple-devices.html)
as a compatibility reference; test non-Apple clients too.

Seeking outside prepared media supersedes old work and starts a generation at a
decodable boundary. Discard pre-target frames; keep audio/subtitles on the logical
timeline. Stale responses cannot attach to a new generation. Rapid scrubs coalesce.
Test long GOPs, nonzero/negative source timestamps and track changes after seeks.

First delivery supports manual quality changes with controlled restart at the current
position. Before seamless ABR, choose and measure concurrent aligned encodes versus a
bounded just-in-time scheduler. Record switch latency, resource cost and cache effects.
Only advertise variants deliverable under that strategy. No claim of seamless ABR
until switching tests pass on target devices.

Presets are capped by source dimensions/framerate, device, policy and server budget.
No default upscaling. Include audio/transport headroom; a 1 Mbps link cannot sustain
a 1.5 Mbps video preset. Proposed starting selector uses at most 70% of the estimated
bandwidth. If no preset fits, return `bandwidth_insufficient`. Fixed quality is still
policy-bound. LAN classification never bypasses content or account stream limits.

Cache identity includes source revision, streams, conversion parameters, ffmpeg build,
subtitle/font revision, tone-map settings, rendition and generation. Publish complete
segments atomically. Active readers pin media/init segments. Shared transforms still
require authorisation for each consumer. Never put bearer tokens in cache filenames.

Proposed cache cap: 20 GiB, preserving 5 GiB filesystem free, validated against the
actual volume during setup. Evict unpinned entries, cancel speculative work, then
refuse new work when thresholds cannot be met. Restart removes incomplete owned files
safely. Durable user data never lives in `/cache`. Preload uses spare capacity and
never marks the next episode played.

### 4.11 Subtitle and HDR gates

ASS needs a verified [JASSUB](https://github.com/ThaUnknown/jassub) WASM/worker path
under the application's CSP on each target device. Upstream renderer support does
not establish TV-browser performance. Bound attachment size/extraction time and never
execute attachments. Subtitle text cannot become active HTML. Test font fallback,
karaoke, overlapping events, positioning, right-to-left text, offsets and seek resync.

Native/PiP video can omit DOM overlays. Where selected subtitles cannot appear, offer
burn-in or disable that mode visibly; never silently drop subtitles. Bitmap subtitles
start with proven burn-in. Client PGS/VobSub is a later renderer project, not an assumed
built-in browser feature. Burn-in track changes restart at the same logical position.
Extraction may require substantial reads: display preparation, honour cancellation
and allow another track or supported fallback.

HDR acceptance records input/output transfer, primaries, matrix and actual display
mode. HDR queries alone are insufficient. Verify HDR10, HLG and DV separately against
reference visuals on real SDR/HDR screens. SDR output removes stale HDR signalling.
Automated moving-pixel tests cannot establish correct tone mapping or colour.
Inspect the pinned build's filters and [FFmpeg documentation](https://ffmpeg.org/ffmpeg-filters.html);
the original blanket `tonemap_cuda` assumption is withdrawn. Unproven DV conversion
remains a blocked support-matrix cell.

## 5. Web app (UI/UX)

Mockups were referenced at `.superpowers/brainstorm/` in the original design but are
not tracked in this checkout. Treat visual directions as descriptions until owners
attach versioned references; missing mockups are not an implementation dependency.

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
- **Users & invites:** policies (3.3), sessions/devices, linked sign-in providers, sign-up requests, OAuth provider setup.
- **Libraries:** scan status, unmatched items, fix-match, bulk edit.
- **System:** GPU self-test, logs, scheduled tasks, backups/restore, metadata provider keys, TLS.
- **Optional later Plex/Jellyfin import tools**, shown when those features are implemented.

### 5.4 TV mode

- Explicit TV-mode toggle, remembered per device. Detected TV platforms can suggest
  it; screen size or arrow-key use alone must not change a desktop user's layout.
- Full spatial (D-pad) navigation with visible focus rings, larger targets, no hover-only controls.
- QR-code sign-in: the TV shows a code, the phone approves it.
- Targets: PlayStation browser, Hisense VIDAA browser, Android TV browser (until the native app ships).

### 5.5 Phones

PWA (installable), bottom tab bar, touch gestures in the player (double-tap seek).
Regular browser use on local HTTP is the baseline. PWA installation, offline and
other browser features are qualified separately when available; they cannot become
hidden prerequisites for local browsing, login or playback.

### 5.6 Accessibility

Keyboard-complete, ARIA labels, focus management, readable subtitle defaults in every theme. WCAG 2.2 AA target.

### 5.7 Interaction states and navigation

Every browse/detail/settings screen defines loading, empty, partial, offline and
error states. An unmatched file is browsable with a filename and manual match action.
An unavailable source is labelled; Play explains the failure instead of spinning.
Destructive catalogue/config actions show what changes and explicitly state that
source files remain untouched. Save failures retain unsaved form values.

Home rows only include authorised items. Search filters before totals/facets are
calculated, so restricted titles do not leak through counts, autocomplete or artwork.
Search and library queries use server pagination even when the UI scrolls infinitely;
restore scroll/focus after opening a detail page. TV mode has explicit next/previous
loading controls if virtualised scrolling breaks D-pad navigation.

Playback exposes Preparing, Buffering, Reconnecting and Failed separately. Recoverable
errors offer retry or lower quality; terminal errors show a short message and diagnostic
ID. Audio/subtitle menus name language, role, codec and selected fallback. Stats show
effective quality and conversion reasons without exposing filesystem paths to viewers.

Profile/device settings have documented scope: layout/theme/languages are profile
preferences; volume/TV mode/device quirks are device-specific. Changing profiles stops
the old playback context and clears cached private results. Admin routes cannot appear
through stale navigation after privilege changes.

### 5.8 TV, mobile and accessibility acceptance

Every TV journey (sign in, choose profile, browse, search, play, switch track, exit)
must work with D-pad + select + back, with no keyboard/mouse assumption. Focus survives
row loading, modal close, episode change and error retry. A back action closes the
nearest overlay before leaving playback; no focus trap or inaccessible exit is allowed.
Record actual PlayStation generation and Hisense model/firmware before committing to
support; missing browser/MSE/native HLS cannot be fixed by a different CSS layout.

Mobile tests cover rotation, notches/safe areas, touch target sizing, fullscreen exit,
interrupted connections, returning from the background and autoplay restrictions.
Installability does not imply offline media. The initial PWA caches only versioned
application assets; authenticated catalogue, artwork and video must not be cached
indefinitely by a generic service-worker route. Clear user-specific client state on
logout. Offline downloads require section 9's separate gate and storage/revocation policy.

WCAG 2.2 AA is evaluated with automated checks plus keyboard, zoom and screen-reader
checks on critical journeys. Include contrast in every theme, reduced motion, visible
focus, accessible status/error announcements, labelled controls and subtitle usability.
No essential action depends on hover, colour alone, gestures or auto-playing audio.

## 6. Security

### 6.1 Sign-in: local accounts first, social providers later

- **Initial sign-in is local username/password**, for the setup administrator and
  admin-created user accounts. It works on trusted-LAN HTTP with providers disabled,
  no internet/domain and no provider credentials. Passwords use Argon2id; existing
  session, rate-limit and recovery requirements apply from this first usable release.
- **Later optional sign-in uses OAuth 2.0 / OpenID Connect** with **Google, Microsoft (personal +
  work/school), Apple and Facebook**, plus a **generic OIDC** slot (e.g. Authentik/Keycloak).
  Authorization Code flow with PKCE, `state` + `nonce` checks, ID-token signature validation
  against each provider's JWKS. Facebook uses OAuth 2.0 + Graph `/me`, since it is not full OIDC.
- Each provider is enabled in admin by entering its client ID/secret. A built-in setup guide per
  provider shows the exact redirect URL (`https://<domain>/api/v1/auth/callback/<provider>`).
  Unconfigured providers are hidden from the sign-in page, so a fresh install still works.
- **Requirements:** provider-approved HTTPS redirect URLs for deployed social sign-in;
  supported development exceptions are provider-specific. Local sign-in does not
  require a public domain. Apple/Facebook eligibility, keys, review requirements and
  current fees must be checked against their official documentation before that phase.
- **Access control:** users can link several providers to one account. The first social sign-in must come
  through an invite link (which binds that provider identity to the invited account) or a sign-up
  request the admin approves. Identities are matched on the provider's stable subject ID, never on
  email alone, to prevent takeover via unverified emails.
- **Local access and fallback:** the admin account (from the setup wizard) always keeps a local password
  (Argon2id) with TOTP 2FA, so the server can still be managed if the internet or a provider is
  down. Local username/password accounts are enabled for users from G1. Later provider
  configuration must not silently remove a user's working local login or history.
- Home profiles (3.3) sit under the signed-in account and use PINs, not OAuth.
- TV sign-in uses the QR flow (5.4): an authenticated phone session (local or provider)
  approves the TV with restricted authority as specified in 6.5.

### 6.2 Sessions and hardening

- Sessions: opaque tokens stored hashed, per-device, revocable, "sign out everywhere".
  Unlinking a provider identity ends its sessions.
- Login rate limiting and lockout with backoff. Admin alert on repeated failures.
- Authorisation is enforced server-side on every item/stream request against library visibility and rating policy.
- Signed, expiring, user-bound stream/segment URLs.
- Non-root process, read-only media mount, strict CSP on the web app, no third-party analytics.
- Secrets use restricted server-side storage under `/config` or explicitly mounted
  secret files. They are never sent to the web client or logged. Backups containing
  them are sensitive; deployment instructions cover access and encryption at rest.

### 6.3 Account, profile and policy boundaries

An account is an authenticated principal; a profile is a viewing identity under it;
an administrator is an explicit account role. A profile PIN is not equivalent to an
account password or a strong isolation boundary from someone controlling the account.
Kids restrictions must be enforced server-side, including profile-switch grants;
never accept an arbitrary profile ID just because the account can name it.

Proposed account policy is the ceiling and profile policy can only narrow it.
Library grants intersect; bitrate/resolution/concurrency use the stricter applicable
limit; downloads require permission at both levels. Age restrictions include a defined
rating region and explicit handling for unrated/unknown content. Proposed child default
denies unrated items. Missing policy/rating data never silently expands access.
Record how a parent exits a locked profile, rate-limit PIN attempts and expire grants.

Admin can disable accounts, revoke sessions and remove grants immediately. Existing
playback, artwork and subtitle requests recheck effective permission. Public errors
avoid confirming that a forbidden item exists. Security tests cover guessed IDs,
swapped profile/session IDs, stale grants and shared cached resources.

### 6.4 Bootstrap, OAuth linking and recovery

Bootstrap is available only on an uninitialised server through a local deployment
channel or single-use random setup token printed to the owner's local console.
The first web visitor cannot claim an internet-exposed installation. Completing setup
atomically disables bootstrap. Local admin credentials work with providers disabled;
TOTP enrolment includes tested recovery codes stored hashed, shown once.
Specify an owner-only local recovery command before remote deployment; recovery must
not expose a permanent unauthenticated HTTP bypass.

Invites are random, stored hashed, single-use and expiring (proposed 48 hours), with
explicit account/library policy. A social callback alone never grants access. Bind
OAuth state to the initiating browser and intended invite/link action. Linking requires
recent authentication to the existing account and proof of the new identity; identical
email addresses never automatically link accounts. Unlink cannot remove the last
usable administrator login. Disable provider login without deleting local history.

Use maintained libraries for OAuth/OIDC and password/TOTP primitives. Validate issuer,
audience, signatures and expiry; use nonce for OIDC and PKCE where supported, following
[OAuth security BCP](https://www.rfc-editor.org/rfc/rfc9700.html). Provider-specific
adapters handle differences; Facebook is not treated as generic OIDC. Provider tokens
remain server-side, with minimum scopes and explicit retention. Do not log callback
query strings, auth codes, invitation tokens, cookies or device authorisation secrets.

Browser sessions use host-only HttpOnly cookies with explicit `SameSite=Lax` and
server-validated CSRF protection for state-changing browser requests. Set `Secure`
for an HTTPS public origin. Trusted-LAN HTTP mode omits `Secure` so sessions work
at a private IP/hostname, and cannot use cookie-name prefixes that require it.
Do not rely on localhost exceptions for other LAN devices. See
[MDN cookie attributes](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie).
SameSite is defence in depth; use CSRF tokens and validated request origins rather
than treating it as sufficient alone, following
[OWASP CSRF guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html).
The effective public scheme is configured explicitly or obtained from a trusted
proxy, never arbitrary forwarded headers. Origin/scheme changes require a new
authenticated session; an HTTP-mode session must not silently downgrade HTTPS mode.
Rotate the session on sign-in/privilege change. Proposed lifetimes: 7 days
idle, 30 days absolute; sensitive admin changes require reauthentication within 10
minutes. Native apps later use revocable secure token storage, not web localStorage.
Rate limits apply per account/action plus network, with bounded lockout so an attacker
cannot permanently deny the administrator access.

### 6.5 Stream authorisation and TV pairing

Signing a URL does not by itself make it user-bound. Same-origin web streams require
the authenticated session plus a playback-scoped grant; native HLS cookie delivery
must be tested. Future clients without cookies need an explicit scoped delivery-token
design. Proposed URL grant expiry is 5 minutes, refreshable by the live authorised
session; bind to session/profile/item/transform and revoke when its parent is revoked.
Do not bind solely to IP, which changes on mobile and can be shared behind NAT.

Check every range, playlist, init/media segment, font, thumbnail and subtitle request;
knowing an object/cache key cannot bypass permission. Expiry during playback refreshes
without restarting the video where the client supports it. Access logs redact query
tokens, and referrer policy prevents leaks. Private responses cannot enter public CDN
caches. CORS is denied by default except explicitly configured origins.

TV pairing creates a high-entropy polling secret and a separate short human code.
Proposed validity: 5 minutes, one approval and one redemption, polling no faster than
5 seconds with server backoff. The phone displays the server and requesting device
before approval. Pairing cannot transfer admin authority by default. Rate-limit
guessing, permit cancellation, and test expired/replayed/parallel redemption attempts.
The TV never receives the approving phone's password or provider access token.

### 6.6 Network and untrusted-input boundaries

**Trusted-LAN HTTP works from first launch:** setup, local account sign-in, browsing
and playback require no domain or reverse proxy. Authentication/authorisation, CSRF,
rate limits and protected bootstrap still apply; LAN reachability is not a login.
This mode has no transport encryption. Setup/deployment docs state that it is for a
trusted private network and is not to be directly exposed to the internet.

**Remote/internet access requires HTTPS**, initially through an operator-configured
reverse proxy. Social providers use their approved HTTPS callbacks when delivered;
built-in ACME remains later optional work. Enabling remote access must not downgrade
session/security settings to accommodate an HTTP public origin. Do not claim the
application can detect every unsafe port-forward or proxy configuration automatically.
Define public base URL, trusted proxy CIDRs and forwarded-header
handling. Ignore forwarded client identity from untrusted peers. LAN detection uses
configured/trusted network information, not arbitrary client headers.

Restrict provider fetching to approved schemes/hosts and validate redirects/IP targets
to prevent SSRF. Private-network Plex/Jellyfin import is a separate admin-only operation with
explicit destination approval, not an exception for all remote fetches. Treat NFO/XML,
images, fonts, archives and probe output as untrusted: bound sizes/time, disable XML
external entities and reject traversal paths. ffmpeg inputs use approved local files,
never an arbitrary user-provided URL or playlist protocol.

Log security events with actor/action/result and correlation ID, redacting secrets
and private viewing detail from unauthorised viewers. Proposed retention is 14 days,
configurable with a size cap. No third-party analytics; external metadata/OAuth/sync
traffic is disclosed in setup and can be disabled independently.

## 7. Testing

- **Go unit tests** for every package. The playback decision engine gets a table-driven
  matrix of device profiles × file profiles × policies → expected rung and reasons.
- **Test media set:** generated owned samples where possible, plus explicitly
  redistributable specialist fixtures or owner-local files. A script cannot be assumed
  to generate valid DV, DTS-HD, TrueHD or Atmos merely by selecting an encoder. The
  manifest records codec/profile, provenance, redistribution rights and expected result.
  Coverage includes H.264/HEVC 8/10-bit, HDR10/HLG/DV P5/P7/P8, AV1, VP9, lossless/
  multichannel audio, ASS/fonts, PGS, SRT, common containers, odd framerates and interlace.
- **End-to-end tests:** local Playwright runs under Chromium/Firefox/WebKit with
  documented browser/codec builds. Playback, seeks, subtitles and network profiles
  are checked against the supported matrix. Playwright WebKit does not certify Safari,
  iOS or a TV. Proprietary codec availability depends on the actual browser build.
- **API contract tests** against the OpenAPI spec.
- **Accelerator tests** are tagged by backend and run on any qualifying test host
  (self-hosted runner or manual). Normal CI uses CPU paths and needs no contributor's
  private server, credentials or media.
- Gate completion uses the shared fixture matrix plus documented owner-local regressions
  on multiple independent installations. One contributor's problem files supplement
  reproducible coverage; they cannot be the only evidence that a release works.

## 8. Team workflow (Lisimflix org)

- Private repo `Lisimflix/lisimflix`, monorepo: `server/` (Go), `web/` (React), `docs/`,
  `deploy/` (generic compose, optional Unraid template), `testdata/`. Android goes in
  `Lisimflix/lisimflix-android` (G4 expansion milestone).
- The team merge rule for code PRs, including CI bootstrap, is local `make check` + PR + 1 review + green GitHub CI. A new `pull_request` workflow can run from the PR merge branch before reaching the default branch. The org is on the free plan,
  so a `.githooks/pre-push` hook blocks direct pushes instead of GitHub branch protection (see `CONTRIBUTING.md`).
  Switch to a GitHub ruleset when the org upgrades.
- GitHub Actions runs lint,
  unit tests and web build through `make check` on every PR.
  E2E and real-device/GPU checks remain local per `CONTRIBUTING.md`; attach evidence
  to relevant PRs. On merge to `main`: build and push
  `ghcr.io/lisimflix/lisimflix:edge`. Tags `vX.Y.Z` push versioned + `latest`.
- For code changes, authors complete dependency installation and run `make check`
  locally on the finished feature branch before publishing it. The bootstrap PR that
  introduces the command/workflow runs the new command locally on its own branch
  before its first push. The bootstrap PR also requires the independent GitHub run
  and normal second-member review. Report the actual Actions result; if it did not
  run, investigate the trigger/settings rather than assuming a bootstrap exemption.
  This follows GitHub's [pull_request event documentation](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#pull_request).
- Work is tracked as GitHub Issues on an org Project board, grouped by phase/milestone.
  Module boundaries (2.2) map to issue labels (`area:transcode`, `area:web`, ...).
- `CONTRIBUTING.md` and `CLAUDE.md` define conventions for humans and AI agents alike.

### 8.1 Development model: 2–3 contributors in parallel

The project is developed by 2–3 people, each potentially using a coding agent.
Work must be independently claimable, testable and reviewable. A completed application
skeleton is not a prerequisite for every feature. Milestone numbers describe product
readiness, not an instruction for every contributor to work through one serial queue.

Use the named feature IDs in 9.4 as the unit of discussion, issue claims, plans and
PRs, following the requirement-catalogue pattern used by Trillion's PRD. Ownership is
per active feature, not permanent ownership of an entire subsystem. One person claims
an issue, identifies the other reviewer and declares their write scope before coding.
Features stay unassigned until a contributor chooses an available one and assigns
its issue to themselves. Pete does not allocate work or preassign subsystem roles.
An active claim is visible to everyone; another contributor picks a different ready
feature or agrees a scope split with the claimant before touching overlapping files.

Keep at most one primary feature active per contributor. Anyone can choose any
unclaimed feature whose start dependencies are satisfied, across platform, playback,
player, catalogue or other areas. The track examples below describe parallelism and
integration, not assigned teams or a mandated picking order. A feature needing a
missing shared contract identifies that small dependency explicitly; its contributor
can propose the seed/change or pick other ready work while it is resolved.

### 8.2 Small shared contracts before integration

Agree the smallest shared contracts needed for the next parallel features, not a
complete speculative API. `CTR-1` records package locations, language manifests,
playback request/result/error shapes, capability and media-stream descriptors,
source-resolver results, effective-policy snapshots, subtitle-asset descriptors,
timeline units, session/generation
identity and representative fixtures. `CTR-2`
records catalogue/profile/progress contracts when their consumers are ready.

Each contract has one issue owner, a reviewer from its first consumer, a version and
matching example/negative fixtures. Go and TypeScript representations must agree;
OpenAPI is authoritative for HTTP shapes. The producer owns the fake/test adapter
specification with consumer review, so each contributor does not invent a different
definition of progress, source identity or playback state. Contracts never bypass auth.

The smallest shared seed may add language manifests and contract fixtures before
the runtime/container/database skeleton is complete. This is a real dependency;
keep it small and explicit. UI design and fixture authoring can start while it is
reviewed. Module acceptance then runs via package tests, a CLI/test harness or a
fixture-backed browser harness without requiring the full server or database.

Fixture servers/harnesses bind to loopback, use only synthetic accounts and approved
test media, and are excluded from production startup. A fake policy/identity adapter
must never become the default on a deployed instance or expose private library data.

Plans distinguish **start dependency**, **module completion dependency** and
**integration dependency**. A test adapter can satisfy a module dependency when the
contract allows it; it cannot prove production integration. Do not replace a dependency
on one method/schema with “wait for all of foundation/auth/library to finish.”

### 8.3 Write boundaries and change coordination

Each feature issue lists owned files/packages, consumed and produced contracts,
test commands, review owner and integration partner. Work in separate branches and
separate working directories. Two contributors must not mutate the same checkout.
If write scopes overlap, split the feature or sequence that shared-file edit first.

Shared hotspots have one temporary integration owner: Go module manifests, frontend
package/lock files, root build/CI/container files, route registration, canonical
OpenAPI/schema generation and migration ordering. Feature owners propose additions;
the integration owner lands the shared change in a small PR. Do not concurrently
rewrite the API document, regenerate clients or allocate the same migration number.
Generated clients are regenerated from the reviewed contract, not edited by hand.

Contract changes identify affected consumers before merge. Prefer additive compatible
changes, update fixtures and producers/consumers together where needed, and avoid
unannounced breaking changes that strand another contributor's branch. Reconcile
with current main before review; do not use force-pushed shared feature branches.
Coordination uses issues/PRs per CONTRIBUTING; no chat-only claim counts as ownership.

### 8.4 What future implementation plans must contain

The first M1 draft plan set starts with [CTR-1 contracts and the plan map](../plans/2026-10-25-ctr-1-m1-contracts.md).
Runtime, direct playback and player plans are independently claimable after the small
seed; the integration plan separately proves the complete movie. These are review
drafts, not assignments or evidence that implementation has started.

Produce small feature/subsystem plans rather than one enormous serial implementation
plan. Every task names feature IDs, owner role, reviewer, file scope, input/output
contracts, real dependencies, standalone test command and integration proof. The plan
must say what can run now against fixtures and what genuinely needs another feature.
Exact file paths/signatures and test assertions are settled in those implementation
plans; they are not invented independently by each worker after claiming a task.

Feature delivery has two evidence stages: **module accepted** (its own behaviour and
failure tests pass) and **integrated** (real adapters pass the cross-feature journey).
A fixture-backed player can be module accepted before the server exists; reliable
playback Goal 1 is complete only after real player/server/media integration passes.
An issue records both stages and links proof. “Merged” alone never proves the user
outcome, and “module accepted” must not be advertised as a working released feature.

## 9. Proposed delivery gates and retained roadmap

This revises delivery order for review; it does not claim owners have approved dropping
features. The original broad phases delayed UI testing until after a large streaming
engine. Ship a minimal browse/player slice early so real devices constrain the design.
Security, consistent backups and policy enforcement start with foundation, not a final
hardening phase. Build plans must reference requirement IDs in section 13.

| Gate | Included | Exit evidence |
|---|---|---|
| G0 — prove the risky path | Deployment/client profiles, shared fixtures and CPU direct-play baseline; conversion, accelerator and subtitle spikes can proceed independently for their later features | Recorded capabilities/failures per profile; M1 is not blocked by unneeded conversion/HDR work; no private-host dependency or production cut-over |
| G1 — local vertical slice | First prove M1; next focus on Movies, TV and separate Anime libraries plus layout C browse/detail/play. Foundation, accounts/profiles, metadata/progress and broader direct/remux/SDR-conversion features retain their independent work packages | A second tester deploys fresh and restores a backup; required G1 features and playback cases work end to end with permissions enforced |
| G2 — deployment and client compatibility | Anime identity/preferences, tested subtitles, accelerator admission/fallback, seek recovery, TV controls on agreed devices and diagnostics | Mandatory matrix passes across at least two independently configured installations, including generic non-Unraid and CPU-only coverage; one real long-form session per target client; blocked cells accepted or fixed |
| G3 — invited remote trial | Google/Microsoft sign-in + invites, remote HTTPS/proxy, upload caps, session revocation, QR pairing if TV trial needs it, operational runbooks | Security/failure tests pass; 14-day trials on independently configured installations with local and invited remote users meet section 13 targets |
| G4 — expansion | Optional Plex/Jellyfin imports, HDR/DV playback and tone mapping, remaining providers, layouts A/B, additional media types, ABR, trickplay/intro detection, external sync, native Android, offline | Each feature has its own approved contract, fixtures and acceptance evidence before being advertised |

For migration trials, the existing media server stays running and each installation's
administrator chooses its own cut-over after G3 evidence. New installations have no
Plex/Jellyfin prerequisite. A failure in a mandatory playback journey pauses that
installation's cut-over and blocks release claims for the affected support profile.

### 9.1 Feature placement ledger

| Feature from original vision | Proposed placement / boundary |
|---|---|
| Movies, TV and separate Anime library options; basic metadata; local accounts/profiles; layout C; manual quality with bounded SDR software conversion | G1 baseline; library/interface focus follows M1, with other work progressing in parallel |
| Advanced anime matching/order; core providers; audio/ASS/PGS; GPU; TV navigation | G2, with unproven conversions visibly blocked |
| Plex/Jellyfin watch-state migration | Later optional G4; core data compatibility is designed/tested earlier under 10.3, without blocking basic development |
| HDR/DV playback and tone mapping | G4 later expansion; not a first-playback or initial library/interface prerequisite |
| Google/Microsoft + invites; HTTPS remote delivery; QR pairing; bandwidth policy | G3, or earlier when required for real-device testing |
| Apple/Facebook/generic OIDC; admin-approved signup requests; built-in ACME | G4; local fallback and secure remote deployment already exist |
| Music, photos, home videos | Retained G4 scope; need domain/player contracts before implementation |
| Layouts A/B, nested collections, smart playlists, advanced shared rows | G4; basic authorised rows/watchlist remain part of baseline UI |
| Missing episode display, richer ratings/artwork providers, alternative order UI | G4 except ordering needed to identify the G2 anime fixture set correctly |
| Seamless ABR, seek thumbnails, automatic intro/credits, speculative next preload | G4 optimisations; manual seeks/next episode work earlier |
| Native Android phone/TV (Kotlin/Media3), offline originals/transcodes/batches | G4 separate milestones; browser offline capability is independently proven |
| ARM64 container images and other server architectures | Optional later G4 qualification; initial server image is x86-64/amd64 and is not blocked by ARM work |
| AniList/MAL outbound sync | G4 after terms, scopes and mapping verification |

### 9.2 Contracts required before expansion features start

Music needs album/artist/disc/track identity, gapless/queue behaviour and tag precedence.
Photos need EXIF orientation/privacy, thumbnails and supported formats. Home videos
need local titles/dates without forced movie matching. Collections/playlists need
authorisation for every member and cycle prevention for nesting.

Intro/credits detection needs confidence, manual override, false-positive evaluation
and background budgets; fingerprint agreement is evidence, not proof of an intro.
Skip ranges cannot hide unwatched narrative without an undo action. Trickplay must
remain within resource/cache budgets and use the same item permissions.

Offline needs per-device storage quotas, job cancellation, partial download recovery,
manifest integrity, track selection, app upgrade handling and a clear revocation model.
Already downloaded unencrypted files cannot be remotely erased with certainty. PWA
storage/eviction/background restrictions need real-platform tests. Do not advertise
offline support simply because a service worker installs successfully.

### 9.3 Goal 1 — reliable playback, with an enabling foundation

The first product goal is reliable playback. A running container, frontend shell or
passing build alone does not achieve it. The following milestones divide G1 into
reviewable work packages; they do not add a new product phase or impose serial task
ordering. Foundation, playback packages and the player can progress in parallel under
section 8. G0 experiments also run alongside them. Remaining release
scope and numeric performance proposals still require maintainer review.

#### M1 — one complete movie in a browser at original quality

This is the first measurable playback milestone, confirmed by Pete. A complete
1080p SDR MP4 movie with H.264 video plays through the real Lisimflix media endpoint and browser
player at its original video and audio quality, without re-encoding or modifying the
source. For this first proof use direct play: select a source whose container, video
and audio are supported by the named browser. Universal codec support is not implied.

Required clients are **Chrome and Firefox on Linux and macOS**: all four
combinations, using the same source movie with recorded browser/OS versions. The
movie's audio must play unchanged on each target; selecting its codec/profile is
part of fixture qualification, not permission to convert an incompatible track.
MKV/remux, HDR and other containers/formats are outside M1. HDR is later expansion.
Chrome and Firefox on Windows are not M1 targets for this proof of concept; they are
required in a later milestone (decided by Pete and Lammy, 2026-10-27; #19).
Windows is expected to work but is unproven until its own runs: Firefox there decodes
H.264 through Media Foundation, a different path from Linux and macOS.

| ID | Pass criterion | Required evidence |
|---|---|---|
| MOVIE-01 | The MP4 movie plays from beginning to end with video and audio on all four targets | Record the shared source identity/duration and each browser/OS version; a full-duration run on each combination reaches the actual end with advancing video and audible, synchronised audio. A short clip or a seek to the last frame is not end-to-end proof. |
| MOVIE-02 | Original encoded video and audio are delivered unchanged | Route is direct play; source codec/profile, resolution, frame rate and selected audio stream are preserved. No video/audio encoder job runs, no lower-bitrate rendition substitutes for the source, and representative HTTP range bytes match the source. Browser decoding for display/audio output is expected. |
| MOVIE-03 | Playback remains stable on a suitable connection | Record host, storage, connection and source bitrate. With enough sustained bandwidth and working storage, the full run has no crash, unhandled playback error or post-start buffering interruption. Record startup time separately; the initial M1 pass does not depend on the provisional p95 benchmarks. |
| MOVIE-04 | Source media remains untouched | Media is mounted read-only and before/after source integrity checks match. Only application-owned config/cache paths may receive writes. |

The test uses a complete movie the tester is authorised to use, with a known duration.
Live server/player/media integration is required: a fixture-only UI, `file://` playback
or opening the source outside Lisimflix cannot pass M1. Record the result with its
limitations; one successful browser/OS run does not pass the other three, and these
four combinations do not certify every movie, codec profile or device.

M1 needs only enough safe runtime, media selection and player functionality to prove
these four outcomes. A controlled local proof can use one authorised test account;
production access remains protected under section 6. A completed foundation checklist,
full scanner, external metadata, multi-user administration, subtitles, watch-progress
persistence, transcoding and polished layouts are not M1 prerequisites. The real
dependencies are the small source/delivery/player contracts and required safety checks.
Other features continue in parallel against their agreed harnesses.

M1 is a subset of G1, not a redefinition of all G1 completion criteria. Remux,
software/hardware conversion, subtitles, seeking, persistent resume, failure recovery
and additional browsers remain the broader FIRSTPLAY and later support-matrix work.
Container-only remux may preserve encoded quality, but it is a separate proof from
the direct-play baseline selected here.

After M1, the next product focus is the library and interface journey: add Movies,
TV or Anime libraries, browse their items and start playback. Anime is independently
configurable for its indexing/providers. This sequencing describes the user-visible
milestones; the library/interface contributors can work in parallel before M1 passes.
Wider format support and HDR do not delay that next library/interface milestone.

#### G1a — internal foundation work package

The foundation provides the deployed runtime for the integrated playback loop.
Playback packages and player work use the small shared contracts and their own
harnesses while this package is unfinished:

| ID | Capability | Acceptance evidence |
|---|---|---|
| BASE-01 | Generic non-root container starts without a GPU or external provider credentials | Fresh documented compose deployment with writable config/cache and read-only media mount; invalid paths/permissions produce actionable startup errors |
| BASE-02 | Go serves the built React application | A browser loads a working application shell and a real server response; no separate frontend development server is required in the deployed image |
| BASE-03 | SQLite initialises and migrates durably | Empty config creates the database; restart retains a non-secret test record and does not reapply a completed migration; migration failure blocks readiness |
| BASE-04 | Health and shutdown are observable | Liveness responds while the process is running; readiness distinguishes usable versus failed initialisation; shutdown exits within the documented grace period |
| BASE-05 | A common check command works locally and in CI | `make check` runs real Go/frontend lint, relevant unit checks and a production web build; failed checks fail the command and CI, with no placeholder always-green jobs |

Bootstrap remains protected under 6.4. The unauthenticated foundation shell exposes no
private library or administrative state. Account protection is required before adding
media browsing; trusted-LAN HTTP is supported under 6.6 and internet access requires
HTTPS. An internal skeleton is not a safe public media server. It needs no complete
metadata provider stack or polished home screen.

#### G1b — integrated playback outcome

Build the smallest usable path from authenticated media selection to playback, using
a read-only sample library, a basic player and persistent progress. File names and
local probe data are sufficient for this proof; optional external metadata, import,
social login, advanced layouts and discovery features cannot block the experiment.
Playback decision/process supervision and the player each have earlier independent
module acceptance under 9.4–9.5; G1b is their integration checkpoint, not a ban on
starting them before G1a is complete.
Existing G1 features remain required before G1 as a whole is declared complete.

| ID | User-visible outcome | Acceptance evidence |
|---|---|---|
| FIRSTPLAY-01 | Select a permitted file and start the correct supported route | Shared direct, remux and bounded SDR software-transcode fixtures play on named baseline clients; route/reasons match PLAY-01, and first-frame results meet agreed PERF-01 conditions |
| FIRSTPLAY-02 | Watch sustained media without unexplained stalls or silent track loss | Each mandatory route completes the 30-minute stability run under PERF-03; a full-duration fixture reaches its end; selected supported audio/text subtitles remain correct |
| FIRSTPLAY-03 | Seek, pause and resume with correct state | Seek forwards/backwards, pause/resume and reopen after client interruption; position, selected tracks and progress satisfy PERF-02/PERF-06 and DATA-01 |
| FIRSTPLAY-04 | Recover or fail clearly when playback cannot continue | Unsupported fixtures, source loss, encoder failure, repeated seeks and excess work produce bounded fallback or a named error; no orphan jobs, endless spinner or modified media |
| FIRSTPLAY-05 | Repeat the result on a fresh baseline setup | Another tester follows generic deployment instructions and reproduces the declared playback cases using shared fixtures without access to a contributor's private server/library |

Supported fixture/client choices and performance conditions are recorded before the
run; removing a failing mandatory case does not count as passing it. Hardware decode,
complex ASS/bitmap rendering, HDR/DV and seamless ABR expand the support matrix through
their own later gates. Passing G1b does not imply universal format/device compatibility.

The implementation plan decomposes MOVIE/BASE/FIRSTPLAY outcomes into files, interfaces,
ordered tasks and concrete tests. This spec defines the acceptance boundary; it does
not prescribe a directory full of empty future modules as a deliverable.

### 9.4 Named feature catalogue

Feature IDs are permanent references, like `SPN-1` in Trillion. Prefixes identify the
area, not the person implementing it. Never renumber/reuse an ID; record supersession
or split a large feature into new IDs. Section numbers may change without breaking
issue links. Existing BASE/FIRSTPLAY/PLAY/PERF and other test IDs remain acceptance
references; the feature catalogue does not rename or duplicate those detailed tests.

The GitHub-label column maps features to the live repository's area labels. Apply
the listed labels to the feature issue; a PR delivering a narrower slice uses the
affected subset. Labels describe work areas, not exclusive file ownership or priority.
Prefixes are not a mechanical label rule: `DATA-3` uses `area:importer`, for example.
`area:platform` covers runtime/deployment/storage work and `area:android` covers native
clients. P0/P1/P2 remain spec priorities; no priority labels are assumed to exist.

**P0** delivers the first reliable-playback slice (G1); **P1** expands compatibility
and the independently deployed/remote trial (G2/G3); **P2** is retained later scope
(G4). Priority is not a serial order or a requirement to finish every lower-numbered
ID first. G0 validates risk across these groups. Gate placement remains the release
scope authority; a shared safety requirement applies whenever its feature is exposed.
All catalogue entries are planned, not implemented. Live assignment/status belongs
in linked GitHub issues, avoiding a second progress ledger in this document.

#### Contracts and platform

| ID | Named feature / outcome | Pri / gate | Acceptance reference | GitHub labels |
|---|---|---|---|---|
| CTR-1 | **Playback contract seed:** shared manifests, typed media/capability/playback/error fixtures and isolated harness entry points | P0 / before consumer integration | 8.2; producer/consumer fixture agreement without full runtime | `area:api`, `area:playback` |
| CTR-2 | **Catalogue and identity contracts:** item/profile/progress/policy shapes and revision rules | P0 / G1 | 3.8, 6.3, 10, 12; matching producer/consumer tests | `area:api`, `area:media`, `area:auth`, `area:userdata` |
| OPS-1 | **Runnable runtime:** non-root image, embedded frontend, configuration and health/shutdown | P0 / G1a | BASE-01, BASE-02, BASE-04 | `area:platform`, `area:api`, `area:web` |
| OPS-2 | **Durable storage:** SQLite migrations, persistence and consistent snapshot/restore | P0 / G1 | BASE-03, OPS-01, 11.3–11.4 | `area:platform`, `area:admin` |
| OPS-3 | **Common checks:** local/CI checks and reproducible builds | P0 / G1a | BASE-05, 13.5 | `area:ci` |
| OPS-4 | **Portable deployment:** generic compose, mount mapping and independently reproduced deployment | P0 / G1; qualification G2 | DEPLOY-01, FIRSTPLAY-05 | `area:platform` |
| OPS-5 | **Operations diagnostics:** redacted logs, session/job/resource and backup health | P1 / G2–G3 | 11.2, PLAY-02; baseline errors required in G1 | `area:admin` |

#### Playback and conversion

| ID | Named feature / outcome | Pri / gate | Acceptance reference | GitHub labels |
|---|---|---|---|---|
| PLY-1 | **Playback decisions:** deterministic route/track selection with policy and reasons | P0 / G1 | PLAY-01; table tests consume probe/capability/policy fixtures | `area:playback` |
| PLY-2 | **Direct media delivery:** authorised range responses and remux playback | P0 / G1 | MOVIE-01–04 for initial direct play; FIRSTPLAY-01–02, 4.1, 12 for wider delivery; real sample bytes in isolated HTTP harness | `area:playback`, `area:transcode` |
| PLY-3 | **Software conversion:** supervised bounded SDR conversion and manual quality | P0 / G1 | FIRSTPLAY-01–02, 4.9–4.10; real ffmpeg sample output | `area:transcode` |
| PLY-4 | **Session lifecycle:** seek generations, pause/expiry/stop and job cleanup | P0 / G1 | FIRSTPLAY-03–04, PLAY-02; isolated supervisor/session tests | `area:playback`, `area:transcode` |
| PLY-5 | **Qualified acceleration:** optional backend probes, admission and bounded fallback | P1 / G2 | 2.3, 4.6, PLAY-02; per-backend evidence | `area:transcode` |
| PLY-6 | **Colour-correct output:** qualified HDR/DV play/conversion or explicit refusal | P2 / G4 | HDR-01; real display/reference evidence | `area:transcode` |
| PLY-7 | **Adaptive variants:** measured aligned-rendition scheduling and switching | P2 / G4 | 4.10; switch latency/resource proof | `area:transcode` |
| PLY-8 | **Remote bandwidth governance:** trusted LAN/WAN classification and upload/profile caps | P1 / G3 | REMOTE-01, 4.3, 6.6 | `area:playback` |
| SUB-1 | **Text subtitle producer:** bounded extraction/conversion to WebVTT, authorised sidecar delivery and asset/timeline/error contract | P0 / G1 | 4.4, FIRSTPLAY-02–03, AUTH-01; real embedded/sidecar fixtures in an isolated backend harness | `area:subtitles` |

#### Libraries, metadata and user data

| ID | Named feature / outcome | Pri / gate | Acceptance reference | GitHub labels |
|---|---|---|---|---|
| LIB-1 | **Read-only discovery/probe:** stable-file checks, bounded jobs and source revisions | P0 / G1 | SAFE-01, 3.6; temporary-directory and probe fixtures | `area:library` |
| LIB-2 | **Resilient catalogue:** resumable scans, watcher reconciliation and offline-root safety | P0 / G1 | SCAN-01, SCALE-01–02, PERF-05; temporary SQLite/root-disconnect fixtures plus actual-scale qualification | `area:library` |
| LIB-3 | **Versions and extras:** stable logical identities, version choice and supplemental media | P1 / G2 | 3.1, 10.1; version/cut reconciliation fixtures | `area:media` |
| LIB-4 | **Import-triggered scans:** optional authenticated Sonarr/Radarr notifications queue scoped, coalesced scans through the standard pipeline | P0 / G1 library milestone | SCAN-02, 3.6; import/upgrade/test, duplicate/batch, path-mapping and missed-event fixtures | `area:library`, `area:api` |
| META-1 | **Basic matching:** NFO/provider hints, candidate matches, locks and permitted primary provider | P0 / G1 | 3.2, 3.7, SCALE-03, PERF-05; local/provider fake fixtures and actual-scale edits; offline playback unaffected | `area:metadata` |
| META-2 | **Anime identity/order:** versioned mappings, alternate numbering and fix-match preservation | P1 / G2 | META-01, 3.7 | `area:metadata` |
| META-3 | **Extended enrichment:** additional approved providers, art/ratings and language variants | P2 / G4 | 3.2, 3.7; provider terms and contract fixtures | `area:metadata` |
| DATA-1 | **Personal progress:** ordered events, watched rules, resume and user overrides | P0 / G1 | DATA-01, PERF-06, 3.8; in-memory/temporary repository harness | `area:userdata` |
| DATA-2 | **Preferences and watchlist:** per-profile/show tracks and personal rows | P0 / G1 | 3.3–3.4, 3.8, 5.2 | `area:userdata` |
| DATA-3 | **Plex migration:** optional read-only dry-run, apply, rerun and guarded undo | P2 / G4 | DATA-01, 3.9, 10.3; source API/snapshot fixtures before optional live test | `area:importer` |
| DATA-4 | **External list sync:** opt-in permitted outbound updates with conflict protection | P2 / G4 | 3.8; mapping, revocation and retry fixtures | `area:userdata`, `area:metadata` |
| DATA-5 | **Jellyfin migration:** optional read-only dry-run, explicit profile/media mapping, rerun and guarded undo | P2 / G4 | DATA-01, 3.9, 10.3; validate source/version fields and source-specific fixtures before live claims | `area:importer` |

#### Accounts, player and discovery

| ID | Named feature / outcome | Pri / gate | Acceptance reference | GitHub labels |
|---|---|---|---|---|
| ACC-1 | **Local accounts/profiles:** protected bootstrap, admin-created username/password users and session lifecycle on trusted-LAN HTTP | P0 / G1 | AUTH-01, LAN-01, 6.3–6.4; isolated auth/repository harness and private-LAN browser test | `area:auth` |
| ACC-2 | **Content policy:** server-enforced account/profile/library/rating rules | P0 / G1 | AUTH-01; shared evaluator with deny/allow fixtures | `area:auth` |
| ACC-3 | **Invites and initial social sign-in:** Google/Microsoft identity/linking and TV pairing | P1 / G3 | REMOTE-01, 6.4–6.5 | `area:auth` |
| ACC-4 | **Additional sign-in:** remaining providers, generic OIDC and approved signup requests | P2 / G4 | 6.1, 6.4; provider-specific security fixtures | `area:auth` |
| UI-1 | **Player surface:** transport controls, media events, tracks, quality and errors | P0 / G1 | MOVIE-01–03 for first integrated movie; FIRSTPLAY-01–04 for broader controls; fixture-backed real-video browser harness for module acceptance | `area:web` |
| UI-2 | **Browse-to-play:** layout C, permitted library/detail selection and resume | P0 / G1 | 5.1–5.2, 5.7, PERF-05, SCALE-02; API-fixture module acceptance then actual-scale API/browser integration | `area:web` |
| UI-3 | **Text subtitle player:** selected sidecars, offsets and seek resynchronisation | P0 / G1 | 4.4, FIRSTPLAY-02–03; real browser rendering; module harness consumes agreed subtitle fixtures, integration consumes SUB-1 | `area:web`, `area:subtitles` |
| UI-4 | **Advanced subtitles:** ASS/fonts and bitmap burn-in/client support gates | P1 / G2 | TEXT-01, 4.11; renderer/device evidence | `area:subtitles`, `area:web`, `area:transcode` |
| UI-5 | **Accessible TV/mobile journeys:** focus, D-pad, keyboard, touch and error states | P1 / G2 | UX-01, 5.8; basic accessible controls required from G1 | `area:web`, `accessibility` |
| UI-6 | **Admin surface:** users/libraries/activity, matching and operational tools | P1 / G2–G3 | 5.3; minimal setup/user controls already required in G1 | `area:web`, `area:admin` |
| UI-7 | **Additional layouts and discovery:** A/B shells, smart/nested collections and advanced rows | P2 / G4 | 5.1–5.2, 9.2 | `area:web`, `area:media` |

#### Later media and clients

| ID | Named feature / outcome | Pri / gate | Acceptance reference | GitHub labels |
|---|---|---|---|---|
| EXT-1 | **Additional media types:** music/photos/home-video domain and player contracts | P2 / G4 | 9.2; separate per-type plans before implementation | `area:media`, `area:library` |
| EXT-2 | **Seek previews and skips:** trickplay, intro/credits confidence and overrides | P2 / G4 | 4.7, 9.2 | `area:trickplay` |
| EXT-3 | **Native Android:** phone/TV client against the public API | P2 / G4 | 9.1; independent client/device qualification | `area:android` |
| EXT-4 | **Offline downloads:** permissions, jobs, integrity, quotas and honest revocation | P2 / G4 | 9.2; actual offline playback on each advertised platform | `area:playback`, `area:web`, `area:android` |
| EXT-5 | **Optional built-in TLS:** supported listener/ACME deployment and recovery | P2 / G4 | 6.6, 11.1; existing HTTPS ingress works earlier | `area:tls` |
| EXT-6 | **Additional server architectures:** optional ARM64 container build and platform qualification | P2 / G4 | 11.1; real architecture startup/playback/restore evidence before support claims; not an initial release dependency | `area:platform`, `area:ci` |

### 9.5 Parallel work breakdown and integration checkpoints

These tracks group compatible work and expose dependencies for 2–3 contributors;
they are not assignments. Contributors self-select individual features rather than
being allocated a track. Each claimed feature records its claimant, reviewer, scope
and target checkpoint before coding. Unclaimed work remains available to anyone.
Use feature plans to keep PRs small.

| Track | Initial feature scope / write boundary | Can start / prove independently | Real integration dependency |
|---|---|---|---|
| A · Platform | CTR-1 seed coordination, OPS-1–4; manifests/build/deploy/bootstrap wiring | Fresh image, DB/health and checks; contract/fixture work starts immediately | Consumer contributions to CTR-1; real player/media adapters for final image smoke |
| B · Playback | PLY-1–4, SUB-1 and local LIB-1 probe boundary; playback/transcode/subtitle backend files | Sample descriptors + owned files + temporary cache; real ffmpeg and isolated HTTP tests after tiny contract/manifests seed | ACC-2 policy adapter, catalogue/source resolver and UI-1/3 for FIRSTPLAY integration |
| C · Player | UI-1–3; player components and client adapter files | Browser fixture adapter and real test video, without Go server, DB, auth service or deployed skeleton | PLY/session API, SUB-1 subtitle producer and ACC-1/2 + DATA-1 adapters for integrated viewing |
| D · Catalogue/identity | LIB-2/4, META-1, ACC-1/2, DATA-1/2 as separately claimed features | Temporary repositories, fake providers and contract fixtures; no running container/player needed | Route/schema migration coordination and real auth/catalogue wiring; LIB-4 uses source-scoped authenticated ingress |
| E · Integration/qualification | FIRSTPLAY evidence, OPS-4 and G2/G3 matrix | Shared fixture manifest, deployment instructions and harness design from day one | Real implementations of the journey being claimed; no mock-only Goal 1 sign-off |

Tracks A–D can be chosen in any order when their actual start dependencies are met;
catalogue/identity work does not wait for three other people or the full skeleton.
Track E can also be claimed for fixture/harness work immediately, with real journey
sign-off waiting for its actual producer/consumer implementations. Each integration
change still has an explicit volunteer owner so it does not become unowned cleanup.

Checkpoint progression is **contract agreed → module accepted → producer/consumer
integrated → user outcome verified**. The first two can happen while the skeleton is
unfinished. Merge small compatible slices into main after review and run real
cross-track smoke checks as adapters become available, rather than saving all merges
for the end. Each PR lists consumer impact, proof and the next integration obligation.

G1 completes when its required feature set and FIRSTPLAY outcomes are integrated;
G2/G3 remain release qualification gates. A feature's standalone milestone is valid
progress even before those aggregate gates pass. Plan reviews reject artificial
whole-phase dependencies, overlapping file ownership and “done” claims based only on
fakes where the requirement calls for actual media, storage, permissions or devices.

## 10. Domain model, persistence and module contracts

### 10.1 Stable identities and relationships

| Entity | Minimum identity / invariant |
|---|---|
| Account, external identity, profile | Account owns identities `(issuer, subject)`; profile owns viewing data; admin role belongs to account |
| Library, root, scan run | Library type/config separate from root availability; scan has revision, counters and terminal status |
| Movie/show/season/episode | Stable local IDs independent of provider IDs, filenames and chosen episode order |
| Media file, playable version, stream | File revision + probe; version/cut maps to logical items; streams retain original index and semantic role |
| Provider match, mapping edge, field lock | Provenance/revision/confidence; explicit overrides survive refresh |
| Progress, history, preference, watchlist | Profile-scoped; manual actions cannot be undone by stale automated events |
| Playback session, job, cache entry | Owned lifecycle, generation, capacity reservation, transform identity and terminal reason |
| Invite, pairing request, session, audit event | Hashed secrets, expiry/revocation and explicit actor/target; secrets excluded from event payloads |
| Import batch, backup, migration | Source/version manifest, status and verifiable recovery boundary |

Names are descriptive, not a prescribed SQL schema. Final table/JSON names belong in
the implementation plan and OpenAPI. IDs are opaque to clients; filesystem paths and
provider ordering are never primary public identity. Store UTC instants; preserve
media times in an explicit unit and distinguish duration from wall time.

### 10.2 Ownership and transactions

Module boundaries in 2.2 are ownership areas, not a demand for fourteen frameworks.
Keep related code together initially; split when a stable interface has real callers.
`library` owns discovery/probe updates; `metadata` owns provider results/mappings;
`userdata` owns progress; `playback` chooses/routes/reserves; `transcode` executes only
validated plans. `api` handles transport/auth context, not independent policy rules.

Use one shared policy evaluator for discovery and media access. Multi-row changes
(identity remap, import application, invite redemption, reservation) are transactional.
Do not hold database transactions across network calls or ffmpeg execution. Background
jobs persist intent/status and have idempotent retry semantics; no external message
broker is required. Crashes between DB update and side effect must reconcile on restart.

SQLite WAL lives on local disk, never SMB/NFS. Keep transactions short, bound write
contention and surface busy/timeout failures; WAL still permits only one writer at a
time. See [SQLite WAL](https://www.sqlite.org/wal.html). Pin/test the chosen SQLite/Go
driver versions. A static Go binary is a build target to verify, not an assumption
independent of the selected driver's C/toolchain requirements.

### 10.3 Import-compatible core data without early import implementation

Plex/Jellyfin import must not become a dependency for M1, the first home screen or
basic library development. Their shared concern now is preserving meaningful data,
not building an importer framework, placeholder endpoints or source-specific tables.
Use core records already needed by the application and allow additive migrations
for future source mappings/import batches.

| Core concern | Required data semantics |
|---|---|
| Stable media identity | Local movie/show/episode/version IDs survive path/provider/order changes; provider IDs retain namespaces and provenance. Future source item IDs are additionally scoped by source system and server instance, never treated as local primary keys. |
| Stable viewing identity | Profile/account identity is separate from display names; imports explicitly map source users to profiles rather than joining by email/name. Native progress is profile + logical item, with source/version duration retained. |
| Progress and watched state | Position/duration have explicit units, watched is separate from resume position, and manual overrides retain their revision/origin. Unknown values remain unknown rather than invented zeros or false watched/unwatched assertions. |
| Time and provenance | Preserve UTC event instants, source-reported time where available, local receipt/update time, origin and revision as distinct meanings. Import arrival cannot automatically make old progress newer than local activity; unknown source dates remain nullable. |
| Safe reconciliation | Item/profile mappings, conflict policy and eventual batch/source record identity support dry-run, idempotent reruns and guarded undo. Ambiguous matches require review; unrelated cuts/episode orders cannot share progress blindly. |

**MIGRATE-01** is early contract/persistence proof: native profile/item/progress records
round-trip without losing identity, units, timestamps, unknown values or manual override
semantics. Minimal neutral fixtures demonstrate that namespaced external IDs can map
to stable local IDs without collisions or overwriting newer local progress. These
are compatibility fixtures, not a functioning Plex/Jellyfin importer. Each source
adapter's extraction details and completeness are planned and tested at G4.

## 11. Deployment, operations and recovery

### 11.1 Supported deployment contract

Initial platform target is **Linux x86-64/amd64 containers** on supported Docker
setups, independently of a specific server/NAS distribution. Pete approved x86-64
first; ARM64 and other server architectures are later nice-to-haves under EXT-6.
Neither multi-architecture builds nor access to ARM hardware blocks initial work.
Linux/Docker is the initial
server target; Windows/macOS are browser clients, with no native server install
required for this release. Generic compose is the primary deployment example;
Unraid is an additional adapter. Local HTTP setup and username/password login work
out of the box; a domain, HTTPS proxy and external identity provider are not initial
requirements. Optional remote access uses configured HTTPS ingress. Other
architectures are not initially supported and remain experimental until separately
qualified. Publish a pinned
image digest, tested runtime/CPU baseline and per-accelerator driver/device requirements;
the CPU-only path has no NVIDIA prerequisite. Go/Node build tools stay out of the
runtime image. Include dependency versions, licenses and an SBOM.

Run with an effective non-root UID/GID able to write `/config` and `/cache` and read
the approved `/media` roots. Explain how PUID/PGID is applied; do not recursively chown
media or assume arbitrary mounts have appropriate ownership. No privileged container,
Docker socket or unnecessary capabilities. GPU access is optional and explicitly
configured using the supported container runtime mechanism.

HTTP listens on 8420 in the container and supports normal use via the documented
host port on a trusted private LAN. Publish an initial URL using the operator's server
IP/hostname and mapped port, without assuming localhost on the viewer's device.
HTTPS ingress is an optional local upgrade and a requirement for remote access.
Later built-in TLS uses a separately configured listener/port and validated ACME
reachability; do not require root simply to bind a privileged port.
Document public base URL, trusted proxies, host/container volume mappings, optional
accelerator setup and secret injection. Samples use placeholders rather than private
hostnames, paths, user IDs or domain names from any contributor's installation.

Startup validates writable paths/free space, media-root visibility, schema support
and required binaries. GPU probe failure degrades supported paths; database migration
failure blocks readiness. Root unavailability marks that library offline. Health
endpoints separate process liveness from usable readiness; metadata outages alone do
not make the server unready. Readiness never leaks secrets or full host paths.

### 11.2 Resource budgets and observability

All queues have capacity/backpressure. Playback has priority over scans, thumbnails,
fingerprints and speculative preloads. Proposed background defaults are 2 probes and
1 enrichment worker per provider; shared provider limits still apply across libraries.
Operators can pause maintenance without interrupting active playback.

Measure active/preparing sessions, route/reason counts, first-frame/seek latency,
rebuffer duration, encoding speed, resource reservations, queue depth/age, scan counts,
provider errors, database contention, cache/free space and backup freshness. Emit
structured bounded logs with request/session/job correlation IDs and redaction.
Diagnostic exports are explicit admin actions, show included fields and omit secrets.

Disk full, provider throttling, probe crash, killed ffmpeg, GPU loss and process restart
have tested behaviour. Existing playable sessions continue where safe; new work is
refused clearly when resources are exhausted. No workload can grow indefinitely just
because the container still has host memory available. Implementation plans name and
test memory/process limits after G0 measurements.

### 11.3 Backup and restore

Use the [SQLite online backup API](https://www.sqlite.org/backup.html) or another
documented consistent snapshot mechanism. A backup includes DB, durable settings,
identity/provider secrets and a manifest with app/schema version and checksums.
Artwork may be rebuildable; identify whether included. Source media and transcode cache
are excluded. Avoid retaining plaintext secrets in temporary staging or public URLs.

Nightly backup retains the last 7 successful snapshots; failed jobs never prune the
last usable one. Proposed RPO is 24 hours, with visible warning after 26 hours without
success. Owner provides a second location outside the container's data disk; a snapshot
on the same failed disk is not disaster recovery. Encryption/key custody is documented
before secrets-bearing backups leave the host.

Restore requires admin reauthentication and explicit confirmation, enters maintenance,
stops jobs, takes a pre-restore snapshot and validates archive paths, checksums, schema
compatibility and database integrity before switching atomically. It never extracts
outside staging or merges old/new database files. On validation failure keep the
current installation. Revoke old sessions after successful restore. A separate clean
instance restore rehearsal proves account access, library identity and progress.
Proposed recovery target: service ready within 30 minutes on the documented sample
dataset, excluding source-media recovery/artwork regeneration; measure before claiming it.

### 11.4 Upgrade, shutdown and rollback

Before migration, create a verified backup. One process performs migrations; startup
refuses a newer schema it cannot understand. Do not promise binary-only downgrade after
a destructive migration: rollback restores a compatible image plus its pre-upgrade
snapshot. Release notes state compatibility and recovery steps.

Shutdown stops admission, flushes accepted progress, cancels background jobs, signals
and reaps children within the configured container grace period, and leaves incomplete
cache entries unpublished. Restart reconciles interrupted jobs/reservations. Secret
rotation and account disabling do not require deleting profiles or watch history.

### 11.5 Dependency distribution

Pin jellyfin-ffmpeg and web/WASM dependencies. Capture the actual ffmpeg build flags,
license texts and corresponding-source/notice requirements before distributing images;
external-process use does not remove obligations for bundled binaries. The
[FFmpeg legal guidance](https://www.ffmpeg.org/legal.html) is the starting reference,
not a claim that the proposed image has already passed a license review.

## 12. Public API and client contract

The OpenAPI document is versioned with the implementation and generates the web client.
No endpoint is considered complete until auth, errors, pagination, resource limits
and an integration example are defined. The following route families are proposed;
exact paths belong in the first API contract PR, not independent client guesses.

| Family | Required operations / rules |
|---|---|
| Setup/auth/accounts/profiles | One-time bootstrap, login/logout, provider callback/link, invite redemption, profile grants, revocation |
| Libraries/items/search | Paginated authorised queries, detail/versions/tracks, artwork, scan jobs/status, admin matching and scoped optional import-notification ingestion |
| User data | Progress events, watched overrides, history/watchlist/preferences, conflict/revision behaviour |
| Playback | Prepare/start, heartbeat/progress, seek/quality generation, stop, authorised range/HLS/subtitle access |
| Admin/jobs/recovery | Activity, safe cancellation, scan/import dry-run/apply, backup/restore, diagnostics |

JSON uses stable field names, opaque IDs, RFC 3339 timestamps and explicitly named
duration/position units. Paginated lists default to 50, max 200 (proposed), with stable
ordering/cursors. Bound request bodies and search lengths. Unknown optional fields
can be ignored; unknown enum values have a client fallback. Breaking changes need
an explicit compatibility/version plan, especially after native clients ship.

Errors include HTTP status, stable code, safe message, request ID and optional retry
information. Distinguish unauthenticated, forbidden/not-found policy, conflict, invalid
input, throttled, unsupported and capacity unavailable. Internal paths, SQL, ffmpeg
commands and secrets are admin diagnostics, not public error payloads.

Mutations with retryable side effects accept idempotency keys scoped to actor/action;
reusing a key with a different request is a conflict. Editable metadata/policy uses
revision preconditions to prevent lost updates. Long operations return a job ID/status
with cancellation and per-item outcomes rather than holding a request open indefinitely.
Range delivery tests include valid partial responses, invalid ranges, cancellation
and source changes; response length/content type must match actual served media.

## 13. Verification matrix and measurable acceptance

### 13.1 Proposed performance targets

PERF-05 and the actual-scale workload below were approved by Pete; the other numeric
targets remain provisional for owner review. None is a claim of achieved performance.
Record host, dataset, network latency/loss, client versions, cache state, resolution,
input bitrate, duration and test count. Measure at least 20 starts/seeks for p95 claims;
small smoke runs report individual samples instead of misleading percentiles.

| ID | Measurement | Proposed target / condition |
|---|---|---|
| PERF-01 | Time from Play to advancing media | p95 ≤3s direct/remux warm; ≤8s transcode; cold extraction shown separately |
| PERF-02 | Seek to requested content | p95 ≤3s cached, ≤8s cold transcode; position within 1s after stabilising |
| PERF-03 | Stability | Rebuffer ≤1% of played time in each 30-minute supported-path run with adequate sustained bandwidth |
| PERF-04 | Capacity | Sustain the declared stream/conversion mix for each published deployment profile for 60 minutes; no unbounded queue/memory growth; deliberate excess load is rejected safely |
| PERF-05 | Interactive browsing and search | p95 ≤1 second from user action to usable, correct results visible in the browser on LAN at the actual-scale baseline below, including during scan and metadata-update workloads; report server/query timing separately |
| PERF-06 | Progress | Resume within 15s after client crash; explicit watched override survives stale/replayed events |
| PERF-07 | Recovery | Backup ≤24h RPO; restore rehearsal ≤30min under documented dataset conditions |

Cold scan/probe/match throughput cannot have an honest target until the actual file
count, storage latency and provider rate limits are recorded. G0 must establish a
baseline per deployment profile and maintainers set acceptable scan durations before
G1 is signed off. An installation operator can set stricter local limits.

Capacity results are attached to a declared profile: CPU/RAM, optional accelerator,
storage, catalogue, network and conversion mix. The legacy three-stream/two-transcode
observation may be a reference workload, never a universal minimum or maximum. Publish
minimum tested resources separately from larger benchmark profiles. Limits are local
configuration, and overload behaviour must pass even on the smallest supported profile.

#### Actual-scale library acceptance

Pete's reported collection is approximately **5,000 movies and 1,000 shows**, including
their actual individual TV/anime episodes. This is the minimum large-library test
workload for the early library/interface milestone, not a minimum installation size.
Record exact film/show/episode/file totals, catalogue/artwork size, host resources and
storage/network topology when qualifying it; show count alone does not measure cost.
TV/Anime are separate configured types; do not duplicate the same series to inflate counts.

Use this real collection or an authorised equivalent at its measured scale. No new
media purchases or extra storage for a larger hypothetical library is required.
Small/generated fixtures still test failures and contracts; they cannot replace the
large-library acceptance run. Larger capacity claims need separate future evidence.

| ID | Workload / outcome | Acceptance evidence |
|---|---|---|
| SCALE-01 | Initial and repeat scans remain bounded and observable | Full baseline scan records elapsed time, file/probe/provider counts, queue/DB waits and errors; repeat scans avoid re-probing unchanged file revisions. Progress/cancellation work and source media stays unchanged. Initial scan-duration target is set after measurement. |
| SCALE-02 | Browse and search stay fast while library work runs | PERF-05 passes independently at idle, during a rescan and during metadata updates on the populated baseline. Include title/alternate-title searches, filters, sorting and deep browsing; results respect permissions and show real selectable items rather than empty acknowledgements/loading placeholders. |
| SCALE-03 | Metadata correction preserves intent without freezing the catalogue | Correct a match, lock an edited field and run a representative bulk update at baseline scale; work reports progress/per-item failures, preserves manual changes through refresh/rescan, and concurrent browse/search meets PERF-05. Long work runs as bounded jobs rather than one unbounded write transaction. |
| SCALE-04 | Performance failures have measured causes | Record representative query plans/durations, database busy/lock time, transaction duration, queue depth, filesystem/provider waits and resource use. Use evidence to distinguish database, scanner, provider and UI costs; no unmeasured database-engine replacement or claim of superiority to another product. |

Measure each workload separately, including warm/cold cache observations and the
documented browser/network conditions. For PERF-05 record enough representative
interactions to report its percentile honestly, not only the fastest query. During
an incomplete initial scan, clearly label partial results; the populated-catalogue
load test must not silently shrink the indexed dataset to meet the timing target.
Profiling adds bounded telemetry, not conversation/viewing data sent to third parties.

### 13.2 Required test dimensions

Maintain `testdata` manifests in the implementation with fixture ID, provenance,
probe summary, intended route, selected tracks, expected appearance and support tier.
Generated fixtures contain original test content. Proprietary specialist samples stay
owner-local unless redistribution rights are explicit. Missing samples are recorded
as missing coverage, not represented by a different codec with a similar extension.

| Dimension | Required cases |
|---|---|
| Delivery/video | Direct ranges, MKV remux, audio-only conversion, hardware/software/hybrid video, H.264 Hi10P, HEVC Main10, AV1/VP9, interlaced and variable framerate |
| Audio/subtitles | Stereo/surround/downmix, commentary, forced/full/SDH/signs, ASS/fonts, PGS/VobSub, no audio, corrupt/missing selected stream |
| Colour | SDR, HDR10, HLG, DV P5/P7/P8 on actual SDR/HDR screens; verified fallback or explicit unsupported |
| Network/resources | 1/3/10 Mbps and LAN, latency/loss/reconnect, constrained CPU/GPU/cache, concurrent sessions, upload saturation |
| Lifecycle/data | Rapid seeks, quality/track switches, pause/expiry, crash/restart, replaced file, offline root, provider 429/outage, partial import/restore failure |
| Security | Cross-account/profile access, PIN bypass, stale signed URLs, revocation, invite/QR replay, CSRF, SSRF/path traversal, protected cache/art/subtitle access |

For each cell, record supported route, owner-accepted limitation or unresolved blocker.
A low-bandwidth case passes by playing an appropriate lower quality or correctly
rejecting an impossible request; it does not require original 4K at 1 Mbps.
Unsupported must never count as successful playback for a mandatory target cell.

### 13.3 Client tiers and evidence

| Tier | Definition | Evidence |
|---|---|---|
| Supported release clients | Maintainer-approved device/browser profiles representing local and remote viewers across independent installations | Real-device start, seek, audio/subtitle, resume and 30-minute playback; TV D-pad journey where TV support is claimed |
| Automated regression | Pinned Playwright Chromium/Firefox/WebKit builds | Repeatable supported-codec scenarios and UI/API assertions; not hardware certification |
| Experimental | Unverified devices, formats or display paths | Labelled as experimental; visible fallback/failure; no cut-over promise |

G0 names exact client/device profiles before support is promised. One household's
devices seed testing without limiting the public support matrix. Native HLS and hls.js
paths are tested independently; see [hls.js supported platforms](https://github.com/video-dev/hls.js).
NVIDIA's [support matrix](https://developer.nvidia.com/video-encode-decode-support-matrix)
guides codec capability, while actual host tests establish throughput. Browser codec
support varies by platform; [Jellyfin's client documentation](https://jellyfin.org/docs/general/clients/codec-support/)
is useful comparative evidence, not a guarantee inherited by this application.

### 13.4 Requirement-to-test ledger

| ID | Requirement | Gate / proving test |
|---|---|---|
| SAFE-01 | Source media stays unchanged | G1/G2: read-only mounts plus before/after source metadata checks during scan/import/play |
| SCAN-01 | Offline roots never mass-retire data | G1: disconnect/empty-root/return fixtures and interrupted scan recovery |
| SCAN-02 | Optional import events safely trigger scoped scans | G1 library milestone: authenticated import/upgrade/test events, deduped season packs, path escapes/mappings, unavailable roots, missed-event reconciliation and receipt-versus-completion status |
| META-01 | Locks/mappings preserve user intent | G2: refresh/order/fix-match with watched episodes and conflicting providers |
| AUTH-01 | Every resource observes policy | G1/G3: swapped account/profile IDs, art/fonts/subtitles/segments and revoked sessions |
| PLAY-01 | Deterministic route and reasons | G1/G2: file × capability × policy table tests plus real subprocess output |
| PLAY-02 | Bounded work and recovery | G2: overload, killed encoder, GPU failure, heartbeat expiry, rapid seek and job reaping |
| TEXT-01 | Selected subtitles remain usable | G2: ASS/bitmap/sidecar with seeks, fonts, fullscreen and PiP/fallback |
| HDR-01 | Correct display or explicit rejection | G4: reference colours and signalling on physical SDR/HDR devices; earlier releases reject unqualified HDR paths explicitly |
| DATA-01 | Progress/import cannot erase newer work | G1: duplicate/out-of-order events and two sessions; G4: source import rerun/conflicts and guarded undo |
| MIGRATE-01 | Core viewing data remains compatible with future imports | G1 contract/persistence tests in 10.3; no live source server or importer implementation required |
| OPS-01 | Recoverable deployment | G1/G3: fresh setup, backup, clean-instance restore, migration failure and documented rollback |
| UX-01 | Accessible viewing journey | G2: D-pad-only and keyboard/screen-reader journeys; all core empty/error/loading states |
| REMOTE-01 | Remote policies and identities safe | G3: invite/link/QR replay, proxy spoofing, bitrate enforcement, outage fallback |
| LAN-01 | Local HTTP and username/password work from first launch | G1: fresh Linux/Docker setup and real login/browse/playback from another LAN device at its private IP/hostname, no domain/proxy/provider; valid HTTP cookies, auth/CSRF checks, and separate HTTPS/proxy-spoofing regression cases |
| ARCH-01 | Initial server release targets x86-64/amd64 | G1: image architecture and actual startup/playback evidence on a named Linux x86-64 host; ARM64 qualification belongs to EXT-6/G4 |
| DEPLOY-01 | Independent installations require no contributor-specific configuration | G1/G2: fresh generic non-Unraid + CPU-only deployment, second independent configuration, remapped-root restore, distinct accounts/keys and absence of private host defaults |
| MOVIE-01–04 | First measurable milestone is one complete original-quality movie in a browser | M1 within G1: the four direct-play criteria in 9.3; real full-duration playback and unchanged source/encoded delivery |
| SCALE-01–04 | Large-library scanning, browsing and metadata management qualify early | First library/interface milestone: actual-scale workload in 13.1 with PERF-05 and measured bottlenecks |
| BASE-01–05 | Internal foundation enables a runnable, testable application | G1a: the five foundation acceptance rows in 9.3; skeleton completion alone is not Goal 1 completion |
| FIRSTPLAY-01–05 | First product goal is demonstrated reliable playback | G1b: the five playback outcomes in 9.3 with declared fixtures, clients and PERF/PLAY/DATA evidence |

### 13.5 PR and trial completion rules

`make check` is the common local/CI gate: lint, unit tests, contract validation when
present and web production build. Local E2E/GPU/device evidence is attached when a PR
touches those paths. Deterministic unit tests do not replace real decoder/display
evidence. Run `make check` locally on the complete code branch before pushing/opening
the PR; after push, GitHub Actions independently runs the same command, including
on the CI-bootstrap PR. New `pull_request` workflows can run from the PR merge
branch before reaching the default branch; require the actual Actions pass and
normal second-member review.
No blanket tests for every trivial helper; test invariants and failures.

A gate records implemented requirement IDs, measured results, fixture/client versions,
known limitations and maintainer sign-off. Release qualification includes 14 consecutive
days on at least two independently administered installations after mandatory journeys
pass, with local and invited remote viewers represented. Include generic non-Unraid
and CPU-only coverage (they may share one installation). No unresolved blocking playback,
data-loss or access-control issue is allowed for a claimed support profile. Record
actual sessions and feedback, not just uptime. Feature additions do not reset an
installation's trial unless they change a mandatory journey; regression in one does.
Maintainers decide release readiness; each operator separately decides migration.

## 14. Decision and challenge register

The following are proposals/questions for Pete and Lammy. No entry is silently treated
as approval. Record decision, owner/date and rationale here when settled.

| ID | Challenge / decision | Proposed position | Blocks |
|---|---|---|---|
| D01 | Must first trial include all media types/providers/layouts? | Movies/TV/anime + layout C first; retain expansion ledger | G1/G2 scope approval |
| D02 | Which x86-64 deployment profiles/operators qualify the first release? | Approved Linux/Docker x86-64 first; ARM64 later optional. zraid is Lammy's reference; select another independent installation with generic Linux/CPU-only coverage and per-profile capacity evidence | Remaining hardware/operator evidence; architecture choice settled |
| D03 | Which client/device profiles qualify beyond M1? | M1 approved: Chrome/Firefox on Linux/macOS; record tested versions. Windows Chrome/Firefox deferred from M1 to a later milestone (2026-10-27, #19). TV/mobile/additional client profiles still need qualification | Wider G2/G3 matrix; M1 platform selection settled |
| D04 | Is automatic ABR required for first remote trial? | Proven manual switching first; ABR after scheduling/alignment spike | ABR plan; possibly G3 if owners disagree |
| D05 | What happens when AV1/DV/HDR cannot convert in real time? | Lower supported output or explicit refusal; no unlimited CPU fallback | Codec matrix and playback budget |
| D06 | Are AniList and mapping sources permitted/sufficient? | Approved by Pete and Lammy, 2026-10-26 (#10); AniDB updated 2026-10-28 (#16): **AniList** direct per install, library-scoped requests only, rate-limited persistent cache, no bulk prefetch or mirroring. **AniDB** follows D15's TMDB model: **each install registers its own AniDB HTTP client** (client name and version), asked for in the initial setup flow and changeable in admin settings, both with short "how to register an AniDB client" steps and a link to AniDB's client registration. Lisimflix ships no shared default client. Until a client is entered, AniDB matching is off and local matching still works (§0.3). Limits per install: at most 1 request per 2 s, heavy per-title caching, ban detected from the XML body with a 24 h+ backoff, no bulk download. **No AniDB cache service** (#16 dropped: AniDB didn't respond); the project's registered `lisimflix` client is for our own development and testing only, as is any internal test cache, and nothing public depends on either. **Jikan** excluded (MAL terms bar scraping); MAL data, if ever needed, uses the official MAL API after its own terms check. **Mapping datasets** (Anime-Lists, Fribb) fetched at runtime only, weekly with a configurable URL, never bundled in the image or repo; fixtures synthetic. Local matching works with no provider (§0.3) | Settled for META-2 planning; outbound sync stays G4 |
| D07 | Initial versus later sign-in rollout | Approved by Pete and Lammy, 2026-10-26 (#9): local username/password accounts first, social providers later; G3 Google/Microsoft and G4 additional providers retain their later gates. Social sign-in needs a public HTTPS domain for redirects, so local-first fits D09 | Initial choice settled; provider qualification later |
| D08 | Profile PIN / child-policy guarantees? | Server-enforced restricted profile grants; account controller remains privileged; deny unrated by default for kids | Policy UX and security tests |
| D09 | Local versus remote ingress/TLS | Approved: local HTTP from first launch with no domain/proxy; Linux/Docker server. Remote access requires HTTPS; proxy configuration/certificate ownership is settled before G3 | Initial local mode settled; remote deployment details later |
| D10 | Plex/Jellyfin source access and import completeness? | Approved as later optional G4 features; validate read-only source/version data before promising complete history. Core compatibility is required under 10.3 now | Source adapters only; not basic development |
| D11 | Which remaining numeric defaults/targets are accepted? | Approved: actual-scale baseline and PERF-05 one-second browse/search target. Discovery has no universal one-minute deadline; remaining budgets/targets await measurement/review | Remaining acceptance sign-off |
| D12 | Backup destination, recovery owner and key custody? | Owner names external copy target and tests restore before remote trial | Disaster-recovery claim |
| D13 | Original mockups missing from tracked repo? | Settled by Pete, 2026-10-27: new versioned layout C mockups and logo in [`docs/design/ui-2/`](../../design/ui-2/README.md) replace the untracked originals; details may still change in the UI-2 plan | Settled; UI-2 plan builds on them |
| D14 | License and specialist fixture redistribution? | Pin builds; document notices/source and sample rights before publication | Image/test artifact distribution |
| D15 | Which provider is primary for movies/TV, and who supplies its API key? | Approved by Pete and Lammy, 2026-10-27 (#73): **TMDB** is the G1 primary provider for movies and TV, used non-commercially with attribution: an About/Credits screen shows the TMDB logo and, prominently, "This product uses TMDB and the TMDB APIs but is not endorsed, certified, or otherwise approved by TMDB" (TMDB API terms §3). **Each install supplies its own TMDB API key**, asked for in the initial setup flow and changeable in admin settings; Lisimflix ships no shared project key. Both places show short "how to get your TMDB API key" steps with a link to TMDB's API key page, next to the key field. Until a key is entered, TMDB matching is off and local NFO matching still works (§0.3). The key stays server-side: never sent to clients or logged. Cached TMDB data and artwork are refreshed or dropped within 6 months, and purged when the key is removed. Per-install rate limit well under TMDB's ~40 requests/s, honouring 429 and `Retry-After`. Anyone selling hosted Lisimflix needs their own TMDB agreement | Settled for META-1 planning; the attribution screen and setup step are UI work |

### 14.1 Resolved documentation contradictions in this draft

- E2E is local, matching `CONTRIBUTING.md`; core CI is `make check`.
- SQLite backup is consistent snapshot or stopped full copy, not a live main-file copy.
- Tone mapping and GPU session budgets are measured paths, not assumed filter names/capacity.
- Offline PWA, universal TV playback and specialist fixture generation are validation
  work, not capabilities guaranteed by framework choice.
- Scope and delivery changes are marked proposed, retaining original features in 9.1.

## 15. Primary references and research limits

References checked for this revision on 2026-10-25. Recheck version-specific behaviour
when implementing. External documents establish constraints, not local host readiness.

| Reference | Used for |
|---|---|
| [SQLite backup API](https://www.sqlite.org/backup.html), [WAL](https://www.sqlite.org/wal.html) | Consistent online backups, local storage and writer contention |
| [Apple HLS authoring](https://developer.apple.com/streaming/hls-authoring-specification-for-apple-devices.html), [hls.js](https://github.com/video-dev/hls.js) | Timeline/rendition interoperability and browser delivery paths |
| [NVIDIA support matrix](https://developer.nvidia.com/video-encode-decode-support-matrix), [FFmpeg filters](https://ffmpeg.org/ffmpeg-filters.html) | Hardware capability boundaries and actual filter selection |
| [JASSUB](https://github.com/ThaUnknown/jassub), [Jellyfin client codec documentation](https://jellyfin.org/docs/general/clients/codec-support/) | Subtitle renderer and platform-specific compatibility constraints |
| [OAuth BCP RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html) | Provider adapter and authentication security requirements |
| [MDN Set-Cookie](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie), [OWASP CSRF](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html) | Explicit HTTP/HTTPS session-cookie modes and browser-request CSRF protection |
| [AniList terms](https://docs.anilist.co/guide/terms-of-use), [limits](https://docs.anilist.co/guide/rate-limiting), [Anime-Lists](https://github.com/Anime-Lists/anime-lists) | Provider-use constraints, backoff and mapping verification |
| [FFmpeg license guidance](https://www.ffmpeg.org/legal.html) | Distribution review for the actual bundled build |
| [Sonarr webhook implementation](https://github.com/Sonarr/Sonarr/blob/develop/src/NzbDrone.Core/Notifications/Webhook/Webhook.cs), [Radarr webhook implementation](https://github.com/Radarr/Radarr/blob/develop/src/NzbDrone.Core/Notifications/Webhook/Webhook.cs) | Optional import/upgrade notification adapter contracts; supported installed versions still require fixtures |

Not performed in this revision: reference-host access, GPU/codec benchmarks, real-device tests,
provider app registration, API credential use, Plex extraction, legal clearance or
application implementation. These remain named evidence gates above; this document
must not be used as a completed test report.

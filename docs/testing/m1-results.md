# M1 playback qualification: results

**Result: M1 passes.** All four required rows pass MOVIE-01–04 with the same
source, each played from the beginning to the actual end without seeking
(spec §9.3, four targets since #38). Chrome and Firefox on Windows are deferred
to a later milestone (decision on #19).

This is the reviewed, redacted summary. Each tester keeps their raw evidence
in their own `.m1-evidence/` (not in Git). Host names, addresses and
credentials are left out.

**Source:** *Tears of Steel* MP4 (`testdata/media/README.md`), source ID
`m1-tos-1080p`, 734.167 s, H.264 Main@L4.0 1920×800 24 fps SDR, MP3 stereo
44.1 kHz, about 6.4 Mb/s, sha256
`c23e19a0fd7075b10f873a3924d6ba8f2bda04184f1d65977d9490038b534738`.

**Build:** image from `main` at `edd5e9e` (#31), `make image`, run with the
`deploy/compose.m1.yml` settings: non-root, read-only root, tmpfs `/tmp`, no
capabilities, `/media` read-only.

## Summary

| # | Browser | OS | Tester | Status |
|---|---|---|---|---|
| 1 | Chrome 156.0.8078.47 | Linux (Arch, kernel 7.2.8) | @lisim | **pass** |
| 2 | Firefox 158.0.1 | Linux (Arch, kernel 7.2.8) | @lisim | **pass** |
| 3 | Chrome 156 | macOS 15.6.1 | @Lammy93 | **pass** |
| 4 | Firefox 158 | macOS 15.6.1 | @Lammy93 | **pass** |

(The macOS rows were called rows 5–6 on #19, before #38 renumbered the
template.)

## Per row

| Field | Rows 1–2 (Linux) | Rows 3–4 (macOS) |
|---|---|---|
| Server | Independent Unraid host; movie on a 5400 rpm array HDD with its page cache dropped before each run | The other tester's Unraid host; movie on NVMe cache storage |
| Connection | Wired LAN, plain HTTP on a LAN address (`isSecureContext` false) | Remote over Tailscale (WireGuard), plain HTTP to a MagicDNS name |
| Start | Row 1: near instant (estimate). Row 2: first frame in 94 ms (measured) | Played after Play; start times not recorded (row 4 ran about 14:38–14:53 AEST by the server log) |
| Interruptions after start | None. Row 2's only `waiting` was before the first frame; Play → `ended` took 734.4 s for a 734.167 s movie | None |
| Audio | Audible and in sync, by ear | Audible and in sync, by ear |
| Actual end reached | Yes, no seeking | Yes, no seeking |
| `canPlayType` (probe-built string) | `probably` | Not recorded on the MacBook. The earlier headless pre-check (Playwright: Chrome 156, Firefox **155**, not the 158 used for row 4) returned `probably` for `video/mp4; codecs="avc1.4D0028, mp3"` |
| Range bytes | 4 × 1 MiB ranges (start, 1 MiB, middle, last 1 MiB): all `206` and byte-identical to the source | 3 × 1 MiB ranges (start, middle, end) over the same path: byte-identical to the source |
| Route / encoder | `route=direct`; the image has no encoder | `route=direct`; the image has no encoder |
| Revocation | Logout, then content: `401` | Re-checked on the same server over the same Tailscale path (2026-10-27): content `206` before logout, logout `204`, then content `401` with no cookie and `401` with the old session cookie |
| Source sha256 after | Unchanged | Unchanged |
| Server state | Healthy, 0 restarts, no OOM, no error log lines | Healthy, 0 restarts, no ERROR/WARN lines; during row 4, 31 samples at 30 s intervals showed memory ≤ 51 MiB and about 520 MB delivered steadily |

## MOVIE criteria

| ID | Result | Evidence |
|---|---|---|
| MOVIE-01 | **pass** | All four rows reached the actual end with video and audio, with no seeking |
| MOVIE-02 | **pass** | `route=direct` on every row; no encoder in the image; range bytes identical to the source |
| MOVIE-03 | **pass** | No crash, unhandled error or post-start buffering on any row, on a slow-disk LAN host and over a remote Tailscale link |
| MOVIE-04 | **pass** | `/media` read-only; source sha256 unchanged after every row |

BASE-01, BASE-02 and BASE-04 are met by the same runs and the #31 image
checks: the non-root generic container starts, Go serves the built app, and
health and shutdown work.

## Limits and follow-ups

- Four combinations, one movie and one codec profile. They don't certify
  other movies, profiles, devices or Windows (spec §9.3).
- Windows is unproven until its own runs. Firefox on Windows decodes H.264
  through a different video stack (#64).
- The server logs no auth or playback events, so start times came from the
  browser or the tester (#37).
- Seen during the runs, and **fixed by #68**: with Docker's default port
  publishing, IPv6 clients arrived through `docker-proxy` and shared one
  rate-limit key (#39). The runs used IPv4; `deploy/compose.m1.yml` now
  publishes IPv4 only.
- One mistyped-password lockout before row 4 was the login limiter working as
  designed, not a playback fault.

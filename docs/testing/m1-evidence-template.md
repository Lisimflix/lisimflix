# M1 playback evidence

M1 passes only when all six rows below pass MOVIE-01–04 with the same source,
each run played from the beginning to the actual end without seeking (spec
§9.3). One row never stands in for another. A failed row stays open and is
rerun after a fix.

Copy this template into `.m1-evidence/` (ignored by Git) to fill in while testing.
Each tester keeps their own raw evidence; only the reviewed summary is shared.
Commit only the reviewed, redacted summary after the real runs, as
`docs(testing): record M1 playback qualification`. Never pre-fill a result.

**Source:** *Tears of Steel* MP4, `testdata/media/README.md`. Anonymised source
ID `m1-tos-1080p`. Duration 734.167 s. H.264 Main@L4.0 1920×800 24 fps SDR,
MP3 stereo 44.1 kHz. Source bitrate about 6.4 Mb/s. sha256
`c23e19a0fd7075b10f873a3924d6ba8f2bda04184f1d65977d9490038b534738`.

## Summary

| # | Browser | OS | Tester | Status |
|---|---|---|---|---|
| 1 | Chrome | Linux | @lisim | not run |
| 2 | Firefox | Linux | @lisim | not run |
| 3 | Chrome | Windows | @Lammy93 | not run |
| 4 | Firefox | Windows | @Lammy93 | not run |
| 5 | Chrome | macOS | @Lammy93 | not run |
| 6 | Firefox | macOS | @Lammy93 | not run |

Status is one of: **not run**, **pass**, **fail** (with the failing MOVIE ID).

## Run record

Copy this block once per row.

### Row N: Browser / OS

| Field | Value |
|---|---|
| Status | not run |
| Image commit / digest | |
| Server host, storage, connection | |
| Client OS and version | |
| Browser and version | |
| Source ID / duration | m1-tos-1080p / 734.167 s |
| Codecs, profile, resolution, frame rate | |
| `canPlayType` for the probe-built string (`video/mp4; codecs="avc1.4D0028, mp3"` for Tears of Steel) | |
| Source bitrate | |
| Source sha256 before | |
| Start time (UTC) | |
| Time to first frame | |
| Start result | |
| Interruptions (buffering after start, errors, crashes) | |
| Audio audible and in sync | |
| Actual end reached, end time (UTC) | |
| Range-byte check (representative ranges match the source) | |
| Route was direct play; no encoder job ran | |
| Source sha256 after | |
| Notes | |

Build the codec string from the probe, not by hand: video `avc1.PPCCLL` from
the H.264 profile and level (Main `4D`, High `64`, Constrained Baseline `42`;
L4.0 is `28`), audio `mp4a.40.2` for AAC-LC (`mp4a.40.5` HE-AAC) or `mp3` for
MP3. Check audio **by ear**: it must be audible and in sync.

**MOVIE-01** plays start to end with video and audio. **MOVIE-02** original
streams delivered unchanged. **MOVIE-03** no crash, unhandled error or
post-start buffering on a suitable connection. **MOVIE-04** source unchanged
and mounted read-only.

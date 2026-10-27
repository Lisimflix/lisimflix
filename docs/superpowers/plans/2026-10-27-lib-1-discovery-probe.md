# LIB-1 Read-only Discovery and Bounded Probe Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Find video files under a media root without changing them, wait until each one is stable, and probe it with ffprobe under fixed limits. Every result is tagged with the source revision it describes.

**Architecture:** Two new packages, using only the standard library. `probe` runs ffprobe with a deadline and an output cap, reading the file through an `os.Root` handle passed as `/dev/fd/3`, and parses its JSON. `library` walks a root read-only, records each file's revision (size + mtime), and runs probe jobs on a fixed number of workers. Duplicate jobs coalesce. Each job checks the file before probing (quiet period) and after (unchanged revision). Nothing is wired into `serve` yet: LIB-2 adds library definitions, storage and scheduling on top.

**Tech Stack:** Go 1.27.3 standard library (`os.Root`, `io/fs`, `os/exec`). ffprobe/ffmpeg only for the opt-in integration test.

**Spec:** [`docs/superpowers/specs/2026-10-24-lisimflix-design.md`](../specs/2026-10-24-lisimflix-design.md): §3.6 (scan lifecycle, probe bounds), §9.4 LIB-1 row, §13.4 SAFE-01.

## Feature header (spec §8.4)

| | |
|---|---|
| Feature ID | **LIB-1**: read-only discovery/probe. P0 / G1. Track B (spec §9.5) |
| Issue | #45 |
| Owner / reviewer | Pete (claimant) / Lammy |
| Acceptance | SAFE-01; §3.6 stable-file check, bounded probe, source revisions. Proven with temporary-directory and probe fixtures |
| File scope | New only: `server/internal/probe/**`, `server/internal/library/**`, one line in `README.md`. No changes to `go.mod`, contracts, existing packages, `Dockerfile` or CI |
| Consumes | Nothing from other features (standard library only) |
| Produces | `probe.Runner`, `probe.Result` (+ stream types), `probe.Parse`; `library.Discover`, `library.File`, `library.Revision`, `library.RevisionOf`, `library.Queue`, `library.NewQueue`, `library.Outcome`, `library.Prober`, `ErrNotStable`/`ErrChanged`/`ErrGone`, `DefaultQuiet`, `DefaultWorkers` |
| Start dependency | None. Can start now |
| Standalone test | `mise exec -- go -C server test -race ./internal/probe/ ./internal/library/`, then `make check` |
| Module proof | Unit tests with temp trees and a fake ffprobe (the test binary), plus `go -C server test -tags integration ./internal/library/` with real ffmpeg/ffprobe (SAFE-01 before/after snapshot) |
| Integration proof (not in this feature) | LIB-2 wires `Discover` + `Queue` into the catalogue and stores `Outcome`s with compare-and-set on `Rev`. The container image needs an ffprobe binary (OPS/PLY-3 decision; the M1 image has none). Record "module accepted" on #45 when this merges; "integrated" when LIB-2's scan uses it |

## Global Constraints

- Go 1.27.3, Node 24.22.1 (`mise.local.toml`). Module `github.com/Lisimflix/lisimflix/server`. `CGO_ENABLED=0`.
- Checks are `make check` (gofmt, vet, `go test -count=1 ./...`, web checks). Run it locally before pushing; Actions runs it on the PR. CI has no ffmpeg, so real-ffprobe tests use the `integration` build tag, as the M1 integration test does.
- **Read-only media (SAFE-01):** nothing in this plan writes, renames, deletes or changes timestamps under a media root. Production code only opens, stats and reads. Tests write only inside `t.TempDir()`.
- **Paths:** every file access goes through `os.Root`, which rejects `..`, absolute paths and symlinks that leave the root (Go `os.Root` docs). Subprocess arguments never pass through a shell.
- **Probe bounds (spec §3.6):** 2 probes at once, 30 s each, 8 MiB of captured JSON. A limit violation is a distinct error (`probe.ErrTimeout`, `probe.ErrOutputTooLarge`), never a truncated result. The admin override is a later UI: the limits are `Runner` fields.
- **Stable files (spec §3.6):** quiet period 30 s. A size/mtime change invalidates a probe.
- No new Go dependencies. No logging in these packages: callers get errors and outcomes.
- Workflow: branch `feat/lib-1-probe`. Never push `main`, never `--no-verify`. PR title `feat(library): read-only discovery and bounded probe (LIB-1)`, template filled, reviewer Lammy, author squash-merges after approval. Agents never merge.

## Decisions taken in this plan

- **Scope follows the spec row, not the old map.** The archived draft's name/NFO parsing (T8–T10) is identity work. It moves to LIB-2/META-1. This plan updates the plans map's LIB-1 row to match.
- **ffprobe reads `/dev/fd/3`.** The runner opens the file through `os.Root` (so escape checks apply to the exact file probed), then passes the open file to ffprobe as fd 3. ffprobe never sees a host path, and a symlink swapped in after the check can't redirect it. Checked locally with FFmpeg n9.0.2: `ffprobe ... /dev/fd/3 3<clip.mp4` probes normally, including an MP4 with its index at the end (the file stays seekable). Works on Linux and macOS (`/dev/fd`). Whether the production container has `/dev/fd` is an integration check for whoever adds ffprobe to the image.
- **Revision = size + mtime (ns)**, as a comparable struct. `playback.Revision` stays as it is. It hashes the same two values plus the movie ID, and the catalogue can keep using it per item.
- **Stable-file check is stateless:** a file is probed only when its mtime is at least the quiet period old, and its revision is the same before and after the probe. Otherwise the outcome is `ErrNotStable` or `ErrChanged` with the latest revision, and the caller re-queues it. A `shortcut:` comment records the clock-skew limit.
- **Symlinks:** a symlinked *file* is followed only while it stays inside the root. Symlinked *folders* are not walked (`fs.WalkDir` doesn't follow them), which also avoids loops. Escaping or dangling links are skipped.
- **A partial walk is an error.** Any unreadable folder fails `Discover`, so LIB-2 never marks files missing after an incomplete enumeration (§3.6).
- **No JSON tags on `probe.Result`.** How results are stored and served is LIB-2/CTR-2's decision (camelCase per the map's delta 9).
- **Skipped names:** hidden files and folders, non-video extensions (which also covers `.part`, `.!qB` and other in-progress downloads), `@eaDir`-style `@` folders, `#recycle`, `$RECYCLE.BIN`, `lost+found`, `System Volume Information`, `Sample(s)`.

## File Structure

```
server/internal/probe/
  probe.go                 Result + stream types, Parse (ffprobe JSON), Runner (bounded ffprobe run)
  probe_test.go            parser tests on recorded fixtures
  runner_test.go           Runner tests; the test binary doubles as a fake ffprobe
  testdata/hevc_hdr10_truehd_pgs_ass.json, h264_aac_movtext.json, hevc_dv_p8_eac3.json
server/internal/library/
  discover.go              Revision, File, Discover, skip rules
  discover_test.go         temp-tree discovery tests
  queue.go                 Prober, Outcome, Queue (coalescing, bounded workers, stable/changed checks)
  queue_test.go            queue tests with a fake Prober
  integration_test.go      //go:build integration: real ffmpeg/ffprobe, SAFE-01 snapshot
README.md                  one line under Checks for the integration test
```

---

### Task 1: `probe` package: parser and bounded runner

Copied from the archived draft's Task 1, with these changes: JSON tags removed from the public types, and the draft's unbounded `Run(ctx, path, file)` replaced by `Runner.Probe` with the §3.6 limits.

**Files:**
- Create: `server/internal/probe/probe.go`
- Create: `server/internal/probe/testdata/hevc_hdr10_truehd_pgs_ass.json`, `h264_aac_movtext.json`, `hevc_dv_p8_eac3.json`
- Test: `server/internal/probe/probe_test.go`, `server/internal/probe/runner_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `probe.Result{Container string; DurationMs, BitRate int64; Video []VideoStream; Audio []AudioStream; Subtitles []SubtitleStream; Chapters []Chapter}`; `HDRType` (`SDR`, `HDR10`, `HDR10Plus`, `HLG`, `DolbyVision`); `SubtitleKind` (`SubText`, `SubImage`, `SubASS`); `probe.Parse(data []byte) (Result, error)`; `probe.Runner{Path string; Timeout time.Duration; MaxOutput int}` with `(Runner) Probe(ctx context.Context, root *os.Root, rel string) (Result, error)`; `ErrTimeout`, `ErrOutputTooLarge`; `DefaultTimeout = 30 * time.Second`, `DefaultMaxOutput = 8 << 20`.

- [ ] **Step 1: Branch**

```bash
git config core.hooksPath   # must print .githooks
git switch main && git pull && git switch -c feat/lib-1-probe
```

- [ ] **Step 2: Add the recorded ffprobe fixtures**

Trimmed `ffprobe -v error -print_format json -show_format -show_streams -show_chapters` outputs:
- a 4K HEVC HDR10 MKV with TrueHD and AC-3, a forced PGS track and an ASS track
- an H.264 MP4 with AAC, mov_text and cover art
- a Dolby Vision profile 8.1 MKV with E-AC-3 and SRT

`server/internal/probe/testdata/hevc_hdr10_truehd_pgs_ass.json`:
```json
{
    "streams": [
        {
            "index": 0,
            "codec_name": "hevc",
            "codec_long_name": "H.265 / HEVC (High Efficiency Video Coding)",
            "profile": "Main 10",
            "codec_type": "video",
            "codec_tag_string": "[0][0][0][0]",
            "codec_tag": "0x0000",
            "width": 3840,
            "height": 2160,
            "coded_width": 3840,
            "coded_height": 2160,
            "has_b_frames": 2,
            "sample_aspect_ratio": "1:1",
            "display_aspect_ratio": "16:9",
            "pix_fmt": "yuv420p10le",
            "level": 153,
            "color_range": "tv",
            "color_space": "bt2020nc",
            "color_transfer": "smpte2084",
            "color_primaries": "bt2020",
            "chroma_location": "left",
            "refs": 1,
            "r_frame_rate": "24000/1001",
            "avg_frame_rate": "24000/1001",
            "time_base": "1/1000",
            "start_pts": 0,
            "start_time": "0.000000",
            "extradata_size": 2484,
            "disposition": {
                "default": 1,
                "dub": 0,
                "original": 0,
                "comment": 0,
                "lyrics": 0,
                "karaoke": 0,
                "forced": 0,
                "hearing_impaired": 0,
                "visual_impaired": 0,
                "clean_effects": 0,
                "attached_pic": 0,
                "timed_thumbnails": 0
            },
            "tags": {
                "BPS": "52034562",
                "DURATION": "02:01:05.024000000"
            },
            "side_data_list": [
                {
                    "side_data_type": "Mastering display metadata",
                    "red_x": "35400/50000",
                    "red_y": "14600/50000",
                    "green_x": "8500/50000",
                    "green_y": "39850/50000",
                    "blue_x": "6550/50000",
                    "blue_y": "2300/50000",
                    "white_point_x": "15635/50000",
                    "white_point_y": "16450/50000",
                    "min_luminance": "50/10000",
                    "max_luminance": "40000000/10000"
                },
                {
                    "side_data_type": "Content light level metadata",
                    "max_content": 1000,
                    "max_average": 400
                }
            ]
        },
        {
            "index": 1,
            "codec_name": "truehd",
            "codec_long_name": "TrueHD",
            "codec_type": "audio",
            "codec_tag_string": "[0][0][0][0]",
            "codec_tag": "0x0000",
            "sample_fmt": "s32",
            "sample_rate": "48000",
            "channels": 8,
            "channel_layout": "7.1",
            "bits_per_sample": 0,
            "bits_per_raw_sample": "24",
            "r_frame_rate": "0/0",
            "avg_frame_rate": "0/0",
            "time_base": "1/1000",
            "disposition": {
                "default": 1,
                "forced": 0,
                "hearing_impaired": 0,
                "attached_pic": 0
            },
            "tags": {
                "language": "eng",
                "title": "TrueHD 7.1 Atmos",
                "BPS": "4523112"
            }
        },
        {
            "index": 2,
            "codec_name": "ac3",
            "codec_long_name": "ATSC A/52A (AC-3)",
            "codec_type": "audio",
            "sample_fmt": "fltp",
            "sample_rate": "48000",
            "channels": 6,
            "channel_layout": "5.1(side)",
            "bits_per_sample": 0,
            "r_frame_rate": "0/0",
            "avg_frame_rate": "0/0",
            "bit_rate": "640000",
            "disposition": {
                "default": 0,
                "forced": 0,
                "hearing_impaired": 0,
                "attached_pic": 0
            },
            "tags": {
                "language": "jpn"
            }
        },
        {
            "index": 3,
            "codec_name": "hdmv_pgs_subtitle",
            "codec_long_name": "HDMV Presentation Graphic Stream subtitles",
            "codec_type": "subtitle",
            "r_frame_rate": "0/0",
            "avg_frame_rate": "0/0",
            "width": 1920,
            "height": 1080,
            "disposition": {
                "default": 0,
                "forced": 1,
                "hearing_impaired": 0,
                "attached_pic": 0
            },
            "tags": {
                "language": "eng",
                "title": "Forced"
            }
        },
        {
            "index": 4,
            "codec_name": "ass",
            "codec_long_name": "ASS (Advanced SSA) subtitle",
            "codec_type": "subtitle",
            "r_frame_rate": "0/0",
            "avg_frame_rate": "0/0",
            "disposition": {
                "default": 1,
                "forced": 0,
                "hearing_impaired": 1,
                "attached_pic": 0
            },
            "tags": {
                "language": "eng",
                "title": "Full [SDH]"
            }
        },
        {
            "index": 5,
            "codec_name": "ttf",
            "codec_long_name": "TrueType font",
            "codec_type": "attachment",
            "r_frame_rate": "0/0",
            "avg_frame_rate": "0/0",
            "disposition": {
                "default": 0,
                "forced": 0,
                "attached_pic": 0
            },
            "tags": {
                "filename": "OpenSans-Semibold.ttf",
                "mimetype": "application/x-truetype-font"
            }
        }
    ],
    "chapters": [
        {
            "id": 1,
            "time_base": "1/1000000000",
            "start": 0,
            "start_time": "0.000000",
            "end": 312000000000,
            "end_time": "312.000000",
            "tags": {
                "title": "Opening"
            }
        },
        {
            "id": 2,
            "time_base": "1/1000000000",
            "start": 312000000000,
            "start_time": "312.000000",
            "end": 7265024000000,
            "end_time": "7265.024000",
            "tags": {
                "title": "Chapter 02"
            }
        }
    ],
    "format": {
        "filename": "/media/movies/Example (2024)/Example (2024) - 2160p.mkv",
        "nb_streams": 6,
        "nb_programs": 0,
        "format_name": "matroska,webm",
        "format_long_name": "Matroska / WebM",
        "start_time": "0.000000",
        "duration": "7265.024000",
        "size": "51234567890",
        "bit_rate": "56417835",
        "probe_score": 100,
        "tags": {
            "title": "Example",
            "ENCODER": "libebml v1.4.4 + libmatroska v1.7.1"
        }
    }
}
```

`server/internal/probe/testdata/h264_aac_movtext.json`:
```json
{
    "streams": [
        {
            "index": 0,
            "codec_name": "h264",
            "codec_long_name": "H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10",
            "profile": "High",
            "codec_type": "video",
            "codec_tag_string": "avc1",
            "codec_tag": "0x31637661",
            "width": 1920,
            "height": 1080,
            "has_b_frames": 2,
            "pix_fmt": "yuv420p",
            "level": 41,
            "color_range": "tv",
            "color_space": "bt709",
            "color_transfer": "bt709",
            "color_primaries": "bt709",
            "chroma_location": "left",
            "is_avc": "true",
            "r_frame_rate": "25/1",
            "avg_frame_rate": "25/1",
            "time_base": "1/12800",
            "duration": "1320.480000",
            "bit_rate": "4800123",
            "bits_per_raw_sample": "8",
            "nb_frames": "33012",
            "disposition": {
                "default": 1,
                "forced": 0,
                "hearing_impaired": 0,
                "attached_pic": 0
            },
            "tags": {
                "language": "und",
                "handler_name": "VideoHandler"
            }
        },
        {
            "index": 1,
            "codec_name": "aac",
            "codec_long_name": "AAC (Advanced Audio Coding)",
            "profile": "LC",
            "codec_type": "audio",
            "codec_tag_string": "mp4a",
            "sample_fmt": "fltp",
            "sample_rate": "48000",
            "channels": 2,
            "channel_layout": "stereo",
            "bits_per_sample": 0,
            "r_frame_rate": "0/0",
            "avg_frame_rate": "0/0",
            "bit_rate": "192000",
            "disposition": {
                "default": 1,
                "forced": 0,
                "hearing_impaired": 0,
                "attached_pic": 0
            },
            "tags": {
                "language": "eng",
                "handler_name": "SoundHandler"
            }
        },
        {
            "index": 2,
            "codec_name": "mov_text",
            "codec_long_name": "MOV text",
            "codec_type": "subtitle",
            "codec_tag_string": "tx3g",
            "r_frame_rate": "0/0",
            "avg_frame_rate": "0/0",
            "bit_rate": "75",
            "disposition": {
                "default": 0,
                "forced": 0,
                "hearing_impaired": 0,
                "attached_pic": 0
            },
            "tags": {
                "language": "eng",
                "handler_name": "SubtitleHandler"
            }
        },
        {
            "index": 3,
            "codec_name": "mjpeg",
            "codec_long_name": "Motion JPEG",
            "profile": "Baseline",
            "codec_type": "video",
            "width": 600,
            "height": 900,
            "pix_fmt": "yuvj420p",
            "level": -99,
            "r_frame_rate": "90000/1",
            "avg_frame_rate": "0/0",
            "disposition": {
                "default": 0,
                "forced": 0,
                "attached_pic": 1
            }
        }
    ],
    "chapters": [],
    "format": {
        "filename": "/media/tv/Example Show (2019)/Season 01/Example Show - S01E02 - Pilot Part 2.mp4",
        "nb_streams": 4,
        "format_name": "mov,mp4,m4a,3gp,3g2,mj2",
        "format_long_name": "QuickTime / MOV",
        "start_time": "0.000000",
        "duration": "1320.512000",
        "size": "812345678",
        "bit_rate": "4921330",
        "probe_score": 100,
        "tags": {
            "major_brand": "isom",
            "encoder": "Lavf61.7.100"
        }
    }
}
```

`server/internal/probe/testdata/hevc_dv_p8_eac3.json`:
```json
{
    "streams": [
        {
            "index": 0,
            "codec_name": "hevc",
            "codec_long_name": "H.265 / HEVC (High Efficiency Video Coding)",
            "profile": "Main 10",
            "codec_type": "video",
            "codec_tag_string": "[0][0][0][0]",
            "width": 3840,
            "height": 1600,
            "pix_fmt": "yuv420p10le",
            "level": 150,
            "color_range": "tv",
            "color_space": "bt2020nc",
            "color_transfer": "smpte2084",
            "color_primaries": "bt2020",
            "r_frame_rate": "24000/1001",
            "avg_frame_rate": "24000/1001",
            "disposition": {
                "default": 1,
                "forced": 0,
                "attached_pic": 0
            },
            "tags": {
                "BPS": "18123456"
            },
            "side_data_list": [
                {
                    "side_data_type": "DOVI configuration record",
                    "dv_version_major": 1,
                    "dv_version_minor": 0,
                    "dv_profile": 8,
                    "dv_level": 6,
                    "rpu_present_flag": 1,
                    "el_present_flag": 0,
                    "bl_present_flag": 1,
                    "dv_bl_signal_compatibility_id": 1,
                    "dv_md_compression": "none"
                }
            ]
        },
        {
            "index": 1,
            "codec_name": "eac3",
            "codec_long_name": "ATSC A/52B (AC-3, E-AC-3)",
            "profile": "Dolby Digital Plus + Dolby Atmos",
            "codec_type": "audio",
            "sample_fmt": "fltp",
            "sample_rate": "48000",
            "channels": 6,
            "channel_layout": "5.1(side)",
            "r_frame_rate": "0/0",
            "avg_frame_rate": "0/0",
            "bit_rate": "768000",
            "disposition": {
                "default": 1,
                "forced": 0,
                "attached_pic": 0
            },
            "tags": {
                "language": "eng",
                "title": "DDP 5.1 Atmos"
            }
        },
        {
            "index": 2,
            "codec_name": "subrip",
            "codec_long_name": "SubRip subtitle",
            "codec_type": "subtitle",
            "r_frame_rate": "0/0",
            "avg_frame_rate": "0/0",
            "disposition": {
                "default": 0,
                "forced": 0,
                "attached_pic": 0
            },
            "tags": {
                "language": "spa"
            }
        }
    ],
    "chapters": [],
    "format": {
        "filename": "/media/movies/Example DV (2023)/Example DV (2023) - 2160p.mkv",
        "nb_streams": 3,
        "format_name": "matroska,webm",
        "format_long_name": "Matroska / WebM",
        "start_time": "0.000000",
        "duration": "5400.000000",
        "size": "13500000000",
        "bit_rate": "20000000",
        "probe_score": 100
    }
}
```

- [ ] **Step 3: Write the failing tests**

`server/internal/probe/probe_test.go`:
```go
package probe

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func parseFixture(t *testing.T, name string) Result {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestParseHEVCHDR10MKV(t *testing.T) {
	res := parseFixture(t, "hevc_hdr10_truehd_pgs_ass.json")
	if res.Container != "matroska,webm" || res.DurationMs != 7265024 || res.BitRate != 56417835 {
		t.Fatalf("format: %+v", res)
	}
	wantVideo := []VideoStream{{
		Index: 0, Codec: "hevc", Profile: "Main 10", Level: 153, Width: 3840, Height: 2160,
		BitDepth: 10, FrameRate: 23.976, PixFmt: "yuv420p10le", ColorTransfer: "smpte2084",
		ColorPrimaries: "bt2020", ColorSpace: "bt2020nc", HDR: HDR10, BitRate: 52034562, Default: true,
	}}
	if !reflect.DeepEqual(res.Video, wantVideo) {
		t.Fatalf("video:\n got %+v\nwant %+v", res.Video, wantVideo)
	}
	wantAudio := []AudioStream{
		{Index: 1, Codec: "truehd", Channels: 8, Layout: "7.1", SampleRate: 48000, BitRate: 4523112,
			Language: "eng", Title: "TrueHD 7.1 Atmos", Default: true},
		{Index: 2, Codec: "ac3", Channels: 6, Layout: "5.1(side)", SampleRate: 48000, BitRate: 640000,
			Language: "jpn"},
	}
	if !reflect.DeepEqual(res.Audio, wantAudio) {
		t.Fatalf("audio:\n got %+v\nwant %+v", res.Audio, wantAudio)
	}
	wantSubs := []SubtitleStream{
		{Index: 3, Codec: "hdmv_pgs_subtitle", Kind: SubImage, Language: "eng", Title: "Forced", Forced: true},
		{Index: 4, Codec: "ass", Kind: SubASS, Language: "eng", Title: "Full [SDH]", Default: true, HearingImpaired: true},
	}
	if !reflect.DeepEqual(res.Subtitles, wantSubs) {
		t.Fatalf("subtitles:\n got %+v\nwant %+v", res.Subtitles, wantSubs)
	}
	wantCh := []Chapter{{StartMs: 0, EndMs: 312000, Title: "Opening"}, {StartMs: 312000, EndMs: 7265024, Title: "Chapter 02"}}
	if !reflect.DeepEqual(res.Chapters, wantCh) {
		t.Fatalf("chapters: %+v", res.Chapters)
	}
}

func TestParseH264MP4SkipsCoverArt(t *testing.T) {
	res := parseFixture(t, "h264_aac_movtext.json")
	if len(res.Video) != 1 {
		t.Fatalf("cover art must be skipped, got %d video streams", len(res.Video))
	}
	v := res.Video[0]
	if v.Codec != "h264" || v.Profile != "High" || v.Level != 41 || v.BitDepth != 8 ||
		v.FrameRate != 25 || v.HDR != SDR || v.Width != 1920 || v.Height != 1080 {
		t.Fatalf("video: %+v", v)
	}
	a := res.Audio[0]
	if a.Codec != "aac" || a.Profile != "LC" || a.Channels != 2 || a.Layout != "stereo" || a.Language != "eng" || !a.Default {
		t.Fatalf("audio: %+v", a)
	}
	s := res.Subtitles[0]
	if s.Codec != "mov_text" || s.Kind != SubText || s.Language != "eng" || s.Default {
		t.Fatalf("subtitle: %+v", s)
	}
	if res.DurationMs != 1320512 || len(res.Chapters) != 0 || res.Chapters == nil {
		t.Fatalf("duration %d chapters %#v", res.DurationMs, res.Chapters)
	}
}

func TestParseDolbyVisionProfile8(t *testing.T) {
	res := parseFixture(t, "hevc_dv_p8_eac3.json")
	v := res.Video[0]
	if v.HDR != DolbyVision || v.DVProfile != 8 || v.DVLevel != 6 || v.DVBLCompatID != 1 || v.BitDepth != 10 {
		t.Fatalf("video: %+v", v)
	}
	if a := res.Audio[0]; a.Profile != "Dolby Digital Plus + Dolby Atmos" || a.Codec != "eac3" {
		t.Fatalf("audio: %+v", a)
	}
	if s := res.Subtitles[0]; s.Kind != SubText || s.Language != "spa" {
		t.Fatalf("subtitle: %+v", s)
	}
}

func TestDetectHDR(t *testing.T) {
	cases := []struct {
		name string
		s    ffStream
		want HDRType
	}{
		{"sdr", ffStream{ColorTransfer: "bt709"}, SDR},
		{"hlg", ffStream{ColorTransfer: "arib-std-b67"}, HLG},
		{"hdr10", ffStream{ColorTransfer: "smpte2084"}, HDR10},
		{"hdr10+", ffStream{ColorTransfer: "smpte2084", SideData: []map[string]any{
			{"side_data_type": "HDR Dynamic Metadata SMPTE2094-40 (HDR10+)"}}}, HDR10Plus},
		{"dv p5 without transfer", ffStream{SideData: []map[string]any{
			{"side_data_type": "DOVI configuration record", "dv_profile": float64(5)}}}, DolbyVision},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _, _, _ := detectHDR(c.s); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestBitDepth(t *testing.T) {
	cases := []struct {
		s    ffStream
		want int
	}{
		{ffStream{PixFmt: "yuv420p"}, 8},
		{ffStream{PixFmt: "yuv420p10le"}, 10},
		{ffStream{PixFmt: "yuv444p12le"}, 12},
		{ffStream{PixFmt: "yuv420p", BitsPerRawSample: "10"}, 10}, // bits_per_raw_sample wins
	}
	for _, c := range cases {
		if got := bitDepth(c.s); got != c.want {
			t.Fatalf("%+v: got %d want %d", c.s, got, c.want)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte(`not json`)); err == nil {
		t.Fatal("want error")
	}
	if _, err := Parse([]byte(`{"streams":[]}`)); err == nil {
		t.Fatal("want error for missing format")
	}
}
```

The runner tests use the test binary as a fake ffprobe: `TestMain` checks `FAKE_FFPROBE` and acts as ffprobe when it's set. Modes:
- `echo`: proves ffprobe gets the open file as `/dev/fd/3`
- `slow`: for the timeout and cancel tests
- `flood`: for the output cap
- `fail`: ffprobe's error is reported

`server/internal/probe/runner_test.go`:
```go
package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for ffprobe: Runner.Path points at
// the binary and FAKE_FFPROBE picks what the fake does.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_FFPROBE"); mode != "" {
		os.Exit(fakeFFprobe(mode))
	}
	os.Exit(m.Run())
}

func fakeFFprobe(mode string) int {
	switch mode {
	case "echo": // proves ffprobe gets the opened file as /dev/fd/3, not a path
		if os.Args[len(os.Args)-1] != "/dev/fd/3" {
			fmt.Fprintln(os.Stderr, "input is not /dev/fd/3:", os.Args[1:])
			return 2
		}
		b, err := io.ReadAll(os.NewFile(3, "input"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Printf(`{"format":{"format_name":%q,"duration":"1.5"}}`, string(b))
	case "slow":
		time.Sleep(time.Minute)
	case "flood":
		os.Stdout.Write(make([]byte, 2<<10))
		time.Sleep(time.Minute)
	case "fail":
		fmt.Fprintln(os.Stderr, "/dev/fd/3: Invalid data found when processing input")
		return 1
	}
	return 0
}

func fakeRunner(t *testing.T, mode string) (Runner, *os.Root) {
	t.Helper()
	t.Setenv("FAKE_FFPROBE", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), []byte("matroska"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return Runner{Path: exe, Timeout: 5 * time.Second}, root
}

func TestRunnerReadsTheOpenedFile(t *testing.T) {
	r, root := fakeRunner(t, "echo")
	res, err := r.Probe(context.Background(), root, "movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if res.Container != "matroska" || res.DurationMs != 1500 {
		t.Fatalf("got %+v", res)
	}
}

func TestRunnerRefusesPathsOutsideTheRoot(t *testing.T) {
	r, root := fakeRunner(t, "echo")
	outside := filepath.Join(t.TempDir(), "secret.mkv")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root.Name(), "link.mkv")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../secret.mkv", "link.mkv", outside} {
		if _, err := r.Probe(context.Background(), root, rel); err == nil {
			t.Errorf("%s: want an error", rel)
		}
	}
}

func TestRunnerTimeout(t *testing.T) {
	r, root := fakeRunner(t, "slow")
	r.Timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := r.Probe(context.Background(), root, "movie.mkv")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("ffprobe was not stopped promptly: %v", time.Since(start))
	}
}

func TestRunnerOutputLimit(t *testing.T) {
	r, root := fakeRunner(t, "flood")
	r.MaxOutput = 1 << 10
	start := time.Now()
	_, err := r.Probe(context.Background(), root, "movie.mkv")
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("want ErrOutputTooLarge, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("ffprobe was not stopped promptly: %v", time.Since(start))
	}
}

func TestRunnerCancelStopsFFprobe(t *testing.T) {
	r, root := fakeRunner(t, "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := r.Probe(ctx, root, "movie.mkv")
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeout) {
		t.Fatalf("want the caller's context error, got %v", err)
	}
}

func TestRunnerReportsFFprobeFailure(t *testing.T) {
	r, root := fakeRunner(t, "fail")
	_, err := r.Probe(context.Background(), root, "movie.mkv")
	if err == nil || !strings.Contains(err.Error(), "Invalid data") {
		t.Fatalf("want ffprobe's message, got %v", err)
	}
}

func TestRunnerReportsMissingBinary(t *testing.T) {
	r, root := fakeRunner(t, "echo")
	r.Path = filepath.Join(t.TempDir(), "no-ffprobe")
	if _, err := r.Probe(context.Background(), root, "movie.mkv"); err == nil {
		t.Fatal("want an error")
	}
}
```

- [ ] **Step 4: Run them and confirm they fail**

Run: `mise exec -- go -C server test ./internal/probe/`
Expected: FAIL to build (`undefined: Parse`, `undefined: Runner`, ...).

- [ ] **Step 5: Implement**

`server/internal/probe/probe.go`:
```go
// Package probe runs ffprobe on a media file, within bounds, and turns its JSON
// output into the stream summary the catalogue stores and playback decides on.
package probe

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Spec §3.6 defaults for one ffprobe run.
const (
	DefaultTimeout   = 30 * time.Second
	DefaultMaxOutput = 8 << 20
)

// Result is the analysed content of one media file.
type Result struct {
	Container  string // ffprobe format_name, e.g. "matroska,webm"
	DurationMs int64
	BitRate    int64 // bits per second, 0 = unknown
	Video      []VideoStream
	Audio      []AudioStream
	Subtitles  []SubtitleStream
	Chapters   []Chapter
}

// HDRType names the dynamic range format of a video stream. "" means SDR.
type HDRType string

const (
	SDR         HDRType = ""
	HDR10       HDRType = "HDR10"
	HDR10Plus   HDRType = "HDR10+"
	HLG         HDRType = "HLG"
	DolbyVision HDRType = "DV" // see DVProfile / DVBLCompatID for the fallback layer
)

// VideoStream describes one video track. Cover-art pictures are not included.
type VideoStream struct {
	Index          int
	Codec          string // ffprobe codec_name: h264, hevc, av1, vp9 ...
	Profile        string // "High", "Main 10" ...
	Level          int    // raw ffprobe value: H.264 41 = 4.1, HEVC 153 = 5.1 (level x 30)
	Width          int
	Height         int
	BitDepth       int
	FrameRate      float64
	PixFmt         string
	ColorTransfer  string
	ColorPrimaries string
	ColorSpace     string
	HDR            HDRType
	DVProfile      int
	DVLevel        int
	DVBLCompatID   int // 0 none (P5), 1 HDR10, 2 SDR, 4 HLG
	BitRate        int64
	Default        bool
}

// AudioStream describes one audio track.
type AudioStream struct {
	Index      int
	Codec      string // aac, ac3, eac3, truehd, dts, flac, opus ...
	Profile    string // "LC", "DTS-HD MA", "Dolby Digital Plus + Dolby Atmos" ...
	Channels   int
	Layout     string // "5.1(side)", "stereo" ...
	SampleRate int
	BitRate    int64
	Language   string // ISO 639-2 as tagged ("eng", "jpn"); "" when untagged/und
	Title      string
	Default    bool
	Forced     bool
}

// SubtitleKind says how a subtitle track has to be rendered.
type SubtitleKind string

const (
	SubText  SubtitleKind = "text"  // SRT, WebVTT, mov_text: convertible to WebVTT
	SubImage SubtitleKind = "image" // PGS, VobSub, DVB: bitmap, render client-side or burn in
	SubASS   SubtitleKind = "ass"   // ASS/SSA: styled, rendered with JASSUB
)

// SubtitleStream describes one subtitle track.
type SubtitleStream struct {
	Index           int
	Codec           string
	Kind            SubtitleKind
	Language        string
	Title           string
	Default         bool
	Forced          bool
	HearingImpaired bool
}

// Chapter is one chapter marker.
type Chapter struct {
	StartMs int64
	EndMs   int64
	Title   string
}

// Runner runs ffprobe with the spec §3.6 limits: a deadline and a cap on the
// JSON it captures. Concurrency is bounded by the caller's worker count.
type Runner struct {
	Path      string        // ffprobe binary
	Timeout   time.Duration // 0 means DefaultTimeout
	MaxOutput int           // captured JSON bytes; 0 means DefaultMaxOutput
}

var (
	ErrTimeout        = errors.New("probe: ffprobe exceeded its time limit")
	ErrOutputTooLarge = errors.New("probe: ffprobe output exceeded its size limit")
)

// Probe runs ffprobe on rel inside root and parses the result. The file is
// opened through root, so paths and symlinks that leave it are refused, and
// ffprobe reads the already-open file as /dev/fd/3: no host path or shell is
// involved. Limit violations return ErrTimeout or ErrOutputTooLarge, never a
// truncated result.
func (r Runner) Probe(ctx context.Context, root *os.Root, rel string) (Result, error) {
	f, err := root.Open(rel)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return Result{}, errors.New("probe: not a regular file")
	}
	runCtx, cancel := context.WithTimeout(ctx, cmp.Or(r.Timeout, DefaultTimeout))
	defer cancel()
	stdout := &capped{max: cmp.Or(r.MaxOutput, DefaultMaxOutput), over: cancel}
	stderr := &capped{max: 4 << 10, over: func() {}}
	cmd := exec.CommandContext(runCtx, r.Path,
		"-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", "-show_chapters",
		"/dev/fd/3")
	cmd.ExtraFiles = []*os.File{f}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second // don't wait on pipes held open after a kill
	err = cmd.Run()
	switch {
	case ctx.Err() != nil:
		return Result{}, ctx.Err()
	case stdout.exceeded:
		return Result{}, ErrOutputTooLarge
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return Result{}, ErrTimeout
	case err != nil:
		return Result{}, fmt.Errorf("probe: ffprobe failed: %w: %s", err, strings.TrimSpace(stderr.buf.String()))
	}
	return Parse(stdout.buf.Bytes())
}

// capped keeps up to max bytes. One byte more calls over (which kills
// ffprobe) and fails the write, so an oversized result is never parsed.
type capped struct {
	buf      bytes.Buffer
	max      int
	exceeded bool
	over     func()
}

func (c *capped) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.max {
		c.exceeded = true
		c.over()
		return 0, ErrOutputTooLarge
	}
	return c.buf.Write(p)
}

type ffOutput struct {
	Streams  []ffStream  `json:"streams"`
	Chapters []ffChapter `json:"chapters"`
	Format   *struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
}

type ffStream struct {
	Index            int               `json:"index"`
	CodecName        string            `json:"codec_name"`
	CodecType        string            `json:"codec_type"`
	Profile          string            `json:"profile"`
	Level            int               `json:"level"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	PixFmt           string            `json:"pix_fmt"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	ColorTransfer    string            `json:"color_transfer"`
	ColorPrimaries   string            `json:"color_primaries"`
	ColorSpace       string            `json:"color_space"`
	RFrameRate       string            `json:"r_frame_rate"`
	AvgFrameRate     string            `json:"avg_frame_rate"`
	Channels         int               `json:"channels"`
	ChannelLayout    string            `json:"channel_layout"`
	SampleRate       string            `json:"sample_rate"`
	BitRate          string            `json:"bit_rate"`
	Disposition      map[string]int    `json:"disposition"`
	Tags             map[string]string `json:"tags"`
	SideData         []map[string]any  `json:"side_data_list"`
}

type ffChapter struct {
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

// Parse converts ffprobe JSON (-show_format -show_streams -show_chapters) into a Result.
func Parse(data []byte) (Result, error) {
	var out ffOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return Result{}, fmt.Errorf("parse ffprobe json: %w", err)
	}
	if out.Format == nil {
		return Result{}, fmt.Errorf("parse ffprobe json: no format section")
	}
	res := Result{
		Container:  out.Format.FormatName,
		DurationMs: secondsToMs(out.Format.Duration),
		BitRate:    atoi64(out.Format.BitRate),
		Video:      []VideoStream{},
		Audio:      []AudioStream{},
		Subtitles:  []SubtitleStream{},
		Chapters:   []Chapter{},
	}
	for _, s := range out.Streams {
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 {
				continue // embedded cover art, not a playable track
			}
			res.Video = append(res.Video, videoStream(s))
		case "audio":
			res.Audio = append(res.Audio, AudioStream{
				Index:      s.Index,
				Codec:      s.CodecName,
				Profile:    s.Profile,
				Channels:   s.Channels,
				Layout:     s.ChannelLayout,
				SampleRate: int(atoi64(s.SampleRate)),
				BitRate:    streamBitRate(s),
				Language:   language(s.Tags),
				Title:      s.Tags["title"],
				Default:    s.Disposition["default"] == 1,
				Forced:     s.Disposition["forced"] == 1,
			})
		case "subtitle":
			res.Subtitles = append(res.Subtitles, SubtitleStream{
				Index:           s.Index,
				Codec:           s.CodecName,
				Kind:            subtitleKind(s.CodecName),
				Language:        language(s.Tags),
				Title:           s.Tags["title"],
				Default:         s.Disposition["default"] == 1,
				Forced:          s.Disposition["forced"] == 1,
				HearingImpaired: s.Disposition["hearing_impaired"] == 1,
			})
		}
	}
	for _, c := range out.Chapters {
		res.Chapters = append(res.Chapters, Chapter{
			StartMs: secondsToMs(c.StartTime),
			EndMs:   secondsToMs(c.EndTime),
			Title:   c.Tags["title"],
		})
	}
	return res, nil
}

func videoStream(s ffStream) VideoStream {
	v := VideoStream{
		Index:          s.Index,
		Codec:          s.CodecName,
		Profile:        s.Profile,
		Level:          s.Level,
		Width:          s.Width,
		Height:         s.Height,
		BitDepth:       bitDepth(s),
		FrameRate:      frameRate(s),
		PixFmt:         s.PixFmt,
		ColorTransfer:  s.ColorTransfer,
		ColorPrimaries: s.ColorPrimaries,
		ColorSpace:     s.ColorSpace,
		BitRate:        streamBitRate(s),
		Default:        s.Disposition["default"] == 1,
	}
	if v.Level < 0 {
		v.Level = 0
	}
	v.HDR, v.DVProfile, v.DVLevel, v.DVBLCompatID = detectHDR(s)
	return v
}

// detectHDR checks, in order: Dolby Vision configuration record, HDR10+ dynamic
// metadata, PQ transfer (HDR10), HLG transfer. HDR10+ metadata usually only shows
// up per frame, so a stream-level probe often reports HDR10+ files as HDR10;
// a later frame probe can refine this.
func detectHDR(s ffStream) (hdr HDRType, dvProfile, dvLevel, dvCompat int) {
	for _, sd := range s.SideData {
		if t, _ := sd["side_data_type"].(string); t == "DOVI configuration record" {
			return DolbyVision, num(sd["dv_profile"]), num(sd["dv_level"]), num(sd["dv_bl_signal_compatibility_id"])
		}
	}
	for _, sd := range s.SideData {
		if t, _ := sd["side_data_type"].(string); strings.Contains(t, "SMPTE2094-40") || strings.Contains(t, "HDR10+") {
			return HDR10Plus, 0, 0, 0
		}
	}
	switch s.ColorTransfer {
	case "smpte2084":
		return HDR10, 0, 0, 0
	case "arib-std-b67":
		return HLG, 0, 0, 0
	}
	return SDR, 0, 0, 0
}

func bitDepth(s ffStream) int {
	if n := atoi64(s.BitsPerRawSample); n > 0 {
		return int(n)
	}
	p := s.PixFmt
	switch {
	case strings.Contains(p, "p16") || strings.HasSuffix(p, "16le") || strings.HasSuffix(p, "16be"):
		return 16
	case strings.Contains(p, "p12") || strings.HasSuffix(p, "12le") || strings.HasSuffix(p, "12be"):
		return 12
	case strings.Contains(p, "p10") || strings.HasSuffix(p, "10le") || strings.HasSuffix(p, "10be") || p == "p010le":
		return 10
	case p == "":
		return 0
	default:
		return 8
	}
}

func frameRate(s ffStream) float64 {
	if f := ratio(s.AvgFrameRate); f > 0 {
		return f
	}
	return ratio(s.RFrameRate)
}

// ratio parses "24000/1001" and rounds to 3 decimals (23.976).
func ratio(r string) float64 {
	num, den, ok := strings.Cut(r, "/")
	if !ok {
		return 0
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	return math.Round(n/d*1000) / 1000
}

func subtitleKind(codec string) SubtitleKind {
	switch codec {
	case "hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub", "dvb_teletext":
		return SubImage
	case "ass", "ssa":
		return SubASS
	default:
		return SubText
	}
}

func language(tags map[string]string) string {
	l := strings.ToLower(strings.TrimSpace(tags["language"]))
	if l == "und" {
		return ""
	}
	return l
}

// streamBitRate prefers bit_rate, then the Matroska BPS statistics tag.
func streamBitRate(s ffStream) int64 {
	if n := atoi64(s.BitRate); n > 0 {
		return n
	}
	return atoi64(s.Tags["BPS"])
}

func secondsToMs(s string) int64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0
	}
	return int64(math.Round(f * 1000))
}

func atoi64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func num(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		return int(atoi64(x))
	}
	return 0
}
```

- [ ] **Step 6: Run the tests and confirm they pass**

Run: `mise exec -- go -C server test -race -count=1 ./internal/probe/ && mise exec -- go -C server vet ./internal/probe/`
Expected: `ok`. The timeout and cancel tests take about 0.2 s each. If `TestRunnerOutputLimit` takes 5 s, the cap isn't killing ffprobe.

- [ ] **Step 7: Commit**

```bash
git add server/internal/probe
git commit -m "feat(library): bounded ffprobe runner and stream parser (LIB-1)"
```

---

### Task 2: Read-only discovery and source revisions

**Files:**
- Create: `server/internal/library/discover.go`
- Test: `server/internal/library/discover_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `library.Revision{Size, ModTime int64}` (comparable; ModTime is Unix ns); `library.RevisionOf(fs.FileInfo) Revision`; `library.File{Path string; Rev Revision}` (Path is slash-separated, relative to the root); `library.Discover(ctx context.Context, root *os.Root, found func(File) error) error`. Package-internal: `isVideo(name) bool`, `skipDir(name) bool`. Task 3's tests reuse the test helpers `writeTree(t, files...) string` (mtimes set an hour back) and `openRoot(t, dir) *os.Root`.

- [ ] **Step 1: Write the failing tests**

`server/internal/library/discover_test.go`:
```go
package library

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// writeTree creates files (slash paths) under a new temp dir, with mtimes an
// hour old so they count as settled.
func writeTree(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func discoverAll(t *testing.T, root *os.Root) ([]File, error) {
	t.Helper()
	var got []File
	err := Discover(context.Background(), root, func(f File) error {
		got = append(got, f)
		return nil
	})
	return got, err
}

func paths(files []File) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func TestDiscoverFindsOnlyVideoFiles(t *testing.T) {
	dir := writeTree(t,
		"Movies/Alien (1979)/Alien (1979).mkv",
		"Movies/Alien (1979)/Alien (1979).nfo",
		"Movies/Alien (1979)/.Alien (1979).mkv.swp",
		"Movies/Heat (1995)/Heat.MP4",
		"Movies/Heat (1995)/Sample/sample.mkv",
		"TV/Show/Season 01/S01E01.mkv.part",
		"TV/Show/Season 01/S01E02.m2ts",
		".grab/x.mkv",
		"@eaDir/thumb.mkv",
		"#recycle/old.mkv",
	)
	outside := writeTree(t, "secret.mkv")
	if err := os.Symlink("../Heat (1995)/Heat.MP4", filepath.Join(dir, "Movies/Alien (1979)/linked.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.mkv"), filepath.Join(dir, "escape.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../..", filepath.Join(dir, "TV/loop")); err != nil {
		t.Fatal(err)
	}

	got, err := discoverAll(t, openRoot(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Movies/Alien (1979)/Alien (1979).mkv",
		"Movies/Alien (1979)/linked.mp4",
		"Movies/Heat (1995)/Heat.MP4",
		"TV/Show/Season 01/S01E02.m2ts",
	}
	if !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("paths:\n got %q\nwant %q", paths(got), want)
	}
	info, err := os.Stat(filepath.Join(dir, "Movies/Heat (1995)/Heat.MP4"))
	if err != nil {
		t.Fatal(err)
	}
	if got[2].Rev != RevisionOf(info) || got[2].Rev.Size != int64(len("Movies/Heat (1995)/Heat.MP4")) {
		t.Fatalf("revision %+v", got[2].Rev)
	}
}

func TestDiscoverFailsOnUnreadableFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any folder")
	}
	dir := writeTree(t, "a/movie.mkv", "b/movie.mkv")
	if err := os.Chmod(filepath.Join(dir, "b"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "b"), 0o755) })
	if _, err := discoverAll(t, openRoot(t, dir)); err == nil {
		t.Fatal("a partial walk must fail")
	}
}

func TestDiscoverStopsWhenCancelled(t *testing.T) {
	dir := writeTree(t, "a.mkv", "b.mkv")
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	err := Discover(ctx, openRoot(t, dir), func(File) error {
		n++
		cancel()
		return nil
	})
	if err != context.Canceled || n != 1 {
		t.Fatalf("err %v after %d files", err, n)
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `mise exec -- go -C server test ./internal/library/`
Expected: FAIL to build (`undefined: Discover`, `undefined: RevisionOf`, ...).

- [ ] **Step 3: Implement**

`server/internal/library/discover.go`:
```go
// Package library finds video files under a media root and probes them
// read-only (LIB-1, spec §3.6). Library definitions and the catalogue (LIB-2)
// build on it.
package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Revision identifies one version of a source file. A size or mtime change
// makes a new revision and invalidates any probe of the old one.
type Revision struct {
	Size    int64
	ModTime int64 // Unix nanoseconds
}

func RevisionOf(info fs.FileInfo) Revision {
	return Revision{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
}

// File is one discovered video file: its slash-separated path inside the root
// and its revision when it was seen.
type File struct {
	Path string
	Rev  Revision
}

// Discover walks root read-only, in lexical order, and calls found for each
// video file. Symlinked files are followed only while they stay inside root;
// symlinked folders are not walked. Any unreadable folder fails the whole
// walk, so a caller never mistakes a partial walk for a complete one (spec
// §3.6: files go missing only after a successful enumeration).
func Discover(ctx context.Context, root *os.Root, found func(File) error) error {
	fsys := root.FS()
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !isVideo(d.Name()) {
			return nil
		}
		info, err := fs.Stat(fsys, p) // follows symlinks; root refuses ones that leave it
		if err != nil {
			if d.Type()&fs.ModeSymlink != 0 || errors.Is(err, fs.ErrNotExist) {
				return nil // escaping or dangling link, or deleted since the folder was read
			}
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return found(File{Path: p, Rev: RevisionOf(info)})
	})
}

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".wmv": true,
	".ts": true, ".m2ts": true, ".mts": true, ".webm": true, ".mpg": true, ".mpeg": true,
	".flv": true, ".ogv": true, ".3gp": true, ".divx": true, ".asf": true, ".rmvb": true,
	".f4v": true, ".vob": true,
}

// isVideo also excludes hidden files and in-progress downloads such as
// "x.mkv.part" (their extension isn't a video one).
func isVideo(name string) bool {
	return !strings.HasPrefix(name, ".") && videoExts[strings.ToLower(path.Ext(name))]
}

func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") {
		return true // .grab, .Trash-1000, @eaDir (Synology) ...
	}
	switch strings.ToLower(name) {
	case "#recycle", "$recycle.bin", "lost+found", "system volume information", "sample", "samples":
		return true
	}
	return false
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `mise exec -- go -C server test -race -count=1 ./internal/library/`
Expected: `ok`. `TestDiscoverFailsOnUnreadableFolder` skips if you run as root.

- [ ] **Step 5: Commit**

```bash
git add server/internal/library/discover.go server/internal/library/discover_test.go
git commit -m "feat(library): read-only discovery with source revisions (LIB-1)"
```

---

### Task 3: Bounded probe queue with stable-file and revision checks

**Files:**
- Create: `server/internal/library/queue.go`
- Test: `server/internal/library/queue_test.go`

**Interfaces:**
- Consumes: `probe.Result`, and `probe.Runner` as the production `Prober` (Task 1). `Revision`, `RevisionOf`, and the test helpers `writeTree`/`openRoot` (Task 2).
- Produces: `library.Prober` interface `Probe(ctx context.Context, root *os.Root, rel string) (probe.Result, error)`; `library.Outcome{Path string; Rev Revision; Probe probe.Result; Err error}`; `ErrNotStable`, `ErrChanged`, `ErrGone`; `library.NewQueue(root *os.Root, p Prober, quiet time.Duration, done func(Outcome)) *Queue`; `(*Queue) Add(rel string)`; `(*Queue) Run(ctx context.Context, workers int)` (blocks until ctx ends); `DefaultQuiet = 30 * time.Second`, `DefaultWorkers = 2`.
- Contract for LIB-2: `done` runs on worker goroutines, possibly at the same time. On `ErrNotStable`/`ErrChanged`, `Rev` is the latest revision and the caller re-queues later. A stored result may be replaced only by an outcome for the same path whose `Rev` matches the file's current revision (compare-and-set). Cancelled jobs are not reported.

- [ ] **Step 1: Write the failing tests**

`server/internal/library/queue_test.go`:
```go
package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// fakeProber records calls and runs an optional hook inside each probe.
type fakeProber struct {
	calls   atomic.Int32
	running atomic.Int32
	maxRun  atomic.Int32
	hook    func(ctx context.Context, rel string) error
}

func (p *fakeProber) Probe(ctx context.Context, root *os.Root, rel string) (probe.Result, error) {
	p.calls.Add(1)
	n := p.running.Add(1)
	defer p.running.Add(-1)
	for {
		m := p.maxRun.Load()
		if n <= m || p.maxRun.CompareAndSwap(m, n) {
			break
		}
	}
	if p.hook != nil {
		if err := p.hook(ctx, rel); err != nil {
			return probe.Result{}, err
		}
	}
	return probe.Result{Container: "fake:" + rel}, nil
}

// runQueue starts the queue, lets its workers go idle, adds rels, and returns
// outcomes by path once want of them have arrived.
func runQueue(t *testing.T, root *os.Root, p Prober, workers, want int, rels ...string) map[string]Outcome {
	t.Helper()
	var mu sync.Mutex
	got := map[string]Outcome{}
	all := make(chan struct{})
	q := NewQueue(root, p, DefaultQuiet, func(o Outcome) {
		mu.Lock()
		defer mu.Unlock()
		got[o.Path] = o
		if len(got) == want {
			close(all)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx, workers); close(done) }()
	time.Sleep(20 * time.Millisecond) // workers are now waiting for work
	for _, r := range rels {
		q.Add(r)
	}
	select {
	case <-all:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out with %d of %d outcomes", len(got), want)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return got
}

func TestQueueProbesSettledFile(t *testing.T) {
	dir := writeTree(t, "movie.mkv")
	p := &fakeProber{}
	got := runQueue(t, openRoot(t, dir), p, 1, 1, "movie.mkv")
	o := got["movie.mkv"]
	info, _ := os.Stat(filepath.Join(dir, "movie.mkv"))
	if o.Err != nil || o.Probe.Container != "fake:movie.mkv" || o.Rev != RevisionOf(info) {
		t.Fatalf("outcome %+v", o)
	}
}

func TestQueueWaitsForQuietPeriod(t *testing.T) {
	dir := writeTree(t, "movie.mkv")
	now := time.Now()
	if err := os.Chtimes(filepath.Join(dir, "movie.mkv"), now, now); err != nil {
		t.Fatal(err)
	}
	p := &fakeProber{}
	o := runQueue(t, openRoot(t, dir), p, 1, 1, "movie.mkv")["movie.mkv"]
	if !errors.Is(o.Err, ErrNotStable) || o.Rev.ModTime != now.UnixNano() || p.calls.Load() != 0 {
		t.Fatalf("outcome %+v, %d probes", o, p.calls.Load())
	}
}

func TestQueueRejectsResultWhenFileChangesDuringProbe(t *testing.T) {
	dir := writeTree(t, "movie.mkv")
	p := &fakeProber{hook: func(context.Context, string) error {
		f, err := os.OpenFile(filepath.Join(dir, "movie.mkv"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString("more")
		return err
	}}
	o := runQueue(t, openRoot(t, dir), p, 1, 1, "movie.mkv")["movie.mkv"]
	info, _ := os.Stat(filepath.Join(dir, "movie.mkv"))
	if !errors.Is(o.Err, ErrChanged) || o.Rev != RevisionOf(info) || o.Probe.Container != "" {
		t.Fatalf("outcome %+v", o)
	}
}

func TestQueueReportsMissingFileAndProbeErrors(t *testing.T) {
	dir := writeTree(t, "bad.mkv")
	boom := errors.New("ffprobe failed")
	p := &fakeProber{hook: func(context.Context, string) error { return boom }}
	got := runQueue(t, openRoot(t, dir), p, 1, 2, "gone.mkv", "bad.mkv")
	if !errors.Is(got["gone.mkv"].Err, ErrGone) {
		t.Fatalf("gone: %+v", got["gone.mkv"])
	}
	if o := got["bad.mkv"]; !errors.Is(o.Err, boom) || o.Rev.Size == 0 {
		t.Fatalf("bad: %+v", o)
	}
}

func TestQueueCoalescesDuplicates(t *testing.T) {
	dir := writeTree(t, "movie.mkv", "last.mkv")
	p := &fakeProber{}
	finished := make(chan string, 4)
	q := NewQueue(openRoot(t, dir), p, DefaultQuiet, func(o Outcome) { finished <- o.Path })
	for _, r := range []string{"movie.mkv", "movie.mkv", "movie.mkv", "last.mkv"} {
		q.Add(r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx, 1)
	// One worker takes jobs in order, so every earlier job is done once last.mkv is.
	for path := range finished {
		if path == "last.mkv" {
			break
		}
	}
	if n := p.calls.Load(); n != 2 {
		t.Fatalf("%d probes, want 2", n)
	}
}

func TestQueueBoundsConcurrency(t *testing.T) {
	files := []string{"1.mkv", "2.mkv", "3.mkv", "4.mkv", "5.mkv", "6.mkv"}
	dir := writeTree(t, files...)
	p := &fakeProber{hook: func(context.Context, string) error {
		time.Sleep(20 * time.Millisecond)
		return nil
	}}
	got := runQueue(t, openRoot(t, dir), p, 2, len(files), files...)
	if len(got) != 6 || p.maxRun.Load() != 2 {
		t.Fatalf("%d outcomes, %d probes at once (want 6, 2)", len(got), p.maxRun.Load())
	}
}

func TestQueueCancelStopsProbesAndDropsTheirResults(t *testing.T) {
	dir := writeTree(t, "movie.mkv")
	started := make(chan struct{})
	p := &fakeProber{hook: func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	var reported atomic.Int32
	q := NewQueue(openRoot(t, dir), p, DefaultQuiet, func(Outcome) { reported.Add(1) })
	q.Add("movie.mkv")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx, 2); close(done) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if reported.Load() != 0 {
		t.Fatal("a cancelled job was reported")
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `mise exec -- go -C server test ./internal/library/`
Expected: FAIL to build (`undefined: NewQueue`, `undefined: Outcome`, ...).

- [ ] **Step 3: Implement**

`server/internal/library/queue.go`:
```go
package library

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// Spec §3.6 defaults.
const (
	DefaultQuiet   = 30 * time.Second // a file must be unchanged this long before it is probed
	DefaultWorkers = 2                // probes at once
)

// Prober probes one file inside a root. probe.Runner implements it.
type Prober interface {
	Probe(ctx context.Context, root *os.Root, rel string) (probe.Result, error)
}

var (
	ErrNotStable = errors.New("library: file changed within the quiet period")
	ErrChanged   = errors.New("library: file changed while it was probed")
	ErrGone      = errors.New("library: file is no longer available")
)

// Outcome is the result of one probe job. Rev is the revision the result
// belongs to; a consumer must not let it overwrite data from a newer revision.
// On ErrNotStable and ErrChanged, Rev is the file's latest revision and the
// caller queues the file again later.
type Outcome struct {
	Path  string
	Rev   Revision
	Probe probe.Result
	Err   error
}

// Queue probes files with a fixed number of workers. Adding a path that is
// already waiting does nothing, so duplicate work coalesces.
type Queue struct {
	root   *os.Root
	prober Prober
	quiet  time.Duration
	done   func(Outcome)

	mu      sync.Mutex
	pending map[string]bool
	order   []string
	wake    chan struct{}
}

// NewQueue returns a queue over root. done is called from worker goroutines,
// possibly at the same time, once per finished job.
func NewQueue(root *os.Root, p Prober, quiet time.Duration, done func(Outcome)) *Queue {
	return &Queue{root: root, prober: p, quiet: quiet, done: done,
		pending: map[string]bool{}, wake: make(chan struct{}, 1)}
}

// Add queues rel for probing unless it is already waiting.
func (q *Queue) Add(rel string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending[rel] {
		return
	}
	q.pending[rel] = true
	q.order = append(q.order, rel)
	q.signal()
}

func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *Queue) next() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.order) == 0 {
		return "", false
	}
	rel := q.order[0]
	q.order = q.order[1:]
	delete(q.pending, rel)
	if len(q.order) > 0 {
		q.signal() // wake another idle worker
	}
	return rel, true
}

// Run probes queued files with the given number of workers until ctx ends.
// Cancelling stops running probes; jobs already finished have been reported,
// and cancelled ones are not.
func (q *Queue) Run(ctx context.Context, workers int) {
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for ctx.Err() == nil {
				rel, ok := q.next()
				if !ok {
					select {
					case <-ctx.Done():
					case <-q.wake:
					}
					continue
				}
				if out := q.job(ctx, rel); ctx.Err() == nil {
					q.done(out)
				}
			}
		})
	}
	wg.Wait()
}

// job checks the file has been quiet, probes it, then checks it again so a
// result never describes a different revision from the one it reports.
func (q *Queue) job(ctx context.Context, rel string) Outcome {
	before, err := q.stat(rel)
	if err != nil {
		return Outcome{Path: rel, Err: err}
	}
	// shortcut: quiet is judged from mtime alone, so a source whose clock runs
	// behind can look settled early; the re-check after probing still catches
	// a file that grows meanwhile. Track first-seen times if that shows up.
	if time.Since(time.Unix(0, before.ModTime)) < q.quiet {
		return Outcome{Path: rel, Rev: before, Err: ErrNotStable}
	}
	res, err := q.prober.Probe(ctx, q.root, rel)
	after, statErr := q.stat(rel)
	switch {
	case statErr != nil:
		return Outcome{Path: rel, Err: statErr}
	case after != before:
		return Outcome{Path: rel, Rev: after, Err: ErrChanged}
	case err != nil:
		return Outcome{Path: rel, Rev: before, Err: err}
	}
	return Outcome{Path: rel, Rev: before, Probe: res}
}

func (q *Queue) stat(rel string) (Revision, error) {
	info, err := q.root.Stat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return Revision{}, ErrGone
	}
	return RevisionOf(info), nil
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `mise exec -- go -C server test -race -count=5 ./internal/library/`
Expected: `ok` on all five runs (the queue tests use goroutines; five runs shake out flakes).

- [ ] **Step 5: Commit**

```bash
git add server/internal/library/queue.go server/internal/library/queue_test.go
git commit -m "feat(library): bounded probe queue with stable-file and revision checks (LIB-1)"
```

---

### Task 4: Real-ffprobe SAFE-01 test, README, PR

**Files:**
- Create: `server/internal/library/integration_test.go`
- Modify: `README.md` (Checks section, after the `make check` paragraph)

**Interfaces:**
- Consumes: everything above, plus `ffmpeg` and `ffprobe` on `PATH`.
- Produces: nothing new.

- [ ] **Step 1: Write the integration test**

This makes two real clips. One is an hour old and gets probed. The other was just written and must wait for the quiet period. The test snapshots size, mtime, mode and SHA-256 of every source file before and after.

`server/internal/library/integration_test.go`:
```go
//go:build integration

package library

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// Run on request: go -C server test -tags integration ./internal/library/
// Needs ffmpeg and ffprobe on PATH.
func TestDiscoverAndProbeRealFilesReadOnly(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("ffprobe is not on PATH")
	}
	dir := t.TempDir()
	for _, name := range []string{"Movies/Clip (2026)/Clip (2026).mp4", "Movies/Late (2026)/Late (2026).mkv"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		gen := exec.Command("ffmpeg", "-v", "error",
			"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2",
			"-metadata:s:a:0", "language=eng", p)
		if out, err := gen.CombinedOutput(); err != nil {
			t.Fatalf("generate %s: %v\n%s", name, err, out)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "Movies/Clip (2026)/Clip (2026).mp4"), old, old); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)

	root := openRoot(t, dir)
	got := map[string]Outcome{}
	done := make(chan Outcome)
	q := NewQueue(root, probe.Runner{Path: ffprobe}, DefaultQuiet, func(o Outcome) { done <- o })
	err = Discover(context.Background(), root, func(f File) error { q.Add(f.Path); return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx, DefaultWorkers)
	for range 2 {
		select {
		case o := <-done:
			got[o.Path] = o
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for probes")
		}
	}

	clip := got["Movies/Clip (2026)/Clip (2026).mp4"]
	if clip.Err != nil {
		t.Fatal(clip.Err)
	}
	v, a := clip.Probe.Video, clip.Probe.Audio
	if len(v) != 1 || v[0].Codec != "h264" || v[0].Width != 320 || v[0].Height != 240 || v[0].BitDepth != 8 || v[0].FrameRate != 25 {
		t.Fatalf("video: %+v", v)
	}
	if len(a) != 1 || a[0].Codec != "aac" || a[0].Channels != 2 || a[0].Language != "eng" {
		t.Fatalf("audio: %+v", a)
	}
	if d := clip.Probe.DurationMs; d < 1900 || d > 2200 {
		t.Fatalf("duration %d ms", d)
	}
	if late := got["Movies/Late (2026)/Late (2026).mkv"]; late.Err != ErrNotStable {
		t.Fatalf("a just-written file must wait for the quiet period: %+v", late)
	}
	// SAFE-01: discovery and probing leave every source byte and timestamp alone.
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("source tree changed:\nbefore %v\n after %v", before, after)
	}
}

// snapshot records each file's size, mtime, mode and SHA-256 under dir.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[p] = fmt.Sprintf("%v %v %d %x", info.Mode(), info.ModTime(), info.Size(), sha256.Sum256(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
```

- [ ] **Step 2: Run it**

Run: `mise exec -- go -C server test -race -count=1 -tags integration -run Real ./internal/library/`
Expected: `ok`. If it fails with `ffprobe is not on PATH`, install FFmpeg (Arch `pacman -S ffmpeg`, Debian/Ubuntu `apt install ffmpeg`, macOS `brew install ffmpeg`).

- [ ] **Step 3: Add the README line**

In `README.md`, after the paragraph ending "...after changing `docs/contracts/m1-openapi.json`.", add:

```markdown
Tests that need real ffmpeg/ffprobe use the `integration` build tag and run only on request,
e.g. `go -C server test -tags integration ./internal/library/` (needs both on `PATH`).
```

- [ ] **Step 4: Run the full check**

```bash
npm --prefix web ci
mise exec -- make check
```

Expected: exit 0. Record the result in the PR.

- [ ] **Step 5: Commit, push and open the PR**

```bash
git add server/internal/library/integration_test.go README.md
git commit -m "test(library): real-ffprobe discovery and SAFE-01 check (LIB-1)"
git push -u origin feat/lib-1-probe
gh pr create --title "feat(library): read-only discovery and bounded probe (LIB-1)" --reviewer Lammy93 --label area:library
```

PR body (template):
- **What changed:** the two packages.
- **Source:** LIB-1, SAFE-01, spec §3.6. Write "Part of #45", not a closing keyword: #45 stays open until LIB-2 integrates this (spec §8.4 records both stages).
- **How it was tested:** the local `make check` result, the integration test result (with the ffprobe version), and the Actions result. This PR proves **module accepted** only. Integration is LIB-2.
- Tick the README box.

---

## Done when

- `make check` passes locally and on Actions; the integration test passes with real ffprobe.
- Lammy approves; Pete squash-merges; #45 records "module accepted" and links the PR.
- LIB-2's plan can use `Discover`, `NewQueue`/`Outcome` and `probe.Runner` as described in Task 3's contract.

## Self-review

- **Spec coverage:**
  - §3.6 read-only discovery: Task 2.
  - stable-file check / quiet period: Task 3 `ErrNotStable`.
  - size/mtime change invalidates the probe: Task 3 `ErrChanged`.
  - jobs carry a source revision: `Outcome.Rev`.
  - duplicate work coalesces: `Add`.
  - cancel preserves completed work and stops owned probes: `Run`, and `exec.CommandContext` in `Runner`.
  - bounded probes (2 / 30 s / 8 MiB, visible failures): `DefaultWorkers` plus the `Runner` limits and errors.
  - paths stay in roots, symlink escapes rejected: `os.Root` in Tasks 1 and 2.
  - no shell: `exec.Command` argv.
  - SAFE-01 before/after check: Task 4.
- **Deferred to LIB-2 and named in #45:**
  - "stale results cannot overwrite newer data" needs storage (compare-and-set on `Rev`)
  - resumable scans, watcher, schedules, offline-root states, status reasons
- **Tests bite (checked before writing this plan).** Each of these mutations fails at least one test:
  - removing the quiet check
  - removing the post-probe revision check
  - removing coalescing
  - reporting cancelled jobs
  - swallowing an unreadable folder
  - removing the skip-dir rules
  - failing the walk on an escaping symlink
  - not killing ffprobe when its output overflows
  - passing a path instead of `/dev/fd/3`
  - reporting a caller cancel as a timeout
- **One mutation has no deterministic test:** removing the extra wake-up in `next()`. The queue still works but can leave a worker idle during a burst of adds.
- **Built from this plan's code before writing it:** `go vet`, gofmt, `go test -race -count=5` and the integration test (FFmpeg n9.0.2, Arch) all pass on top of `main` at `96e2c51`.
- **Placeholders:** none. Every code step is the whole file.
- **Type consistency:** `Prober.Probe(ctx, *os.Root, string) (probe.Result, error)` matches `probe.Runner.Probe`. `Outcome.Rev`, `RevisionOf` and `File.Rev` are the same `Revision` type.

# M1 test media

No media is stored in this repository. Testers download or build the files
below themselves, keep them outside the checkout, and mount them read-only.
Private evidence goes in `.m1-evidence/` (ignored), never in Git.

## The M1 source: *Tears of Steel*

The M1 movie is *Tears of Steel* (Blender Foundation, 2012), the complete
12:14 film. It is an open movie, so every tester can use the same file.

**Licence and credit.** The film, including its audio track, is licensed
[Creative Commons Attribution 3.0](https://creativecommons.org/licenses/by/3.0/)
([about](https://mango.blender.org/about/), [sharing](https://mango.blender.org/sharing/)).
The required credit is:

> (CC) Blender Foundation | mango.blender.org

Blender allows sharing and showing it "for as long you keep the credit scroll
included", so always use the complete film, credits and all. Our MP4 is a
container-only remux of Blender's file (below), with no change to the video or
audio streams. The separately released soundtrack files are CC BY-ND 3.0
([copyright.txt](https://download.blender.org/demo/movies/ToS/copyright.txt));
they are not used here.

| | |
|---|---|
| Container | MP4, `moov` before `mdat` (faststart) |
| Video | H.264 Main@L4.0, 1920×800 (2.40:1 1080p), 24 fps, yuv420p 8-bit, about 6.2 Mb/s |
| Colour | Transfer, primaries and matrix are unspecified: SDR by default, not HDR |
| Audio | MP3 stereo, 44.1 kHz, 192 kb/s; plays in MP4 in Chrome and Firefox |
| Duration | 734.167 s |
| Size | 583,770,545 bytes |
| sha256 | `c23e19a0fd7075b10f873a3924d6ba8f2bda04184f1d65977d9490038b534738` |

Blender publishes the 1080p film only as a QuickTime `.mov`. The M1 file is a
container-only remux of it, with no re-encoding: the video and audio streams
are bit-identical to the original (stream MD5s
`21f4307aeedb090dcc05cf60365e922d` and `9fddbf1e9ee1ece2f84d7b9969d2b07f`).

### Build it

```sh
mkdir -p ~/lisimflix-media && cd ~/lisimflix-media
curl -LO https://download.blender.org/demo/movies/ToS/tears_of_steel_1080p.mov.zip
echo "d87a41de040d3814dbde143e9ab85ef122caf22265f660b0bebf476cd8b357a5  tears_of_steel_1080p.mov.zip" | sha256sum -c
unzip tears_of_steel_1080p.mov.zip   # tears_of_steel_1080p.mov

FFMPEG=linuxserver/ffmpeg@sha256:6547359fac23a2747e606d380e10267b2923a38f68485d9cacefd039e74d3aa8
docker run --rm -v "$PWD":/m --entrypoint ffmpeg $FFMPEG \
  -i /m/tears_of_steel_1080p.mov -map 0:v:0 -map 0:a:0 -c copy \
  -movflags +faststart -fflags +bitexact -map_metadata -1 /m/tears_of_steel_1080p.mp4
echo "c23e19a0fd7075b10f873a3924d6ba8f2bda04184f1d65977d9490038b534738  tears_of_steel_1080p.mp4" | sha256sum -c
```

The remux is reproducible with this pinned image. If your sha256 differs, ask
for a copy of the file instead; the licence allows sharing it.

### Probe record

`lisimflix init --probe-json` takes ffprobe's JSON for the source:

```sh
docker run --rm -v "$PWD":/m:ro --entrypoint ffprobe $FFMPEG \
  -v error -show_format -show_streams -of json /m/tears_of_steel_1080p.mp4 \
  > tears_of_steel_1080p.probe.json
```

Its colour fields are absent. `init` must treat an unspecified transfer with
8-bit 4:2:0 video as SDR, and reject only HDR transfers (`smpte2084`,
`arib-std-b67`), or it will refuse this file.

## The smoke clip

The short smoke run (integration Task 2) uses the first 60 seconds of the
same film, cut without re-encoding:

```sh
docker run --rm -v "$PWD":/m --entrypoint ffmpeg $FFMPEG \
  -i /m/tears_of_steel_1080p.mp4 -map 0:v:0 -map 0:a:0 -t 60 -c copy \
  -movflags +faststart -fflags +bitexact -map_metadata -1 /m/tos_smoke_60s.mp4
echo "f61c1110ddecb769347b42569284887f25e3806fb5787fff02994f6a78629086  tos_smoke_60s.mp4" | sha256sum -c
docker run --rm -v "$PWD":/m:ro --entrypoint ffprobe $FFMPEG \
  -v error -show_format -show_streams -of json /m/tos_smoke_60s.mp4 > tos_smoke_60s.probe.json
```

It is 60.042 s and 41,824,901 bytes. Run the tagged integration test with:

```sh
LISIMFLIX_M1_MOVIE=~/lisimflix-media/tos_smoke_60s.mp4 \
LISIMFLIX_M1_PROBE=~/lisimflix-media/tos_smoke_60s.probe.json \
go -C server test -tags=integration -v ./cmd/lisimflix -run 'TestM1RealHTTPRangeAndRevocation|TestProbeOfTheRealM1Source'
```

It provisions a config with `init`, composes the real modules, signs in,
checks three byte ranges against the file, sends a heartbeat, signs out and
checks the content is refused. It logs the process memory and checks that the
file's sha256 is unchanged. The same command works with the full movie. The
test fails, rather than skips, when the variables are unset; plain
`go test ./...` doesn't build it.

The clip qualifies the harness only. It never replaces a full-length run for
MOVIE-01–04.

## Source integrity (MOVIE-04)

Before and after every run, on the host that serves the file:

```sh
sha256sum ~/lisimflix-media/tears_of_steel_1080p.mp4
```

Both must equal the sha256 above. Mount the media directory read-only
(`/media:ro`).

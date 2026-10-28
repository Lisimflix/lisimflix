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
// Planned: LIB-2 (the catalogue scan probes discovered files with it).
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
	stderr := &capped{max: 4 << 10, truncate: true} // only stdout is a result
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

// capped keeps up to max bytes. With truncate, the rest is dropped. Otherwise
// one byte more calls over (which kills ffprobe) and fails the write, so an
// oversized result is never parsed.
type capped struct {
	buf      bytes.Buffer
	max      int
	truncate bool
	exceeded bool
	over     func()
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); len(p) > room {
		if c.truncate {
			c.buf.Write(p[:room])
			return len(p), nil
		}
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

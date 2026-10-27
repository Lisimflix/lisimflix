package main

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
)

// probed is what init takes from an owner-generated ffprobe record.
type probed struct {
	VideoCodec, AudioCodec string
	Width, Height          int
	DurationMS             int64
	Size                   int64 // bytes, from format.size; 0 if the record has none
}

type probeJSON struct {
	Streams []struct {
		CodecType     string `json:"codec_type"`
		CodecName     string `json:"codec_name"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		PixFmt        string `json:"pix_fmt"`
		ColorTransfer string `json:"color_transfer"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		Size       string `json:"size"`
		Tags       struct {
			MajorBrand string `json:"major_brand"`
		} `json:"tags"`
	} `json:"format"`
}

// M1 browser direct play: audio both Chrome and Firefox decode from MP4.
var m1Audio = map[string]bool{"aac": true, "mp3": true}

// parseProbe qualifies a source for M1: an MP4 with exactly one H.264 8-bit
// 4:2:0 SDR video stream within 1920x1080, exactly one browser-safe audio
// stream and a positive duration. An unspecified colour transfer counts as
// SDR; only PQ and HLG are rejected as HDR.
func parseProbe(b []byte) (probed, error) {
	var p probeJSON
	if err := json.Unmarshal(b, &p); err != nil {
		return probed{}, errors.New("probe: not ffprobe JSON (-show_format -show_streams -of json)")
	}
	// ffprobe gives QuickTime .mov the same format_name as MP4; the brand
	// (padded, e.g. "qt  ") tells them apart.
	if !strings.Contains(","+p.Format.FormatName+",", ",mp4,") || strings.TrimSpace(p.Format.Tags.MajorBrand) == "qt" {
		return probed{}, errors.New("probe: the source is not an MP4")
	}
	var out probed
	var videos, audios int
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			videos++
			if s.CodecName != "h264" {
				return probed{}, errors.New("probe: video must be H.264")
			}
			if s.PixFmt != "yuv420p" && s.PixFmt != "yuvj420p" {
				return probed{}, errors.New("probe: video must be 8-bit 4:2:0")
			}
			if s.ColorTransfer == "smpte2084" || s.ColorTransfer == "arib-std-b67" {
				return probed{}, errors.New("probe: HDR sources are outside M1")
			}
			if s.Width < 1 || s.Height < 1 || s.Width > 1920 || s.Height > 1080 {
				return probed{}, errors.New("probe: video must be within 1920x1080")
			}
			out.VideoCodec, out.Width, out.Height = s.CodecName, s.Width, s.Height
		case "audio":
			audios++
			if !m1Audio[s.CodecName] {
				return probed{}, errors.New("probe: audio must be AAC or MP3 for M1 direct play")
			}
			out.AudioCodec = s.CodecName
		}
	}
	if videos != 1 || audios != 1 {
		return probed{}, errors.New("probe: M1 needs exactly one video and one audio stream")
	}
	d, err := strconv.ParseFloat(p.Format.Duration, 64)
	if err != nil || d <= 0 || math.IsInf(d, 0) || math.IsNaN(d) {
		return probed{}, errors.New("probe: duration must be positive")
	}
	out.DurationMS = int64(math.Round(d * 1000))
	if p.Format.Size != "" {
		out.Size, _ = strconv.ParseInt(p.Format.Size, 10, 64)
	}
	return out, nil
}

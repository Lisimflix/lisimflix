package library

import (
	"regexp"
	"slices"
	"strings"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// fileFacts are the facet values derived once, when a probe is stored.
type fileFacts struct {
	Resolution string // "", "sd", "720p", "1080p", "4k"; "" when there is no video
	HDR        bool
	DurationMs int64
	AudioLangs []string // ISO 639-2/T, sorted, distinct
}

var resolutionRank = map[string]int{"sd": 0, "720p": 1, "1080p": 2, "4k": 3}

func bucket(v int, t4k, t1080, t720 int) string {
	switch {
	case v >= t4k:
		return "4k"
	case v >= t1080:
		return "1080p"
	case v >= t720:
		return "720p"
	}
	return "sd"
}

// defaultVideo returns the Default video stream, else the first.
func defaultVideo(r probe.Result) (probe.VideoStream, bool) {
	for _, v := range r.Video {
		if v.Default {
			return v, true
		}
	}
	if len(r.Video) > 0 {
		return r.Video[0], true
	}
	return probe.VideoStream{}, false
}

func deriveFile(r probe.Result) fileFacts {
	f := fileFacts{DurationMs: r.DurationMs}
	if v, ok := defaultVideo(r); ok {
		w, h := bucket(v.Width, 3200, 1600, 1100), bucket(v.Height, 2000, 1000, 700)
		f.Resolution = w
		if resolutionRank[h] > resolutionRank[w] {
			f.Resolution = h
		}
		f.HDR = v.HDR != probe.SDR
	}
	for _, a := range r.Audio {
		if l, ok := langT(a.Language); ok {
			f.AudioLangs = append(f.AudioLangs, l)
		}
	}
	slices.Sort(f.AudioLangs)
	f.AudioLangs = slices.Compact(f.AudioLangs)
	return f
}

var bToT = map[string]string{
	"alb": "sqi", "arm": "hye", "baq": "eus", "bur": "mya", "chi": "zho",
	"cze": "ces", "dut": "nld", "fre": "fra", "geo": "kat", "ger": "deu",
	"gre": "ell", "ice": "isl", "mac": "mkd", "mao": "mri", "may": "msa",
	"per": "fas", "rum": "ron", "slo": "slk", "tib": "bod", "wel": "cym",
}

var threeLetters = regexp.MustCompile(`^[a-z]{3}$`)

// langT lower-cases an ISO 639-2 tag and maps /B codes to /T. It reports false
// for untagged, "und" and anything that isn't three letters.
func langT(tag string) (string, bool) {
	tag = strings.ToLower(tag)
	if tag == "und" || !threeLetters.MatchString(tag) {
		return "", false
	}
	if t, ok := bToT[tag]; ok {
		return t, true
	}
	return tag, true
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// langPtr is a stream's language as tagged, or nil unless the tag fits the
// contract's ^(?!und$)[a-z]{3}$: untagged, "und", "en" and "english" are null.
func langPtr(tag string) *string {
	if tag == "und" || !threeLetters.MatchString(tag) {
		return nil
	}
	return &tag
}

func hdrName(h probe.HDRType) string {
	switch h {
	case probe.HDR10:
		return contracts.HDR10
	case probe.HDR10Plus:
		return contracts.HDR10Plus
	case probe.HLG:
		return contracts.HDRHLG
	case probe.DolbyVision:
		return contracts.HDRDolbyVision
	}
	return contracts.HDRNone
}

func subKind(codec string) string {
	switch codec {
	case "hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle":
		return contracts.SubtitleImage
	case "ass", "ssa":
		return contracts.SubtitleASS
	}
	return contracts.SubtitleText
}

// streams maps a probe result to the contract's stream lists.
func streams(r probe.Result) (video []contracts.VideoStream, audio []contracts.AudioStream, subs []contracts.SubtitleStream) {
	for _, v := range r.Video {
		video = append(video, contracts.VideoStream{Index: v.Index, Codec: v.Codec, Width: v.Width, Height: v.Height, HDR: hdrName(v.HDR), Default: v.Default})
	}
	for _, a := range r.Audio {
		audio = append(audio, contracts.AudioStream{Index: a.Index, Codec: a.Codec, Profile: strPtr(a.Profile), Channels: a.Channels,
			Language: langPtr(a.Language), Title: strPtr(a.Title), Default: a.Default, Forced: a.Forced})
	}
	for _, s := range r.Subtitles {
		subs = append(subs, contracts.SubtitleStream{Index: s.Index, Codec: s.Codec, Kind: subKind(s.Codec), Language: langPtr(s.Language),
			Title: strPtr(s.Title), Default: s.Default, Forced: s.Forced, HearingImpaired: s.HearingImpaired})
	}
	return
}

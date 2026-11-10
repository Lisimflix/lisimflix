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

// listedVideo and listedAudio are the streams the contract can carry (#168):
// streams() lists only these, and deriveFile derives facts only from them, so
// a filter never matches a track or a size the detail leaves out (#181 review).
func listedVideo(v probe.VideoStream) bool { return v.Codec != "" && v.Width >= 1 && v.Height >= 1 }
func listedAudio(a probe.AudioStream) bool { return a.Codec != "" && a.Channels >= 1 }

// defaultVideo returns the Default listed video stream, else the first listed.
func defaultVideo(r probe.Result) (probe.VideoStream, bool) {
	var first *probe.VideoStream
	for i, v := range r.Video {
		if !listedVideo(v) {
			continue
		}
		if v.Default {
			return v, true
		}
		if first == nil {
			first = &r.Video[i]
		}
	}
	if first != nil {
		return *first, true
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
		if !listedAudio(a) {
			continue
		}
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

// langPtr is a stream's language as the /T code the facets and filters use
// (#170), or nil when untagged, "und" or not three letters ("en", "english").
func langPtr(tag string) *string {
	t, ok := langT(tag)
	if !ok {
		return nil
	}
	return &t
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
	case "hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub", "dvb_teletext":
		return contracts.SubtitleImage
	case "ass", "ssa":
		return contracts.SubtitleASS
	}
	return contracts.SubtitleText
}

// streams maps a probe result to the contract's stream lists. A stream the
// contract can't carry (no codec, a video stream without a size, an audio
// stream without channels) is left out rather than breaking the response
// schema; the rest keep their original indexes (#168).
func streams(r probe.Result) (video []contracts.VideoStream, audio []contracts.AudioStream, subs []contracts.SubtitleStream) {
	for _, v := range r.Video {
		if !listedVideo(v) {
			continue
		}
		video = append(video, contracts.VideoStream{Index: v.Index, Codec: v.Codec, Width: v.Width, Height: v.Height, HDR: hdrName(v.HDR), Default: v.Default})
	}
	for _, a := range r.Audio {
		if !listedAudio(a) {
			continue
		}
		audio = append(audio, contracts.AudioStream{Index: a.Index, Codec: a.Codec, Profile: strPtr(a.Profile), Channels: a.Channels,
			Language: langPtr(a.Language), Title: strPtr(a.Title), Default: a.Default, Forced: a.Forced})
	}
	for _, s := range r.Subtitles {
		if s.Codec == "" {
			continue
		}
		subs = append(subs, contracts.SubtitleStream{Index: s.Index, Codec: s.Codec, Kind: subKind(s.Codec), Language: langPtr(s.Language),
			Title: strPtr(s.Title), Default: s.Default, Forced: s.Forced, HearingImpaired: s.HearingImpaired})
	}
	return
}

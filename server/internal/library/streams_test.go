package library

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

func vres(w, h int) probe.Result {
	return probe.Result{Video: []probe.VideoStream{{Codec: "h264", Width: w, Height: h, Default: true}}}
}

func TestResolutionBuckets(t *testing.T) {
	cases := []struct {
		name string
		r    probe.Result
		want string
	}{
		{"uhd", vres(3840, 2160), "4k"},
		{"uhd scope", vres(3840, 1600), "4k"},
		{"fhd", vres(1920, 1080), "1080p"},
		{"fhd scope", vres(1920, 800), "1080p"},
		{"4:3 fhd", vres(1440, 1080), "1080p"},
		{"hd", vres(1280, 720), "720p"},
		{"hd scope", vres(1280, 536), "720p"},
		{"pal", vres(720, 576), "sd"},
		{"vga", vres(640, 480), "sd"},
		{"no video", probe.Result{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deriveFile(c.r).Resolution; got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestDefaultVideoStreamDecides(t *testing.T) {
	r := probe.Result{DurationMs: 5000, Video: []probe.VideoStream{
		{Codec: "h264", Width: 1280, Height: 720},
		{Codec: "hevc", Width: 3840, Height: 2160, HDR: probe.HDR10, Default: true},
	}}
	f := deriveFile(r)
	if f.Resolution != "4k" || !f.HDR || f.DurationMs != 5000 {
		t.Fatalf("got %+v", f)
	}
	r.Video[1].Default = false
	f = deriveFile(r)
	if f.Resolution != "720p" || f.HDR {
		t.Fatalf("none default: got %+v", f)
	}
}

func TestAudioLangsSortedDistinct(t *testing.T) {
	r := probe.Result{Audio: []probe.AudioStream{{Codec: "aac", Channels: 2, Language: "ger"}, {Codec: "aac", Channels: 2, Language: "eng"}, {Codec: "aac", Channels: 2, Language: "deu"}, {Codec: "aac", Channels: 2, Language: ""}, {Codec: "aac", Channels: 2, Language: "ENG"}}}
	if got, want := deriveFile(r).AudioLangs, []string{"deu", "eng"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLangT(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"ger", "deu", true}, {"fre", "fra", true}, {"chi", "zho", true}, {"ENG", "eng", true},
		{"und", "", false}, {"", "", false}, {"english", "", false}, {"en", "", false}, {"e1g", "", false},
	}
	for _, c := range cases {
		got, ok := langT(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("langT(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestStreamsMap(t *testing.T) {
	r := probe.Result{
		Video: []probe.VideoStream{
			{Index: 0, Codec: "hevc", Width: 3840, Height: 2160, HDR: probe.HDR10Plus, Default: true},
			{Index: 1, Codec: "hevc", Width: 1, Height: 1, HDR: probe.DolbyVision},
			{Index: 2, Codec: "h264", Width: 1, Height: 1, HDR: probe.SDR},
			{Index: 3, Codec: "h264", Width: 1, Height: 1, HDR: probe.HLG},
		},
		Audio: []probe.AudioStream{
			{Index: 4, Codec: "truehd", Profile: "Dolby TrueHD + Dolby Atmos", Channels: 8, Language: "eng", Title: "Main", Default: true},
			{Index: 5, Codec: "aac", Channels: 2, Language: "und"},
		},
		Subtitles: []probe.SubtitleStream{
			{Index: 6, Codec: "hdmv_pgs_subtitle", Kind: probe.SubImage, Language: "eng", Forced: true},
			{Index: 7, Codec: "ass", Kind: probe.SubASS, Title: "Signs"},
			{Index: 8, Codec: "subrip", Kind: probe.SubText, HearingImpaired: true},
		},
	}
	v, a, s := streams(r)
	wantHDR := []string{contracts.HDR10Plus, contracts.HDRDolbyVision, contracts.HDRNone, contracts.HDRHLG}
	for i, w := range wantHDR {
		if v[i].HDR != w {
			t.Errorf("video %d hdr %q want %q", i, v[i].HDR, w)
		}
	}
	if v[0].Codec != "hevc" || v[0].Width != 3840 || !v[0].Default {
		t.Errorf("video 0: %+v", v[0])
	}
	if a[0].Profile == nil || *a[0].Profile != "Dolby TrueHD + Dolby Atmos" || a[0].Language == nil || *a[0].Language != "eng" || a[0].Title == nil || *a[0].Title != "Main" || a[0].Channels != 8 || !a[0].Default {
		t.Errorf("audio 0: %+v", a[0])
	}
	if a[1].Language != nil || a[1].Profile != nil || a[1].Title != nil {
		t.Errorf("audio 1 should have nil optional fields: %+v", a[1])
	}
	wantKind := []string{contracts.SubtitleImage, contracts.SubtitleASS, contracts.SubtitleText}
	for i, w := range wantKind {
		if s[i].Kind != w {
			t.Errorf("sub %d kind %q want %q", i, s[i].Kind, w)
		}
	}
	if s[0].Language == nil || *s[0].Language != "eng" || !s[0].Forced || s[1].Language != nil || s[1].Title == nil || !s[2].HearingImpaired {
		t.Errorf("subs: %+v", s)
	}
}

func TestStreamsKindFromCodec(t *testing.T) {
	r := probe.Result{Subtitles: []probe.SubtitleStream{{Codec: "dvd_subtitle"}, {Codec: "dvb_subtitle"}, {Codec: "ssa"}, {Codec: "mov_text"}, {Codec: "xsub"}, {Codec: "dvb_teletext"}}}
	_, _, s := streams(r)
	want := []string{"image", "image", "ass", "text", "image", "image"}
	for i, w := range want {
		if s[i].Kind != w {
			t.Errorf("%d: %q want %q", i, s[i].Kind, w)
		}
	}
}

// A stream language is served only when it fits the contract's
// ^(?!und$)[a-z]{3}$, as the /T code the filters use (#170); anything else is
// null rather than an invalid value.
func TestLangPtrFitsContractPattern(t *testing.T) {
	for tag, want := range map[string]string{"eng": "eng", "ger": "deu", "": "", "und": "", "UND": "", "en": "", "english": "", "ENG": "eng", "e1g": ""} {
		got := langPtr(tag)
		if (got == nil) != (want == "") || (got != nil && *got != want) {
			t.Errorf("langPtr(%q) = %v, want %q", tag, got, want)
		}
	}
}

// #168: every ISO 639-2/B code maps to its /T code, in either case, and a
// /T code passes through unchanged.
func TestLangTMapsEveryBibliographicCode(t *testing.T) {
	pairs := map[string]string{
		"alb": "sqi", "arm": "hye", "baq": "eus", "bur": "mya", "chi": "zho",
		"cze": "ces", "dut": "nld", "fre": "fra", "geo": "kat", "ger": "deu",
		"gre": "ell", "ice": "isl", "mac": "mkd", "mao": "mri", "may": "msa",
		"per": "fas", "rum": "ron", "slo": "slk", "tib": "bod", "wel": "cym",
	}
	if len(pairs) != len(bToT) {
		t.Fatalf("bToT has %d pairs, the table %d", len(bToT), len(pairs))
	}
	for b, want := range pairs {
		for _, in := range []string{b, strings.ToUpper(b)} {
			if got, ok := langT(in); !ok || got != want {
				t.Errorf("langT(%q) = %q,%v want %q", in, got, ok, want)
			}
		}
		if got, ok := langT(want); !ok || got != want {
			t.Errorf("langT(%q) = %q,%v want it unchanged", want, got, ok)
		}
	}
}

// #170: stream languages use the same /T codes as the audio facet and filter,
// so the UI never shows "ger" and "deu" for one language; an upper-case tag
// is read, not dropped.
func TestStreamLanguagesMatchFacetCodes(t *testing.T) {
	r := probe.Result{
		Audio: []probe.AudioStream{
			{Index: 1, Codec: "aac", Channels: 2, Language: "ger"},
			{Index: 2, Codec: "aac", Channels: 2, Language: "ENG"},
			{Index: 3, Codec: "aac", Channels: 2, Language: "und"},
		},
		Subtitles: []probe.SubtitleStream{{Index: 4, Codec: "subrip", Language: "fre"}, {Index: 5, Codec: "subrip", Language: "en"}},
	}
	_, a, s := streams(r)
	facets := deriveFile(r).AudioLangs
	if !reflect.DeepEqual(facets, []string{"deu", "eng"}) {
		t.Fatalf("facets %v", facets)
	}
	for i, want := range []string{"deu", "eng"} {
		if a[i].Language == nil || *a[i].Language != want {
			t.Errorf("audio %d language %v want %q", i, a[i].Language, want)
		}
	}
	if a[2].Language != nil || s[0].Language == nil || *s[0].Language != "fra" || s[1].Language != nil {
		t.Errorf("audio 3 %v, subs %v %v", a[2].Language, s[0].Language, s[1].Language)
	}
}

// #168: a stream the contract can't carry (no codec, no size, no channels) is
// left out rather than breaking the response schema; the others keep their
// original indexes.
func TestStreamsOutsideContractBoundsAreLeftOut(t *testing.T) {
	r := probe.Result{
		Video: []probe.VideoStream{
			{Index: 0, Codec: "", Width: 1920, Height: 1080},
			{Index: 1, Codec: "mjpeg", Width: 0, Height: 0},
			{Index: 2, Codec: "h264", Width: 1920, Height: 0},
			{Index: 3, Codec: "h264", Width: 1920, Height: 1080},
		},
		Audio: []probe.AudioStream{
			{Index: 4, Codec: "aac", Channels: 0},
			{Index: 5, Codec: "", Channels: 2},
			{Index: 6, Codec: "aac", Channels: 2},
		},
		Subtitles: []probe.SubtitleStream{{Index: 7, Codec: ""}, {Index: 8, Codec: "subrip"}},
	}
	v, a, s := streams(r)
	if len(v) != 1 || v[0].Index != 3 || len(a) != 1 || a[0].Index != 6 || len(s) != 1 || s[0].Index != 8 {
		t.Fatalf("video %+v audio %+v subs %+v", v, a, s)
	}
}

// #168: MediaVersion.label is 1-100 characters; an empty stored label is
// null and a longer one is cut on a character boundary.
func TestVersionLabelFitsContract(t *testing.T) {
	long := strings.Repeat("é", 120)
	cases := []struct {
		in   sql.NullString
		want *string
	}{
		{sql.NullString{}, nil},
		{sql.NullString{Valid: true}, nil},
		{sql.NullString{String: "Director's Cut", Valid: true}, strPtr("Director's Cut")},
		{sql.NullString{String: long, Valid: true}, strPtr(strings.Repeat("é", 100))},
	}
	for _, c := range cases {
		got := versionLabel(c.in)
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("versionLabel(%q) = %v want %v", c.in.String, got, c.want)
		}
	}
}

// #181 review: facts come only from the streams the detail lists, so a filter
// never matches a track or a size the detail leaves out.
func TestFactsUseOnlyListedStreams(t *testing.T) {
	r := probe.Result{
		Video: []probe.VideoStream{
			{Index: 0, Codec: "mjpeg", Default: true}, // no size: not listed
			{Index: 1, Codec: "h264", Width: 1280, Height: 720},
		},
		Audio: []probe.AudioStream{
			{Index: 2, Codec: "aac", Channels: 0, Language: "fre"},
			{Index: 3, Channels: 2, Language: "jpn"},
			{Index: 4, Codec: "aac", Channels: 2, Language: "eng"},
		},
	}
	f := deriveFile(r)
	v, a, _ := streams(r)
	if f.Resolution != "720p" || len(v) != 1 || v[0].Index != 1 {
		t.Fatalf("resolution %q from %+v", f.Resolution, v)
	}
	if !reflect.DeepEqual(f.AudioLangs, []string{"eng"}) || len(a) != 1 || a[0].Index != 4 {
		t.Fatalf("audio langs %v, listed %+v", f.AudioLangs, a)
	}
}

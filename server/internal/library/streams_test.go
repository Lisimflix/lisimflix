package library

import (
	"reflect"
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

func vres(w, h int) probe.Result {
	return probe.Result{Video: []probe.VideoStream{{Width: w, Height: h, Default: true}}}
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
		{Width: 1280, Height: 720},
		{Width: 3840, Height: 2160, HDR: probe.HDR10, Default: true},
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
	r := probe.Result{Audio: []probe.AudioStream{{Language: "ger"}, {Language: "eng"}, {Language: "deu"}, {Language: ""}, {Language: "ENG"}}}
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
// ^(?!und$)[a-z]{3}$; anything else is null rather than an invalid value.
func TestLangPtrFitsContractPattern(t *testing.T) {
	for tag, want := range map[string]string{"eng": "eng", "ger": "ger", "": "", "und": "", "en": "", "english": "", "ENG": "", "e1g": ""} {
		got := langPtr(tag)
		if (got == nil) != (want == "") || (got != nil && *got != want) {
			t.Errorf("langPtr(%q) = %v, want %q", tag, got, want)
		}
	}
}

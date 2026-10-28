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

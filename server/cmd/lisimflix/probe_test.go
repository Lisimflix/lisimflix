package main

import (
	"encoding/json"
	"testing"
)

// A trimmed ffprobe record shaped like Tears of Steel's: colour fields absent.
const tos = `{"streams":[
 {"index":0,"codec_name":"h264","codec_type":"video","profile":"Main","width":1920,"height":800,"pix_fmt":"yuv420p","r_frame_rate":"24/1"},
 {"index":1,"codec_name":"mp3","codec_type":"audio","sample_rate":"44100","channels":2}],
 "format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"734.166667"}}`

func TestProbeAcceptsUntaggedSDR(t *testing.T) {
	m, err := parseProbe([]byte(tos))
	if err != nil {
		t.Fatal(err)
	}
	if m.VideoCodec != "h264" || m.AudioCodec != "mp3" || m.Width != 1920 || m.Height != 800 || m.DurationMS != 734167 {
		t.Fatalf("probe = %+v", m)
	}
	// An MP4 brand and a recorded size are accepted and kept.
	var v map[string]any
	json.Unmarshal([]byte(tos), &v)
	v["format"].(map[string]any)["tags"] = map[string]any{"major_brand": "isom"}
	v["format"].(map[string]any)["size"] = "583770545"
	b, _ := json.Marshal(v)
	if m, err := parseProbe(b); err != nil || m.Size != 583770545 {
		t.Fatalf("isom probe = %+v, %v", m, err)
	}
}

func TestProbeRejectsUnqualifiedSources(t *testing.T) {
	edit := func(f func(map[string]any)) []byte {
		var v map[string]any
		json.Unmarshal([]byte(tos), &v)
		f(v)
		b, _ := json.Marshal(v)
		return b
	}
	stream := func(v map[string]any, i int) map[string]any { return v["streams"].([]any)[i].(map[string]any) }
	for name, b := range map[string][]byte{
		"pq hdr":       edit(func(v map[string]any) { stream(v, 0)["color_transfer"] = "smpte2084" }),
		"hlg hdr":      edit(func(v map[string]any) { stream(v, 0)["color_transfer"] = "arib-std-b67" }),
		"10-bit":       edit(func(v map[string]any) { stream(v, 0)["pix_fmt"] = "yuv420p10le" }),
		"hevc":         edit(func(v map[string]any) { stream(v, 0)["codec_name"] = "hevc" }),
		"4k":           edit(func(v map[string]any) { stream(v, 0)["width"] = 3840.0 }),
		"no audio":     edit(func(v map[string]any) { v["streams"] = v["streams"].([]any)[:1] }),
		"two audio":    edit(func(v map[string]any) { v["streams"] = append(v["streams"].([]any), stream(v, 1)) }),
		"two video":    edit(func(v map[string]any) { v["streams"] = append([]any{stream(v, 0)}, v["streams"].([]any)...) }),
		"matroska":     edit(func(v map[string]any) { v["format"].(map[string]any)["format_name"] = "matroska,webm" }),
		"no duration":  edit(func(v map[string]any) { v["format"].(map[string]any)["duration"] = "0" }),
		"bad duration": edit(func(v map[string]any) { v["format"].(map[string]any)["duration"] = "abc" }),
		"ac3 audio":    edit(func(v map[string]any) { stream(v, 1)["codec_name"] = "ac3" }),
		"not json":     []byte("nope"),
		// A size that doesn't parse must not silently skip init's size check.
		"bad size":      edit(func(v map[string]any) { v["format"].(map[string]any)["size"] = "12abc" }),
		"negative size": edit(func(v map[string]any) { v["format"].(map[string]any)["size"] = "-5" }),
		// QuickTime .mov shares format_name with MP4; ffprobe pads the brand.
		"quicktime brand": edit(func(v map[string]any) { v["format"].(map[string]any)["tags"] = map[string]any{"major_brand": "qt  "} }),
	} {
		if _, err := parseProbe(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

package contracts

import (
	"encoding/json"
	"testing"
)

var catalogueFixtures = map[string]func() any{
	"libraries.json":                func() any { return &LibraryList{} },
	"items-movies.json":             func() any { return &ItemPage{} },
	"items-episodes.json":           func() any { return &ItemPage{} },
	"item-movie.json":               func() any { return &ItemDetail{} },
	"item-episode-shared-file.json": func() any { return &ItemDetail{} },
	"item-show.json":                func() any { return &ItemDetail{} },
}

func TestCatalogueFixturesRoundTrip(t *testing.T) { roundTripFixtures(t, catalogueFixtures) }

func TestEmptyPagesEncodeItemsAsArray(t *testing.T) {
	for _, v := range []any{LibraryList{}, ItemPage{}} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != `{"items":[],"nextCursor":null}` {
			t.Fatalf("%T encoded as %s", v, b)
		}
	}
}

func TestCatalogueEnumsMatchOpenAPI(t *testing.T) {
	checkEnum(t, "Library", "type", LibraryMovies, LibraryTV, LibraryAnime)
	checkEnum(t, "ItemSummary", "kind", KindMovie, KindShow, KindSeason, KindEpisode)
	checkEnum(t, "VideoStream", "hdr", HDRNone, HDR10, HDR10Plus, HDRHLG, HDRDolbyVision)
	checkEnum(t, "SubtitleStream", "kind", SubtitleText, SubtitleImage, SubtitleASS)
}

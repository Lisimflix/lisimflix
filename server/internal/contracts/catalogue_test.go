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
	"facets.json":                   func() any { return &LibraryFacets{} },
	"continue-watching.json":        func() any { return &ItemPage{} },
	"next-up.json":                  func() any { return &ItemPage{} },
}

func TestCatalogueFixturesRoundTrip(t *testing.T) { roundTripFixtures(t, catalogueFixtures) }

func TestEmptyPagesEncodeItemsAsArray(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
	}{
		{LibraryList{}, `{"items":[],"nextCursor":null}`},
		{ItemPage{}, `{"items":[],"nextCursor":null,"total":null}`},
	} {
		b, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.want {
			t.Fatalf("%T encoded as %s", c.v, b)
		}
	}
}

func TestCatalogueEnumsMatchOpenAPI(t *testing.T) {
	checkEnum(t, "Library", "type", LibraryMovies, LibraryTV, LibraryAnime)
	checkEnum(t, "ItemSummary", "kind", KindMovie, KindShow, KindSeason, KindEpisode)
	checkEnum(t, "VideoStream", "hdr", HDRNone, HDR10, HDR10Plus, HDRHLG, HDRDolbyVision)
	checkEnum(t, "SubtitleStream", "kind", SubtitleText, SubtitleImage, SubtitleASS)
	checkEnum(t, "LibraryScan", "state", ScanIdle, ScanScanning)
	checkEnum(t, "Library", "availability", AvailabilityAvailable, AvailabilityPartial, AvailabilityOffline)
	checkEnum(t, "LibraryFacets", "resolutions", ResolutionSD, Resolution720, Resolution1080, Resolution4K)
	checkParamEnum(t, "Sort", SortTitle, SortAdded, SortYear)
	checkParamEnum(t, "Resolution", ResolutionSD, Resolution720, Resolution1080, Resolution4K)
}

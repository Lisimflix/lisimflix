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
	"item-movie-admin.json":         func() any { return &ItemDetail{} },
	"item-episode-shared-file.json": func() any { return &ItemDetail{} },
	"item-episode-chaptered.json":   func() any { return &ItemDetail{} },
	"item-show.json":                func() any { return &ItemDetail{} },
	"facets.json":                   func() any { return &LibraryFacets{} },
	"continue-watching.json":        func() any { return &ItemPage{} },
	"next-up.json":                  func() any { return &ItemPage{} },
}

func TestCatalogueFixturesRoundTrip(t *testing.T) { roundTripFixtures(t, catalogueFixtures) }

// Decision 4: an extra's ID is its version's ID (the extra's playback itemId).
func TestExtraIDIsVersionID(t *testing.T) {
	for name, newValue := range catalogueFixtures {
		d, ok := newValue().(*ItemDetail)
		if !ok {
			continue
		}
		decodeStrict(t, loadCatalogueFixture(t, name), d)
		for _, e := range d.Extras {
			if e.ID != e.Version.ID {
				t.Errorf("%s: extra %s has version %s", name, e.ID, e.Version.ID)
			}
		}
	}
}

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
	checkSchemaEnum(t, "Resolution", ResolutionSD, Resolution720, Resolution1080, Resolution4K)
	checkParamEnum(t, "Sort", SortTitle, SortAdded, SortYear)
	checkSchemaEnum(t, "ExtraType", ExtraTrailer, ExtraFeaturette, ExtraBehindTheScenes, ExtraDeletedScene, ExtraInterview, ExtraScene, ExtraShort, ExtraOther)
}

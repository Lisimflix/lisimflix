package contracts

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var metadataFixtures = map[string]func() any{
	"item-metadata.json": func() any { return &ItemMetadata{} },
}

func TestMetadataFixturesRoundTrip(t *testing.T) { roundTripFixtures(t, metadataFixtures) }

func TestMetadataEnumsMatchOpenAPI(t *testing.T) {
	checkEnum(t, "ItemMetadata", "matchStatus", MatchUnmatched, MatchMatched, MatchReview, MatchConflict)
}

// Every file in testdata/contracts/catalogue is round-tripped by one of the
// CTR-2 tests, so a new fixture can't skip the Go check.
func TestEveryCatalogueFixtureIsRoundTripped(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "..", "testdata", "contracts", "catalogue"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			files = append(files, e.Name())
		}
	}
	var tested []string
	for _, m := range []map[string]func() any{userDataFixtures, catalogueFixtures, profileFixtures, metadataFixtures} {
		tested = slices.AppendSeq(tested, maps.Keys(m))
	}
	slices.Sort(files)
	slices.Sort(tested)
	if !slices.Equal(files, tested) {
		t.Fatalf("fixtures %v, round-tripped %v", files, tested)
	}
}

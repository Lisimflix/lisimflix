package contracts

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Every contract type, so the null check below covers it. A new exported struct
// type fails TestEveryContractTypeIsListed until it is added here (#129).
var contractTypes = map[string]any{
	"Principal": Principal{}, "Profile": Profile{}, "ProfilePolicy": ProfilePolicy{},
	"UserItemState": UserItemState{}, "Resume": Resume{}, "Watched": Watched{},
	"ProgressEvent": ProgressEvent{}, "ProgressResult": ProgressResult{}, "WatchedRequest": WatchedRequest{},
	"ItemMetadata": ItemMetadata{}, "MetadataFields": MetadataFields{}, "Field": Field[[]string]{},
	"Provenance": Provenance{}, "MetadataExternalID": MetadataExternalID{},
	"Page": Page[Movie]{}, "Library": Library{}, "LibraryScan": LibraryScan{}, "LibraryFacets": LibraryFacets{},
	"YearRange": YearRange{}, "ContentRating": ContentRating{}, "ItemSummary": ItemSummary{}, "ItemPage": ItemPage{},
	"CommunityRating": CommunityRating{}, "ExternalID": ExternalID{}, "ItemDetail": ItemDetail{},
	"MediaVersion": MediaVersion{}, "Segment": Segment{}, "VideoStream": VideoStream{}, "AudioStream": AudioStream{},
	"SubtitleStream": SubtitleStream{}, "Viewer": Viewer{}, "AuthState": AuthState{}, "Movie": Movie{},
	"MovieList": MovieList{}, "PlaybackSession": PlaybackSession{}, "APIError": APIError{},
	"LoginRequest": LoginRequest{}, "PlaybackSessionRequest": PlaybackSessionRequest{}, "HeartbeatRequest": HeartbeatRequest{},
}

func TestEveryContractTypeIsListed(t *testing.T) {
	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, p := range pkgs {
		for _, f := range p.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					return true
				}
				if _, isStruct := ts.Type.(*ast.StructType); isStruct {
					if _, listed := contractTypes[ts.Name.Name]; !listed {
						missing = append(missing, ts.Name.Name)
					}
				}
				return true
			})
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("add to contractTypes: %v", missing)
	}
}

// An array field must encode as [], never null, even in a zero value: the
// contract types them as arrays, and clients iterate them (#128, #129).
func TestNoArrayFieldEncodesAsNull(t *testing.T) {
	for name, v := range contractTypes {
		rt := reflect.TypeOf(v)
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			tag := f.Tag.Get("json")
			key, opts, _ := strings.Cut(tag, ",")
			if f.Type.Kind() != reflect.Slice || key == "-" || key == "" || strings.Contains(opts, "omitempty") {
				continue
			}
			if string(m[key]) == "null" {
				t.Errorf("%s.%s encodes as null", name, key)
			}
		}
	}
}

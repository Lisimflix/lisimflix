package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Helpers for the CTR-2 fixtures in testdata/contracts/catalogue.

func loadCatalogueFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "contracts", "catalogue", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// roundTripFixtures decodes each fixture strictly into its Go type and checks
// that encoding it again gives the same JSON, so Go producers emit exactly
// the shapes the web app is built against.
func roundTripFixtures(t *testing.T, cases map[string]func() any) {
	t.Helper()
	for name, newValue := range cases {
		t.Run(name, func(t *testing.T) {
			raw := loadCatalogueFixture(t, name)
			v := newValue()
			decodeStrict(t, raw, v)
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := canonical(t, out), canonical(t, raw); got != want {
				t.Fatalf("round trip changed fixture\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// checkEnum fails unless goValues are exactly the string values of the
// OpenAPI enum at schema.prop (a null member is ignored). The Go constants
// are producers' only spelling of each value.
func checkEnum(t *testing.T, schema, prop string, goValues ...string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Enum  []any `json:"enum"`
					Items struct {
						Enum []any `json:"enum"`
					} `json:"items"` // array properties: the enum is on the items
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	p := doc.Components.Schemas[schema].Properties[prop]
	compareEnum(t, schema+"."+prop, append(p.Enum, p.Items.Enum...), goValues)
}

// checkParamEnum is checkEnum for a query parameter under components.parameters.
func checkParamEnum(t *testing.T, param string, goValues ...string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Parameters map[string]struct {
				Schema struct {
					Enum []any `json:"enum"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	compareEnum(t, "parameter "+param, doc.Components.Parameters[param].Schema.Enum, goValues)
}

func compareEnum(t *testing.T, name string, enum []any, goValues []string) {
	t.Helper()
	var want []string
	for _, v := range enum {
		if s, ok := v.(string); ok {
			want = append(want, s)
		}
	}
	got := slices.Sorted(slices.Values(goValues))
	slices.Sort(want)
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("%s: go %v, openapi %v", name, got, want)
	}
}

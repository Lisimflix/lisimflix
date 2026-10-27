package contracts

import "testing"

var profileFixtures = map[string]func() any{
	"profiles.json": func() any { return &ProfileList{} },
}

func TestProfileFixturesRoundTrip(t *testing.T) { roundTripFixtures(t, profileFixtures) }

package contracts

import "encoding/json"

// Profile wire types (CTR-2, spec §6.3).

// Profile is a viewing identity under the signed-in account.
type Profile struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	PinRequired bool          `json:"pinRequired"`
	Policy      ProfilePolicy `json:"policy"`
}

// ProfilePolicy is a profile's effective policy: the account's ceiling
// narrowed by the profile (spec §6.3). It is for display and fixtures; the
// server enforces policy itself.
type ProfilePolicy struct {
	LibraryIDs    []string       `json:"libraryIds"`
	RatingCeiling *ContentRating `json:"ratingCeiling"` // null: no rating limit
	AllowUnrated  bool           `json:"allowUnrated"`
}

// MarshalJSON encodes nil LibraryIDs as [] because the contract requires an array.
func (p ProfilePolicy) MarshalJSON() ([]byte, error) {
	type plain ProfilePolicy // no methods: avoids recursion
	p.LibraryIDs = nonNil(p.LibraryIDs)
	return json.Marshal(plain(p))
}

type ProfileList = Page[Profile]

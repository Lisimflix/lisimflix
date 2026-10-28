package contracts

import "time"

// Metadata provenance wire types (CTR-2, spec §3.7): where each field came
// from and whether it is locked. Admin-only.

// Match states (ItemMetadata.matchStatus).
const (
	MatchUnmatched = "unmatched"
	MatchMatched   = "matched"
	MatchReview    = "review"
	MatchConflict  = "conflict"
)

// ItemMetadata is an item's editable metadata with provenance. Revision
// starts at 1 and counts committed changes; edits send it back as
// expectedRevision.
type ItemMetadata struct {
	ItemID      string               `json:"itemId"`
	Revision    int64                `json:"revision"`
	MatchStatus string               `json:"matchStatus"`
	Fields      MetadataFields       `json:"fields"`
	ExternalIDs []MetadataExternalID `json:"externalIds"`
}

type MetadataFields struct {
	Title         TextField     `json:"title"`
	SortTitle     TextField     `json:"sortTitle"`
	OriginalTitle TextField     `json:"originalTitle"`
	Overview      TextField     `json:"overview"`
	Year          IntegerField  `json:"year"`
	ContentRating RatingField   `json:"contentRating"`
	Genres        TextListField `json:"genres"`
}

// Field is one metadata value with its provenance. Source is null when the
// value has no recorded origin yet. A locked field keeps its value through
// refreshes and rescans.
type Field[T any] struct {
	Value  T           `json:"value"`
	Source *Provenance `json:"source"`
	Locked bool        `json:"locked"`
}

type (
	TextField     = Field[*string]
	IntegerField  = Field[*int]
	RatingField   = Field[*ContentRating]
	TextListField = Field[[]string]
)

// Provenance records where a value came from: a provider namespace (tmdb,
// tvdb, anilist ...), "nfo" for local NFO files or "user" for manual edits.
type Provenance struct {
	Provider   string    `json:"provider"`
	ProviderID *string   `json:"providerId"`
	FetchedAt  time.Time `json:"fetchedAt"`
}

type MetadataExternalID struct {
	Namespace string      `json:"namespace"`
	ID        string      `json:"id"`
	Source    *Provenance `json:"source"`
	Locked    bool        `json:"locked"`
}

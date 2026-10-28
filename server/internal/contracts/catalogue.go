package contracts

import "encoding/json"

// Catalogue wire types (CTR-2). Item, file and version IDs are opaque
// strings: clients never parse them, and they survive renames, provider
// changes and episode-order changes (spec §10.1). Every property is
// required; "unknown" is null, never an invented zero. Producers send empty
// slices, not nil: a nil slice encodes as null, which the contract rejects.

// Library types (Library.type).
const (
	LibraryMovies = "movies"
	LibraryTV     = "tv"
	LibraryAnime  = "anime"
)

// Item kinds (ItemSummary.kind).
const (
	KindMovie   = "movie"
	KindShow    = "show"
	KindSeason  = "season"
	KindEpisode = "episode"
)

// HDR formats (VideoStream.hdr).
const (
	HDRNone        = "sdr"
	HDR10          = "hdr10"
	HDR10Plus      = "hdr10plus"
	HDRHLG         = "hlg"
	HDRDolbyVision = "dolbyVision"
)

// Subtitle kinds (SubtitleStream.kind).
const (
	SubtitleText  = "text"
	SubtitleImage = "image"
	SubtitleASS   = "ass"
)

// Page is one page of a cursor-paginated list. NextCursor is null on the
// last page.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// MarshalJSON encodes nil Items as [] because the contract requires an array.
func (p Page[T]) MarshalJSON() ([]byte, error) {
	items := p.Items
	if items == nil {
		items = []T{}
	}
	return json.Marshal(struct {
		Items      []T     `json:"items"`
		NextCursor *string `json:"nextCursor"`
	}{items, p.NextCursor})
}

type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type LibraryList = Page[Library]

// ContentRating is a certification in one rating region, e.g. AU "PG".
type ContentRating struct {
	Region string `json:"region"` // ISO 3166-1 alpha-2, upper case
	Value  string `json:"value"`
}

type ItemSummary struct {
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	LibraryID       string         `json:"libraryId"`
	ParentID        *string        `json:"parentId"` // season → show, episode → season; null for movies and shows
	Title           string         `json:"title"`    // in the current profile's title language
	SortTitle       string         `json:"sortTitle"`
	Year            *int           `json:"year"`
	Index           *int           `json:"index"` // season or episode number in the library's chosen order
	DurationMs      *int64         `json:"durationMs"`
	ContentRating   *ContentRating `json:"contentRating"`
	PrimaryImageURL *string        `json:"primaryImageUrl"`
	BackdropURL     *string        `json:"backdropUrl"`
	UserState       *UserItemState `json:"userState"` // null for shows and seasons
}

type ItemPage = Page[ItemSummary]

// ExternalID is a provider ID in its namespace, e.g. tmdb "603". The item's
// kind says which of the provider's ID spaces it belongs to.
type ExternalID struct {
	Namespace string `json:"namespace"`
	ID        string `json:"id"`
}

type ItemDetail struct {
	Item          ItemSummary    `json:"item"`
	OriginalTitle *string        `json:"originalTitle"`
	Overview      *string        `json:"overview"`
	Genres        []string       `json:"genres"`
	ExternalIDs   []ExternalID   `json:"externalIds"`
	Versions      []MediaVersion `json:"versions"` // empty for shows and seasons
}

// MediaVersion is one playable version of a movie or episode: a cut or
// quality backed by one file. Several episodes may share a file; Segment
// locates the episode inside it when its boundaries are known.
type MediaVersion struct {
	ID             string           `json:"id"`
	FileID         string           `json:"fileId"`
	Label          *string          `json:"label"`
	DurationMs     *int64           `json:"durationMs"`
	SourceRevision string           `json:"sourceRevision"` // SourceRevision(FileID, size, mtime)
	Segment        *Segment         `json:"segment"`
	Container      string           `json:"container"`
	Video          []VideoStream    `json:"video"`
	Audio          []AudioStream    `json:"audio"`
	Subtitles      []SubtitleStream `json:"subtitles"`
}

// Segment is a range of a file's media timeline, in milliseconds.
type Segment struct {
	StartMs int64 `json:"startMs"`
	EndMs   int64 `json:"endMs"`
}

// Stream indexes are the original ffprobe stream indexes in the file.

type VideoStream struct {
	Index   int    `json:"index"`
	Codec   string `json:"codec"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	HDR     string `json:"hdr"`
	Default bool   `json:"default"`
}

type AudioStream struct {
	Index    int     `json:"index"`
	Codec    string  `json:"codec"`
	Profile  *string `json:"profile"` // e.g. "Dolby TrueHD + Dolby Atmos", "DTS-HD MA"; null when ffprobe reports none
	Channels int     `json:"channels"`
	Language *string `json:"language"` // ISO 639-2 as tagged; null when untagged or "und"
	Title    *string `json:"title"`
	Default  bool    `json:"default"`
	Forced   bool    `json:"forced"`
}

type SubtitleStream struct {
	Index           int     `json:"index"`
	Codec           string  `json:"codec"`
	Kind            string  `json:"kind"`
	Language        *string `json:"language"`
	Title           *string `json:"title"`
	Default         bool    `json:"default"`
	Forced          bool    `json:"forced"`
	HearingImpaired bool    `json:"hearingImpaired"`
}

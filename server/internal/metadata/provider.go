package metadata

import (
	"context"
	"errors"
)

// Provider is a metadata source such as TMDB.
// Planned: META-1b (the TMDB provider implements it).
type Provider interface {
	// Name is the provider's namespace, used for provenance and its own IDs.
	Name() string
	// Search returns at most one page of candidates, best first.
	Search(ctx context.Context, q Query) ([]Candidate, error)
	// Lookup returns the record of kind with id in namespace: the provider's
	// own namespace or one it can resolve (e.g. imdb for TMDB). It returns
	// ErrNotFound if there's no such record.
	Lookup(ctx context.Context, kind, namespace, id string) (Record, error)
	// Namespaces lists the namespaces Lookup accepts, the provider's own first.
	Namespaces() []string
}

// Record is a provider's metadata for one movie or show. A nil or zero field
// means "not provided" and leaves the stored field alone; it never clears it.
type Record struct {
	ID, Kind                                  string
	Title, OriginalTitle, SortTitle, Overview *string
	Year                                      *int
	ContentRating                             *Rating
	Genres                                    []string
	ExternalIDs                               map[string]string // namespace → ID, besides the provider's own
}

// Rating is a content rating in a region, such as {"AU", "MA15+"}.
type Rating struct{ Region, Value string }

var (
	// ErrNotFound: the provider has no such record.
	ErrNotFound = errors.New("metadata: not found at the provider")
	// ErrUnavailable: a timeout, server error or rate limit; retry later.
	ErrUnavailable = errors.New("metadata: provider unavailable")
	// ErrAuth: the provider refused the credentials; it is paused until resumed.
	ErrAuth = errors.New("metadata: provider refused the credentials")
)

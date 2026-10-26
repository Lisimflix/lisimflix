// Package contracts holds the public M1 API wire types shared by the runtime,
// playback and web player. docs/contracts/m1-openapi.json is the source of
// truth; TestOpenAPIMatchesGoTypes keeps these structs' field names and
// required set in step with it (not their types or nullability).
package contracts

import "encoding/json"

type Viewer struct {
	AccountID string `json:"accountId"`
	ProfileID string `json:"profileId"`
	Username  string `json:"username"`
}

type AuthState struct {
	Authenticated bool    `json:"authenticated"`
	Viewer        *Viewer `json:"viewer"`
	CSRFToken     string  `json:"csrfToken"`
}

type Movie struct {
	ID             string `json:"id"`
	SourceRevision string `json:"sourceRevision"`
	Title          string `json:"title"`
	DurationMs     int64  `json:"durationMs"`
	Container      string `json:"container"`
	VideoCodec     string `json:"videoCodec"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	AudioCodec     string `json:"audioCodec"`
}

type MovieList struct {
	Items      []Movie `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// MarshalJSON encodes nil Items as [] because the contract requires an array.
func (l MovieList) MarshalJSON() ([]byte, error) {
	type plain MovieList
	if l.Items == nil {
		l.Items = []Movie{}
	}
	return json.Marshal(plain(l))
}

type PlaybackSession struct {
	ID                       string `json:"id"`
	MovieID                  string `json:"movieId"`
	SourceRevision           string `json:"sourceRevision"`
	ContentURL               string `json:"contentUrl"`
	Route                    string `json:"route"`
	HeartbeatIntervalSeconds int    `json:"heartbeatIntervalSeconds"`
	IdleTimeoutSeconds       int    `json:"idleTimeoutSeconds"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type PlaybackSessionRequest struct {
	MovieID string `json:"movieId"`
}

// HeartbeatRequest.Sequence must be nonnegative; handlers reject negatives with invalid_request.
type HeartbeatRequest struct {
	Sequence int64 `json:"sequence"`
}

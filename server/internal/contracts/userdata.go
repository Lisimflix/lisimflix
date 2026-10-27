package contracts

import "time"

// Per-profile user data wire types (CTR-2). Progress belongs to
// profile + logical item and keeps the version and source revision it was
// recorded against (spec §3.8, §10.3).

// Watched origins (Watched.origin).
const (
	WatchedAuto   = "auto"
	WatchedManual = "manual"
	WatchedImport = "import"
)

// Progress event kinds (ProgressEvent.kind).
const (
	ProgressTick  = "tick"
	ProgressPause = "pause"
	ProgressSeek  = "seek"
	ProgressStop  = "stop"
)

// Reasons an event was not applied (ProgressResult.reason).
const (
	ProgressDuplicate = "duplicate"
	ProgressStale     = "stale"
)

// UserItemState is the current profile's state for one movie or episode.
// Revision counts committed changes (0 = nothing recorded yet); writes send
// it back as expectedRevision.
type UserItemState struct {
	ItemID                     string     `json:"itemId"`
	Revision                   int64      `json:"revision"`
	Resume                     *Resume    `json:"resume"`
	Watched                    *Watched   `json:"watched"` // null: not watched yet, or reset by marking unwatched
	PlayCount                  *int       `json:"playCount"`
	LastPlayedAt               *time.Time `json:"lastPlayedAt"`
	HiddenFromContinueWatching bool       `json:"hiddenFromContinueWatching"`
}

// Resume is a resume position on one version's timeline.
type Resume struct {
	PositionMs     int64     `json:"positionMs"`
	DurationMs     int64     `json:"durationMs"`
	VersionID      string    `json:"versionId"`
	SourceRevision string    `json:"sourceRevision"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Watched is the watched state and where it came from. A manual watched mark
// is never undone by later playback. Marking unwatched is a reset instead:
// watched and resume become null, so finishing the item marks it watched again
// (origin auto).
type Watched struct {
	Value  bool      `json:"value"`
	Origin string    `json:"origin"`
	At     time.Time `json:"at"`
}

// ProgressEvent is one progress report for a live playback session. The
// session fixes the item, version and source revision. FromPositionMs is set
// only for seeks.
type ProgressEvent struct {
	Sequence       int64  `json:"sequence"`
	Kind           string `json:"kind"`
	PositionMs     int64  `json:"positionMs"`
	FromPositionMs *int64 `json:"fromPositionMs"`
}

// ProgressResult says whether an event was applied; a duplicate or stale
// event is answered with accepted:false and the unchanged state, not an error.
type ProgressResult struct {
	Accepted bool          `json:"accepted"`
	Reason   *string       `json:"reason"`
	State    UserItemState `json:"state"`
}

type WatchedRequest struct {
	Watched          bool  `json:"watched"`
	ExpectedRevision int64 `json:"expectedRevision"`
}

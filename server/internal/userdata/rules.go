// Package userdata is the DATA-1a personal progress store: per-profile
// watched and played state derived from covered playback spans.
package userdata

import "slices"

// Positions and durations are milliseconds.
const (
	// WatchedPositionPercent: the position must be past this share of the
	// duration (the end always is: positions are clamped to it).
	WatchedPositionPercent = 95
	// WatchedCoveredPercent: at least this share must be covered (§3.8, D11).
	WatchedCoveredPercent = 50
	// PlayedMinMs is the covered time that makes an item count as played.
	PlayedMinMs int64 = 30_000
	// MaxTickAdvanceMs is the largest forward jump a progress tick may
	// credit; bigger jumps are seeks, not watching.
	MaxTickAdvanceMs int64 = 30_000
	// StopToleranceMs is how far behind the last accepted position a pause or
	// stop may land (player rounding) and still be accepted.
	StopToleranceMs int64 = 1_000
)

// Span is a half-open playback range [StartMs, EndMs).
type Span struct{ StartMs, EndMs int64 }

// Merge returns spans sorted, non-overlapping, with touching spans joined and
// empty spans dropped. The input is not modified.
func Merge(spans []Span) []Span {
	s := slices.DeleteFunc(slices.Clone(spans), func(x Span) bool { return x.EndMs <= x.StartMs })
	slices.SortFunc(s, func(a, b Span) int {
		switch {
		case a.StartMs < b.StartMs:
			return -1
		case a.StartMs > b.StartMs:
			return 1
		}
		return 0
	})
	out := s[:0]
	for _, x := range s {
		if n := len(out); n > 0 && x.StartMs <= out[n-1].EndMs {
			out[n-1].EndMs = max(out[n-1].EndMs, x.EndMs)
			continue
		}
		out = append(out, x)
	}
	return out
}

// Covered is the total length of the merged spans.
func Covered(spans []Span) int64 {
	var total int64
	for _, s := range Merge(spans) {
		total += s.EndMs - s.StartMs
	}
	return total
}

// Advance turns a progress tick into the span it credits: forward movement of
// at most MaxTickAdvanceMs. Backward, zero or larger jumps credit nothing.
func Advance(prevMs, posMs int64) (Span, bool) {
	if d := posMs - prevMs; d <= 0 || d > MaxTickAdvanceMs {
		return Span{}, false
	}
	return Span{prevMs, posMs}, true
}

// IsWatched reports whether an item is watched (§3.8): the position is past
// WatchedPositionPercent of the duration and coveredMs reaches
// WatchedCoveredPercent of it. An unknown (non-positive) duration is never watched.
func IsWatched(positionMs, coveredMs, durationMs int64) bool {
	return durationMs > 0 && pastWatchedPosition(positionMs, durationMs) && coveredMs*100 >= durationMs*WatchedCoveredPercent
}

// pastWatchedPosition reports whether positionMs is past WatchedPositionPercent of a known duration.
func pastWatchedPosition(positionMs, durationMs int64) bool {
	return durationMs > 0 && positionMs*100 >= durationMs*WatchedPositionPercent
}

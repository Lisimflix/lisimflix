// Package userdata is the DATA-1a personal progress store: per-profile
// watched and played state derived from covered playback spans.
package userdata

import "slices"

// Positions and durations are milliseconds.
//
// Planned: DATA-1a (Apply, SetWatched).
const (
	// WatchedPercent is the share of the duration that must be covered for
	// an item to count as watched.
	WatchedPercent = 90
	// PlayedMinMs is the covered time that makes an item count as played.
	PlayedMinMs int64 = 30_000
	// MaxTickAdvanceMs is the largest forward jump a progress tick may
	// credit; bigger jumps are seeks, not watching.
	MaxTickAdvanceMs int64 = 30_000
)

// Span is a half-open playback range [StartMs, EndMs).
//
// Planned: DATA-1a (Apply, SetWatched).
type Span struct{ StartMs, EndMs int64 }

// Merge returns spans sorted, non-overlapping, with touching spans joined and
// empty spans dropped. The input is not modified.
//
// Planned: DATA-1a (Apply, SetWatched).
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
//
// Planned: DATA-1a (Apply, SetWatched).
func Covered(spans []Span) int64 {
	var total int64
	for _, s := range Merge(spans) {
		total += s.EndMs - s.StartMs
	}
	return total
}

// Advance turns a progress tick into the span it credits: forward movement of
// at most MaxTickAdvanceMs. Backward, zero or larger jumps credit nothing.
//
// Planned: DATA-1a (Apply).
func Advance(prevMs, posMs int64) (Span, bool) {
	if d := posMs - prevMs; d <= 0 || d > MaxTickAdvanceMs {
		return Span{}, false
	}
	return Span{prevMs, posMs}, true
}

// IsWatched reports whether coveredMs reaches WatchedPercent of durationMs.
// An unknown (non-positive) duration is never watched.
//
// Planned: DATA-1a (Apply, SetWatched).
func IsWatched(coveredMs, durationMs int64) bool {
	return durationMs > 0 && coveredMs*100 >= durationMs*WatchedPercent
}

// IsPlayed reports whether coveredMs reaches PlayedMinMs.
//
// Planned: DATA-1a (Apply).
func IsPlayed(coveredMs int64) bool { return coveredMs >= PlayedMinMs }

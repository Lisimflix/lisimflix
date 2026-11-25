package library

import (
	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

// minSegmentMs is the shortest chapter taken as a whole episode (decision 1).
const minSegmentMs = 10 * 60 * 1000

// SegmentOf locates episode index (0-based, among the file's count episodes
// ordered by number) in a file shared by several episodes. It trusts the
// chapters only when there is one per episode, each at least 10 minutes, and
// together they tile the file from 0 to within a second of its duration;
// otherwise it returns nil. It never splits a file evenly (spec §3.8).
func SegmentOf(chapters []probe.Chapter, durationMs int64, index, count int) *contracts.Segment {
	if count < 2 || len(chapters) != count || index < 0 || index >= count || durationMs <= 0 {
		return nil
	}
	var at int64
	for _, c := range chapters {
		if c.StartMs != at || c.EndMs-c.StartMs < minSegmentMs {
			return nil
		}
		at = c.EndMs
	}
	if d := at - durationMs; d > 1000 || d < -1000 {
		return nil
	}
	return &contracts.Segment{StartMs: chapters[index].StartMs, EndMs: chapters[index].EndMs}
}

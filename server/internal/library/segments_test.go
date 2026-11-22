package library

import (
	"testing"

	"github.com/Lisimflix/lisimflix/server/internal/contracts"
	"github.com/Lisimflix/lisimflix/server/internal/probe"
)

func TestSegmentOf(t *testing.T) {
	two := []probe.Chapter{{StartMs: 0, EndMs: 1_500_000}, {StartMs: 1_500_000, EndMs: 3_000_000}}
	unequal := []probe.Chapter{{StartMs: 0, EndMs: 1_000_000}, {StartMs: 1_000_000, EndMs: 3_000_000}}
	seg := func(a, b int64) *contracts.Segment { return &contracts.Segment{StartMs: a, EndMs: b} }
	for _, c := range []struct {
		name     string
		chapters []probe.Chapter
		duration int64
		count    int
		want0    *contracts.Segment
		want1    *contracts.Segment
	}{
		{"tiles", two, 3_000_000, 2, seg(0, 1_500_000), seg(1_500_000, 3_000_000)},
		{"unequal lengths", unequal, 3_000_000, 2, seg(0, 1_000_000), seg(1_000_000, 3_000_000)},
		{"ends within a second", two, 3_000_500, 2, seg(0, 1_500_000), seg(1_500_000, 3_000_000)},
		{"three chapters for two", append(append([]probe.Chapter{}, unequal[0]), probe.Chapter{StartMs: 1_000_000, EndMs: 2_000_000},
			probe.Chapter{StartMs: 2_000_000, EndMs: 3_000_000}), 3_000_000, 2, nil, nil},
		{"10 s gap", []probe.Chapter{{StartMs: 0, EndMs: 1_500_000}, {StartMs: 1_510_000, EndMs: 3_000_000}}, 3_000_000, 2, nil, nil},
		{"starts at 5 s", []probe.Chapter{{StartMs: 5_000, EndMs: 1_500_000}, {StartMs: 1_500_000, EndMs: 3_000_000}}, 3_000_000, 2, nil, nil},
		{"ends 30 s short", two, 3_030_000, 2, nil, nil},
		{"chapter under 10 min", []probe.Chapter{{StartMs: 0, EndMs: 599_999}, {StartMs: 599_999, EndMs: 3_000_000}}, 3_000_000, 2, nil, nil},
		{"no chapters", nil, 3_000_000, 2, nil, nil},
		{"one episode", two[:1], 1_500_000, 1, nil, nil},
		{"no duration", two, 0, 2, nil, nil},
	} {
		got0, got1 := SegmentOf(c.chapters, c.duration, 0, c.count), SegmentOf(c.chapters, c.duration, 1, c.count)
		if !sameSegment(got0, c.want0) || !sameSegment(got1, c.want1) {
			t.Errorf("%s: got %v %v, want %v %v", c.name, got0, got1, c.want0, c.want1)
		}
	}
	// Never assume equal-length episodes (spec §3.8).
	if a, b := SegmentOf(nil, 3_000_000, 0, 2), SegmentOf(nil, 3_000_000, 1, 2); a != nil || b != nil {
		t.Fatalf("no chapters gave %v %v", a, b)
	}
	if SegmentOf(two, 3_000_000, 2, 2) != nil || SegmentOf(two, 3_000_000, -1, 2) != nil {
		t.Fatal("index out of range gave a segment")
	}
}

func sameSegment(a, b *contracts.Segment) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

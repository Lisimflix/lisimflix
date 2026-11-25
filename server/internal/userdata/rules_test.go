package userdata

import (
	"slices"
	"testing"
)

func TestMerge(t *testing.T) {
	want := []Span{{0, 30}, {40, 50}}
	got := Merge([]Span{{0, 10}, {5, 20}, {20, 30}, {40, 50}})
	if !slices.Equal(got, want) {
		t.Fatalf("Merge = %v, want %v", got, want)
	}
	got = Merge([]Span{{40, 50}, {20, 30}, {5, 20}, {0, 10}})
	if !slices.Equal(got, want) {
		t.Fatalf("Merge(unsorted) = %v, want %v", got, want)
	}
	if got := Merge([]Span{{5, 5}}); len(got) != 0 {
		t.Fatalf("Merge(empty span) = %v, want none", got)
	}
}

func TestAdvance(t *testing.T) {
	cases := []struct {
		prev, pos int64
		want      Span
		ok        bool
	}{
		{1000, 11000, Span{1000, 11000}, true},
		{1000, 31000, Span{1000, 31000}, true},
		{1000, 31001, Span{}, false},
		{1000, 1000, Span{}, false},
		{1000, 500, Span{}, false},
	}
	for _, c := range cases {
		got, ok := Advance(c.prev, c.pos)
		if got != c.want || ok != c.ok {
			t.Errorf("Advance(%d, %d) = %v, %v; want %v, %v", c.prev, c.pos, got, ok, c.want, c.ok)
		}
	}
}

func TestIsWatched(t *testing.T) {
	for _, c := range []struct {
		pos, cov, dur int64
		want          bool
	}{
		{95_000, 50_000, 100_000, true},
		{100_000, 100_000, 100_000, true},
		{94_999, 100_000, 100_000, false}, // position not past 95%
		{100_000, 49_999, 100_000, false}, // under half covered
		{1_000_000, 1_000_000, 0, false},
		{0, 0, 0, false},
		{1_000_000, 1_000_000, -5, false},
	} {
		if got := IsWatched(c.pos, c.cov, c.dur); got != c.want {
			t.Errorf("IsWatched(%d, %d, %d) = %v, want %v", c.pos, c.cov, c.dur, got, c.want)
		}
	}
}

func TestReplayCountsOnce(t *testing.T) {
	spans := []Span{{0, 10_000}, {5_000, 20_000}, {40_000, 50_000}}
	doubled := append(slices.Clone(spans), spans...)
	if a, b := Covered(Merge(doubled)), Covered(spans); a != b {
		t.Fatalf("replay covered %d, want %d", a, b)
	}
}

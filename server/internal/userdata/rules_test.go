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
	if IsWatched(89_999, 100_000) {
		t.Error("89,999 of 100,000 watched")
	}
	if !IsWatched(90_000, 100_000) {
		t.Error("90,000 of 100,000 not watched")
	}
	if IsWatched(1_000_000, 0) || IsWatched(0, 0) {
		t.Error("zero duration watched")
	}
	if IsWatched(1_000_000, -5) {
		t.Error("negative duration watched")
	}
}

func TestIsPlayed(t *testing.T) {
	if IsPlayed(29_999) {
		t.Error("29,999 played")
	}
	if !IsPlayed(30_000) {
		t.Error("30,000 not played")
	}
}

func TestReplayCountsOnce(t *testing.T) {
	spans := []Span{{0, 10_000}, {5_000, 20_000}, {40_000, 50_000}}
	doubled := append(slices.Clone(spans), spans...)
	if a, b := Covered(Merge(doubled)), Covered(spans); a != b {
		t.Fatalf("replay covered %d, want %d", a, b)
	}
}

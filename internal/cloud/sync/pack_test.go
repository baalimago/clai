package sync

import (
	"slices"
	"testing"
)

func TestPack_smallFilesShareRequestsAndLargeOnesGoAlone(t *testing.T) {
	var sizes []int64
	for i := range 600 {
		sizes = append(sizes, int64(1+i%7))
	}
	sizes = append(sizes, 900, 5000, 700)
	packs := pack(sizes, func(n int64) int64 { return n }, packLimits{count: 250, bytes: 1 << 20, alone: 800})
	var shape []int
	var totals []int64
	for _, p := range packs {
		shape = append(shape, len(p))
		var n int64
		for _, s := range p {
			n += s
		}
		totals = append(totals, -n)
	}
	slices.Sort(shape)
	if !slices.Equal(shape, []int{1, 1, 101, 250, 250}) {
		t.Fatalf("pack sizes = %v, want the two large files alone, then 250, 250, 101", shape)
	}
	if !slices.IsSorted(totals) || packs[0][0] != 5000 {
		t.Fatalf("pack bytes = %v, want the largest first", totals)
	}
	for _, p := range packs {
		if len(p) > 1 && !slices.IsSorted(p) {
			t.Fatalf("pack %v is not smallest first", p)
		}
	}
}

func TestPack_capsBytes(t *testing.T) {
	packs := pack([]int64{40, 40, 40, 40, 40}, func(n int64) int64 { return n }, packLimits{count: 250, bytes: 100, alone: 100})
	if len(packs) != 3 {
		t.Fatalf("%d packs, want 3 under a 100-byte cap: %v", len(packs), packs)
	}
}

func TestPack_nothingIsNoPacks(t *testing.T) {
	if packs := pack(nil, func(n int64) int64 { return n }, packLimits{count: 1, bytes: 1, alone: 1}); len(packs) != 0 {
		t.Fatalf("packs = %v, want none", packs)
	}
}

func TestLimits_zeroFieldsTakeTheProductionConstants(t *testing.T) {
	if got, want := (limits{}).upload(), (packLimits{count: batchMaxBlobs, bytes: batchMaxBytes, alone: batchBlobMax}); got != want {
		t.Fatalf("upload limits = %+v, want %+v", got, want)
	}
	if got, want := (limits{}).fetch(), (packLimits{count: fetchMaxBlobs, bytes: fetchMaxBytes, alone: fetchAlone}); got != want {
		t.Fatalf("fetch limits = %+v, want %+v", got, want)
	}
	if got := (limits{fetchBlobs: 5}).fetch().count; got != 5 {
		t.Fatalf("fetch count = %d, want the override 5", got)
	}
}

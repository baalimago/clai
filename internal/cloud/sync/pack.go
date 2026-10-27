package sync

import (
	"cmp"
	"slices"
)

// packLimits bound one request: at most count files and bytes bytes, and
// a file larger than alone gets a request of its own.
type packLimits struct {
	count int
	bytes int64
	alone int64
}

// pack groups items into requests, smallest first so many small files
// share one, and orders the requests largest first so the big ones start
// while the small ones fill the other workers.
func pack[T any](items []T, size func(T) int64, l packLimits) [][]T {
	sorted := slices.Clone(items)
	slices.SortStableFunc(sorted, func(a, b T) int { return cmp.Compare(size(a), size(b)) })
	var packs [][]T
	var cur []T
	var curBytes int64
	for _, it := range sorted {
		n := size(it)
		if n > l.alone {
			packs = append(packs, []T{it})
			continue
		}
		if len(cur) == l.count || (len(cur) > 0 && curBytes+n > l.bytes) {
			packs = append(packs, cur)
			cur, curBytes = nil, 0
		}
		cur = append(cur, it)
		curBytes += n
	}
	if len(cur) > 0 {
		packs = append(packs, cur)
	}
	total := func(p []T) int64 {
		var n int64
		for _, it := range p {
			n += size(it)
		}
		return n
	}
	slices.SortStableFunc(packs, func(a, b []T) int { return cmp.Compare(total(b), total(a)) })
	return packs
}

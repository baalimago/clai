package jsonltest

import (
	"io/fs"
	"os"
	"sort"
	"sync"
)

// FSCounts records how often a CountingFS was asked to open or stat each path.
// It is safe for concurrent use, so a parallel scan can be counted.
type FSCounts struct {
	mu    sync.Mutex
	opens map[string]int
	stats map[string]int
}

func (c *FSCounts) record(m map[string]int, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m[name]++
}

func (c *FSCounts) Opens(name string) int { return c.count(c.opens, name) }

func (c *FSCounts) Stats(name string) int { return c.count(c.stats, name) }

func (c *FSCounts) count(m map[string]int, name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return m[name]
}

func (c *FSCounts) TotalOpens() int { return c.total(c.opens) }

func (c *FSCounts) TotalStats() int { return c.total(c.stats) }

func (c *FSCounts) total(m map[string]int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (c *FSCounts) OpenedPaths() []string { return c.paths(c.opens) }

func (c *FSCounts) StattedPaths() []string { return c.paths(c.stats) }

func (c *FSCounts) paths(m map[string]int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// countingFS implements fs.StatFS on purpose: fs.Stat falls back to Open plus
// File.Stat on a filesystem that does not, which would make a "never opened"
// assertion fail against correct code.
type countingFS struct {
	inner  fs.FS
	counts *FSCounts
}

// CountingFS returns a filesystem rooted at root and the counts it records.
// Paths are counted as the caller spells them, which through vendors.OpenAbs
// means the absolute path with its leading separator trimmed.
func CountingFS(root string) (fs.FS, *FSCounts) {
	counts := &FSCounts{opens: map[string]int{}, stats: map[string]int{}}
	return &countingFS{inner: os.DirFS(root), counts: counts}, counts
}

func (c *countingFS) Open(name string) (fs.File, error) {
	c.counts.record(c.counts.opens, name)
	return c.inner.Open(name)
}

func (c *countingFS) Stat(name string) (fs.FileInfo, error) {
	c.counts.record(c.counts.stats, name)
	return fs.Stat(c.inner, name)
}

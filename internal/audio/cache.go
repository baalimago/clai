package audio

import "sync"

// requestCache returns raw segments for an identical request within one
// run. Hits are reported, never counted as requests.
type requestCache struct {
	mu      sync.Mutex
	entries map[cacheKey][]Segment
	hits    int
}

type cacheKey struct {
	requestHash string
	model       string
	endpoint    string
	options     string
}

func newRequestCache() *requestCache {
	return &requestCache{entries: map[cacheKey][]Segment{}}
}

func (c *requestCache) get(k cacheKey) ([]Segment, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	segs, ok := c.entries[k]
	if ok {
		c.hits++
	}
	return segs, ok
}

func (c *requestCache) put(k cacheKey, segs []Segment) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[k] = segs
}

func (c *requestCache) hitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

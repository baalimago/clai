package audio

import (
	"context"
	"testing"
	"time"
)

func TestCacheHitSkipsTranscriber(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting()}
	c, asm, _ := newCoordinator(p)
	c.cache = newRequestCache()
	st := &chunkState{index: 0, core: SourceInterval{Start: 0, End: 450 * time.Second}}
	request := c.requestFor(asm, st)
	samples := []SampleClip{{ID: "speaker-1", Start: 10 * time.Second, End: 15 * time.Second}}
	if _, _, err := request(context.Background(), samples, st.core); err != nil {
		t.Fatal(err)
	}
	m, segs, err := request(context.Background(), samples, st.core)
	if err != nil {
		t.Fatal(err)
	}
	if p.requests() != 1 {
		t.Errorf("expected transcriber called once, got %v", p.requests())
	}
	if c.cache.hitCount() != 1 || len(segs) == 0 || m == nil {
		t.Errorf("expected one cache hit with segments, got hits %v segs %v", c.cache.hitCount(), len(segs))
	}
	if st.consumed != 2 || c.Totals().Requests != 1 {
		t.Errorf("cache hits count against the chunk limit but not as requests: consumed %v totals %+v", st.consumed, c.Totals())
	}
}

func TestCacheKeyCoversRequestIdentity(t *testing.T) {
	base := cacheKey{requestHash: "h", model: "m", endpoint: "e", options: "o"}
	cache := newRequestCache()
	cache.put(base, []Segment{{Text: "x"}})
	variants := []cacheKey{
		{requestHash: "h2", model: "m", endpoint: "e", options: "o"},
		{requestHash: "h", model: "m2", endpoint: "e", options: "o"},
		{requestHash: "h", model: "m", endpoint: "e2", options: "o"},
		{requestHash: "h", model: "m", endpoint: "e", options: "o2"},
	}
	for _, v := range variants {
		if _, ok := cache.get(v); ok {
			t.Errorf("key %+v must not hit the entry for %+v", v, base)
		}
	}
	if _, ok := cache.get(base); !ok || cache.hitCount() != 1 {
		t.Errorf("expected the identical key to hit once, hits %v", cache.hitCount())
	}
}

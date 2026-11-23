package audio

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func TestCacheHitSkipsTranscriber(t *testing.T) {
	p := &voiceProvider{truth: sixVoiceMeeting()}
	c, asm, _ := newCoordinator(p)
	c.cache = newRequestCache()
	st := &chunkState{index: 0, core: SourceInterval{Start: 0, End: 450 * time.Second}}
	var log bytes.Buffer
	ctx := procenv.Diagnostics(context.Background(), &log, diagnostic.LevelDebug)
	request := func(ctx context.Context, kind RequestKind, samples []SampleClip, core SourceInterval) (*RequestManifest, []Segment, error) {
		return c.request(ctx, asm, st, c.logFor(ctx, st), kind, samples, core)
	}
	samples := []SampleClip{{ID: "speaker-1", Start: 10 * time.Second, End: 15 * time.Second}}
	if _, _, err := request(ctx, RequestLabelling, samples, st.core); err != nil {
		t.Fatal(err)
	}
	m, segs, err := request(ctx, RequestLabelling, samples, st.core)
	if err != nil {
		t.Fatal(err)
	}
	assertRecords(t, log.String(),
		`request chunk=1 attempt=1 kind=labelling core=0\.0-450\.0 seconds=456\.0 bytes=0 cache=miss samples=1 prefix=speaker-1@9\.8-15\.2>0\.0-5\.5$`,
		`request chunk=1 attempt=2 kind=labelling .* cache=hit samples=1 `)
	if p.requests() != 1 {
		t.Errorf("expected transcriber called once, got %v", p.requests())
	}
	if c.cache.hitCount() != 1 || len(segs) == 0 || m == nil {
		t.Errorf("expected one cache hit with segments, got hits %v segs %v", c.cache.hitCount(), len(segs))
	}
	if st.consumed.Load() != 2 || c.Totals().Requests != 1 {
		t.Errorf("a cache hit is a request of the chunk but no upload: consumed %v totals %+v", st.consumed.Load(), c.Totals())
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

package audio

import (
	"context"
	"sync"
	"testing"
	"time"
)

var (
	chunk1 = SourceInterval{Start: 0, End: 600 * time.Second}
	chunk2 = SourceInterval{Start: 600 * time.Second, End: 1200 * time.Second}
)

func newDiscovery(p *voiceProvider, max int) *Discovery {
	return &Discovery{Registry: NewSpeakerRegistry(max), Transcribe: p.transcribe}
}

// assertOneIDPerVoice checks every truth voice in core maps to exactly one
// global ID and no two voices share one.
func assertOneIDPerVoice(t *testing.T, m Mapping, truth []voiceSpan, core SourceInterval, voices ...string) map[string]string {
	t.Helper()
	ids := identityOf(m, truth, core)
	got := map[string]string{}
	for _, v := range voices {
		if len(ids[v]) != 1 {
			t.Errorf("voice %v maps to %v, expected one global ID", v, ids[v])
			continue
		}
		for id := range ids[v] {
			got[v] = id
		}
	}
	seen := map[string]string{}
	for v, id := range got {
		if other, dup := seen[id]; dup {
			t.Errorf("voices %v and %v merged onto %v", other, v, id)
		}
		seen[id] = v
	}
	return got
}

func TestBootstrapPermutedLabels(t *testing.T) {
	p := &voiceProvider{truth: threeVoiceMeeting()}
	d := newDiscovery(p, 8)
	m, rep, err := d.Bootstrap(context.Background(), chunk1)
	if err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	if p.requests() != 2 {
		t.Errorf("expected two bootstrap requests, got %v", p.requests())
	}
	if len(p.calls[0].Samples) != 0 || len(p.calls[1].Samples) != 3 {
		t.Errorf("expected an uncalibrated pass then three candidates, got %v and %v samples", len(p.calls[0].Samples), len(p.calls[1].Samples))
	}
	snap := d.Registry.Snapshot()
	if snap.Version != 3 || len(snap.Speakers) != 3 {
		t.Fatalf("expected three speakers at version 3, got %+v", snap)
	}
	assertOneIDPerVoice(t, m, p.truth, chunk1, "v1", "v2", "v3")
	if len(m.Unmapped) != 0 || len(rep.Promoted) != 3 {
		t.Errorf("expected everything mapped and three promotions, got unmapped %+v promoted %v", m.Unmapped, rep.Promoted)
	}
	for _, sp := range snap.Speakers {
		if sp.Sample.End-sp.Sample.Start < sampleMin || len(sp.Ranges) == 0 {
			t.Errorf("speaker %v lacks a proper sample or verified ranges: %+v", sp.ID, sp)
		}
	}
}

func TestDiscoveryFindsTwoNewSpeakersInOneChunk(t *testing.T) {
	p := &voiceProvider{truth: withLaterVoices(threeVoiceMeeting())}
	d := newDiscovery(p, 8)
	if _, _, err := d.Bootstrap(context.Background(), chunk1); err != nil {
		t.Fatal(err)
	}
	before := p.requests()
	snap := d.Registry.Snapshot()
	first, err := d.Pass(context.Background(), chunk2, snap)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(first.MaterialUnmapped()); n != 2 {
		t.Fatalf("expected two unmapped material labels after the first pass, got %+v", first.Unmapped)
	}
	m, rep, err := d.Resolve(context.Background(), chunk2, first, snap)
	if err != nil {
		t.Fatal(err)
	}
	if p.requests()-before != 2 {
		t.Errorf("expected first pass plus one retranscription, got %v requests", p.requests()-before)
	}
	after := d.Registry.Snapshot()
	if after.Version != snap.Version+2 || len(after.Speakers) != 5 {
		t.Errorf("expected two promotions, got version %v→%v with %v speakers", snap.Version, after.Version, len(after.Speakers))
	}
	if rep.Attempts != 1 || len(rep.Promoted) != 2 {
		t.Errorf("unexpected report %+v", rep)
	}
	assertOneIDPerVoice(t, m, p.truth, chunk2, "v1", "v4", "v5")
	if len(m.MaterialUnmapped()) != 0 {
		t.Errorf("expected no material unmapped labels, got %+v", m.Unmapped)
	}
}

func TestSpuriousLabelCollapsesOntoKnownVoice(t *testing.T) {
	p := &voiceProvider{truth: withLaterVoices(threeVoiceMeeting()), splitVoice: map[int]string{3: "v1"}}
	d := newDiscovery(p, 8)
	if _, _, err := d.Bootstrap(context.Background(), chunk1); err != nil {
		t.Fatal(err)
	}
	snap := d.Registry.Snapshot()
	first, err := d.Pass(context.Background(), chunk2, snap) // request 3: v1 split into two labels
	if err != nil {
		t.Fatal(err)
	}
	m, rep, err := d.Resolve(context.Background(), chunk2, first, snap)
	if err != nil {
		t.Fatal(err)
	}
	after := d.Registry.Snapshot()
	if len(after.Speakers) != 5 {
		t.Fatalf("expected v4 and v5 promoted and no seventh identity for the split, got %v speakers", len(after.Speakers))
	}
	ids := assertOneIDPerVoice(t, m, p.truth, chunk2, "v1", "v4", "v5")
	if ids["v1"] != snap.Speakers[0].ID && ids["v1"] != snap.Speakers[1].ID && ids["v1"] != snap.Speakers[2].ID {
		t.Errorf("v1 should map to a bootstrap speaker, got %v", ids["v1"])
	}
	collapsed := 0
	for _, r := range rep.Rejected {
		if r.Reason == reasonCollapsedOntoKnown {
			collapsed++
		}
	}
	if collapsed != 1 {
		t.Errorf("expected one candidate rejected as collapsed onto a known voice, got %+v", rep.Rejected)
	}
}

func TestFailedSampleTriggersReplacementAndRetry(t *testing.T) {
	p := &voiceProvider{truth: withLaterVoices(threeVoiceMeeting()), mixSample: map[int]string{3: "speaker-2"}}
	d := newDiscovery(p, 8)
	if _, _, err := d.Bootstrap(context.Background(), chunk1); err != nil {
		t.Fatal(err)
	}
	snap := d.Registry.Snapshot()
	oldSample := snap.Speakers[1].Sample
	first, err := d.Pass(context.Background(), chunk1, snap)
	if err != nil {
		t.Fatal(err)
	}
	if f := first.Failed(); len(f) != 1 || f[0].ID != "speaker-2" || f[0].Status != SampleMixed {
		t.Fatalf("expected speaker-2 mixed, got %+v", first.Samples)
	}
	before := p.requests()
	m, applied, err := d.Recover(context.Background(), chunk1, first, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0] != "speaker-2" {
		t.Errorf("expected one replacement for speaker-2, got %v", applied)
	}
	if p.requests()-before != 1 {
		t.Errorf("expected exactly one retry request, got %v", p.requests()-before)
	}
	after := d.Registry.Snapshot()
	if after.Speakers[1].Replacements != 1 || after.Speakers[1].Sample == oldSample {
		t.Errorf("expected replaced sample, got %+v", after.Speakers[1])
	}
	if len(m.Failed()) != 0 {
		t.Errorf("expected retry to map cleanly, got %+v", m.Samples)
	}
	assertOneIDPerVoice(t, m, p.truth, chunk1, "v1", "v2", "v3")
}

func TestConcurrentWorkersShareOneNewVoice(t *testing.T) {
	truth := withLaterVoices(threeVoiceMeeting())
	chunk3 := SourceInterval{Start: 1200 * time.Second, End: 1800 * time.Second}
	for t0 := 1200.0; t0 < 1800; t0 += 60 {
		truth = append(truth, voiceSpan{t0, t0 + 25, "v1"}, voiceSpan{t0 + 26, t0 + 55, "v4"})
	}
	p := &voiceProvider{truth: truth}
	d := newDiscovery(p, 8)
	if _, _, err := d.Bootstrap(context.Background(), chunk1); err != nil {
		t.Fatal(err)
	}
	snap := d.Registry.Snapshot()
	firstB, err := d.Pass(context.Background(), chunk2, snap)
	if err != nil {
		t.Fatal(err)
	}
	firstC, err := d.Pass(context.Background(), chunk3, snap)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]Mapping, 2)
	errs := make([]error, 2)
	for i, job := range []struct {
		core  SourceInterval
		first Mapping
	}{{chunk2, firstB}, {chunk3, firstC}} {
		wg.Go(func() {
			results[i], _, errs[i] = d.Resolve(context.Background(), job.core, job.first, snap)
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	after := d.Registry.Snapshot()
	if len(after.Speakers) != 5 {
		t.Fatalf("expected v4 and v5 once each (5 speakers), got %v", len(after.Speakers))
	}
	idB := assertOneIDPerVoice(t, results[0], truth, chunk2, "v1", "v4", "v5")
	idC := assertOneIDPerVoice(t, results[1], truth, chunk3, "v1", "v4")
	if idB["v4"] != idC["v4"] {
		t.Errorf("v4 got two IDs across workers: %v vs %v", idB["v4"], idC["v4"])
	}
}

func TestConcurrentSampleFailureConsumesOneReplacement(t *testing.T) {
	p := &voiceProvider{truth: withLaterVoices(threeVoiceMeeting()), mixSample: map[int]string{3: "speaker-2", 4: "speaker-2"}}
	d := newDiscovery(p, 8)
	if _, _, err := d.Bootstrap(context.Background(), chunk1); err != nil {
		t.Fatal(err)
	}
	snap := d.Registry.Snapshot()
	firstA, _ := d.Pass(context.Background(), chunk1, snap)
	firstB, _ := d.Pass(context.Background(), chunk2, snap)
	if len(firstA.Failed()) != 1 || len(firstB.Failed()) != 1 {
		t.Fatalf("expected both passes to report the failed sample, got %+v / %+v", firstA.Samples, firstB.Samples)
	}
	var wg sync.WaitGroup
	applied := make([][]string, 2)
	for i, job := range []struct {
		core  SourceInterval
		first Mapping
	}{{chunk1, firstA}, {chunk2, firstB}} {
		wg.Go(func() {
			_, applied[i], _ = d.Recover(context.Background(), job.core, job.first, snap)
		})
	}
	wg.Wait()
	if n := len(applied[0]) + len(applied[1]); n != 1 {
		t.Errorf("expected exactly one applied replacement across workers, got %v", applied)
	}
	if r := d.Registry.Snapshot().Speakers[1].Replacements; r != 1 {
		t.Errorf("expected replacement count 1, got %v", r)
	}
}

func TestDiscoveryStopsAfterTwoIterations(t *testing.T) {
	p := &voiceProvider{truth: withLaterVoices(threeVoiceMeeting()), mixAllCandidates: true}
	d := newDiscovery(p, 8)
	// Seed v1 directly so bootstrap's candidate handling is not under test here
	_ = d.Registry.Discover(context.Background(), func(tx *RegistryTx) error {
		_, _, _ = tx.AddSpeaker(SampleClip{Start: 0, End: 8 * time.Second}, []SourceInterval{{Start: 0, End: 30 * time.Second}})
		return nil
	})
	snap := d.Registry.Snapshot()
	first, err := d.Pass(context.Background(), chunk2, snap)
	if err != nil {
		t.Fatal(err)
	}
	before := p.requests()
	m, rep, err := d.Resolve(context.Background(), chunk2, first, snap)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Attempts != discoveryIterations || p.requests()-before != discoveryIterations {
		t.Errorf("expected exactly %v iterations, got attempts %v requests %v", discoveryIterations, rep.Attempts, p.requests()-before)
	}
	if len(m.MaterialUnmapped()) != 2 {
		t.Errorf("expected both new voices still unmapped, got %+v", m.Unmapped)
	}
	if len(d.Registry.Snapshot().Speakers) != 1 {
		t.Errorf("mixed candidates must not be promoted, got %v speakers", len(d.Registry.Snapshot().Speakers))
	}
	if len(rep.Rejected) == 0 {
		t.Error("expected rejected candidates with reasons")
	}
}

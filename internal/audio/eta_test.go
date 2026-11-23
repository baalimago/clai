package audio

import (
	"testing"
	"time"
)

// L2.8: per unfinished chunk the requests left of its stages, sent workers
// at a time, plus the merge's links at the measured rate.
func TestEtaEstimateCountsStagesAndLinks(t *testing.T) {
	core, prefix := 20*time.Minute, 88*time.Second
	e := newEtaModel(4, prefix)
	t0 := time.Date(2026, 11, 22, 14, 0, 0, 0, time.UTC)
	at := func(rate float64, d time.Duration) time.Duration { return time.Duration(float64(d) * rate) }
	// Nothing sent: four surveys and four labelling requests on four
	// workers, three links; the worst case adds a retry per chunk.
	chunks := []etaChunk{{core: core}, {core: core}, {core: core}, {core: core}}
	survey, labelled := at(defaultProviderRate, core), at(defaultProviderRate, core+prefix)
	link := at(defaultProviderRate, prefix)
	typical, worst := e.estimate(chunks, 3, t0)
	if want := (4*(survey+labelled)/4 + 3*link).Round(time.Second); typical != want {
		t.Errorf("typical %v, want %v", typical, want)
	}
	if want := (4*(survey+2*labelled)/4 + 3*link).Round(time.Second); worst != want {
		t.Errorf("worst %v, want %v", worst, want)
	}
	// One survey measured at 0.5× realtime: the rate follows it
	e.started(0, core, t0)
	e.completed(0, t0.Add(10*time.Minute))
	if r := e.rate(); r < 0.49 || r > 0.51 {
		t.Errorf("rate %v, want 0.5", r)
	}
	// A merge request has a key of its own and moves the rate too
	e.started(-1, prefix, t0)
	e.completed(-1, t0.Add(prefix/2))
	e.completed(-7, t0)
	if r := e.rate(); r < 0.49 || r > 0.51 {
		t.Errorf("rate %v after a merge request, want 0.5", r)
	}
	// Chunk 0 done; chunk 1 labelling in flight for 4 minutes; chunks 2 and
	// 3 surveyed; the merge is over
	e.started(1, core+prefix, t0.Add(10*time.Minute))
	chunks = []etaChunk{{core: core, consumed: 2, done: true}, {core: core, consumed: 2, active: true}, {core: core, consumed: 1}, {core: core, consumed: 1}}
	typical, worst = e.estimate(chunks, 0, t0.Add(14*time.Minute))
	perNow := at(0.5, core+prefix)
	left := perNow - 4*time.Minute
	if want := ((left + 2*perNow) / 4).Round(time.Second); (typical - want).Abs() > time.Second {
		t.Errorf("typical %v, want about %v", typical, want)
	}
	if want := ((left + 5*perNow) / 4).Round(time.Second); (worst - want).Abs() > time.Second {
		t.Errorf("worst %v, want about %v", worst, want)
	}
	all := []etaChunk{{core: core, done: true}, {core: core, done: true}}
	typical, worst = e.estimate(all, 0, t0)
	if etaText(typical, worst) != "finishing" {
		t.Errorf("expected finishing, got %q", etaText(typical, worst))
	}
	if got := etaText(22*time.Minute+10*time.Second, 61*time.Minute); got != "≈22m left (≤1h1m)" {
		t.Errorf("unexpected text %q", got)
	}
	var none *etaModel
	none.started(0, core, t0)
	none.completed(0, t0)
}

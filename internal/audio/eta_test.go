package audio

import (
	"testing"
	"time"
)

func TestEtaEstimateUsesMeasuredRateAndRemainingRequests(t *testing.T) {
	core := 20 * time.Minute
	e := newEtaModel(3, 4, 90*time.Second)
	t0 := time.Date(2026, 9, 2, 14, 0, 0, 0, time.UTC)
	// Before any completion: default rate, four chunks, nothing started:
	// typical = 4 passes / 3 workers + 4 discovery requests, serialized
	chunks := []etaChunk{{core: core}, {core: core}, {core: core}, {core: core}}
	per := time.Duration(float64(core+90*time.Second) * defaultProviderRate)
	typical, worst := e.estimate(chunks, t0)
	if want := (4*per/3 + 4*per).Round(time.Second); typical != want {
		t.Errorf("typical %v, want %v", typical, want)
	}
	if want := (4*per/3 + 12*per).Round(time.Second); worst != want {
		t.Errorf("worst %v, want %v", worst, want)
	}
	// One request measured at 0.5× realtime: rate follows the measurement
	e.started(0, core, t0)
	e.completed(0, t0.Add(10*time.Minute))
	if r := e.rate(); r < 0.49 || r > 0.51 {
		t.Errorf("rate %v, want 0.5", r)
	}
	// Chunk 0 done after two requests; chunk 1 in flight for 4 of its expected 10.75 min
	e.started(1, core+90*time.Second, t0.Add(10*time.Minute))
	chunks = []etaChunk{{core: core, consumed: 2, done: true}, {core: core, consumed: 1, active: true}, {core: core}, {core: core}}
	typical, worst = e.estimate(chunks, t0.Add(14*time.Minute))
	perNow := time.Duration(float64(core+90*time.Second) * 0.5)
	left := perNow - 4*time.Minute
	wantTypical := ((left + 2*perNow) / 3) + (1+2)*perNow // chunk 1: one discovery; chunks 2,3: one each
	if diff := (typical - wantTypical.Round(time.Second)).Abs(); diff > time.Second {
		t.Errorf("typical %v, want about %v", typical, wantTypical)
	}
	if worst <= typical {
		t.Errorf("worst %v must exceed typical %v", worst, typical)
	}
	// Everything done: finishing
	all := []etaChunk{{core: core, done: true}, {core: core, done: true}}
	typical, worst = e.estimate(all, t0)
	if etaText(typical, worst) != "finishing" {
		t.Errorf("expected finishing, got %q", etaText(typical, worst))
	}
	if got := etaText(22*time.Minute+10*time.Second, 61*time.Minute); got != "≈22m left (≤1h1m)" {
		t.Errorf("unexpected text %q", got)
	}
}

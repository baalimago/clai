package audio

import (
	"fmt"
	"sync"
	"time"
)

// defaultProviderRate is the wall time per second of uploaded audio assumed
// before any request has completed (Phase 0 measured about 0.28).
const defaultProviderRate = 0.3

// etaModel estimates remaining wall time from the measured provider rate:
// each unfinished chunk's requests left of its stages (survey, labelling,
// and in the worst case a retry), sent workers at a time, plus the merge's
// links, whose two requests run at once and one link after the other.
type etaModel struct {
	mu        sync.Mutex
	wall      time.Duration // wall time of completed requests
	audio     time.Duration // audio of completed requests
	inflight  map[int]inflightRequest
	workers   int
	prefixEst time.Duration
}

type inflightRequest struct {
	audio   time.Duration
	started time.Time
}

type etaChunk struct {
	core     time.Duration
	consumed int
	done     bool
	active   bool
}

// newEtaModel takes the requests a stage sends at once and the prefix a
// labelled request carries at most.
func newEtaModel(workers int, prefix time.Duration) *etaModel {
	return &etaModel{inflight: map[int]inflightRequest{}, workers: max(workers, 1), prefixEst: prefix}
}

// started and completed key a chunk's request by its index and a merge
// request by a negative key of its own.
func (e *etaModel) started(key int, audio time.Duration, at time.Time) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inflight[key] = inflightRequest{audio: audio, started: at}
}

func (e *etaModel) completed(key int, at time.Time) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.inflight[key]
	if !ok {
		return
	}
	delete(e.inflight, key)
	e.wall += at.Sub(r.started)
	e.audio += r.audio
}

// rate is wall seconds per audio second, measured or default.
func (e *etaModel) rate() float64 {
	if e.audio <= 0 {
		return defaultProviderRate
	}
	return float64(e.wall) / float64(e.audio)
}

// estimate returns the typical and worst-case remaining wall time with
// links merge links still to send. Typical is no retry; worst is a retry
// for every chunk.
func (e *etaModel) estimate(chunks []etaChunk, links int, now time.Time) (typical, worst time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rate := e.rate()
	wall := func(audio time.Duration) time.Duration { return time.Duration(float64(audio) * rate) }
	var stages, worstStages time.Duration
	for i, c := range chunks {
		if c.done {
			continue
		}
		if r, ok := e.inflight[i]; ok {
			left := max(0, wall(r.audio)-now.Sub(r.started))
			stages += left
			worstStages += left
		}
		if c.consumed == 0 {
			stages += wall(c.core)
			worstStages += wall(c.core)
		}
		if c.consumed <= 1 {
			stages += wall(c.core + e.prefixEst)
			worstStages += wall(c.core + e.prefixEst)
		}
		if c.consumed <= 2 {
			worstStages += wall(c.core + e.prefixEst)
		}
	}
	merge := time.Duration(links) * wall(e.prefixEst)
	typical = stages/time.Duration(e.workers) + merge
	worst = worstStages/time.Duration(e.workers) + merge
	return typical.Round(time.Second), worst.Round(time.Second)
}

// etaText renders the estimate for the footer.
func etaText(typical, worst time.Duration) string {
	if typical <= 0 && worst <= 0 {
		return "finishing"
	}
	return fmt.Sprintf("≈%v left (≤%v)", shortDuration(typical), shortDuration(worst))
}

// shortDuration renders minutes-level precision: 1h1m, 22m, 45s.
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	}
	d = d.Round(time.Minute)
	h, m := int(d.Hours()), int(d.Minutes())%60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

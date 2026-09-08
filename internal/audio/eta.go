package audio

import (
	"fmt"
	"sync"
	"time"
)

// defaultProviderRate is the wall time per second of uploaded audio assumed
// before any request has completed (Phase 0 measured about 0.28).
const defaultProviderRate = 0.3

// etaModel estimates remaining wall time from the measured provider rate
// and the requests each unfinished chunk still needs. Discovery requests
// run one chunk at a time, passes run on the worker pool.
type etaModel struct {
	mu        sync.Mutex
	wall      time.Duration // wall time of completed requests
	audio     time.Duration // audio of completed requests
	inflight  map[int]inflightRequest
	workers   int
	limit     int
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

func newEtaModel(workers, limit int, prefix time.Duration) *etaModel {
	return &etaModel{inflight: map[int]inflightRequest{}, workers: max(workers, 1), limit: max(limit, 1), prefixEst: prefix}
}

func (e *etaModel) started(chunk int, audio time.Duration, at time.Time) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inflight[chunk] = inflightRequest{audio: audio, started: at}
}

func (e *etaModel) completed(chunk int, at time.Time) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.inflight[chunk]
	if !ok {
		return
	}
	delete(e.inflight, chunk)
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

// estimate returns the typical and worst-case remaining wall time. Typical
// assumes each unfinished chunk needs one more request after its current
// or first pass (one discovery iteration, the common case); worst assumes
// every chunk uses its full request limit.
func (e *etaModel) estimate(chunks []etaChunk, now time.Time) (typical, worst time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rate := e.rate()
	perRequest := func(core time.Duration) time.Duration {
		return time.Duration(float64(core+e.prefixEst) * rate)
	}
	var passes, discovery, worstPasses, worstDiscovery time.Duration
	for i, c := range chunks {
		if c.done {
			continue
		}
		remainingTypical := max(0, 2-c.consumed)
		remainingWorst := max(0, e.limit-c.consumed)
		if r, ok := e.inflight[i]; ok {
			left := max(0, time.Duration(float64(r.audio)*rate)-now.Sub(r.started))
			passes += left
			worstPasses += left
		} else if !c.active && remainingTypical > 0 {
			passes += perRequest(c.core)
			remainingTypical--
			worstPasses += perRequest(c.core)
			remainingWorst--
		}
		discovery += time.Duration(remainingTypical) * perRequest(c.core)
		worstDiscovery += time.Duration(remainingWorst) * perRequest(c.core)
	}
	typical = passes/time.Duration(e.workers) + discovery
	worst = worstPasses/time.Duration(e.workers) + worstDiscovery
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

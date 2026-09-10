package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/utils"
)

const defaultMaxRequestsPerChunk = 4

// RequestAssembler is the assembler seam the coordinator drives.
type RequestAssembler interface {
	Assemble(ctx context.Context, req AssembleRequest) (*RequestManifest, error)
	Remove(m *RequestManifest) error
	Close() error
}

// RequestTranscriber transcribes one assembled request file.
type RequestTranscriber interface {
	TranscribeRequest(ctx context.Context, m *RequestManifest) ([]Segment, error)
}

// FileTranscriber adapts the vendor Transcriber (file path in) to requests.
type FileTranscriber struct{ Transcriber }

func (f FileTranscriber) TranscribeRequest(ctx context.Context, m *RequestManifest) ([]Segment, error) {
	return f.Transcribe(ctx, m.FilePath)
}

// RequestLimitError is the per-chunk safety stop.
type RequestLimitError struct {
	Chunk      int
	Interval   SourceInterval
	Limit      int
	Consumed   int
	Unresolved []string
}

func (e *RequestLimitError) Error() string {
	return fmt.Sprintf("chunk %v (%v–%v) reached max-requests-per-chunk %v after %v requests; unresolved labels: %v",
		e.Chunk+1, e.Interval.Start, e.Interval.End, e.Limit, e.Consumed, strings.Join(e.Unresolved, ", "))
}

// UnresolvedError is the strict-speakers failure.
type UnresolvedError struct {
	Chunk    int
	Interval SourceInterval
	Labels   []UnmappedLabel
	Rejected []CandidateRejection
	Attempts int
	Requests int
}

func (e *UnresolvedError) Error() string {
	var labels, rejected []string
	for _, u := range e.Labels {
		labels = append(labels, fmt.Sprintf("%v (%v speech, %v)", u.Label, u.Speech.Round(time.Millisecond), u.Reason))
	}
	for _, r := range e.Rejected {
		rejected = append(rejected, fmt.Sprintf("%v@%v: %v", r.Label, r.Clip.Start.Round(time.Millisecond), r.Reason))
	}
	msg := fmt.Sprintf("strict-speakers: chunk %v (%v–%v) has unresolved material speaker labels after %v discovery attempts and %v requests: %v",
		e.Chunk+1, e.Interval.Start, e.Interval.End, e.Attempts, e.Requests, strings.Join(labels, "; "))
	if len(rejected) > 0 {
		msg += "; candidates rejected: " + strings.Join(rejected, ", ")
	}
	return msg
}

// Totals is the run summary written to stderr.
type Totals struct {
	Requests  int
	Retries   int
	CacheHits int
	Uploaded  time.Duration
	Unknown   time.Duration
	Cores     int
}

// Coordinator drives planning, assembly, mapping, discovery, and stitching
// for one oversized diarized recording.
type Coordinator struct {
	Runner      CommandRunner
	Budgets     Budgets
	Transcriber RequestTranscriber
	// NewAssembler builds the run's assembler; nil means the real one
	NewAssembler        func(runner CommandRunner, budgets Budgets, sourcePath string, sourceDuration time.Duration) (RequestAssembler, error)
	Parallelism         int
	Strict              bool
	MaxRequestsPerChunk int
	Model               string
	Endpoint            string
	Options             string
	StatusOut           io.Writer

	totals   Totals
	totalsMu sync.Mutex
	cache    *requestCache
	registry *SpeakerRegistry
	board    *board.Board
	// forceLive renders the board as on a terminal (tests)
	forceLive bool
	eta       *etaModel
	spans     []SourceInterval
	states    []*chunkState
	started   time.Time
	cores     int
}

type chunkState struct {
	index    int
	core     SourceInterval
	consumed int
	retries  int
	mu       sync.Mutex
}

type chunkResult struct {
	mapping  Mapping
	attempts int
	rejected []CandidateRejection
}

// Run returns the stitched, calibrated transcript in source time.
func (c *Coordinator) Run(ctx context.Context, filePath string) ([]Segment, error) {
	plan, err := (&ChunkPlanner{Runner: c.Runner, Budgets: c.Budgets}).Plan(ctx, filePath)
	if err != nil {
		return nil, err
	}
	newAssembler := c.NewAssembler
	if newAssembler == nil {
		newAssembler = func(r CommandRunner, b Budgets, p string, d time.Duration) (RequestAssembler, error) {
			return NewAssembler(r, b, p, d)
		}
	}
	assembler, err := newAssembler(c.Runner, c.Budgets, filePath, plan.SourceDuration)
	if err != nil {
		return nil, err
	}
	defer assembler.Close()
	c.cache = newRequestCache()
	c.totals = Totals{Cores: len(plan.Cores)}
	c.started = time.Now()
	c.cores = len(plan.Cores)
	registry := NewSpeakerRegistry(c.Budgets.MaxSpeakers)
	c.registry = registry
	spans := make([]SourceInterval, len(plan.Cores))
	for i, core := range plan.Cores {
		spans[i] = SourceInterval{Start: core.Start, End: core.End}
	}
	c.board = newProgressBoard(c.StatusOut, c.forceLive || utils.IsTerminalWriter(c.StatusOut), fmt.Sprintf("calibrated diarization  %v  %v cores × %v  reserve %v  %v workers  ≤%v requests",
		filepath.Base(filePath), len(plan.Cores), plan.CoreTarget.Round(time.Second), plan.PrefixReserve, c.workers(), c.limit()*len(plan.Cores)), spans)

	c.spans = spans
	c.eta = newEtaModel(c.workers(), c.limit(), plan.PrefixReserve)
	c.board.SetFooterFunc(func(rows []board.Row) string {
		c.totalsMu.Lock()
		t := c.totals
		c.totalsMu.Unlock()
		typical, worst := c.eta.estimate(c.etaChunks(rows), time.Now())
		return fmt.Sprintf("%v · %v elapsed · requests %v/≤%v · in flight %v · speakers %v · unknown %v · uploaded %v",
			etaText(typical, worst), shortDuration(time.Since(c.started)), t.Requests, c.limit()*c.cores, board.InFlight(rows),
			len(registry.Snapshot().Speakers), unknownSoFar(rows).Round(time.Second), shortDuration(t.Uploaded))
	})
	c.board.SetPhase(fmt.Sprintf("bootstrap · core 1 of %v · uncalibrated pass then calibrated pass", len(plan.Cores)))
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	states := make([]*chunkState, len(plan.Cores))
	for i, core := range plan.Cores {
		states[i] = &chunkState{index: i, core: SourceInterval{Start: core.Start, End: core.End}}
	}
	c.states = states
	go c.board.Animate(runCtx)
	results := make([]chunkResult, len(plan.Cores))
	defer func() {
		// Leave the board readable on every exit path
		c.board.Finish(c.board.Footer())
	}()
	// Bootstrap on the first core, sequentially
	first, err := c.bootstrap(runCtx, assembler, registry, states[0])
	if err != nil {
		return nil, err
	}
	results[0] = first
	c.chunkDone(states[0], first, len(registry.Snapshot().Speakers))
	if len(states) > 1 {
		c.board.SetPhase(fmt.Sprintf("calibrating · cores 2–%v · %v workers · discovery runs one chunk at a time", len(states), c.workers()))
	}

	// Workers pull chunks in source order so scheduling is deterministic
	queue := make(chan *chunkState, len(states))
	for _, st := range states[1:] {
		queue <- st
	}
	close(queue)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	for range c.workers() {
		wg.Go(func() {
			for st := range queue {
				if runCtx.Err() != nil {
					return
				}
				res, err := c.transcribeChunk(runCtx, assembler, registry, st)
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					cancel()
					return
				}
				results[st.index] = res
				c.chunkDone(st, res, len(registry.Snapshot().Speakers))
			}
		})
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.board.SetPhase("stitching · relabeling cores, numbering unknown speech")
	if c.Strict {
		for i, r := range results {
			if unresolved := r.mapping.MaterialUnmapped(); len(unresolved) > 0 {
				c.board.SetPhase(fmt.Sprintf("failed · strict-speakers · chunk %v has unresolved labels", i+1))
				return nil, &UnresolvedError{Chunk: i, Interval: states[i].core, Labels: unresolved, Rejected: r.rejected, Attempts: r.attempts, Requests: states[i].consumed}
			}
		}
	}
	mappings := make([]Mapping, len(results))
	for i, r := range results {
		mappings[i] = r.mapping
	}
	segs, unknown := stitch(mappings)
	total, detail := summarizeUnknown(unknown)
	c.totalsMu.Lock()
	c.totals.Unknown = total
	c.totals.CacheHits = c.cache.hitCount()
	t := c.totals
	c.totalsMu.Unlock()
	footer := fmt.Sprintf("requests %v/≤%v · retries %v · cache hits %v · uploaded %v · speakers %v · unknown %v · %v elapsed",
		t.Requests, c.limit()*c.cores, t.Retries, t.CacheHits, t.Uploaded.Round(time.Second), len(registry.Snapshot().Speakers), t.Unknown.Round(time.Second), time.Since(c.started).Round(time.Second))
	c.board.SetPhase(fmt.Sprintf("done · %v segments · %v elapsed", len(segs), time.Since(c.started).Round(time.Second)))
	c.board.Finish(footer, detail...)
	return segs, nil
}

func (c *Coordinator) workers() int {
	if c.Parallelism <= 0 {
		return 3
	}
	return c.Parallelism
}

func (c *Coordinator) limit() int {
	if c.MaxRequestsPerChunk <= 0 {
		return defaultMaxRequestsPerChunk
	}
	return c.MaxRequestsPerChunk
}

// requestFor returns the accounted, cached request function for one chunk.
func (c *Coordinator) requestFor(assembler RequestAssembler, st *chunkState) RequestFunc {
	return func(ctx context.Context, samples []SampleClip, core SourceInterval) (*RequestManifest, []Segment, error) {
		st.mu.Lock()
		if st.consumed >= c.limit() {
			consumed := st.consumed
			st.mu.Unlock()
			return nil, nil, &RequestLimitError{Chunk: st.index, Interval: st.core, Limit: c.limit(), Consumed: consumed, Unresolved: sampleIDs(samples)}
		}
		st.consumed++
		st.mu.Unlock()
		m, err := assembler.Assemble(ctx, AssembleRequest{Samples: samples, Core: core})
		if err != nil {
			return nil, nil, fmt.Errorf("chunk %v: %w", st.index+1, err)
		}
		defer assembler.Remove(m)
		key := cacheKey{requestHash: m.RequestHash, model: c.Model, endpoint: c.Endpoint, options: c.Options}
		if segs, ok := c.cache.get(key); ok {
			return m, segs, nil
		}
		c.totalsMu.Lock()
		c.totals.Requests++
		c.totals.Uploaded += m.Duration
		n := c.totals.Requests
		c.totalsMu.Unlock()
		purpose := requestPurpose(st, samples)
		consumed := st.consumed
		c.board.Update(st.index, func(r *board.Row) {
			if r.Started.IsZero() {
				r.Started = time.Now()
			}
			r.Active, r.Cells[colState] = true, fmt.Sprintf("%v · %v", purpose, m.Duration.Round(time.Second))
			r.Cells[colReq] = fmt.Sprintf("%v/%v", consumed, c.limit())
		})
		_ = n
		c.eta.started(st.index, m.Duration, time.Now())
		segs, err := c.Transcriber.TranscribeRequest(ctx, m)
		c.eta.completed(st.index, time.Now())
		if err != nil {
			c.board.Update(st.index, func(r *board.Row) { r.Active, r.Mark, r.Cells[colState] = false, board.MarkWarn, "request failed" })
			return nil, nil, fmt.Errorf("chunk %v request %v (%v with %v samples): %w", st.index+1, st.consumed, m.Duration.Round(time.Second), len(samples), err)
		}
		c.board.Update(st.index, func(r *board.Row) {
			r.Cells[colState] = purpose + " · mapping"
		})
		c.cache.put(key, segs)
		return m, segs, nil
	}
}

// requestPurpose names what a request is for, from the chunk state alone.
func requestPurpose(st *chunkState, samples []SampleClip) string {
	if cands := len(sampleIDs(samples)); cands > 0 {
		return fmt.Sprintf("discovery ·%v", cands)
	}
	switch {
	case st.index == 0 && st.consumed == 1:
		return "uncalibrated pass"
	case st.consumed == 1:
		return "calibrated pass"
	default:
		return "recovery retry"
	}
}

// chunkDone freezes a chunk's row: ✓ when everything material mapped, ✗
// with the count when unresolved labels remain.
func (c *Coordinator) chunkDone(st *chunkState, res chunkResult, speakers int) {
	m := res.mapping
	// registry samples verified; in bootstrap (candidates only) the
	// candidates that anchored
	mapped, samples := 0, 0
	for _, s := range m.Samples {
		if s.Candidate {
			continue
		}
		samples++
		if s.Status == SampleMapped {
			mapped++
		}
	}
	if samples == 0 {
		for _, s := range m.Samples {
			samples++
			if s.Status == SampleMapped || s.Status == SampleCollapsed {
				mapped++
			}
		}
	}
	unresolved := len(m.MaterialUnmapped())
	unknown := time.Duration(0)
	for _, u := range m.Unmapped {
		unknown += u.Speech
	}
	c.board.Update(st.index, func(r *board.Row) {
		r.Active = false
		r.Elapsed = time.Since(r.Started)
		r.Cells[colReq] = fmt.Sprintf("%v/%v", st.consumed, c.limit())
		r.Cells[colVerified] = fmt.Sprintf("%v/%v", mapped, samples)
		r.Cells[colUnresolved] = fmt.Sprint(unresolved)
		r.Cells[colUnknown] = unknown.Round(time.Second).String()
		if unresolved > 0 {
			r.Mark, r.Cells[colState] = board.MarkWarn, fmt.Sprintf("%v unresolved · %v speakers", unresolved, speakers)
			return
		}
		r.Mark, r.Cells[colState] = board.MarkDone, fmt.Sprintf("calibrated · %v speakers", speakers)
	})
}

func sampleIDs(samples []SampleClip) []string {
	ids := make([]string, 0, len(samples))
	for _, s := range samples {
		if s.Candidate {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

func (c *Coordinator) bootstrap(ctx context.Context, assembler RequestAssembler, registry *SpeakerRegistry, st *chunkState) (chunkResult, error) {
	d := &Discovery{Registry: registry, Transcribe: c.requestFor(assembler, st)}
	m, rep, err := d.Bootstrap(ctx, st.core)
	if err != nil {
		return chunkResult{}, err
	}
	d.RecordAccepted(m)
	return chunkResult{mapping: m, attempts: rep.Attempts, rejected: rep.Rejected}, nil
}

// transcribeChunk runs one later chunk: pass, one recovery retry, then
// discovery (architecture/audio.md, "Adaptive Speaker Calibration").
func (c *Coordinator) transcribeChunk(ctx context.Context, assembler RequestAssembler, registry *SpeakerRegistry, st *chunkState) (chunkResult, error) {
	d := &Discovery{Registry: registry, Transcribe: c.requestFor(assembler, st)}
	snap := registry.Snapshot()
	m, err := d.Pass(ctx, st.core, snap)
	if err != nil {
		return chunkResult{}, err
	}
	switch {
	case len(m.Ambiguous()) > 0:
		c.countRetry(st)
		if m, err = d.PassWith(ctx, st.core, reversed(snap.Samples())); err != nil {
			return chunkResult{}, err
		}
	case len(m.Failed()) > 0:
		c.countRetry(st)
		if m, _, err = d.Recover(ctx, st.core, m, snap); err != nil {
			return chunkResult{}, err
		}
	}
	attempts := 0
	var rejected []CandidateRejection
	if len(m.Failed()) > 0 {
		// Still failing after the recovery slot: the next carrier replaces it
		d.RecordAccepted(m)
		return chunkResult{mapping: m}, nil
	}
	if len(m.MaterialUnmapped()) > 0 {
		var rep DiscoveryReport
		if m, rep, err = d.Resolve(ctx, st.core, m, snap); err != nil {
			return chunkResult{}, err
		}
		attempts, rejected = rep.Attempts, rep.Rejected
	}
	d.RecordAccepted(m)
	return chunkResult{mapping: m, attempts: attempts, rejected: rejected}, nil
}

func (c *Coordinator) countRetry(st *chunkState) {
	st.mu.Lock()
	st.retries++
	st.mu.Unlock()
	c.totalsMu.Lock()
	c.totals.Retries++
	c.totalsMu.Unlock()
}

// etaChunks derives per-chunk progress for the estimator from a row
// snapshot (never touches the board, which is locked by the caller).
func (c *Coordinator) etaChunks(rows []board.Row) []etaChunk {
	out := make([]etaChunk, len(c.states))
	for i, st := range c.states {
		st.mu.Lock()
		consumed := st.consumed
		st.mu.Unlock()
		out[i] = etaChunk{core: st.core.End - st.core.Start, consumed: consumed}
		if i < len(rows) {
			out[i].active = rows[i].Active
			out[i].done = !rows[i].Active && (rows[i].Mark == board.MarkDone || rows[i].Mark == board.MarkWarn)
		}
	}
	return out
}

func reversed(samples []SampleClip) []SampleClip {
	out := make([]SampleClip, len(samples))
	for i, s := range samples {
		out[len(samples)-1-i] = s
	}
	return out
}

// Totals returns the last run's totals.
func (c *Coordinator) Totals() Totals {
	c.totalsMu.Lock()
	defer c.totalsMu.Unlock()
	return c.totals
}

// IsRequestLimit reports whether err is the per-chunk safety stop.
func IsRequestLimit(err error) bool {
	var e *RequestLimitError
	return errors.As(err, &e)
}

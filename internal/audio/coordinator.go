package audio

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/resend"
	"github.com/baalimago/clai/pkg/claierr"
)

// Request kinds of the survey path. The merge's two kinds are phase 1's.
const (
	RequestSurvey    RequestKind = "survey"
	RequestLabelling RequestKind = "labelling"
	RequestRetry     RequestKind = "retry"
)

// requestsPerChunk is a chunk's survey, labelling and retry (I8).
const requestsPerChunk = 3

// errNoManifest is an answer whose manifest maps no region of the request.
var errNoManifest = errors.New("request answered without a manifest")

const (
	retryAccepted         = "accepted"
	retryKept             = "kept"
	reasonNoGain          = "no-gain"
	reasonRequestFailed   = "request-failed"
	reasonNothingToChange = "nothing-to-change"
	candidatePrefix       = "cand-"
)

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

// Totals is the run summary written to stderr.
type Totals struct {
	Requests int
	// Resent counts sends of a request again; they are not requests
	Resent    int
	Retries   int
	CacheHits int
	Uploaded  time.Duration
	Unknown   time.Duration
	Cores     int
}

// Coordinator drives the survey, the merge, the labelling pass, the join,
// the retry and the stitch for one oversized diarized recording.
type Coordinator struct {
	Runner      CommandRunner
	Budgets     Budgets
	Transcriber RequestTranscriber
	// NewAssembler builds the run's assembler; nil means the real one
	NewAssembler func(runner CommandRunner, budgets Budgets, sourcePath string, sourceDuration time.Duration) (RequestAssembler, error)
	// Parallelism caps the requests in flight; zero or less means no cap.
	Parallelism int
	Model       string
	Endpoint    string
	Options     string
	StatusOut   io.Writer
	// Retry resends a request the provider may answer later
	Retry resend.Schedule
	// StatusLive draws the board as on a terminal.
	StatusLive bool
	// Merger builds the registry; nil means ChainMerger over the run's
	// request function.
	Merger Merger

	totals   Totals
	totalsMu sync.Mutex
	cache    *requestCache
	registry Snapshot
	board    *chunkBoard
	// forceLive renders the board as on a terminal (tests)
	forceLive bool
	eta       *etaModel
	states    []*chunkState
	started   time.Time
	bound     int
	slots     chan struct{}
	speakers  atomic.Int64
	linksLeft atomic.Int64
	answered  atomic.Int64
	mergeSeq  atomic.Int64
}

type chunkState struct {
	index    int
	core     SourceInterval
	consumed atomic.Int64
	resent   atomic.Int64
	retries  atomic.Int64
}

// answer is one chunk request's manifest and raw segments, with the clips
// it carried in request order.
type answer struct {
	manifest *RequestManifest
	segs     []Segment
	clips    []SampleClip
}

// Run returns the stitched, labelled transcript of an oversized anchored
// recording in source time: survey, merge, labelling pass, join, one retry
// per chunk, stitch (architecture/audio.md, "Adaptive Speaker Calibration";
// the clauses Run.1–18 are in the survey-calibration worklog's phase 2). Only
// a failed plan, a failed survey, merge or labelling request, or an ended
// context is an error: a failed retry keeps the chunk's labelling answer, and
// labels never fail a run.
func (c *Coordinator) Run(ctx context.Context, filePath string) ([]Segment, error) {
	plan, err := (&ChunkPlanner{Runner: c.Runner, Budgets: c.Budgets}).Plan(ctx, filePath)
	if err != nil {
		return nil, err
	}
	assembler, err := c.newAssembler(c.Budgets, filePath, plan.SourceDuration)
	if err != nil {
		return nil, err
	}
	defer assembler.Close()
	c.start(ctx, filePath, plan)
	logPlan(ctx, plan, c.Budgets, c.Parallelism, c.bound)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.board.Animate(runCtx)
	defer func() {
		// Leave the board readable on every exit path; the success path
		// finished it already, so reaching here unfinished is a failure.
		c.board.Fail()
		c.board.Finish(c.finalFooter())
	}()
	surveys, err := c.surveyAll(runCtx, cancel, assembler)
	if err != nil {
		return nil, err
	}
	registry, report, err := c.merge(runCtx, assembler, c.candidates(runCtx, surveys))
	if err != nil {
		return nil, err
	}
	labelled, err := c.labelAll(runCtx, cancel, assembler, registry)
	if err != nil {
		return nil, err
	}
	joined := joinVoices(registry, report, labelled)
	logJoin(ctx, joined)
	c.registry = joined.registry
	c.speakers.Store(int64(len(joined.registry.Speakers)))
	mappings, failed, err := c.retryAll(runCtx, cancel, assembler, joined, labelled)
	if err != nil {
		return nil, err
	}
	return c.finish(ctx, mappings, report.Found(), failed), nil
}

func (c *Coordinator) newAssembler(budgets Budgets, filePath string, duration time.Duration) (RequestAssembler, error) {
	if c.NewAssembler != nil {
		return c.NewAssembler(c.Runner, budgets, filePath, duration)
	}
	return NewAssembler(c.Runner, budgets, filePath, duration)
}

// start resets the run's state and draws the board's first frame.
func (c *Coordinator) start(ctx context.Context, filePath string, plan *Plan) {
	n := len(plan.Cores)
	c.bound = requestsPerChunk*n + 2*(n-1)
	c.cache = newRequestCache()
	c.totals = Totals{Cores: n}
	c.started = time.Now()
	c.registry = Snapshot{}
	c.speakers.Store(0)
	c.linksLeft.Store(int64(max(n-1, 0)))
	c.answered.Store(0)
	c.slots = nil
	if c.Parallelism > 0 {
		c.slots = make(chan struct{}, c.Parallelism)
	}
	spans := make([]SourceInterval, n)
	c.states = make([]*chunkState, n)
	for i, core := range plan.Cores {
		spans[i] = SourceInterval{Start: core.Start, End: core.End}
		c.states[i] = &chunkState{index: i, core: spans[i]}
	}
	c.eta = newEtaModel(c.stageWorkers(n), plan.PrefixReserve)
	c.board = newChunkBoard(ctx, c.StatusOut, c.forceLive || c.StatusLive, fmt.Sprintf("speaker labelling  %v  %v chunks × %v  ≤%v requests",
		filepath.Base(filePath), n, plan.CoreTarget.Round(time.Second), c.bound), spans)
	c.board.SetFooterFunc(func(rows []board.Row) string {
		c.totalsMu.Lock()
		t := c.totals
		c.totalsMu.Unlock()
		typical, worst := c.eta.estimate(c.etaChunks(rows), int(c.linksLeft.Load()), time.Now())
		return fmt.Sprintf("%v · %v elapsed · requests %v/≤%v · resent %v · in flight %v · speakers %v · unknown %v · uploaded %v",
			etaText(typical, worst), shortDuration(time.Since(c.started)), t.Requests, c.bound, t.Resent, board.InFlight(rows),
			c.speakers.Load(), unknownSoFar(rows).Round(time.Second), shortDuration(t.Uploaded))
	})
}

// stageWorkers is how many of n chunk requests a stage sends at once.
func (c *Coordinator) stageWorkers(n int) int {
	if c.Parallelism <= 0 {
		return n
	}
	return min(c.Parallelism, n)
}

// surveyAll sends every chunk once without a prefix (Run.2).
func (c *Coordinator) surveyAll(ctx context.Context, cancel context.CancelFunc, assembler RequestAssembler) ([]Mapping, error) {
	c.board.SetPhase(fmt.Sprintf("finding voices · %v chunks", len(c.states)))
	surveys := make([]Mapping, len(c.states))
	err := c.eachChunk(ctx, cancel, c.states, func(st *chunkState) error {
		_, m, err := c.ask(ctx, assembler, st, RequestSurvey, nil, nil)
		if err != nil {
			return err
		}
		surveys[st.index] = m
		voices := len(materialLabels(m))
		c.board.Update(st.index, func(r *board.Row) { r.Active, r.Cells[colState] = false, fmt.Sprintf("%v voices found", voices) })
		return nil
	})
	return surveys, err
}

// candidates cuts the best clip of every material survey label (Run.3).
func (c *Coordinator) candidates(ctx context.Context, surveys []Mapping) []SurveyCandidate {
	var out []SurveyCandidate
	for i, m := range surveys {
		log := c.logFor(ctx, c.states[i])
		for _, label := range materialLabels(m) {
			clips := (SampleExtractor{}).Candidates(m.Core, label)
			if len(clips) == 0 {
				log.emit(ctx, "candidate", "label=%s id=- reason=%s", label, reasonNoRun)
				continue
			}
			sc := SurveyCandidate{Chunk: i, Candidate: clips[0], LabelSpeech: m.Labels[label].Speech}
			log.emit(ctx, "candidate", "label=%s id=%s clip=%s speech=%s clearance=%s label_speech=%s",
				label, sc.ID(), span(sc.Clip.Start, sc.Clip.End), secs(sc.Speech), secs(sc.Clearance), secs(sc.LabelSpeech))
			out = append(out, sc)
		}
	}
	return out
}

// materialLabels lists a mapping's material labels; a segment with no
// speaker is no label (L2.11).
func materialLabels(m Mapping) []string {
	var out []string
	for label, st := range m.Labels {
		if st.Material && label != "" {
			out = append(out, label)
		}
	}
	slices.Sort(out)
	return out
}

// merge builds the run's registry from the candidates (Run.4).
func (c *Coordinator) merge(ctx context.Context, assembler RequestAssembler, cands []SurveyCandidate) (Snapshot, MergeReport, error) {
	c.board.SetPhase(fmt.Sprintf("matching voices · %v clips", len(cands)))
	merger := c.Merger
	if merger == nil {
		merger = ChainMerger{Transcribe: c.mergeRequest(assembler, cands)}
	}
	registry, report, err := merger.Merge(ctx, cands)
	c.linksLeft.Store(0)
	if err != nil {
		return Snapshot{}, MergeReport{}, err
	}
	logMerge(ctx, report)
	c.registry = registry
	c.speakers.Store(int64(len(registry.Speakers)))
	return registry, report, nil
}

// mergeRequest is the run's request function for the merge links; a link
// has no row, and its records carry link=N.
func (c *Coordinator) mergeRequest(assembler RequestAssembler, cands []SurveyCandidate) RequestFunc {
	chunkOf := map[string]int{}
	for _, sc := range cands {
		chunkOf[sc.ID()] = sc.Chunk
	}
	return func(ctx context.Context, kind RequestKind, samples []SampleClip, core SourceInterval) (*RequestManifest, []Segment, error) {
		link := 0
		if at := slices.IndexFunc(samples, func(s SampleClip) bool { return s.Candidate }); at >= 0 {
			link = chunkOf[samples[at].ID] + 1
		}
		m, segs, err := c.request(ctx, assembler, nil, linkLog(ctx, link), kind, samples, core)
		if err == nil && c.answered.Add(1)%2 == 0 {
			c.linksLeft.Add(-1)
		}
		return m, segs, err
	}
}

// labelAll sends every chunk with one clip per registry speaker (Run.6).
func (c *Coordinator) labelAll(ctx context.Context, cancel context.CancelFunc, assembler RequestAssembler, registry Snapshot) ([]answer, error) {
	c.board.SetPhase(fmt.Sprintf("labelling · %v chunks · %v speakers", len(c.states), len(registry.Speakers)))
	clips := labellingClips(registry)
	owners := make([]string, len(clips))
	for i, clip := range clips {
		owners[i] = clip.ID
	}
	answers := make([]answer, len(c.states))
	err := c.eachChunk(ctx, cancel, c.states, func(st *chunkState) error {
		a, _, err := c.ask(ctx, assembler, st, RequestLabelling, clips, owners)
		answers[st.index] = a
		return err
	})
	return answers, err
}

// labellingClips is each speaker's Sample, ordered by source start (L2.5).
func labellingClips(registry Snapshot) []SampleClip {
	clips := registry.Samples()
	slices.SortStableFunc(clips, func(a, b SampleClip) int { return byClipStart(a.Start, b.Start, a.ID, b.ID) })
	return clips
}

// retryAll maps every labelling answer after the join and gives each chunk
// with a material label naming nobody its one retry (Run.8–Run.10a). It
// returns the kept mappings and the retries that failed, in chunk order.
func (c *Coordinator) retryAll(ctx context.Context, cancel context.CancelFunc, assembler RequestAssembler, joined joining, labelled []answer) ([]Mapping, []retryFailure, error) {
	speakers := len(joined.registry.Speakers)
	owners := make([][]string, len(labelled))
	mappings := make([]Mapping, len(labelled))
	var due []*chunkState
	for i, a := range labelled {
		owners[i] = joined.owners(a.clips)
		mappings[i] = a.mapping(owners[i])
		if len(unmatched(mappings[i])) > 0 {
			due = append(due, c.states[i])
			continue
		}
		c.accept(ctx, c.states[i], mappings[i], speakers)
	}
	if len(due) == 0 {
		return mappings, nil, nil
	}
	c.board.SetPhase(fmt.Sprintf("second try · %v chunks with unmatched voices", len(due)))
	failures := make([]*retryFailure, len(labelled))
	err := c.eachChunk(ctx, cancel, due, func(st *chunkState) error {
		i := st.index
		m, failure, err := c.retryChunk(ctx, assembler, st, labelled[i], mappings[i], owners[i], joined.registry)
		if err != nil {
			return err
		}
		mappings[i], failures[i] = m, failure
		c.accept(ctx, st, m, speakers)
		return nil
	})
	var failed []retryFailure
	for _, f := range failures {
		if f != nil {
			failed = append(failed, *f)
		}
	}
	return mappings, failed, err
}

// unmatched are a mapping's material labels naming nobody; speech with no
// speaker is no label (L2.11).
func unmatched(m Mapping) []UnmappedLabel {
	return slices.DeleteFunc(m.MaterialUnmapped(), func(u UnmappedLabel) bool { return u.Label == "" })
}

// accept records a chunk's final answer and freezes its row.
func (c *Coordinator) accept(ctx context.Context, st *chunkState, m Mapping, speakers int) {
	logAccepted(ctx, c.logFor(ctx, st), st, m)
	c.chunkDone(st, m, speakers)
}

// retryFailure is a retry request that failed while the run went on.
type retryFailure struct {
	chunk int
	core  SourceInterval
	cause string
}

// line is the board's finishing line for the failure (Run.10a).
func (f retryFailure) line() string {
	return fmt.Sprintf("chunk %v (%v) second try failed: %v; its labelling result is kept", f.chunk+1, spanText(f.core), f.cause)
}

// retryChunk sends one chunk's retry, or none when it would change nothing,
// and returns the mapping the chunk keeps. Only an ended context is an error.
func (c *Coordinator) retryChunk(ctx context.Context, assembler RequestAssembler, st *chunkState, lab answer, labMap Mapping, owners []string, registry Snapshot) (Mapping, *retryFailure, error) {
	log := c.logFor(ctx, st)
	pending := pendingText(unmatched(labMap))
	p := planRetry(lab, labMap, owners, registry)
	if p.unchanged(lab.clips) {
		log.emit(ctx, "retry", "state=skipped reason=%s pending=%s", reasonNothingToChange, pending)
		return labMap, nil, nil
	}
	log.emit(ctx, "retry", "state=start reversed=%t swapped=%s candidates=%s pending=%s",
		p.reversed, orDash(strings.Join(p.swapped, ",")), orDash(strings.Join(p.candidates, ",")), pending)
	_, m, err := c.ask(ctx, assembler, st, RequestRetry, p.clips, p.owners)
	if err != nil {
		if ctx.Err() != nil {
			return Mapping{}, nil, err
		}
		failure := &retryFailure{chunk: st.index, core: st.core, cause: failureCause(err)}
		log.emit(ctx, "retry", "outcome=%s reason=%s cause=%q", retryKept, reasonRequestFailed, failure.cause)
		return labMap, failure, nil
	}
	change := fmt.Sprintf("mapped=%s>%s unknown=%s>%s material_unmapped=%d>%d", secs(labMap.MappedSpeech()), secs(m.MappedSpeech()),
		secs(labMap.UnknownSpeech()), secs(m.UnknownSpeech()), len(unmatched(labMap)), len(unmatched(m)))
	if m.MappedSpeech() > labMap.MappedSpeech() {
		log.emit(ctx, "retry", "outcome=%s %s", retryAccepted, change)
		return m, nil, nil
	}
	log.emit(ctx, "retry", "outcome=%s reason=%s %s", retryKept, reasonNoGain, change)
	return labMap, nil, nil
}

// failureCause is the one phrase a failed retry is reported with: the
// provider's meaning and status, the connection failure, or the innermost
// error (an assembly or budget refusal).
func failureCause(err error) string {
	var facts claierr.APIErrorer
	var transport *claierr.TransportError
	if errors.As(err, &facts) || errors.As(err, &transport) {
		return resend.Reason(err)
	}
	for next := errors.Unwrap(err); next != nil; next = errors.Unwrap(err) {
		err = next
	}
	return err.Error()
}

// retryPlan is a chunk's retry request (Run.9): the labelling clips, fixed.
type retryPlan struct {
	clips      []SampleClip
	owners     []string
	reversed   bool
	swapped    []string
	candidates []string
}

func planRetry(lab answer, labMap Mapping, owners []string, registry Snapshot) retryPlan {
	var p retryPlan
	anchored := map[string]bool{}
	onLabel := map[string]bool{}
	for i, s := range labMap.Samples {
		if i >= len(owners) || owners[i] == "" {
			continue
		}
		if s.Status == SampleMapped {
			anchored[owners[i]] = true
		}
		if s.Status == SampleMapped || s.Status == SampleAmbiguous {
			onLabel[s.Label] = true
		}
		p.reversed = p.reversed || s.Status == SampleAmbiguous
	}
	sent := func(s SampleClip) bool {
		return slices.ContainsFunc(lab.clips, func(c SampleClip) bool { return c.Start == s.Start && c.End == s.End })
	}
	swappedFor := map[string]bool{}
	for i, clip := range lab.clips {
		id := owners[i]
		clip.ID = id
		if id == "" || anchored[id] || swappedFor[id] {
			p.clips, p.owners = append(p.clips, clip), append(p.owners, id)
			continue
		}
		swappedFor[id] = true
		if sp, ok := registry.find(id); ok {
			if at := slices.IndexFunc(sp.Clips, func(s SampleClip) bool { return !sent(s) }); at >= 0 {
				next := sp.Clips[at]
				p.swapped = append(p.swapped, id+"@"+span(clip.Start, clip.End)+">"+span(next.Start, next.End))
				clip.Start, clip.End = next.Start, next.End
			}
		}
		p.clips, p.owners = append(p.clips, clip), append(p.owners, id)
	}
	if p.reversed {
		slices.Reverse(p.clips)
		slices.Reverse(p.owners)
	}
	pending := slices.DeleteFunc(unmatched(labMap), func(u UnmappedLabel) bool { return onLabel[u.Label] })
	slices.SortStableFunc(pending, func(a, b UnmappedLabel) int { return cmp.Compare(b.Speech, a.Speech) })
	for _, u := range pending {
		if len(p.clips) >= maxVoices {
			break
		}
		cuts := (SampleExtractor{}).Candidates(labMap.Core, u.Label)
		if len(cuts) == 0 {
			continue
		}
		clip := SampleClip{ID: candidatePrefix + u.Label, Start: cuts[0].Clip.Start, End: cuts[0].Clip.End, Candidate: true}
		p.candidates = append(p.candidates, u.Label+":"+span(clip.Start, clip.End))
		p.clips, p.owners = append(p.clips, clip), append(p.owners, "")
	}
	return p
}

// unchanged reports whether the retry would carry the labelling clips again.
func (p retryPlan) unchanged(labelling []SampleClip) bool {
	return slices.EqualFunc(p.clips, labelling, func(a, b SampleClip) bool {
		return a.Start == b.Start && a.End == b.End && a.Candidate == b.Candidate
	})
}

// mapping reads an answer by Run.8: owners gives the speaker each sample
// region stands for, "" for a candidate; a label names a speaker when every
// registry clip settled on it is that speaker's.
func (a answer) mapping(owners []string) Mapping {
	core := a.manifest.Regions[len(a.manifest.Regions)-1]
	out := (LabelMapper{}).Map(&RequestManifest{Regions: []Region{core}}, a.segs)
	out.Manifest = a.manifest
	samples := labelledSamples(a.manifest, a.segs)
	names := map[string]string{}
	for i, s := range samples {
		if i >= len(owners) || owners[i] == "" || !settled(s) {
			continue
		}
		switch prev, seen := names[s.Label]; {
		case !seen:
			names[s.Label] = owners[i]
		case prev != owners[i]:
			names[s.Label] = ""
		}
	}
	labels := make([]string, 0, len(names))
	for label, id := range names {
		if id != "" {
			labels = append(labels, label)
		}
	}
	slices.Sort(labels)
	for _, label := range labels {
		out.resolve(label, "", names[label])
	}
	for i := range samples {
		s := &samples[i]
		if !settled(*s) {
			continue
		}
		id, owner := names[s.Label], ""
		if i < len(owners) {
			owner = owners[i]
		}
		switch {
		case owner == "" && id != "":
			s.Status, s.Onto = SampleCollapsed, id
		case owner == "":
		case id == owner:
			s.Status = SampleMapped
		default:
			s.Status = SampleAmbiguous
		}
	}
	out.Samples = samples
	return out
}

// joining is the join's outcome (Run.7): the registry after it, each
// speaker's ID before it mapped to the ID after it, the joins made and the
// ones the survey refused.
type joining struct {
	registry Snapshot
	after    map[string]string
	joins    []joinStep
	refused  []joinRefusal
}

type joinStep struct {
	from, into string
	shared     int
}

type joinRefusal struct {
	a, b   string
	shared int
	chunks []int
}

// owners is the speaker after the join of each registry clip.
func (j joining) owners(clips []SampleClip) []string {
	out := make([]string, len(clips))
	for i, clip := range clips {
		out[i] = j.after[clip.ID]
	}
	return out
}

// joinVoices applies Run.7 to the labelling answers, whose clips carry the
// merge's speaker IDs.
func joinVoices(registry Snapshot, report MergeReport, answers []answer) joining {
	speakers := registry.Speakers
	settledOn := make([]map[string]string, len(answers))
	for r, a := range answers {
		settledOn[r] = map[string]string{}
		for i, s := range labelledSamples(a.manifest, a.segs) {
			if i < len(a.clips) && settled(s) {
				settledOn[r][a.clips[i].ID] = s.Label
			}
		}
	}
	pair := func(a, b string) (shared, apart int) {
		for _, on := range settledOn {
			la, okA := on[a]
			lb, okB := on[b]
			switch {
			case !okA || !okB:
			case la == lb:
				shared++
			default:
				apart++
			}
		}
		return shared, apart
	}
	heardApart := surveyHeardApart(report, answers, settledOn)
	root := make([]int, len(speakers))
	var steps []joinStep
	var refused []joinRefusal
	for j := range speakers {
		root[j] = j
		for i := range j {
			shared, apart := pair(speakers[i].ID, speakers[j].ID)
			if shared < 2 || apart > 0 {
				continue
			}
			if chunks := heardApart(speakers[i].ID, speakers[j].ID); len(chunks) > 0 {
				refused = append(refused, joinRefusal{a: speakers[i].ID, b: speakers[j].ID, shared: shared, chunks: chunks})
				continue
			}
			kept := true
			for k := range j {
				_, apart := pair(speakers[k].ID, speakers[j].ID)
				chunks := heardApart(speakers[k].ID, speakers[j].ID)
				if root[k] == root[i] && len(chunks) > 0 {
					refused = append(refused, joinRefusal{a: speakers[k].ID, b: speakers[j].ID, shared: shared, chunks: chunks})
				}
				if root[k] == root[i] && (apart > 0 || len(chunks) > 0) {
					kept = false
				}
			}
			if kept {
				root[j] = root[i]
				steps = append(steps, joinStep{from: speakers[j].ID, into: speakers[i].ID, shared: shared})
			}
			break
		}
	}
	after := map[string]string{}
	for _, sp := range speakers {
		after[sp.ID] = sp.ID
	}
	if len(steps) == 0 {
		return joining{registry: registry, after: after, refused: refused}
	}
	ranked := map[string][]SurveyCandidate{}
	for _, v := range report.Voices {
		ranked[v.Speaker] = v.Clips
	}
	// The ranking is the report's (Merge.7); a merger without one joins nothing
	if slices.ContainsFunc(speakers, func(sp Speaker) bool { return len(ranked[sp.ID]) == 0 }) {
		return joining{registry: registry, after: after, refused: refused}
	}
	chain := &voiceChain{}
	byRoot := map[int]*chainVoice{}
	members := map[string][]string{}
	for j, sp := range speakers {
		v, ok := byRoot[root[j]]
		if !ok {
			v = &chainVoice{n: root[j] + 1}
			byRoot[root[j]] = v
			chain.voices = append(chain.voices, v)
		}
		v.add(ranked[sp.ID]...)
		members[v.name()] = append(members[v.name()], sp.ID)
	}
	snap, voices := chain.registry()
	for _, mv := range voices {
		for _, old := range members[mv.Voice] {
			after[old] = mv.Speaker
		}
	}
	return joining{registry: snap, after: after, joins: steps, refused: refused}
}

// surveyHeardApart gives the chunks whose survey heard both speakers under
// labels of their own and whose labelling answer left a voice no clip
// settled on: a bad clip, not one voice (Run.7, I2).
func surveyHeardApart(report MergeReport, answers []answer, settledOn []map[string]string) func(a, b string) []int {
	chunksOf := map[string]map[int]bool{}
	for _, v := range report.Voices {
		chunksOf[v.Speaker] = map[int]bool{}
		for _, c := range v.Clips {
			chunksOf[v.Speaker][c.Chunk] = true
		}
	}
	leftover := make([]bool, len(answers))
	for r, a := range answers {
		claimed := map[string]bool{}
		for _, label := range settledOn[r] {
			claimed[label] = true
		}
		for _, u := range unmatched(a.mapping(nil)) {
			leftover[r] = leftover[r] || !claimed[u.Label]
		}
	}
	return func(a, b string) []int {
		var chunks []int
		for r := range answers {
			if leftover[r] && chunksOf[a][r] && chunksOf[b][r] {
				chunks = append(chunks, r)
			}
		}
		return chunks
	}
}

// eachChunk runs fn over states, pulled in source order by as many workers
// as the stage may send at once; the first error cancels the rest.
func (c *Coordinator) eachChunk(ctx context.Context, cancel context.CancelFunc, states []*chunkState, fn func(*chunkState) error) error {
	queue := make(chan *chunkState, len(states))
	for _, st := range states {
		queue <- st
	}
	close(queue)
	errs := make(chan error, len(states))
	var wg sync.WaitGroup
	for range c.stageWorkers(len(states)) {
		wg.Go(func() {
			for st := range queue {
				if ctx.Err() != nil {
					return
				}
				if err := fn(st); err != nil {
					errs <- err
					cancel()
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	if err, ok := <-errs; ok {
		return err
	}
	return ctx.Err()
}

// ask sends one chunk request and maps its answer for the chunk's records.
func (c *Coordinator) ask(ctx context.Context, assembler RequestAssembler, st *chunkState, kind RequestKind, clips []SampleClip, owners []string) (answer, Mapping, error) {
	started := time.Now()
	log := c.logFor(ctx, st)
	m, segs, err := c.request(ctx, assembler, st, log, kind, clips, st.core)
	if err != nil {
		return answer{}, Mapping{}, err
	}
	a := answer{manifest: m, segs: segs, clips: clips}
	mapping := a.mapping(owners)
	logResponse(ctx, log, len(segs), time.Since(started), mapping)
	return a, mapping, nil
}

// request assembles, checks, caches, counts and sends one request; st is
// its chunk, nil for a merge request, which has no row.
func (c *Coordinator) request(ctx context.Context, assembler RequestAssembler, st *chunkState, log EventLog, kind RequestKind, samples []SampleClip, core SourceInterval) (*RequestManifest, []Segment, error) {
	release, err := c.slot(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	sent := 0
	if st != nil {
		sent = int(st.consumed.Add(1))
	}
	m, err := assembler.Assemble(ctx, AssembleRequest{Samples: samples, Core: core})
	if err == nil && (m == nil || len(m.Regions) == 0) {
		err = diag.Errorf("%v %w", kind, errNoManifest)
	}
	if err != nil {
		if st == nil {
			return nil, nil, err
		}
		c.rowFailed(ctx, st, kind)
		return nil, nil, diag.Errorf("chunk %v: %w", st.index+1, err)
	}
	defer assembler.Remove(m)
	key := cacheKey{requestHash: m.RequestHash, model: c.Model, endpoint: c.Endpoint, options: c.Options}
	if segs, ok := c.cache.get(key); ok {
		logRequest(ctx, log, kind, m, true)
		return m, segs, nil
	}
	logRequest(ctx, log, kind, m, false)
	c.countUpload(st, kind, m.Duration)
	etaKey := -1 - int(c.mergeSeq.Add(1))
	if st != nil {
		etaKey = st.index
	}
	c.eta.started(etaKey, m.Duration, time.Now())
	purpose := cmp.Or(map[RequestKind]string{RequestSurvey: "finding voices", RequestLabelling: "labelling", RequestRetry: "second try"}[kind], string(kind))
	tries := 0
	segs, err := resend.Do(ctx, c.Retry, resend.Throttled, func(t resend.Try) {
		tries = t.Attempt
		c.countResent(st)
		log.emit(ctx, "request-retry", "kind=%s %s", kind, retryText(t))
		c.row(st, func(r *board.Row) { r.Cells[colState] = retryState(t) })
	}, func(ctx context.Context) ([]Segment, error) {
		c.row(st, func(r *board.Row) {
			if r.Started.IsZero() {
				r.Started = time.Now()
			}
			r.Active, r.Cells[colState] = true, fmt.Sprintf("%v · %v", purpose, m.Duration.Round(time.Second))
			if tries > 0 {
				r.Cells[colState] = fmt.Sprintf("%v · try %v", purpose, tries+1)
			}
			r.Cells[colReq] = fmt.Sprintf("%v/%v", sent, requestsPerChunk)
		})
		return c.Transcriber.TranscribeRequest(ctx, m)
	})
	c.eta.completed(etaKey, time.Now())
	if err != nil {
		if st == nil {
			// The merger names the link (Merge.11)
			return nil, nil, err
		}
		c.rowFailed(ctx, st, kind)
		return nil, nil, diag.Errorf("chunk %v request %v (%v with %v samples): %w", st.index+1, sent, m.Duration.Round(time.Second), len(samples), err)
	}
	c.row(st, func(r *board.Row) { r.Cells[colState] = purpose + " · mapping" })
	c.cache.put(key, segs)
	return m, segs, nil
}

// slot takes one of the Parallelism slots; without a cap it takes none.
func (c *Coordinator) slot(ctx context.Context) (func(), error) {
	if c.slots == nil {
		return func() {}, nil
	}
	select {
	case c.slots <- struct{}{}:
		return func() { <-c.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Coordinator) row(st *chunkState, fn func(r *board.Row)) {
	if st != nil {
		c.board.Update(st.index, fn)
	}
}

// rowFailed marks a chunk's failed request on its row, but for a retry the
// run goes on without (Run.10a): that row keeps its labelling result.
func (c *Coordinator) rowFailed(ctx context.Context, st *chunkState, kind RequestKind) {
	if kind == RequestRetry && ctx.Err() == nil {
		return
	}
	c.row(st, func(r *board.Row) { r.Active, r.Mark, r.Cells[colState] = false, board.MarkWarn, stateRequestFailed })
}

// chunkDone freezes a chunk's row: ✓ when every material label names a
// speaker, ✗ with the count when some name nobody.
func (c *Coordinator) chunkDone(st *chunkState, m Mapping, speakers int) {
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
	open := len(unmatched(m))
	unknown := m.UnknownSpeech()
	consumed := st.consumed.Load()
	c.board.Update(st.index, func(r *board.Row) {
		r.Active = false
		r.Elapsed = time.Since(r.Started)
		r.Cells[colReq] = fmt.Sprintf("%v/%v", consumed, requestsPerChunk)
		r.Cells[colVerified] = fmt.Sprintf("%v/%v", mapped, samples)
		r.Cells[colUnresolved] = fmt.Sprint(open)
		r.Cells[colUnknown] = unknown.Round(time.Second).String()
		if open > 0 {
			r.Mark, r.Cells[colState] = board.MarkWarn, fmt.Sprintf("%v unmatched · %v speakers", open, speakers)
			return
		}
		r.Mark, r.Cells[colState] = board.MarkDone, fmt.Sprintf("labelled · %v speakers", speakers)
	})
}

// finish stitches the kept answers and writes the board's last frame.
func (c *Coordinator) finish(ctx context.Context, mappings []Mapping, found int, failed []retryFailure) []Segment {
	c.board.SetPhase("stitching")
	segs, unknown := stitch(mappings)
	total, detail := summarizeUnknown(unknown)
	c.totalsMu.Lock()
	c.totals.Unknown = total
	c.totalsMu.Unlock()
	footer := c.finalFooter()
	logStitched(ctx, len(segs), len(c.registry.Speakers), unknown, c.Totals())
	c.board.SetPhase(fmt.Sprintf("done · %v segments · %v elapsed", len(segs), time.Since(c.started).Round(time.Second)))
	var lines []string
	if found > maxVoices {
		lines = append(lines, fmt.Sprintf("found %v voices; more than %v usually means one person was not recognised across chunks", found, maxVoices))
	}
	for _, f := range failed {
		lines = append(lines, f.line())
	}
	c.board.Finish(footer, append(lines, detail...)...)
	return segs
}

// finalFooter is the board's last line on every exit, failures included.
func (c *Coordinator) finalFooter() string {
	c.totalsMu.Lock()
	c.totals.CacheHits = c.cache.hitCount()
	t := c.totals
	c.totalsMu.Unlock()
	return fmt.Sprintf("requests %v/≤%v · resent %v · retries %v · cache hits %v · uploaded %v · speakers %v · unknown %v · %v elapsed",
		t.Requests, c.bound, t.Resent, t.Retries, t.CacheHits, t.Uploaded.Round(time.Second), c.speakers.Load(), t.Unknown.Round(time.Second), time.Since(c.started).Round(time.Second))
}

func (c *Coordinator) countUpload(st *chunkState, kind RequestKind, d time.Duration) {
	c.totalsMu.Lock()
	c.totals.Requests++
	c.totals.Uploaded += d
	if kind == RequestRetry {
		c.totals.Retries++
	}
	c.totalsMu.Unlock()
	if st != nil && kind == RequestRetry {
		st.retries.Add(1)
	}
}

func (c *Coordinator) countResent(st *chunkState) {
	if st != nil {
		st.resent.Add(1)
	}
	c.totalsMu.Lock()
	c.totals.Resent++
	c.totalsMu.Unlock()
}

// etaChunks derives per-chunk progress for the estimator from a row
// snapshot (never touches the board, which is locked by the caller).
func (c *Coordinator) etaChunks(rows []board.Row) []etaChunk {
	out := make([]etaChunk, len(c.states))
	for i, st := range c.states {
		out[i] = etaChunk{core: st.core.End - st.core.Start, consumed: int(st.consumed.Load())}
		if i < len(rows) {
			out[i].active = rows[i].Active
			out[i].done = !rows[i].Active && (rows[i].Mark == board.MarkDone || rows[i].Mark == board.MarkWarn)
		}
	}
	return out
}

// Totals returns the last run's totals.
func (c *Coordinator) Totals() Totals {
	c.totalsMu.Lock()
	defer c.totalsMu.Unlock()
	return c.totals
}

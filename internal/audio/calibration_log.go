package audio

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/runenv"
)

// RequestKind says why a calibrated request is sent.
type RequestKind string

const (
	RequestBootstrap           RequestKind = "bootstrap"
	RequestBootstrapCandidates RequestKind = "bootstrap-candidates"
	RequestForward             RequestKind = "forward"
	RequestReversedRetry       RequestKind = "reversed-retry"
	RequestDiscovery           RequestKind = "discovery"
	RequestReplacementRetry    RequestKind = "replacement-retry"
)

// EventLog records one calibration event as a key=value line. A nil
// EventLog records nothing.
type EventLog func(ctx context.Context, event, format string, args ...any)

func (l EventLog) emit(ctx context.Context, event, format string, args ...any) {
	if l != nil {
		l(ctx, event, format, args...)
	}
}

func calibrationDebugf(ctx context.Context, event, format string, args ...any) {
	runenv.Debugf(ctx, runenv.SubsystemAudio, "calibration "+event+" "+format, args...)
}

// logFor is the chunk's EventLog, nil unless the audio debug level is shown.
func (c *Coordinator) logFor(ctx context.Context, st *chunkState) EventLog {
	if !runenv.DebugEnabled(ctx, runenv.SubsystemAudio) {
		return nil
	}
	return func(ctx context.Context, event, format string, args ...any) {
		st.mu.Lock()
		attempt := st.consumed
		st.mu.Unlock()
		calibrationDebugf(ctx, event, "chunk=%d attempt=%d %s", st.index+1, attempt, fmt.Sprintf(format, args...))
	}
}

func secs(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) }

func span(start, end time.Duration) string { return secs(start) + "-" + secs(end) }

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func listOf[T any](items []T, text func(T) string) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = text(item)
	}
	return orDash(strings.Join(parts, ","))
}

func pendingText(pending []UnmappedLabel) string {
	return listOf(pending, func(u UnmappedLabel) string { return u.Label + ":" + secs(u.Speech) })
}

func verdictText(samples []SampleResult) string {
	return listOf(samples, func(s SampleResult) string { return s.ID + ":" + string(s.Status) })
}

func rejectedText(rejected []CandidateRejection) string {
	return listOf(rejected, func(r CandidateRejection) string { return r.Label + ":" + r.Reason })
}

func logPlan(ctx context.Context, plan *Plan, budgets Budgets, workers, limit int) {
	if !runenv.DebugEnabled(ctx, runenv.SubsystemAudio) {
		return
	}
	calibrationDebugf(ctx, "plan", "source=%s max_request_seconds=%s max_request_bytes=%d max_speakers=%d prefix_reserve=%s core_target=%s silence_window=%s silences=%d cores=%d workers=%d request_limit=%d",
		secs(plan.SourceDuration), secs(budgets.MaxRequestDuration), budgets.MaxRequestBytes, budgets.MaxSpeakers,
		secs(plan.PrefixReserve), secs(plan.CoreTarget), secs(plan.SilenceWindow), len(plan.Silences), len(plan.Cores), workers, limit)
	for _, core := range plan.Cores {
		calibrationDebugf(ctx, "plan-core", "core=%d start=%s planned_end=%s end=%s align=%s",
			core.Index+1, secs(core.Start), secs(core.Planned), secs(core.End), core.Reason)
	}
}

// logRequest lists the prefix in request order as id@source>request.
func logRequest(ctx context.Context, log EventLog, kind RequestKind, m *RequestManifest, cached bool) {
	if log == nil {
		return
	}
	cache := "miss"
	if cached {
		cache = "hit"
	}
	var prefix []string
	for _, r := range m.Regions {
		if r.Kind == RegionSample {
			prefix = append(prefix, r.Label+"@"+span(r.SrcStart, r.SrcEnd)+">"+span(r.ReqStart, r.ReqEnd))
		}
	}
	log(ctx, "request", "kind=%s core=%s seconds=%s bytes=%d cache=%s samples=%d prefix=%s",
		kind, span(m.CoreStart, m.CoreEnd), secs(m.Duration), m.Bytes, cache, len(prefix), orDash(strings.Join(prefix, ",")))
}

// logResponse records what one request's labels and samples came to, before
// discovery promotes anything.
func logResponse(ctx context.Context, log EventLog, segments int, m Mapping) {
	if log == nil {
		return
	}
	verdicts := map[SampleStatus]int{}
	for _, s := range m.Samples {
		verdicts[s.Status]++
	}
	unmapped := time.Duration(0)
	reasons := map[string]string{}
	for _, u := range m.Unmapped {
		unmapped += u.Speech
		reasons[u.Label] = u.Reason
	}
	log(ctx, "response", "segments=%d labels=%d samples=%d mapped=%d mixed=%d missing=%d ambiguous=%d collapsed=%d speech=%s unmapped=%s",
		segments, len(m.Labels), len(m.Samples), verdicts[SampleMapped], verdicts[SampleMixed], verdicts[SampleMissing],
		verdicts[SampleAmbiguous], verdicts[SampleCollapsed], secs(m.ChunkSpeech), secs(unmapped))
	for _, s := range m.Samples {
		onto := ""
		if s.Onto != "" {
			onto = " onto=" + s.Onto
		}
		log(ctx, "sample", "id=%s label=%s share=%.2f labelled=%s verdict=%s%s", s.ID, orDash(s.Label), s.Share, secs(s.Labeled), s.Status, onto)
	}
	labels := make([]string, 0, len(m.Labels))
	for label := range m.Labels {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	for _, label := range labels {
		stat := m.Labels[label]
		if id, ok := m.LabelToID[label]; ok {
			log(ctx, "label", "label=%s speech=%s material=%t global=%s", label, secs(stat.Speech), stat.Material, id)
			continue
		}
		log(ctx, "label", "label=%s speech=%s material=%t global=- reason=%s", label, secs(stat.Speech), stat.Material, reasons[label])
	}
}

func logAccepted(ctx context.Context, log EventLog, st *chunkState, res chunkResult) {
	if log == nil {
		return
	}
	m := res.mapping
	st.mu.Lock()
	consumed, retries := st.consumed, st.retries
	st.mu.Unlock()
	unknown := time.Duration(0)
	for _, u := range m.Unmapped {
		unknown += u.Speech
	}
	labels := make([]string, 0, len(m.LabelToID))
	for label := range m.LabelToID {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	log(ctx, "accepted", "discovery_attempts=%d requests=%d retries=%d mapped=%s unknown=%s material_unmapped=%d map=%s",
		res.attempts, consumed, retries, secs(m.ChunkSpeech-unknown), secs(unknown), len(m.MaterialUnmapped()),
		listOf(labels, func(label string) string { return label + ">" + m.LabelToID[label] }))
	for _, u := range m.Unmapped {
		log(ctx, "unmapped", "label=%s speech=%s material=%t reason=%s runs=%d", u.Label, secs(u.Speech), u.Material, u.Reason, len(u.Intervals))
	}
}

// logStitched says which unknown-N names each chunk produced; the transcript
// has their times and the chunk's unmapped records their reasons.
func logStitched(ctx context.Context, segments, speakers int, runs []UnknownRun, t Totals) {
	if !runenv.DebugEnabled(ctx, runenv.SubsystemAudio) {
		return
	}
	short := 0
	for first := 0; first < len(runs); {
		last, speech, shortHere := first, time.Duration(0), 0
		for ; last < len(runs) && runs[last].Chunk == runs[first].Chunk; last++ {
			speech += runs[last].Speech
			if runs[last].Speech < unknownDetailFloor {
				shortHere++
			}
		}
		calibrationDebugf(ctx, "unknown", "chunk=%d names=%s..%s runs=%d short_runs=%d speech=%s",
			runs[first].Chunk+1, runs[first].Name, runs[last-1].Name, last-first, shortHere, secs(speech))
		short += shortHere
		first = last
	}
	calibrationDebugf(ctx, "stitched", "segments=%d speakers=%d unknown_runs=%d unknown_short_runs=%d unknown=%s requests=%d retries=%d cache_hits=%d uploaded=%s",
		segments, speakers, len(runs), short, secs(t.Unknown), t.Requests, t.Retries, t.CacheHits, secs(t.Uploaded))
}

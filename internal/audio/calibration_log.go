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

// prefixedLog is an EventLog whose lines begin with prefix(), nil unless
// the audio debug level is shown.
func prefixedLog(ctx context.Context, prefix func() string) EventLog {
	if !runenv.DebugEnabled(ctx, runenv.SubsystemAudio) {
		return nil
	}
	return func(ctx context.Context, event, format string, args ...any) {
		calibrationDebugf(ctx, event, "%s %s", prefix(), fmt.Sprintf(format, args...))
	}
}

// logFor is the chunk's EventLog; attempt is its request count so far.
func (c *Coordinator) logFor(ctx context.Context, st *chunkState) EventLog {
	return prefixedLog(ctx, func() string { return fmt.Sprintf("chunk=%d attempt=%d", st.index+1, st.consumed.Load()) })
}

// linkLog is a merge link's EventLog; link is the number of the chunk whose
// candidates it holds.
func linkLog(ctx context.Context, link int) EventLog {
	return prefixedLog(ctx, func() string { return "link=" + strconv.Itoa(link) })
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

// logPlan records the plan; parallelism zero or less is "all".
func logPlan(ctx context.Context, plan *Plan, budgets Budgets, parallelism, requestsMax int) {
	if !runenv.DebugEnabled(ctx, runenv.SubsystemAudio) {
		return
	}
	inFlight := "all"
	if parallelism > 0 {
		inFlight = strconv.Itoa(parallelism)
	}
	calibrationDebugf(ctx, "plan", "source=%s max_request_seconds=%s max_request_bytes=%d voices=%d prefix_reserve=%s core_target=%s silence_window=%s silences=%d cores=%d parallelism=%s requests_max=%d",
		secs(plan.SourceDuration), secs(budgets.MaxRequestDuration), budgets.MaxRequestBytes, maxVoices,
		secs(plan.PrefixReserve), secs(plan.CoreTarget), secs(plan.SilenceWindow), len(plan.Silences), len(plan.Cores), inFlight, requestsMax)
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

// logResponse records what one request's labels and samples came to, as the
// mapper reported them.
func logResponse(ctx context.Context, log EventLog, segments int, took time.Duration, m Mapping) {
	if log == nil {
		return
	}
	verdicts := map[SampleStatus]int{}
	for _, s := range m.Samples {
		verdicts[s.Status]++
	}
	unmapped := m.UnknownSpeech()
	reasons := map[string]string{}
	for _, u := range m.Unmapped {
		reasons[u.Label] = u.Reason
	}
	log(ctx, "response", "segments=%d labels=%d samples=%d mapped=%d mixed=%d missing=%d ambiguous=%d collapsed=%d speech=%s unmapped=%s took=%s",
		segments, len(m.Labels), len(m.Samples), verdicts[SampleMapped], verdicts[SampleMixed], verdicts[SampleMissing],
		verdicts[SampleAmbiguous], verdicts[SampleCollapsed], secs(m.ChunkSpeech), secs(unmapped), secs(took))
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

func logAccepted(ctx context.Context, log EventLog, st *chunkState, m Mapping) {
	if log == nil {
		return
	}
	unknown := m.UnknownSpeech()
	labels := make([]string, 0, len(m.LabelToID))
	for label := range m.LabelToID {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	log(ctx, "accepted", "requests=%d resent=%d retries=%d mapped=%s unknown=%s material_unmapped=%d map=%s",
		st.consumed.Load(), st.resent.Load(), st.retries.Load(), secs(m.ChunkSpeech-unknown), secs(unknown), len(unmatched(m)),
		listOf(labels, func(label string) string { return label + ">" + m.LabelToID[label] }))
	for _, u := range m.Unmapped {
		log(ctx, "unmapped", "label=%s speech=%s material=%t reason=%s runs=%d", u.Label, secs(u.Speech), u.Material, u.Reason, len(u.Intervals))
	}
}

// logMerge records every clip of every link, every voice found and the
// merge's totals, from its report.
func logMerge(ctx context.Context, report MergeReport) {
	if !runenv.DebugEnabled(ctx, runenv.SubsystemAudio) {
		return
	}
	verdict := func(v ClipVerdict) string { return fmt.Sprintf("%s:%.2f", orDash(v.Label), v.Share) }
	for _, link := range report.Links {
		for _, clip := range link.Clips {
			calibrationDebugf(ctx, "merge", "link=%d clip=%s source=%s forward=%s reversed=%s outcome=%s voice=%s",
				link.Chunk+1, clip.ID, span(clip.Clip.Start, clip.Clip.End), verdict(clip.Forward), verdict(clip.Reversed), clip.Outcome, orDash(clip.Voice))
		}
	}
	kept := 0
	for _, v := range report.Voices {
		if v.Speaker != "" {
			kept++
		}
		sample := SourceInterval{}
		if len(v.Clips) > 0 {
			sample = v.Clips[0].Clip
		}
		calibrationDebugf(ctx, "merge", "voice=%s speaker=%s clips=%s sample=%s survey_speech=%s",
			v.Voice, orDash(v.Speaker), orDash(strings.Join(candidateIDs(v.Clips), "+")), span(sample.Start, sample.End), secs(v.SurveySpeech))
	}
	calibrationDebugf(ctx, "merge", "state=done links=%d found=%d kept=%d", len(report.Links), report.Found(), kept)
}

func candidateIDs(clips []SurveyCandidate) []string {
	ids := make([]string, len(clips))
	for i, c := range clips {
		ids[i] = c.ID()
	}
	return ids
}

// logJoin records each join, the join's totals and, after a join, every
// speaker with the IDs it held before it.
func logJoin(ctx context.Context, j joining) {
	if !runenv.DebugEnabled(ctx, runenv.SubsystemAudio) {
		return
	}
	for _, step := range j.joins {
		calibrationDebugf(ctx, "join", "from=%s into=%s shared=%d apart=0", step.from, step.into, step.shared)
	}
	for _, r := range j.refused {
		chunks := make([]string, len(r.chunks))
		for i, c := range r.chunks {
			chunks[i] = strconv.Itoa(c + 1)
		}
		calibrationDebugf(ctx, "join", "refused a=%s b=%s shared=%d chunks=%s", r.a, r.b, r.shared, strings.Join(chunks, ","))
	}
	calibrationDebugf(ctx, "join", "state=done joined=%d speakers=%d", len(j.joins), len(j.registry.Speakers))
	if len(j.joins) == 0 {
		return
	}
	was := map[string][]string{}
	for old, now := range j.after {
		was[now] = append(was[now], old)
	}
	for _, sp := range j.registry.Speakers {
		slices.Sort(was[sp.ID])
		calibrationDebugf(ctx, "registry", "id=%s was=%s clips=%d sample=%s", sp.ID, strings.Join(was[sp.ID], "+"), len(sp.Clips), span(sp.Sample.Start, sp.Sample.End))
	}
}

// logStitched says which unknown-N names each chunk produced; the chunk's
// unmapped records carry their reasons.
func logStitched(ctx context.Context, segments, speakers int, voices []UnknownVoice, t Totals) {
	if !runenv.DebugEnabled(ctx, runenv.SubsystemAudio) {
		return
	}
	runs, short := 0, 0
	for first := 0; first < len(voices); {
		last, speech, runsHere, shortHere := first, time.Duration(0), 0, 0
		for ; last < len(voices) && voices[last].Chunk == voices[first].Chunk; last++ {
			speech += voices[last].Speech
			runsHere += voices[last].Runs
			if voices[last].Speech < unknownDetailFloor {
				shortHere++
			}
		}
		calibrationDebugf(ctx, "unknown", "chunk=%d names=%s..%s voices=%d runs=%d short_voices=%d speech=%s",
			voices[first].Chunk+1, voices[first].Name, voices[last-1].Name, last-first, runsHere, shortHere, secs(speech))
		runs, short = runs+runsHere, short+shortHere
		first = last
	}
	calibrationDebugf(ctx, "stitched", "segments=%d speakers=%d unknown_voices=%d unknown_runs=%d unknown_short_voices=%d unknown=%s requests=%d resent=%d retries=%d cache_hits=%d uploaded=%s",
		segments, speakers, len(voices), runs, short, secs(t.Unknown), t.Requests, t.Resent, t.Retries, t.CacheHits, secs(t.Uploaded))
}

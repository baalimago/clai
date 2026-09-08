package audio

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

const crosstalkSpeaker = "crosstalk"

// Annotation is one ground-truth speaker interval.
type Annotation struct {
	Start   time.Duration
	End     time.Duration
	Speaker string
}

// ParseAnnotation reads the tab-separated acceptance annotation: a header
// line, then start, end (seconds) and speaker per line; '#' lines are
// comments; the speaker name "crosstalk" marks excluded intervals.
func ParseAnnotation(r io.Reader) ([]Annotation, error) {
	sc := bufio.NewScanner(r)
	var out []Annotation
	line := 0
	header := false
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if !header {
			header = true
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("annotation line %v: expected 3 tab-separated fields, got %v", line, len(fields))
		}
		start, err1 := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
		end, err2 := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		speaker := strings.TrimSpace(fields[2])
		if err1 != nil || err2 != nil || end <= start || speaker == "" {
			return nil, fmt.Errorf("annotation line %v: invalid interval or speaker: %q", line, text)
		}
		out = append(out, Annotation{Start: time.Duration(start * float64(time.Second)), End: time.Duration(end * float64(time.Second)), Speaker: speaker})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("annotation has no intervals")
	}
	return out, nil
}

// AttributionReport is the acceptance evaluation.
type AttributionReport struct {
	// Confusion[generatedID][annotatedSpeaker] is overlap duration
	Confusion map[string]map[string]time.Duration
	// Mapping is the best one-to-one generated ID → annotated speaker
	Mapping           map[string]string
	AnnotatedSpeakers []string
	GeneratedIDs      []string
	Annotated         time.Duration
	Crosstalk         time.Duration
	Correct           time.Duration
	Unknown           time.Duration
	Merges            int
	Extra             int
}

// Attribution is the correct-identity share of annotated non-crosstalk speech.
func (r AttributionReport) Attribution() float64 {
	if r.Annotated == 0 {
		return 0
	}
	return float64(r.Correct) / float64(r.Annotated)
}

// UnknownShare is the unknown share of annotated non-crosstalk speech.
func (r AttributionReport) UnknownShare() float64 {
	if r.Annotated == 0 {
		return 0
	}
	return float64(r.Unknown) / float64(r.Annotated)
}

// Evaluate compares generated segments with the annotation. Material IDs
// (at least the material label floor of overlap) that map to no annotated
// speaker count as Extra; annotated speakers whose best ID is already taken
// count as Merges.
func Evaluate(segs []Segment, ann []Annotation) AttributionReport {
	r := AttributionReport{Confusion: map[string]map[string]time.Duration{}, Mapping: map[string]string{}}
	speakers := map[string]bool{}
	for _, a := range ann {
		if a.Speaker == crosstalkSpeaker {
			r.Crosstalk += a.End - a.Start
			continue
		}
		speakers[a.Speaker] = true
		r.Annotated += a.End - a.Start
		for _, s := range segs {
			o := overlap(s.Start, s.End, a.Start, a.End)
			if o <= 0 {
				continue
			}
			if r.Confusion[s.Speaker] == nil {
				r.Confusion[s.Speaker] = map[string]time.Duration{}
			}
			r.Confusion[s.Speaker][a.Speaker] += o
		}
	}
	for sp := range speakers {
		r.AnnotatedSpeakers = append(r.AnnotatedSpeakers, sp)
	}
	sort.Strings(r.AnnotatedSpeakers)
	for id := range r.Confusion {
		r.GeneratedIDs = append(r.GeneratedIDs, id)
	}
	sort.Strings(r.GeneratedIDs)
	// Greedy one-to-one assignment by overlap, unknown-N never claims a speaker
	type pair struct {
		id, sp string
		d      time.Duration
	}
	var pairs []pair
	for id, row := range r.Confusion {
		if strings.HasPrefix(id, unknownPrefix) {
			continue
		}
		for sp, d := range row {
			pairs = append(pairs, pair{id, sp, d})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].d != pairs[j].d {
			return pairs[i].d > pairs[j].d
		}
		return pairs[i].id+pairs[i].sp < pairs[j].id+pairs[j].sp
	})
	taken := map[string]bool{}
	for _, p := range pairs {
		if _, done := r.Mapping[p.id]; done || taken[p.sp] {
			continue
		}
		r.Mapping[p.id], taken[p.sp] = p.sp, true
	}
	for id, row := range r.Confusion {
		total := time.Duration(0)
		for _, d := range row {
			total += d
		}
		switch {
		case strings.HasPrefix(id, unknownPrefix):
			r.Unknown += total
		case r.Mapping[id] == "":
			if total >= materialMinSpeech {
				r.Extra++
			}
		default:
			r.Correct += row[r.Mapping[id]]
		}
	}
	for _, sp := range r.AnnotatedSpeakers {
		if !taken[sp] {
			r.Merges++
		}
	}
	return r
}

// String renders the report for the acceptance log.
func (r AttributionReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "annotated speakers: %v; generated IDs: %v\n", len(r.AnnotatedSpeakers), len(r.GeneratedIDs))
	fmt.Fprintf(&b, "attribution %.1f%% (%v of %v), unknown %.1f%% (%v), crosstalk excluded %v, merges %v, extra material IDs %v\n",
		100*r.Attribution(), r.Correct.Round(time.Second), r.Annotated.Round(time.Second), 100*r.UnknownShare(), r.Unknown.Round(time.Second), r.Crosstalk.Round(time.Second), r.Merges, r.Extra)
	fmt.Fprintf(&b, "%-12s", "id\\speaker")
	for _, sp := range r.AnnotatedSpeakers {
		fmt.Fprintf(&b, "%12s", sp)
	}
	b.WriteString("   mapped\n")
	for _, id := range r.GeneratedIDs {
		fmt.Fprintf(&b, "%-12s", id)
		for _, sp := range r.AnnotatedSpeakers {
			fmt.Fprintf(&b, "%12v", r.Confusion[id][sp].Round(time.Second))
		}
		fmt.Fprintf(&b, "   %v\n", r.Mapping[id])
	}
	return b.String()
}

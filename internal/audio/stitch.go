package audio

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const unknownPrefix = "unknown-"

// UnknownVoice is one unmapped local label of one chunk; all its runs share a name.
type UnknownVoice struct {
	Name     string
	Chunk    int
	Label    string
	Start    time.Duration
	End      time.Duration
	Speech   time.Duration
	Runs     int
	Material bool
	Reason   string
}

type unknownKey struct {
	chunk int
	label string
}

// stitch concatenates accepted chunk mappings in source order, drops prefix
// speech (mappings only carry the core), and numbers unmapped speech as
// unknown-N per local label of a chunk, in order of first appearance.
func stitch(mappings []Mapping) ([]Segment, []UnknownVoice) {
	var out []Segment
	var voices []UnknownVoice
	index := map[unknownKey]int{}
	none := unknownKey{chunk: -1}
	prev := none
	for i, m := range mappings {
		reasons := map[string]UnmappedLabel{}
		for _, u := range m.Unmapped {
			reasons[u.Label] = u
		}
		core := make([]Segment, len(m.Core))
		copy(core, m.Core)
		sort.SliceStable(core, func(a, b int) bool { return core[a].Start < core[b].Start })
		for _, s := range core {
			if !m.IsUnmapped(s) {
				out = append(out, s)
				prev = none
				continue
			}
			key := unknownKey{chunk: i, label: s.Speaker}
			at, seen := index[key]
			if !seen {
				at = len(voices)
				index[key] = at
				u := reasons[s.Speaker]
				voices = append(voices, UnknownVoice{
					Name: fmt.Sprintf("%s%d", unknownPrefix, at+1), Chunk: i, Label: s.Speaker,
					Start: s.Start, Material: u.Material, Reason: u.Reason,
				})
			}
			v := &voices[at]
			if key != prev {
				v.Runs++
			}
			v.End, v.Speech = max(v.End, s.End), v.Speech+s.End-s.Start
			prev = key
			s.Speaker = v.Name
			out = append(out, s)
		}
	}
	return out, voices
}

// unknownDetailFloor separates voices worth a line from interjections.
const unknownDetailFloor = 3 * time.Second

// summarizeUnknown returns the total unknown speech and the stderr report:
// one line per voice at or above the detail floor, then one line counting
// the shorter ones.
func summarizeUnknown(voices []UnknownVoice) (time.Duration, []string) {
	if len(voices) == 0 {
		return 0, nil
	}
	total := time.Duration(0)
	reasons := map[string]int{}
	var lines []string
	runs, short, shortSpeech := 0, 0, time.Duration(0)
	for _, v := range voices {
		total += v.Speech
		runs += v.Runs
		reasons[v.Reason]++
		if v.Speech < unknownDetailFloor {
			short++
			shortSpeech += v.Speech
			continue
		}
		lines = append(lines, fmt.Sprintf("    %s–%s  %6s  %-12s %s · runs %v", clock(v.Start), clock(v.End), shortSeconds(v.Speech), v.Name, v.Reason, v.Runs))
	}
	var reasonParts []string
	for _, reason := range []string{reasonNoSample, reasonNonMaterial} {
		if n := reasons[reason]; n > 0 {
			reasonParts = append(reasonParts, fmt.Sprintf("%v %v", n, reason))
		}
	}
	head := fmt.Sprintf("  unknown speech kept as unknown-N · %s · voices %v · runs %v · %s", shortSeconds(total), len(voices), runs, strings.Join(reasonParts, ", "))
	out := append([]string{head}, lines...)
	if short > 0 {
		out = append(out, fmt.Sprintf("    %v voices under %v (%s total) are interjections and overlaps, kept as unknown-N in the transcript", short, unknownDetailFloor, shortSeconds(shortSpeech)))
	}
	return total, out
}

// shortSeconds renders speech durations as 13.9s or 1m52s.
func shortSeconds(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Round(time.Second).Seconds())%60)
}

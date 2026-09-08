package audio

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const unknownPrefix = "unknown-"

// UnknownRun is one contiguous stretch of unmapped speech in the output.
type UnknownRun struct {
	Name     string
	Chunk    int
	Label    string
	Start    time.Duration
	End      time.Duration
	Speech   time.Duration
	Material bool
	Reason   string
}

// stitch concatenates accepted chunk mappings in source order, drops prefix
// speech (mappings only carry the core), and numbers unmapped speech as
// unknown-N per contiguous run of one local label.
func stitch(mappings []Mapping) ([]Segment, []UnknownRun) {
	var out []Segment
	var runs []UnknownRun
	reasons := map[int]map[string]UnmappedLabel{}
	for i, m := range mappings {
		reasons[i] = map[string]UnmappedLabel{}
		for _, u := range m.Unmapped {
			reasons[i][u.Label] = u
		}
	}
	prevChunk, prevLabel := -1, ""
	for i, m := range mappings {
		core := make([]Segment, len(m.Core))
		copy(core, m.Core)
		sort.SliceStable(core, func(a, b int) bool { return core[a].Start < core[b].Start })
		for _, s := range core {
			if !m.IsUnmapped(s) {
				out = append(out, s)
				prevChunk, prevLabel = -1, ""
				continue
			}
			if !(prevChunk == i && prevLabel == s.Speaker) {
				u := reasons[i][s.Speaker]
				runs = append(runs, UnknownRun{
					Name: fmt.Sprintf("%s%d", unknownPrefix, len(runs)+1), Chunk: i, Label: s.Speaker,
					Start: s.Start, End: s.End, Material: u.Material, Reason: u.Reason,
				})
			}
			run := &runs[len(runs)-1]
			run.End, run.Speech = s.End, run.Speech+s.End-s.Start
			prevChunk, prevLabel = i, s.Speaker
			s.Speaker = run.Name
			out = append(out, s)
		}
	}
	return out, runs
}

// unknownDetailFloor separates stretches worth a line from interjections.
const unknownDetailFloor = 3 * time.Second

// summarizeUnknown returns the total unknown speech and a compact report:
// one line per run at or above the detail floor in source order, then one
// line counting the shorter fragments. Every run is still in the output as
// its own unknown-N label; this is the reader's view, not the data.
func summarizeUnknown(runs []UnknownRun) (time.Duration, []string) {
	if len(runs) == 0 {
		return 0, nil
	}
	total := time.Duration(0)
	reasons := map[string]int{}
	var lines []string
	shortRuns, shortSpeech := 0, time.Duration(0)
	for _, r := range runs {
		total += r.Speech
		reasons[r.Reason]++
		if r.Speech < unknownDetailFloor {
			shortRuns++
			shortSpeech += r.Speech
			continue
		}
		lines = append(lines, fmt.Sprintf("    %s–%s  %6s  %-12s %s", clock(r.Start), clock(r.End), shortSeconds(r.Speech), r.Name, r.Reason))
	}
	var reasonParts []string
	for _, reason := range []string{reasonNoSample, reasonReserveExhausted, reasonNonMaterial} {
		if n := reasons[reason]; n > 0 {
			reasonParts = append(reasonParts, fmt.Sprintf("%v %v", n, reason))
		}
	}
	head := fmt.Sprintf("  unknown speech kept as unknown-N · %s in %v runs · %s", shortSeconds(total), len(runs), strings.Join(reasonParts, ", "))
	out := append([]string{head}, lines...)
	if shortRuns > 0 {
		out = append(out, fmt.Sprintf("    %v runs under %v (%s total) are interjections and overlaps, kept as unknown-N in the transcript", shortRuns, unknownDetailFloor, shortSeconds(shortSpeech)))
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

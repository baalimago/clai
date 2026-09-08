package audio

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

// Canonical PCM timeline: s16le mono 16 kHz. Every request boundary is a
// sample count on this grid.
const (
	sampleRate      = 16000
	bytesPerSample  = 2
	sampleDuration  = time.Second / sampleRate
	prefixSlot      = 11 * time.Second // sample max + two guards + one gap
	coreMargin      = 0.02
	silenceNoiseDB  = "-30dB"
	silenceMinDur   = "0.4"
	silenceWindowPc = 0.10

	alignSilence   = "silence-midpoint"
	alignNoSilence = "no-silence"
	alignSourceEnd = "source-end"
)

func samplesOf(d time.Duration) int64 {
	return int64(math.Round(float64(d) / float64(sampleDuration)))
}

func durationOf(samples int64) time.Duration {
	return time.Duration(samples) * sampleDuration
}

// snap rounds a duration onto the PCM sample grid.
func snap(d time.Duration) time.Duration {
	return durationOf(samplesOf(d))
}

// SourceInterval is a half-open [Start, End) interval in source time.
type SourceInterval struct {
	Start time.Duration
	End   time.Duration
}

type Silence = SourceInterval

// CorePlan is one planned core: the audio a request carries after its prefix.
type CorePlan struct {
	Index int
	Start time.Duration
	End   time.Duration
	// Planned is the boundary before silence alignment; Reason says why
	// End differs from it (or does not)
	Planned time.Duration
	Reason  string
}

// Plan is computed once per run. Cores are sized against the fixed prefix
// reserve, never the live prefix, so nothing is replanned.
type Plan struct {
	SourceDuration time.Duration
	PrefixReserve  time.Duration
	CoreTarget     time.Duration
	SilenceWindow  time.Duration
	Silences       []Silence
	Cores          []CorePlan
}

// ChunkPlanner partitions a source into contiguous cores aligned to silence.
type ChunkPlanner struct {
	Runner  CommandRunner
	Budgets Budgets
}

var (
	silenceStartRe = regexp.MustCompile(`silence_start:\s*(-?[0-9.]+)`)
	silenceEndRe   = regexp.MustCompile(`silence_end:\s*(-?[0-9.]+)`)
)

func (p *ChunkPlanner) Plan(ctx context.Context, filePath string) (*Plan, error) {
	reserve := time.Duration(p.Budgets.MaxSpeakers) * prefixSlot
	if reserve >= p.Budgets.MaxRequestDuration {
		return nil, fmt.Errorf("prefix reserve %v (max-speakers %v × %v) reaches max-request-seconds %v; lower max-speakers or raise the budget",
			reserve, p.Budgets.MaxSpeakers, prefixSlot, p.Budgets.MaxRequestDuration)
	}
	seconds, err := probeDuration(ctx, p.Runner, filePath)
	if err != nil {
		return nil, err
	}
	duration := snap(time.Duration(seconds * float64(time.Second)))
	silences, err := p.detectSilence(ctx, filePath)
	if err != nil {
		return nil, err
	}
	maxCore := p.Budgets.MaxRequestDuration - reserve
	target := snap(time.Duration(float64(maxCore) * (1 - coreMargin)))
	window := snap(time.Duration(float64(target) * silenceWindowPc))
	n := max(int(math.Ceil(float64(duration)/float64(target))), 1)
	plan := &Plan{
		SourceDuration: duration,
		PrefixReserve:  reserve,
		CoreTarget:     target,
		SilenceWindow:  window,
		Silences:       silences,
	}
	start := time.Duration(0)
	for i := range n {
		core := CorePlan{Index: i, Start: start}
		if i == n-1 {
			core.Planned, core.End, core.Reason = duration, duration, alignSourceEnd
			plan.Cores = append(plan.Cores, core)
			break
		}
		planned := snap(time.Duration(float64(duration) * float64(i+1) / float64(n)))
		// Both neighbours must stay within maxCore whatever later boundaries do
		lo := max(planned-window, duration-time.Duration(n-i-1)*maxCore)
		hi := min(planned+window, start+maxCore)
		core.Planned = planned
		core.End, core.Reason = nearestSilence(silences, planned, lo, hi)
		plan.Cores = append(plan.Cores, core)
		start = core.End
	}
	return plan, nil
}

// nearestSilence returns the silence midpoint closest to planned inside
// [lo, hi], or planned when none exists.
func nearestSilence(silences []Silence, planned, lo, hi time.Duration) (time.Duration, string) {
	best, bestDist := planned, time.Duration(math.MaxInt64)
	for _, s := range silences {
		mid := snap(s.Start + (s.End-s.Start)/2)
		if mid < lo || mid > hi {
			continue
		}
		if dist := (mid - planned).Abs(); dist < bestDist {
			best, bestDist = mid, dist
		}
	}
	if bestDist == time.Duration(math.MaxInt64) {
		return planned, alignNoSilence
	}
	return best, alignSilence
}

func (p *ChunkPlanner) detectSilence(ctx context.Context, filePath string) ([]Silence, error) {
	_, stderr, err := p.Runner.Run(ctx, ffmpegBin,
		"-hide_banner", "-nostats", "-nostdin", "-v", "info",
		"-i", filePath,
		"-af", "silencedetect=noise="+silenceNoiseDB+":d="+silenceMinDur,
		"-f", "null", "-")
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("silence detection aborted: %w", ctx.Err())
		}
		return nil, fmt.Errorf("ffmpeg silencedetect failed on %v: %w, stderr: %v", filePath, err, tail(stderr))
	}
	starts := silenceStartRe.FindAllStringSubmatch(stderr, -1)
	ends := silenceEndRe.FindAllStringSubmatch(stderr, -1)
	var silences []Silence
	for i := range starts {
		if i >= len(ends) {
			break
		}
		s, err1 := strconv.ParseFloat(starts[i][1], 64)
		e, err2 := strconv.ParseFloat(ends[i][1], 64)
		if err1 != nil || err2 != nil || e <= s {
			continue
		}
		silences = append(silences, Silence{
			Start: snap(time.Duration(s * float64(time.Second))),
			End:   snap(time.Duration(e * float64(time.Second))),
		})
	}
	return silences, nil
}

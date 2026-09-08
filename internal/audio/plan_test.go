package audio

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newPlanner(runner *scriptedRunner, budgets Budgets) *ChunkPlanner {
	return &ChunkPlanner{Runner: runner, Budgets: budgets}
}

func TestPlannerReservesPrefix(t *testing.T) {
	t.Run("reference recording plans four cores under the budget", func(t *testing.T) {
		runner := &scriptedRunner{duration: "5065.000000"}
		plan, err := newPlanner(runner, defaultBudgets()).Plan(context.Background(), "meeting.wav")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(plan.Cores) != 4 {
			t.Fatalf("expected 4 cores, got %v: %+v", len(plan.Cores), plan.Cores)
		}
		if plan.PrefixReserve != 88*time.Second {
			t.Errorf("expected 88 s reserve, got %v", plan.PrefixReserve)
		}
		for _, c := range plan.Cores {
			if c.End-c.Start+plan.PrefixReserve > 1400*time.Second {
				t.Errorf("core %v of %v plus reserve exceeds budget", c.Index, c.End-c.Start)
			}
			if d := c.End - c.Start; d < 1200*time.Second || d > 1300*time.Second {
				t.Errorf("core %v has unexpected length %v", c.Index, d)
			}
		}
		if plan.Cores[0].Start != 0 || plan.Cores[3].End != 5065*time.Second {
			t.Errorf("cores do not cover the source: %+v", plan.Cores)
		}
		for i := 1; i < len(plan.Cores); i++ {
			if plan.Cores[i].Start != plan.Cores[i-1].End {
				t.Errorf("cores %v and %v are not contiguous", i-1, i)
			}
		}
		if runner.count("ffprobe", "") != 1 || runner.count("ffmpeg", "silencedetect") != 1 {
			t.Errorf("expected one probe and one silence pass, got %v", runner.calls)
		}
	})
	t.Run("larger max-speakers grows the reserve and shrinks cores", func(t *testing.T) {
		budgets := defaultBudgets()
		budgets.MaxSpeakers = 12
		plan, err := newPlanner(&scriptedRunner{duration: "5065.000000"}, budgets).Plan(context.Background(), "meeting.wav")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.PrefixReserve != 132*time.Second {
			t.Errorf("expected 132 s reserve, got %v", plan.PrefixReserve)
		}
		for _, c := range plan.Cores {
			if c.End-c.Start+132*time.Second > 1400*time.Second {
				t.Errorf("core %v plus reserve exceeds budget: %v", c.Index, c.End-c.Start)
			}
		}
	})
}

func TestPlannerAlignsToSilence(t *testing.T) {
	// Oracle transcript: segments the boundaries must not split.
	segments := [][2]float64{{1250, 1262.5}, {1263, 1290}, {2520, 2535}, {2536, 2560}, {3780, 3798}, {3799.5, 3810}}
	silences := silenceLines(1262.5, 1263.0, 2535.0, 2536.0) // none near the third boundary
	runner := &scriptedRunner{duration: "5065.000000", silences: silences}
	plan, err := newPlanner(runner, defaultBudgets()).Plan(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := plan.Cores[0].End; got != 1262750*time.Millisecond {
		t.Errorf("expected first boundary on silence midpoint 1262.75, got %v", got)
	}
	if plan.Cores[0].Reason != alignSilence {
		t.Errorf("expected silence reason, got %q", plan.Cores[0].Reason)
	}
	if got := plan.Cores[1].End; got != 2535500*time.Millisecond {
		t.Errorf("expected second boundary on silence midpoint 2535.5, got %v", got)
	}
	if plan.Cores[2].Reason != alignNoSilence {
		t.Errorf("expected no-silence fallback on third boundary, got %q", plan.Cores[2].Reason)
	}
	if plan.Cores[2].End != plan.Cores[2].Planned {
		t.Errorf("fallback boundary should equal planned time: %+v", plan.Cores[2])
	}
	if len(plan.Silences) != 2 {
		t.Errorf("expected the two detected silences recorded, got %+v", plan.Silences)
	}
	for _, c := range plan.Cores[:len(plan.Cores)-1] {
		if d := c.End - c.Planned; d > plan.SilenceWindow || d < -plan.SilenceWindow {
			t.Errorf("boundary %v moved outside the window: %v", c.Index, d)
		}
		b := c.End.Seconds()
		for _, s := range segments {
			if b > s[0] && b < s[1] {
				t.Errorf("boundary %.3f splits oracle segment %v", b, s)
			}
		}
	}
}

func TestPlannerFallsBackWithoutSilence(t *testing.T) {
	runner := &scriptedRunner{duration: "3000.000000"}
	plan, err := newPlanner(runner, defaultBudgets()).Plan(context.Background(), "meeting.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Cores) != 3 {
		t.Fatalf("expected 3 cores, got %v", len(plan.Cores))
	}
	for _, c := range plan.Cores[:2] {
		if c.Reason != alignNoSilence || c.End != c.Planned {
			t.Errorf("expected planned boundary with no-silence reason, got %+v", c)
		}
	}
	if plan.Cores[0].End != 1000*time.Second {
		t.Errorf("expected equal partition at 1000 s, got %v", plan.Cores[0].End)
	}
}

func TestPlannerProbeFailure(t *testing.T) {
	t.Run("ffprobe error carries stderr", func(t *testing.T) {
		runner := &scriptedRunner{probeErr: errors.New("exit status 1")}
		_, err := newPlanner(runner, defaultBudgets()).Plan(context.Background(), "meeting.wav")
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("expected error with stderr tail, got %v", err)
		}
	})
	t.Run("unparseable duration", func(t *testing.T) {
		runner := &scriptedRunner{duration: "N/A"}
		_, err := newPlanner(runner, defaultBudgets()).Plan(context.Background(), "meeting.wav")
		if err == nil || !strings.Contains(err.Error(), "N/A") {
			t.Fatalf("expected parse error naming the value, got %v", err)
		}
	})
}

func TestPlannerRejectsOversizedReserve(t *testing.T) {
	budgets := defaultBudgets()
	budgets.MaxRequestDuration = 60 * time.Second
	runner := &scriptedRunner{duration: "5065.000000"}
	_, err := newPlanner(runner, budgets).Plan(context.Background(), "meeting.wav")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"1m28s", "1m0s", "max-speakers", "max-request-seconds"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %q, got: %v", want, err)
		}
	}
	if len(runner.calls) != 0 {
		t.Errorf("expected no binary invoked, got %v", runner.calls)
	}
}

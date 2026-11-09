package cli

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// scriptedSignals is a SignalSource the test sends signals through.
type scriptedSignals struct{ ch chan os.Signal }

func newScriptedSignals() scriptedSignals {
	return scriptedSignals{ch: make(chan os.Signal, 3)}
}

func (s scriptedSignals) Subscribe() (<-chan os.Signal, func(), error) {
	return s.ch, func() {}, nil
}

func withSignals(signals scriptedSignals) func(*process, *Deps) {
	return func(p *process, _ *Deps) {
		p.signals = signals
		p.forceExit = func() {}
	}
}

// hangAfter sends sig and holds the response open until the client leaves.
func hangAfter(signals scriptedSignals, sig os.Signal) providerStep {
	return func(_ http.ResponseWriter, r *http.Request, _ int32) {
		signals.ch <- sig
		<-r.Context().Done()
	}
}

// hangMidStream streams chunks and holds the response open until the client
// leaves.
func hangMidStream(chunks ...string) providerStep {
	return func(w http.ResponseWriter, r *http.Request, _ int32) {
		sse(w, chunks...)
		<-r.Context().Done()
	}
}

// signalOnOutput sends sig once the run's stdout has carried marker, so the
// signal lands after that output and not before it.
type signalOnOutput struct {
	syncBuffer
	marker  string
	signals scriptedSignals
	sig     os.Signal
	once    sync.Once
}

func (w *signalOnOutput) Write(p []byte) (int, error) {
	n, err := w.syncBuffer.Write(p)
	if strings.Contains(w.String(), w.marker) {
		w.once.Do(func() { w.signals.ch <- w.sig })
	}
	return n, err
}

// runInterruptedMidStream runs args and sends sig once stdout carried marker.
func runInterruptedMidStream(t *testing.T, args, marker string, sig os.Signal) cliRun {
	t.Helper()
	signals := newScriptedSignals()
	stdout := &signalOnOutput{marker: marker, signals: signals, sig: sig}
	got := runProcess(t, args, func(p *process, deps *Deps) {
		withSignals(signals)(p, deps)
		p.stdout = stdout
	})
	got.stdout = stdout.String()
	return got
}

func assertInterrupted(t *testing.T, got cliRun, status int) {
	t.Helper()
	if got.status != status {
		t.Fatalf("status %d, want %d: %+v", got.status, status, got)
	}
	for _, unwanted := range []string{"transport failure", "context canceled", "forceful", "stopped"} {
		if strings.Contains(got.stderr, unwanted) {
			t.Fatalf("stderr carries %q, want the interruption reported once as itself: %q", unwanted, got.stderr)
		}
	}
	if strings.Count(got.stderr, "interrupted") != 1 || strings.Count(got.stderr, "error: ") != 1 || !strings.Contains(got.stderr, "warn [clai] shutting down gracefully") {
		t.Fatalf("stderr %q, want the shutdown notice and the interruption as the one terminal error", got.stderr)
	}
}

// TestInterruptStopsTheRun pins the maintainer's interrupt decision: the
// first SIGINT or SIGTERM cancels the run wherever it lands, what arrived is
// persisted, the interruption is reported once, and the exit status is the
// conventional 128 plus the signal number.
func TestInterruptStopsTheRun(t *testing.T) {
	t.Run("SIGINT mid-stream keeps and persists the partial reply", func(t *testing.T) {
		useConfigDir(t)
		scriptedProvider(t, "hang", hangMidStream(chatDelta("Hello "), chatDelta("wor")))
		got := runInterruptedMidStream(t, "-r -cm gpt-hang q hi", "wor", os.Interrupt)
		assertInterrupted(t, got, 130)
		if got.stdout != "Hello wor" {
			t.Fatalf("stdout %q, want the partial reply as streamed", got.stdout)
		}
		if saved := savedMessages(t); !slices.Equal(saved, []string{"user:hi", "assistant:Hello wor"}) {
			t.Fatalf("persisted %q, want the prompt and the partial reply", saved)
		}
	})
	t.Run("on a terminal the notice starts on its own line", func(t *testing.T) {
		useConfigDir(t)
		t.Setenv("NO_COLOR", "1")
		scriptedProvider(t, "hang", hangMidStream(chatDelta("Hello "), chatDelta("wor")))
		signals := newScriptedSignals()
		stdout := &signalOnOutput{marker: "wor", signals: signals, sig: os.Interrupt}
		got := runProcess(t, "-cm gpt-hang q hi", func(p *process, deps *Deps) {
			withSignals(signals)(p, deps)
			p.stdout, p.stdoutTerminal, p.stderrTerminal = stdout, true, true
		})
		if got.status != 130 || !strings.HasPrefix(got.stderr, "\nwarn [clai] shutting down gracefully") {
			t.Fatalf("got %+v, want the notice after a line break that ends the open reply row", got)
		}
	})
	t.Run("off a terminal the notice has no leading line break", func(t *testing.T) {
		useConfigDir(t)
		scriptedProvider(t, "hang", hangMidStream(chatDelta("Hello "), chatDelta("wor")))
		got := runInterruptedMidStream(t, "-r -cm gpt-hang q hi", "wor", os.Interrupt)
		if !strings.HasPrefix(got.stderr, "warn [clai] shutting down gracefully") {
			t.Fatalf("stderr %q, want the notice first", got.stderr)
		}
	})
	t.Run("SIGTERM before the response headers", func(t *testing.T) {
		useConfigDir(t)
		signals := newScriptedSignals()
		scriptedProvider(t, "hang", hangAfter(signals, syscall.SIGTERM))
		got := runProcess(t, "-r -cm gpt-hang q hi", withSignals(signals))
		assertInterrupted(t, got, 143)
		if got.stdout != "" {
			t.Fatalf("stdout %q, want nothing", got.stdout)
		}
		if saved := savedMessages(t); !slices.Equal(saved, []string{"user:hi"}) {
			t.Fatalf("persisted %q, want the prompt", saved)
		}
	})
	t.Run("json has no final reply and a stopped summary", func(t *testing.T) {
		useConfigDir(t)
		scriptedProvider(t, "hang", hangMidStream(chatDelta("Hello "), chatDelta("wor")))
		got := runInterruptedMidStream(t, "-json -query-summary -cm gpt-hang q hi", "wor", os.Interrupt)
		if got.status != 130 {
			t.Fatalf("status %d, want 130: %+v", got.status, got)
		}
		if types := recordTypes(t, got.stdout); !slices.Equal(types, []string{"reply_delta", "reply_delta"}) {
			t.Fatalf("stdout records %v, want the two deltas and no reply: %q", types, got.stdout)
		}
		recordTypes(t, got.stderr)
		if strings.Count(got.stderr, `"type":"error"`) != 1 || !strings.Contains(got.stderr, `{"type":"error","message":"interrupted by signal: interrupt"`) {
			t.Fatalf("stderr %q, want the interruption as the one error record", got.stderr)
		}
		if strings.Count(got.stderr, `"name":"clai.signal"`) != 1 || !strings.Contains(got.stderr, `"signal":"interrupt"`) {
			t.Fatalf("stderr %q, want one clai.signal diagnostic naming the signal", got.stderr)
		}
		if !strings.Contains(got.stderr, `"outcome":"stopped"`) || strings.Contains(got.stderr, `"outcome":"done"`) {
			t.Fatalf("stderr %q, want a stopped run summary", got.stderr)
		}
	})
	t.Run("during a retry wait", func(t *testing.T) {
		useConfigDir(t)
		signals := newScriptedSignals()
		scriptedProvider(t, "down", func(w http.ResponseWriter, _ *http.Request, _ int32) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		got := runProcess(t, "-r -cm gpt-down q hi", func(p *process, deps *Deps) {
			withSignals(signals)(p, deps)
			deps.StepRetry.Wait = func(ctx context.Context, _ time.Duration) error {
				signals.ch <- syscall.SIGTERM
				<-ctx.Done()
				return ctx.Err()
			}
		})
		if got.status != 143 || strings.Contains(got.stderr, "retry wait") || !strings.Contains(got.stderr, "error: interrupted by signal: terminated") {
			t.Fatalf("got %+v, want exit 143 and the interruption, not an error about the retry wait", got)
		}
	})
	t.Run("after a tool step the tool result is persisted", func(t *testing.T) {
		useConfigDir(t)
		signals := newScriptedSignals()
		scriptedProvider(t, "tool", func(w http.ResponseWriter, r *http.Request, n int32) {
			if n == 1 {
				sse(w, chatToolCall("pwd", "{}"), chatFinishTools, chatDone)
				return
			}
			hangAfter(signals, os.Interrupt)(w, r, n)
		})
		got := runProcess(t, "-r -cm gpt-tool -t pwd q hi", withSignals(signals))
		assertInterrupted(t, got, 130)
		saved := savedMessages(t)
		if len(saved) != 3 || saved[1] != "assistant:calls=1" || !strings.HasPrefix(saved[2], "tool:") {
			t.Fatalf("persisted %q, want the prompt, the tool call and its result", saved)
		}
	})
	t.Run("the second signal warns and the third forces the exit", func(t *testing.T) {
		useConfigDir(t)
		signals := newScriptedSignals()
		forced := make(chan struct{}, 1)
		scriptedProvider(t, "hang", func(w http.ResponseWriter, r *http.Request, _ int32) {
			signals.ch <- os.Interrupt
			signals.ch <- os.Interrupt
			signals.ch <- os.Interrupt
			<-forced
			<-r.Context().Done()
		})
		got := runProcess(t, "-r -cm gpt-hang q hi", func(p *process, _ *Deps) {
			p.signals = signals
			p.forceExit = func() { forced <- struct{}{} }
		})
		lines := strings.Split(strings.TrimSpace(got.stderr), "\n")
		if len(lines) != 4 || !strings.Contains(lines[0], "shutting down gracefully") || !strings.Contains(lines[1], "again to force") || !strings.Contains(lines[2], "forcing") || !strings.Contains(lines[3], "error: interrupted") {
			t.Fatalf("stderr %q, want the graceful step, the warning and the forced exit", got.stderr)
		}
	})
}

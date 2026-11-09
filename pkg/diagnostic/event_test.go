package diagnostic_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/diag/diagfake"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/pkg/diagnostic"
)

var errDestination = errors.New("destination failed")

type countingWriter struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.err != nil {
		return 0, w.err
	}
	return len(p), nil
}

func (w *countingWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

func TestReporterContract(t *testing.T) {
	t.Parallel()

	immediate := diagfake.New()
	buffered := diagfake.New()
	reporter, err := diag.NewReporter(
		[]diagnostic.Type{
			{Name: "immediate", Subsystem: "agent", Level: diagnostic.LevelInfo, Mode: diagnostic.Immediate},
			{Name: "buffered", Subsystem: "agent", Level: diagnostic.LevelWarn, Mode: diagnostic.Buffered},
			{Name: "disabled", Subsystem: "agent", Level: diagnostic.LevelDebug, Mode: diagnostic.Immediate},
			{Name: "required", Subsystem: "agent", Level: diagnostic.LevelTrace, Mode: diagnostic.Immediate, ActionRequired: true},
		},
		diag.ResolvedLevels{"agent": diagnostic.LevelInfo},
		map[diagnostic.OutputMode]diagnostic.Deliverer{diagnostic.Immediate: immediate, diagnostic.Buffered: buffered},
	)
	if err != nil {
		t.Fatalf("construct contract subject: %v", err)
	}

	t.Run("Report.1", func(t *testing.T) {
		if err := reporter.Report(context.Background(), diagnostic.Event{Type: "immediate", Message: "now"}); err != nil {
			t.Fatalf("report immediate: %v", err)
		}
		if err := reporter.Report(context.Background(), diagnostic.Event{Type: "buffered", Message: "later"}); err != nil {
			t.Fatalf("report buffered: %v", err)
		}
		gotImmediate := immediate.Events()
		gotBuffered := buffered.Events()
		if len(gotImmediate) != 1 || gotImmediate[0].Subsystem != "agent" || gotImmediate[0].Level != diagnostic.LevelInfo {
			t.Fatalf("immediate route or registered fields: %+v", gotImmediate)
		}
		if len(gotBuffered) != 1 || gotBuffered[0].Subsystem != "agent" || gotBuffered[0].Level != diagnostic.LevelWarn {
			t.Fatalf("buffered route or registered fields: %+v", gotBuffered)
		}
	})

	t.Run("Report.2", func(t *testing.T) {
		before := len(immediate.Events())
		if err := reporter.Report(context.Background(), diagnostic.Event{Type: "disabled"}); err != nil {
			t.Fatalf("disabled detail: %v", err)
		}
		if err := reporter.Report(context.Background(), diagnostic.Event{Type: "required"}); err != nil {
			t.Fatalf("required event: %v", err)
		}
		got := immediate.Events()[before:]
		if len(got) != 1 || got[0].Type != "required" {
			t.Fatalf("disabled and required routing: %+v", got)
		}
	})

	t.Run("Report.3", func(t *testing.T) {
		err := reporter.Report(context.Background(), diagnostic.Event{Type: "unknown"})
		if err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("unknown type error = %v", err)
		}

		failed := diagfake.Failing(errDestination)
		failingReporter, constructErr := diag.NewReporter(
			[]diagnostic.Type{{Name: "failure", Subsystem: "agent", Level: diagnostic.LevelError}},
			diag.ResolvedLevels{"agent": diagnostic.LevelInfo},
			map[diagnostic.OutputMode]diagnostic.Deliverer{diagnostic.Immediate: failed},
		)
		if constructErr != nil {
			t.Fatalf("construct failing reporter: %v", constructErr)
		}
		if err := failingReporter.Report(context.Background(), diagnostic.Event{Type: "failure"}); !errors.Is(err, errDestination) {
			t.Fatalf("delivery error = %v, want %v", err, errDestination)
		}
	})
}

func TestDelivererContract(t *testing.T) {
	t.Parallel()

	t.Run("Deliver.1", func(t *testing.T) {
		implementations := map[string]func(error) (diagnostic.Deliverer, func() int){
			"diagfake failing": func(deliveryErr error) (diagnostic.Deliverer, func() int) {
				deliverer := diagfake.Failing(deliveryErr)
				return deliverer, deliverer.Calls
			},
			"diagfake failing after its allowance": func(deliveryErr error) (diagnostic.Deliverer, func() int) {
				deliverer := diagfake.FailingAfter(0, deliveryErr)
				return deliverer, deliverer.Calls
			},
			"diagfake failing from now": func(deliveryErr error) (diagnostic.Deliverer, func() int) {
				deliverer := diagfake.New()
				deliverer.Fail(deliveryErr)
				return deliverer, deliverer.Calls
			},
			"slog": func(deliveryErr error) (diagnostic.Deliverer, func() int) {
				handler := &recordingHandler{state: &handlerState{}, handleErr: deliveryErr, enabled: true}
				return diagnostic.SlogDeliverer(handler), handler.handledCount
			},
			"buffered": func(deliveryErr error) (diagnostic.Deliverer, func() int) {
				immediate := diagfake.Failing(deliveryErr)
				return diag.NewBufferedDeliverer("", 0, immediate), immediate.Calls
			},
			"text presenter": func(deliveryErr error) (diagnostic.Deliverer, func() int) {
				destination := &countingWriter{err: deliveryErr}
				return diag.NewTextPresenter(destination, diag.Colored, diagnostic.LevelError), destination.count
			},
			"json presenter": func(deliveryErr error) (diagnostic.Deliverer, func() int) {
				destination := &countingWriter{err: deliveryErr}
				return diag.NewJSONPresenter(jsonl.NewEncoder(destination), diagnostic.LevelError), destination.count
			},
		}
		for name, makeDeliverer := range implementations {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				deliverer, count := makeDeliverer(errDestination)
				const calls = 20
				var wg sync.WaitGroup
				for range calls {
					wg.Go(func() {
						if err := deliverer.Deliver(context.Background(), diagnostic.Event{Level: diagnostic.LevelTrace}); !errors.Is(err, errDestination) {
							t.Errorf("delivery error = %v", err)
						}
					})
				}
				wg.Wait()
				if got := count(); got != calls {
					t.Fatalf("delivered %d events, want %d", got, calls)
				}
			})
		}
	})
}

func TestSlogDeliverer(t *testing.T) {
	t.Parallel()

	t.Run("SlogDeliverer.1", func(t *testing.T) {
		handler := &recordingHandler{state: &handlerState{}, enabled: true, handleErr: errDestination}
		deliverer := diagnostic.SlogDeliverer(handler)
		event := diagnostic.Event{
			Subsystem: "vendor.openai",
			Level:     diagnostic.LevelWarn,
			Message:   "retry",
			Facts:     []slog.Attr{slog.Int("attempt", 2)},
		}
		if err := deliverer.Deliver(context.Background(), event); !errors.Is(err, errDestination) {
			t.Fatalf("Handle error = %v", err)
		}
		if handler.enabledLevel() != slog.LevelWarn {
			t.Fatalf("Enabled level = %v, want warn", handler.enabledLevel())
		}
		attrs, records := handler.snapshot()
		if len(records) != 1 || records[0].Message != "retry" {
			t.Fatalf("records = %+v", records)
		}
		var recordAttrs []slog.Attr
		records[0].Attrs(func(attr slog.Attr) bool {
			recordAttrs = append(recordAttrs, attr)
			return true
		})
		if fmt.Sprint(attrs) != "[subsystem=vendor.openai]" || fmt.Sprint(recordAttrs) != "[attempt=2]" {
			t.Fatalf("bound attrs = %v; record attrs = %d", attrs, records[0].NumAttrs())
		}

		disabled := &recordingHandler{state: &handlerState{}, enabled: false, handleErr: errDestination}
		if err := diagnostic.SlogDeliverer(disabled).Deliver(context.Background(), event); err != nil {
			t.Fatalf("disabled Deliver error = %v", err)
		}
		if disabled.handledCount() != 0 {
			t.Fatal("disabled handler received a record")
		}

		levels := []struct {
			diagnostic diagnostic.Level
			slog       slog.Level
		}{
			{diagnostic.LevelError, slog.LevelError},
			{diagnostic.LevelWarn, slog.LevelWarn},
			{diagnostic.LevelInfo, slog.LevelInfo},
			{diagnostic.LevelDebug, slog.LevelDebug},
			{diagnostic.LevelTrace, slog.LevelDebug - 4},
		}
		for _, level := range levels {
			levelHandler := &recordingHandler{state: &handlerState{}, enabled: true}
			if err := diagnostic.SlogDeliverer(levelHandler).Deliver(context.Background(), diagnostic.Event{Level: level.diagnostic}); err != nil {
				t.Fatalf("Deliver level %v: %v", level.diagnostic, err)
			}
			if got := levelHandler.enabledLevel(); got != level.slog {
				t.Errorf("diagnostic level %v mapped to %v, want %v", level.diagnostic, got, level.slog)
			}
		}
	})
}

func TestReporterConcurrent(t *testing.T) {
	t.Parallel()

	t.Run("Report.3", func(t *testing.T) {
		firstDestination := diagfake.New()
		secondDestination := diagfake.New()
		makeReporter := func(level diagnostic.Level, destination diagnostic.Deliverer) diagnostic.Reporter {
			reporter, err := diag.NewReporter(
				[]diagnostic.Type{{Name: "detail", Subsystem: "agent", Level: diagnostic.LevelDebug}},
				diag.ResolvedLevels{"agent": level},
				map[diagnostic.OutputMode]diagnostic.Deliverer{diagnostic.Immediate: destination},
			)
			if err != nil {
				t.Fatalf("construct reporter: %v", err)
			}
			return reporter
		}
		first := makeReporter(diagnostic.LevelDebug, firstDestination)
		second := makeReporter(diagnostic.LevelInfo, secondDestination)
		var wg sync.WaitGroup
		for range 40 {
			wg.Add(2)
			go func() { defer wg.Done(); _ = first.Report(context.Background(), diagnostic.Event{Type: "detail"}) }()
			go func() { defer wg.Done(); _ = second.Report(context.Background(), diagnostic.Event{Type: "detail"}) }()
		}
		wg.Wait()
		if got := len(firstDestination.Events()); got != 40 {
			t.Fatalf("first reporter delivered %d events", got)
		}
		if got := len(secondDestination.Events()); got != 0 {
			t.Fatalf("second reporter delivered %d events", got)
		}
	})
}

func TestNewReporter(t *testing.T) {
	t.Parallel()

	t.Run("NewReporter.1", func(t *testing.T) {
		deliverers := map[diagnostic.OutputMode]diagnostic.Deliverer{diagnostic.Immediate: diagfake.New()}
		tests := []struct {
			name   string
			types  []diagnostic.Type
			levels diag.ResolvedLevels
			want   string
		}{
			{name: "duplicate", types: []diagnostic.Type{{Name: "same", Subsystem: "agent"}, {Name: "same", Subsystem: "agent"}}, levels: diag.ResolvedLevels{"agent": diagnostic.LevelInfo}, want: "same"},
			{name: "unresolved subsystem", types: []diagnostic.Type{{Name: "event", Subsystem: "missing"}}, levels: diag.ResolvedLevels{"agent": diagnostic.LevelInfo}, want: "missing"},
			{name: "missing mode", types: []diagnostic.Type{{Name: "event", Subsystem: "agent", Mode: diagnostic.Buffered}}, levels: diag.ResolvedLevels{"agent": diagnostic.LevelInfo}, want: fmt.Sprint(diagnostic.Buffered)},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				reporter, err := diag.NewReporter(test.types, test.levels, deliverers)
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("error = %v, want name %q", err, test.want)
				}
				if reporter != nil {
					t.Fatal("NewReporter returned Reporter with error")
				}
			})
		}
	})
}

type handlerState struct {
	mu           sync.Mutex
	attrs        []slog.Attr
	records      []slog.Record
	enabledLevel slog.Level
}

type recordingHandler struct {
	state     *handlerState
	bound     []slog.Attr
	enabled   bool
	handleErr error
}

func (h *recordingHandler) sharedState() *handlerState {
	if h.state == nil {
		h.state = &handlerState{}
	}
	return h.state
}

func (h *recordingHandler) Enabled(_ context.Context, level slog.Level) bool {
	state := h.sharedState()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.enabledLevel = level
	state.attrs = append([]slog.Attr(nil), h.bound...)
	return h.enabled
}

func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	state := h.sharedState()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.records = append(state.records, record.Clone())
	return h.handleErr
}

func (h *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &recordingHandler{state: h.sharedState(), bound: append(append([]slog.Attr(nil), h.bound...), attrs...), enabled: h.enabled, handleErr: h.handleErr}
}

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

func (h *recordingHandler) snapshot() ([]slog.Attr, []slog.Record) {
	state := h.sharedState()
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]slog.Attr(nil), state.attrs...), append([]slog.Record(nil), state.records...)
}

func (h *recordingHandler) handledCount() int {
	_, records := h.snapshot()
	return len(records)
}

func (h *recordingHandler) enabledLevel() slog.Level {
	state := h.sharedState()
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.enabledLevel
}

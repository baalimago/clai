package agent

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/diag/diagfake"
	privmodels "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/diagnostic"
)

// eventRecorder keeps every event a caller's Reporter receives, as the
// agent hands it over, before the routing Reporter it wraps fills anything.
type eventRecorder struct {
	inner  diagnostic.Reporter
	mu     sync.Mutex
	events []diagnostic.Event
}

func (r *eventRecorder) Report(ctx context.Context, event diagnostic.Event) error {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	return r.inner.Report(ctx, event)
}

func (r *eventRecorder) received() []diagnostic.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// internalProducingAgent is an agent whose setup reports through clai's
// internal diagnostics, as its querier setup does: a config warning and
// text querier debug detail.
func internalProducingAgent(t *testing.T, options ...Option) *Agent {
	t.Helper()
	a := New(append([]Option{WithConfigDir(t.TempDir())}, options...)...)
	a.querierCreator = func(ctx context.Context, _ text.Configurations) (privmodels.Querier, error) {
		runenv.Warnf(ctx, runenv.SubsystemConfig, "a config warning")
		runenv.Debugf(ctx, runenv.SubsystemTextQuerier, "a debug detail")
		return diagnosticsQuerier{reply: replyChat()}, nil
	}
	return &a
}

func eventNamed(events []diagnostic.Event, name string) (diagnostic.Event, bool) {
	for _, event := range events {
		if event.Type == name {
			return event, true
		}
	}
	return diagnostic.Event{}, false
}

// TestAgentInternalDiagnostics pins the decision that a caller's Reporter or
// Deliverer receives clai's internal diagnostics too, at the caller's level,
// typed with their registered subsystem and level; without either the agent
// stays silent (F3) and writes nothing to the process streams.
func TestAgentInternalDiagnostics(t *testing.T) {
	warning := runenv.TypeName(runenv.SubsystemConfig, runenv.KindWarning)
	debug := runenv.TypeName(runenv.SubsystemTextQuerier, runenv.KindDebug)

	t.Run("DiagnosticTypes declares the internal registry", func(t *testing.T) {
		names := map[string]bool{}
		for _, declared := range DiagnosticTypes() {
			names[declared.Name] = true
		}
		for _, internal := range append(runenv.Types(), agentDiagnosticTypes()...) {
			if !names[internal.Name] {
				t.Fatalf("DiagnosticTypes lacks %q", internal.Name)
			}
		}
	})

	t.Run("a Deliverer receives them at its level", func(t *testing.T) {
		for _, tc := range []struct {
			level     diagnostic.Level
			wantDebug bool
		}{{diagnostic.LevelDebug, true}, {diagnostic.LevelWarn, false}} {
			deliverer := diagfake.New()
			if _, setupErr, queryErr := runSetupAndQuery(t, internalProducingAgent(t, WithDiagnosticDeliverer(deliverer, tc.level))); setupErr != nil || queryErr != nil {
				t.Fatalf("setup %v, query %v", setupErr, queryErr)
			}
			events := deliverer.Events()
			got, ok := eventNamed(events, warning)
			if !ok || got.Subsystem != runenv.SubsystemConfig || got.Level != diagnostic.LevelWarn || got.Message != "a config warning" {
				t.Fatalf("level %v: warning = %+v (%v); events %v", tc.level, got, ok, eventTypes(events))
			}
			if _, ok := eventNamed(events, debug); ok != tc.wantDebug {
				t.Fatalf("level %v: debug delivered = %v, want %v", tc.level, ok, tc.wantDebug)
			}
		}
	})

	t.Run("a caller's Reporter receives them typed", func(t *testing.T) {
		routing, _ := newRecordingReporter(t, nil)
		recorder := &eventRecorder{inner: routing}
		if _, setupErr, queryErr := runSetupAndQuery(t, internalProducingAgent(t, WithDiagnosticReporter(recorder))); setupErr != nil || queryErr != nil {
			t.Fatalf("setup %v, query %v", setupErr, queryErr)
		}
		events := recorder.received()
		for _, want := range []struct {
			name      string
			subsystem string
			level     diagnostic.Level
		}{
			{warning, runenv.SubsystemConfig, diagnostic.LevelWarn},
			{debug, runenv.SubsystemTextQuerier, diagnostic.LevelDebug},
			{diagnosticSetupCompleted, diagnosticSubsystem, diagnostic.LevelDebug},
		} {
			got, ok := eventNamed(events, want.name)
			if !ok || got.Subsystem != want.subsystem || got.Level != want.level {
				t.Fatalf("event %q = %+v (%v), want subsystem %q level %v; events %v", want.name, got, ok, want.subsystem, want.level, eventTypes(events))
			}
		}
	})

	t.Run("without a Reporter the run reports nothing", func(t *testing.T) {
		var seen []runenv.Env
		a := internalProducingAgent(t)
		creator := a.querierCreator
		a.querierCreator = func(ctx context.Context, conf text.Configurations) (privmodels.Querier, error) {
			seen = append(seen, runenv.From(ctx))
			return creator(ctx, conf)
		}
		if err := a.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if len(seen) != 1 || seen[0].Enabled(runenv.SubsystemConfig, diagnostic.LevelError) {
			t.Fatal("the run of an agent without a Reporter reports diagnostics")
		}
	})
}

// TestAgentNilReporterStaysSilent pins that a nil Reporter is no Reporter:
// the run reports nothing and nothing panics (F3).
func TestAgentNilReporterStaysSilent(t *testing.T) {
	chat, setupErr, queryErr := runSetupAndQuery(t, internalProducingAgent(t, WithDiagnosticReporter(nil)))
	if setupErr != nil || queryErr != nil || len(chat.Messages) != len(replyChat().Messages) {
		t.Fatalf("chat %v, setup %v, query %v", chat, setupErr, queryErr)
	}
}

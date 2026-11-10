package agent

import (
	"context"
	"log/slog"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

const (
	diagnosticSubsystem            = "agent"
	diagnosticConfigDirUnavailable = "agent.setup.config_dir_unavailable"
	diagnosticSetupCompleted       = "agent.setup.completed"
	diagnosticQueryCompleted       = "agent.query.completed"
)

var _ SetupQuerier = (*Agent)(nil)

func agentDiagnosticTypes() []diagnostic.Type {
	return []diagnostic.Type{
		{Name: diagnosticConfigDirUnavailable, Subsystem: diagnosticSubsystem, Level: diagnostic.LevelWarn, Mode: diagnostic.Immediate},
		{Name: diagnosticSetupCompleted, Subsystem: diagnosticSubsystem, Level: diagnostic.LevelDebug, Mode: diagnostic.Immediate},
		{Name: diagnosticQueryCompleted, Subsystem: diagnosticSubsystem, Level: diagnostic.LevelDebug, Mode: diagnostic.Buffered},
	}
}

// diagnosticTypes is every type an agent run reports: the agent's own and
// the registry clai's internal producers report through.
func diagnosticTypes() []diagnostic.Type {
	return append(agentDiagnosticTypes(), runenv.Types()...)
}

// callerLevels are the levels of a run whose caller supplied the Reporter:
// every event reaches it, and the Reporter applies the level policy.
func callerLevels() diag.ResolvedLevels {
	return diag.ResolvedLevels{"": diagnostic.LevelTrace}
}

func delivererReporter(deliverer diagnostic.Deliverer, level diagnostic.Level) (diagnostic.Reporter, diag.ResolvedLevels) {
	types := diagnosticTypes()
	levels := diag.ResolvedLevels{"": level}
	for _, diagnosticType := range types {
		levels[diagnosticType.Subsystem] = level
	}
	reporter, err := diag.NewReporter(types, levels, map[diagnostic.OutputMode]diagnostic.Deliverer{
		diagnostic.Immediate: deliverer,
		diagnostic.Buffered:  deliverer,
	})
	if err != nil {
		return unbuildableReporter{err: err}, levels
	}
	return reporter, levels
}

// unbuildableReporter stands in for a Reporter NewReporter refused; every
// Report returns that refusal, which the agent drops as it drops any failed
// Report (Setup.2, Query.2).
type unbuildableReporter struct {
	err error
}

func (r unbuildableReporter) Report(context.Context, diagnostic.Event) error {
	return r.err
}

// report hands one of the agent's own events to the caller's Reporter. A
// failed Report never reaches the call's result or error (F6).
func (a *Agent) report(ctx context.Context, name, message string, facts ...slog.Attr) {
	if a.diagnosticReporter == nil {
		return
	}
	_ = a.diagnosticReporter.Report(ctx, diagnostic.Event{Type: name, Message: message, Facts: facts})
}

// typedReporter fills each event's registered subsystem and level, which
// clai's internal producers leave to the Reporter, before the caller's
// Reporter receives it.
type typedReporter struct {
	inner diagnostic.Reporter
	types map[string]diagnostic.Type
}

func newTypedReporter(inner diagnostic.Reporter) typedReporter {
	types := map[string]diagnostic.Type{}
	for _, diagnosticType := range diagnosticTypes() {
		types[diagnosticType.Name] = diagnosticType
	}
	return typedReporter{inner: inner, types: types}
}

func (r typedReporter) Report(ctx context.Context, event diagnostic.Event) error {
	if registered, ok := r.types[event.Type]; ok {
		if event.Subsystem == "" {
			event.Subsystem = registered.Subsystem
		}
		if event.Level == 0 {
			event.Level = registered.Level
		}
	}
	return r.inner.Report(ctx, event)
}

package agent

import (
	"context"

	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/clai/pkg/text/models"
)

// DiagnosticTypes returns every diagnostic type the agent reports, so a
// caller can register them with its Reporter. Every event the agent reports
// uses one of these names. (DiagnosticTypes.1, F3)
func DiagnosticTypes() []diagnostic.Type {
	return diagnosticTypes()
}

// WithDiagnosticReporter supplies a caller-owned Reporter for agent
// diagnostics on agent.New. It is absent by default and separate from
// WithLogger, which keeps its completed-message records. (WithDiagnosticReporter.1, F3)
func WithDiagnosticReporter(reporter diagnostic.Reporter) Option {
	return func(a *Agent) {
		a.diagnosticReporter, a.diagnosticLevels = nil, nil
		if reporter != nil {
			a.diagnosticReporter, a.diagnosticLevels = newTypedReporter(reporter), callerLevels()
		}
	}
}

// WithDiagnosticDeliverer supplies one caller-owned Deliverer and a Level.
// The agent builds its Reporter with NewReporter from DiagnosticTypes, levels
// that hold the default and every subsystem at that Level, and the Deliverer
// for both output modes; WithDiagnosticDeliverer(diagnostic.SlogDeliverer(h), level) is
// the one-line slog path. The later of this option and WithDiagnosticReporter
// wins. (WithDiagnosticDeliverer.1, F3, F10)
func WithDiagnosticDeliverer(deliverer diagnostic.Deliverer, level diagnostic.Level) Option {
	return func(a *Agent) {
		a.diagnosticReporter, a.diagnosticLevels = delivererReporter(deliverer, level)
	}
}

type SetupQuerier interface {
	// Setup preserves the existing setup contract. A supplied Reporter
	// receives setup events. Without one, the agent emits no diagnostics.
	// (Setup.1, F3)
	//
	// A failed Report does not change Setup's error. (Setup.2, F6)
	Setup(context.Context) error

	// Query preserves the existing result, typed errors, and agent-facing tool
	// error path. A supplied Reporter receives query events. Without one, the
	// agent emits no diagnostics. (Query.1, F2, F3, F9)
	//
	// A failed Report does not change Query's result or error. (Query.2, F6)
	Query(context.Context, models.Chat) (models.Chat, error)
}

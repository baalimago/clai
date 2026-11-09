package diag

import "github.com/baalimago/clai/pkg/diagnostic"

import (
	"context"
	"fmt"
)

// NewReporter builds the routing Reporter over types, the resolved levels,
// and one Deliverer per output mode. It returns an error that names a
// duplicate type name, a type whose subsystem has no entry in levels, or a
// registered mode without a Deliverer; a Reporter it returns never fails for
// those reasons. (NewReporter.1, F4)
func NewReporter(types []diagnostic.Type, levels ResolvedLevels, deliverers map[diagnostic.OutputMode]diagnostic.Deliverer) (diagnostic.Reporter, error) {
	registrations := make(map[string]registration, len(types))
	for _, diagnosticType := range types {
		if _, exists := registrations[diagnosticType.Name]; exists {
			return nil, fmt.Errorf("duplicate diagnostic type %q", diagnosticType.Name)
		}
		resolvedLevel, exists := levels[diagnosticType.Subsystem]
		if !exists {
			return nil, fmt.Errorf("diagnostic subsystem %q has no resolved level", diagnosticType.Subsystem)
		}
		deliverer, exists := deliverers[diagnosticType.Mode]
		if !exists || deliverer == nil {
			return nil, fmt.Errorf("diagnostic output mode %d has no Deliverer", diagnosticType.Mode)
		}
		registrations[diagnosticType.Name] = registration{
			diagnosticType: diagnosticType,
			resolvedLevel:  resolvedLevel,
			deliverer:      deliverer,
		}
	}
	return &reporter{registrations: registrations}, nil
}

type registration struct {
	diagnosticType diagnostic.Type
	resolvedLevel  diagnostic.Level
	deliverer      diagnostic.Deliverer
}

type reporter struct {
	registrations map[string]registration
}

func (r *reporter) Report(ctx context.Context, event diagnostic.Event) error {
	registered, exists := r.registrations[event.Type]
	if !exists {
		return fmt.Errorf("unknown diagnostic type %q", event.Type)
	}
	if !registered.diagnosticType.ActionRequired && registered.diagnosticType.Level > registered.resolvedLevel {
		return nil
	}
	event.Subsystem = registered.diagnosticType.Subsystem
	event.Level = registered.diagnosticType.Level
	return registered.deliverer.Deliver(ctx, event)
}

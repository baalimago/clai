package text

import (
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// costEnricher owns the cost-manager readiness handshake: it waits a bounded
// time for the model price catalog and then enriches the chat with cost
// estimates. The zero value (no manager) is a no-op.
type costEnricher struct {
	manager CostManager
	ready   <-chan struct{}
	// waitFor bounds the readiness wait: the finalizer must never stall the
	// answer on a slow price fetch.
	waitFor time.Duration
	warnf   func(format string, a ...any)
}

func newCostEnricher(manager CostManager, ready <-chan struct{}) costEnricher {
	return costEnricher{
		manager: manager,
		ready:   ready,
		waitFor: 200 * time.Millisecond,
		warnf:   ancli.Warnf,
	}
}

// enrich returns chat with cost estimates, or unchanged when no manager is
// configured, the catalog does not become ready within waitFor, or the
// enrichment itself fails.
func (c costEnricher) enrich(chat pub_models.Chat) pub_models.Chat {
	if c.manager == nil {
		return chat
	}
	timeout := time.NewTimer(c.waitFor)
	defer timeout.Stop()
	select {
	case <-timeout.C:
		c.warnf("skipping wait for cost manager model price fetch after: %v\n", c.waitFor)
		return chat
	case <-c.ready:
	}
	enriched, err := c.manager.Enrich(chat)
	if err != nil {
		c.warnf("failed to enrich chat with cost estimate: %v\n", err)
		return chat
	}
	return enriched
}

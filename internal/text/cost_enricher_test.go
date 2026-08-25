package text

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// fakeCostManager implements CostManager with a stubbed Enrich.
type fakeCostManager struct {
	enrichFn func(pub_models.Chat) (pub_models.Chat, error)
	calls    int
}

func (f *fakeCostManager) Start(context.Context) (<-chan struct{}, <-chan error) {
	return nil, nil
}

func (f *fakeCostManager) Enrich(chat pub_models.Chat) (pub_models.Chat, error) {
	f.calls++
	return f.enrichFn(chat)
}

func Test_costEnricher_ZeroValueIsANoop(t *testing.T) {
	var enricher costEnricher
	chat := pub_models.Chat{ID: "unchanged"}
	if got := enricher.enrich(chat); got.ID != "unchanged" {
		t.Errorf("zero-value enricher mutated chat: %+v", got)
	}
}

func Test_costEnricher_EnrichesWhenReady(t *testing.T) {
	manager := &fakeCostManager{enrichFn: func(chat pub_models.Chat) (pub_models.Chat, error) {
		chat.Queries = append(chat.Queries, pub_models.QueryCost{CostUSD: 0.42})
		return chat, nil
	}}
	ready := make(chan struct{})
	close(ready)
	enricher := newCostEnricher(manager, ready)

	got := enricher.enrich(pub_models.Chat{ID: "chat"})
	if len(got.Queries) != 1 || got.Queries[0].CostUSD != 0.42 {
		t.Errorf("chat not enriched: %+v", got.Queries)
	}
	if manager.calls != 1 {
		t.Errorf("Enrich called %d times, want 1", manager.calls)
	}
}

func Test_costEnricher_ReadinessTimeoutKeepsChat(t *testing.T) {
	manager := &fakeCostManager{enrichFn: func(chat pub_models.Chat) (pub_models.Chat, error) {
		return chat, nil
	}}
	enricher := newCostEnricher(manager, make(chan struct{}))
	enricher.waitFor = time.Millisecond
	var warned strings.Builder
	enricher.warnf = func(format string, a ...any) { fmt.Fprintf(&warned, format, a...) }

	got := enricher.enrich(pub_models.Chat{ID: "chat"})
	if got.ID != "chat" || len(got.Queries) != 0 {
		t.Errorf("chat changed although catalog never became ready: %+v", got)
	}
	if manager.calls != 0 {
		t.Errorf("Enrich called %d times despite timeout, want 0", manager.calls)
	}
	if !strings.Contains(warned.String(), "skipping") {
		t.Errorf("timeout not warned about; got: %q", warned.String())
	}
}

func Test_costEnricher_EnrichErrorKeepsChat(t *testing.T) {
	manager := &fakeCostManager{enrichFn: func(pub_models.Chat) (pub_models.Chat, error) {
		return pub_models.Chat{}, errors.New("catalog is haunted")
	}}
	ready := make(chan struct{})
	close(ready)
	enricher := newCostEnricher(manager, ready)
	var warned strings.Builder
	enricher.warnf = func(format string, a ...any) { fmt.Fprintf(&warned, format, a...) }

	got := enricher.enrich(pub_models.Chat{ID: "chat"})
	if got.ID != "chat" {
		t.Errorf("failed enrichment must return the original chat; got: %+v", got)
	}
	if !strings.Contains(warned.String(), "catalog is haunted") {
		t.Errorf("enrich error not warned about; got: %q", warned.String())
	}
}

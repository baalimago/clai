package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func writeConfig(t *testing.T, config map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.json")
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func chatWithUsageAndUser() pub_models.Chat {
	return pub_models.Chat{
		Messages:   []pub_models.Message{{Role: "user", Content: "hi"}},
		TokenUsage: &pub_models.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

// startAndWait runs Start and returns the error it reported, nil when the
// resolve routine finished cleanly.
func startAndWait(t *testing.T, m *Manager) error {
	t.Helper()
	ready, errCh := m.Start(context.Background())
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("Start never became ready")
	}
	select {
	case err, stillOpen := <-errCh:
		if stillOpen {
			return err
		}
		return nil
	case <-time.After(2 * time.Second):
		t.Fatal("Start never closed its error channel")
	}
	return nil
}

func TestNewManagerResolverDefaultsToModel(t *testing.T) {
	t.Setenv("DEBUG_COST_MANAGER", "")
	m := NewManager(nil, "gpt-4o", "")
	if m.debug {
		t.Errorf("debug = true without the flag")
	}
	if got := m.modelResolver(pub_models.Chat{}); got != "gpt-4o" {
		t.Errorf("default resolver = %q, want gpt-4o", got)
	}
}

func TestNewManagerDebugFlag(t *testing.T) {
	t.Setenv("DEBUG_COST_MANAGER", "1")
	if m := NewManager(nil, "gpt-4o", ""); !m.debug {
		t.Errorf("debug = false with DEBUG_COST_MANAGER set")
	}
}

func TestSetModelResolver(t *testing.T) {
	t.Run("nil keeps the default resolver", func(t *testing.T) {
		m := NewManager(nil, "base", "")
		m.SetModelResolver(nil)
		if got := m.modelResolver(pub_models.Chat{}); got != "base" {
			t.Errorf("resolver = %q, want the default base", got)
		}
	})

	t.Run("a resolver overrides the model name", func(t *testing.T) {
		m := NewManager(nil, "base", "")
		m.SetModelResolver(func(pub_models.Chat) string { return "resolved" })
		if got := m.modelResolver(pub_models.Chat{}); got != "resolved" {
			t.Errorf("resolver = %q, want resolved", got)
		}
	})
}

func TestEnrichModelNaming(t *testing.T) {
	price := ModelPriceScheme{InputUSDPerToken: 1, OutputUSDPerToken: 1}
	testCases := []struct {
		name     string
		resolver func(pub_models.Chat) string
		want     string
	}{
		{name: "resolved name wins", resolver: func(pub_models.Chat) string { return "resolved" }, want: "resolved"},
		{name: "empty resolution falls back", resolver: func(pub_models.Chat) string { return "" }, want: "base"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := Manager{model: "base", modelResolver: tc.resolver, price: &price}
			got, err := m.Enrich(chatWithUsageAndUser())
			if err != nil {
				t.Fatalf("Enrich: %v", err)
			}
			if len(got.Queries) != 1 {
				t.Fatalf("queries = %d, want 1", len(got.Queries))
			}
			if got.Queries[0].Model != tc.want {
				t.Errorf("model = %q, want %q", got.Queries[0].Model, tc.want)
			}
		})
	}
}

func TestEnrichWithoutPricingFails(t *testing.T) {
	m := Manager{model: "base"}
	if _, err := m.Enrich(chatWithUsageAndUser()); err == nil {
		t.Fatalf("Enrich succeeded without a price scheme")
	}
}

func TestManagerStart(t *testing.T) {
	t.Run("cache hit resolves without a fetcher", func(t *testing.T) {
		price := ModelPriceScheme{InputUSDPerToken: 2, OutputUSDPerToken: 3}
		m := NewManager(nil, "model", writeConfig(t, map[string]any{"price": price}))
		if err := startAndWait(t, &m); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if m.price == nil || *m.price != price {
			t.Errorf("price = %+v, want the cached %+v", m.price, price)
		}
	})

	t.Run("cache miss fetches and stores the price", func(t *testing.T) {
		price := ModelPriceScheme{InputUSDPerToken: 4, OutputUSDPerToken: 5}
		path := writeConfig(t, map[string]any{"name": "model"})
		m := NewManager(fakeFetcher{price: price}, "model", path)
		if err := startAndWait(t, &m); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if m.price == nil || *m.price != price {
			t.Fatalf("price = %+v, want the fetched %+v", m.price, price)
		}
		stored, err := m.seekCached()
		if err != nil {
			t.Fatalf("seekCached after Start: %v", err)
		}
		if stored != price {
			t.Errorf("stored price = %+v, want %+v", stored, price)
		}
	})

	t.Run("a fetch failure is reported", func(t *testing.T) {
		wantErr := errors.New("catalog down")
		m := NewManager(failingFetcher{err: wantErr}, "model", writeConfig(t, map[string]any{}))
		err := startAndWait(t, &m)
		if !errors.Is(err, wantErr) {
			t.Fatalf("Start err = %v, want the fetch failure", err)
		}
		var debugErr DebugError
		if !errors.As(err, &debugErr) {
			t.Fatalf("Start err = %T, want a DebugError", err)
		}
	})

	t.Run("a missing fetcher on a cache miss is an error", func(t *testing.T) {
		m := NewManager(nil, "model", writeConfig(t, map[string]any{}))
		if err := startAndWait(t, &m); err == nil || !strings.Contains(err.Error(), "missing model catalog fetcher") {
			t.Fatalf("Start err = %v, want the missing fetcher error", err)
		}
	})
}

type failingFetcher struct{ err error }

func (f failingFetcher) FetchModel(context.Context, string) (ModelPriceScheme, error) {
	return ModelPriceScheme{}, f.err
}

func TestDebugError(t *testing.T) {
	cause := errors.New("catalog down")
	wrapped := NewDebugError(fmt.Errorf("failed to fetch model: %w", cause))
	if got := wrapped.Error(); !strings.Contains(got, "failed to fetch model") {
		t.Fatalf("Error() = %q, want the wrapped message", got)
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("DebugError must unwrap to its cause")
	}
	var debugErr DebugError
	if !errors.As(wrapped, &debugErr) {
		t.Fatal("errors.As must find a DebugError")
	}
}

package cost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestManagerSeekCached(t *testing.T) {
	t.Run("returns cached price", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "cost.json")
		want := ModelPriceScheme{
			InputUSDPerToken:       1.25,
			InputCachedUSDPerToken: 0.5,
			OutputUSDPerToken:      2.5,
		}

		config := map[string]any{
			"name":  "existing",
			"price": want,
		}
		b, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		if err := os.WriteFile(configPath, b, 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}

		mgr := Manager{configFilePath: configPath}

		got, err := mgr.seekCached()
		if err != nil {
			t.Fatalf("seekCached: %v", err)
		}
		if got != want {
			t.Fatalf("price = %+v, want %+v", got, want)
		}
	})

	t.Run("loads cached price from config written concurrently", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "cost.json")
		want := ModelPriceScheme{
			InputUSDPerToken:       1.25,
			InputCachedUSDPerToken: 0.5,
			OutputUSDPerToken:      2.5,
		}

		config := map[string]any{
			"name": "existing",
		}
		b, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		if err := os.WriteFile(configPath, b, 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}

		mgr := Manager{configFilePath: configPath}
		if err := mgr.storePriceScheme(want); err != nil {
			t.Fatalf("storePriceScheme: %v", err)
		}

		got, err := mgr.seekCached()
		if err != nil {
			t.Fatalf("seekCached after store: %v", err)
		}
		if got != want {
			t.Fatalf("price after store = %+v, want %+v", got, want)
		}
	})

	t.Run("missing price returns cache miss", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "cost.json")

		config := map[string]any{
			"name": "existing",
		}
		b, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		if err := os.WriteFile(configPath, b, 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}

		mgr := Manager{configFilePath: configPath}

		_, err = mgr.seekCached()
		if err == nil {
			t.Fatal("expected error")
		}
		if err != errCacheMiss {
			t.Fatalf("err = %v, want %v", err, errCacheMiss)
		}
	})
}

func TestManagerStorePriceScheme_UpdatesSingularPriceAndPreservesOtherFields(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "cost.json")

	type configFile struct {
		Name    string           `json:"name"`
		Enabled bool             `json:"enabled"`
		Price   ModelPriceScheme `json:"price"`
	}

	original := configFile{
		Name:    "existing",
		Enabled: true,
		Price: ModelPriceScheme{
			InputUSDPerToken: 0.1,
		},
	}

	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal original config: %v", err)
	}
	if err := os.WriteFile(configPath, b, 0o644); err != nil {
		t.Fatalf("write original config: %v", err)
	}

	mgr := Manager{model: "new-model", configFilePath: configPath}
	newPrice := ModelPriceScheme{
		InputUSDPerToken:       1.25,
		InputCachedUSDPerToken: 0.5,
		OutputUSDPerToken:      2.5,
	}

	if err := mgr.storePriceScheme(newPrice); err != nil {
		t.Fatalf("storePriceScheme: %v", err)
	}

	updatedBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}

	var got configFile
	if err := json.Unmarshal(updatedBytes, &got); err != nil {
		t.Fatalf("unmarshal updated config: %v", err)
	}

	if got.Name != original.Name {
		t.Fatalf("name changed: got %q want %q", got.Name, original.Name)
	}
	if got.Enabled != original.Enabled {
		t.Fatalf("enabled changed: got %v want %v", got.Enabled, original.Enabled)
	}
	if got.Price != newPrice {
		t.Fatalf("price = %+v, want %+v", got.Price, newPrice)
	}
}

func TestManagerEnrich_AppendsConfiguredModelForNewTurn(t *testing.T) {
	mgr := Manager{
		model: "model-b",
		price: &ModelPriceScheme{
			InputUSDPerToken:  0.001,
			OutputUSDPerToken: 0.002,
		},
	}

	chat := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "first"},
			{Role: "system", Content: "reply"},
			{Role: "user", Content: "second"},
		},
		TokenUsage: &pub_models.Usage{
			PromptTokens:     3,
			CompletionTokens: 6,
			TotalTokens:      9,
		},
		Queries: []pub_models.QueryCost{
			{
				CreatedAt:      time.Now().Add(-time.Minute),
				CostUSD:        0.1,
				MessageTrigger: 1,
				Model:          "model-a",
				Usage: pub_models.Usage{
					PromptTokens:     2,
					CompletionTokens: 4,
					TotalTokens:      6,
				},
			},
		},
	}

	got, err := mgr.Enrich(chat)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(got.Queries) != 2 {
		t.Fatalf("queries len: got %d want %d", len(got.Queries), 2)
	}
	if got.Queries[1].Model != "model-b" {
		t.Fatalf("query[1] model: got %q want %q", got.Queries[1].Model, "model-b")
	}
	if got.Queries[1].MessageTrigger != 3 {
		t.Fatalf("query[1] message trigger: got %d want %d", got.Queries[1].MessageTrigger, 3)
	}
}

func TestManagerEnrich_AccumulatedUsageAppendsIncrementalCostOnly(t *testing.T) {
	mgr := Manager{
		model: "model-b",
		price: &ModelPriceScheme{
			InputUSDPerToken:  0.001,
			OutputUSDPerToken: 0.002,
		},
	}

	chat := pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "question"},
		},
		TokenUsage: &pub_models.Usage{
			PromptTokens:     10,
			CompletionTokens: 7,
			TotalTokens:      17,
		},
		Queries: []pub_models.QueryCost{
			{
				CreatedAt:      time.Now().Add(-time.Minute),
				CostUSD:        0.012,
				MessageTrigger: 1,
				Model:          "model-b",
				Usage: pub_models.Usage{
					PromptTokens:     6,
					CompletionTokens: 1,
					TotalTokens:      7,
				},
			},
		},
	}

	got, err := mgr.Enrich(chat)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(got.Queries) != 2 {
		t.Fatalf("queries len: got %d want %d", len(got.Queries), 2)
	}
	if got.Queries[1].CostUSD != 0.016 {
		t.Fatalf("query[1] cost: got %v want %v", got.Queries[1].CostUSD, 0.016)
	}
	if got.Queries[1].Usage.PromptTokens != 4 || got.Queries[1].Usage.CompletionTokens != 6 || got.Queries[1].Usage.TotalTokens != 10 {
		t.Fatalf("query[1] usage delta: got %+v", got.Queries[1].Usage)
	}
}

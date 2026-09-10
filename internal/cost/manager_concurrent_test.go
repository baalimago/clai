package cost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeFetcher struct{ price ModelPriceScheme }

func (f fakeFetcher) FetchModel(context.Context, string) (ModelPriceScheme, error) {
	return f.price, nil
}

// TestStorePriceScheme_concurrent pins that concurrent price stores and
// cache reads on one per-model config file never observe a torn file and
// that a failed store keeps the fetched price in memory (review 2, R2-01).
func TestStorePriceScheme_concurrent(t *testing.T) {
	t.Run("stores and reads interleave cleanly", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "vendor_model_v.json")
		if err := os.WriteFile(configPath, []byte(`{"model":"m","other":"kept"}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		price := ModelPriceScheme{InputUSDPerToken: 0.1, OutputUSDPerToken: 0.2}
		const workers = 16
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				mgr := Manager{model: "m", configFilePath: configPath}
				if err := mgr.storePriceScheme(price); err != nil {
					t.Errorf("storePriceScheme: %v", err)
				}
			})
			wg.Go(func() {
				mgr := Manager{model: "m", configFilePath: configPath}
				if _, err := mgr.seekCached(); err != nil && !errors.Is(err, errCacheMiss) {
					t.Errorf("seekCached saw a torn file: %v", err)
				}
			})
		}
		wg.Wait()
		b, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("final file unparseable: %v\n%s", err, b)
		}
		if _, ok := got["price"]; !ok || string(got["other"]) != `"kept"` {
			t.Fatalf("final file = %s, want price stored and other fields kept", b)
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp") {
				t.Fatalf("temp file left behind: %v", e.Name())
			}
		}
	})
	t.Run("failed store keeps the fetched price in memory", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "vendor_model_v.json")
		if err := os.WriteFile(configPath, []byte(`{"model":"m"}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		want := ModelPriceScheme{InputUSDPerToken: 0.3}
		mgr := Manager{model: "m", configFilePath: configPath, fetcher: fakeFetcher{price: want}}
		got, err := mgr.resolveModelPrice(context.Background())
		if err != nil {
			t.Fatalf("resolveModelPrice must not fail on a store error, got %v", err)
		}
		if got != want || mgr.price == nil || *mgr.price != want {
			t.Fatalf("price = %+v, in memory %+v, want %+v", got, mgr.price, want)
		}
	})
}

package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type countingFetcher struct {
	calls int
	price ModelPriceScheme
	err   error
}

func (f *countingFetcher) FetchModel(context.Context, string) (ModelPriceScheme, error) {
	f.calls++
	return f.price, f.err
}

func writeVendorConfig(t *testing.T, fields map[string]any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "vendor.json")
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// TestResolveModelPrice_remembersAModelTheCatalogLacks pins that a model the
// catalog has no price for is looked up once per unpricedRecheck, not on
// every run: each lookup is a network round trip the finalizer waits on.
func TestResolveModelPrice_remembersAModelTheCatalogLacks(t *testing.T) {
	path := writeVendorConfig(t, map[string]any{"model": "jev-latest"})
	fetcher := &countingFetcher{err: fmt.Errorf("find price: %w", ErrModelNotInCatalog)}
	for range 3 {
		m := NewManager(fetcher, "jev-latest", path)
		if _, err := m.resolveModelPrice(t.Context()); !errors.Is(err, ErrModelNotInCatalog) {
			t.Fatalf("err = %v, want ErrModelNotInCatalog", err)
		}
	}
	if fetcher.calls != 1 {
		t.Fatalf("catalog fetched %d times, want once", fetcher.calls)
	}
	var config map[string]json.RawMessage
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := config[unpricedKey]; !ok || string(config["model"]) != `"jev-latest"` {
		t.Fatalf("config = %s, want the marker beside the existing fields", b)
	}
}

func TestResolveModelPrice_rechecksAStaleMarker(t *testing.T) {
	stale := time.Now().Add(-unpricedRecheck - time.Hour).UTC().Format(time.RFC3339)
	path := writeVendorConfig(t, map[string]any{unpricedKey: stale})
	want := ModelPriceScheme{InputUSDPerToken: 1}
	fetcher := &countingFetcher{price: want}
	m := NewManager(fetcher, "m", path)
	got, err := m.resolveModelPrice(t.Context())
	if err != nil || got != want || fetcher.calls != 1 {
		t.Fatalf("price = %+v, err = %v, calls = %d; want a fresh fetch", got, err, fetcher.calls)
	}
}

func TestResolveModelPrice_transientFailuresAreNotRemembered(t *testing.T) {
	path := writeVendorConfig(t, map[string]any{})
	fetcher := &countingFetcher{err: errors.New("network down")}
	for range 2 {
		m := NewManager(fetcher, "m", path)
		if _, err := m.resolveModelPrice(t.Context()); err == nil {
			t.Fatal("want the fetch error")
		}
	}
	if fetcher.calls != 2 {
		t.Fatalf("catalog fetched %d times, want a retry after a transient failure", fetcher.calls)
	}
}

func TestResolveModelPrice_corruptMarkerIsAnError(t *testing.T) {
	path := writeVendorConfig(t, map[string]any{unpricedKey: "yesterday"})
	fetcher := &countingFetcher{}
	m := NewManager(fetcher, "m", path)
	if _, err := m.resolveModelPrice(t.Context()); err == nil || errors.Is(err, ErrModelNotInCatalog) {
		t.Fatalf("err = %v, want a decode error", err)
	}
}

package openrouter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type catalogServer struct {
	url     string
	fetches atomic.Int32
	status  atomic.Int32
	body    atomic.Pointer[[]byte]
}

func serveCatalog(t *testing.T) *catalogServer {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("testdata", "openrouter-models-modalities.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	s := &catalogServer{}
	s.status.Store(http.StatusOK)
	s.body.Store(&fixture)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" || r.Method != http.MethodGet {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		s.fetches.Add(1)
		w.WriteHeader(int(s.status.Load()))
		_, _ = w.Write(*s.body.Load())
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func cacheFile(dir, model string) string {
	sum := sha256.Sum256([]byte(model))
	return filepath.Join(dir, "openrouter-modalities", hex.EncodeToString(sum[:])+".json")
}

func assertModalities(t *testing.T, lookup ModalityLookup, model string, want []string) {
	t.Helper()
	got, err := lookup(context.Background(), model)
	if err != nil {
		t.Fatalf("lookup %s: %v", model, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lookup %s = %v, want %v", model, got, want)
	}
}

// Proves CatalogModalities.1–3.
func TestCatalogModalities(t *testing.T) {
	srv := serveCatalog(t)
	cache := t.TempDir()
	const imageModel = "google/gemini-3.1-flash-lite-image"

	lookup := CatalogModalities(srv.url, cache, http.DefaultClient)
	assertModalities(t, lookup, imageModel, []string{"image", "text"})
	assertModalities(t, lookup, imageModel, []string{"image", "text"})
	assertModalities(t, CatalogModalities(srv.url, cache, http.DefaultClient), imageModel, []string{"image", "text"})
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("fetches across two calls and two lookups = %d, want 1", n)
	}
	if _, err := os.Stat(cacheFile(cache, imageModel)); err != nil {
		t.Fatalf("cache file: %v", err)
	}
	assertModalities(t, lookup, "anthropic/claude-haiku-4.5", []string{"text"})

	t.Run("missing model is not cached", func(t *testing.T) {
		before := srv.fetches.Load()
		for range 2 {
			if _, err := lookup(context.Background(), "nobody/nothing"); !errors.Is(err, ErrModelNotInCatalog) {
				t.Fatalf("err = %v, want ErrModelNotInCatalog", err)
			}
		}
		if n := srv.fetches.Load() - before; n != 2 {
			t.Fatalf("fetches = %d, want 2", n)
		}
		if _, err := os.Stat(cacheFile(cache, "nobody/nothing")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a missing model was cached: %v", err)
		}
	})

	t.Run("fetch failure is retried", func(t *testing.T) {
		const model = "openai/gpt-5-image-mini"
		srv.status.Store(http.StatusInternalServerError)
		if _, err := lookup(context.Background(), model); err == nil {
			t.Fatal("lookup on a failing server = nil error")
		}
		if _, err := os.Stat(cacheFile(cache, model)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a fetch failure was cached: %v", err)
		}
		srv.status.Store(http.StatusOK)
		before := srv.fetches.Load()
		assertModalities(t, lookup, model, []string{"image", "text"})
		if n := srv.fetches.Load() - before; n != 1 {
			t.Fatalf("retry fetches = %d, want 1", n)
		}
	})

	t.Run("undecodable catalog", func(t *testing.T) {
		bad := []byte("not json")
		good := srv.body.Swap(&bad)
		defer srv.body.Store(good)
		if _, err := lookup(context.Background(), "google/gemini-2.5-flash-image"); err == nil {
			t.Fatal("lookup on an undecodable catalog = nil error")
		}
	})

	t.Run("corrupt cache file is refetched", func(t *testing.T) {
		if err := os.WriteFile(cacheFile(cache, imageModel), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		before := srv.fetches.Load()
		assertModalities(t, lookup, imageModel, []string{"image", "text"})
		if n := srv.fetches.Load() - before; n != 1 {
			t.Fatalf("fetches = %d, want 1", n)
		}
	})

	t.Run("no cache dir", func(t *testing.T) {
		uncached := CatalogModalities(srv.url, "", http.DefaultClient)
		before := srv.fetches.Load()
		assertModalities(t, uncached, imageModel, []string{"image", "text"})
		assertModalities(t, uncached, imageModel, []string{"image", "text"})
		if n := srv.fetches.Load() - before; n != 2 {
			t.Fatalf("fetches = %d, want 2", n)
		}
	})

	t.Run("bad base URL", func(t *testing.T) {
		if _, err := CatalogModalities("://bad", "", http.DefaultClient)(context.Background(), imageModel); err == nil {
			t.Fatal("lookup over a malformed URL = nil error")
		}
	})
}

// Proves ModalityLookup.1: a chat URL with no catalog beside it sends nothing to it.
func TestOpenRouterDefaultLookup_NoCatalogURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			hits.Add(1)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("CLAI_CACHE_DIR", t.TempDir())

	o := OpenRouter{Model: "or:google/gemini-3.1-flash-lite-image"}
	if err := o.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	o.StreamCompleter.URL = srv.URL
	ch, err := o.StreamCompletions(context.Background(), textOnlyChat())
	if err != nil {
		t.Fatalf("StreamCompletions: %v", err)
	}
	for range ch {
	}
	if hits.Load() != 0 {
		t.Fatalf("catalog GETs = %d, want 0", hits.Load())
	}
}

func textOnlyChat() pub_models.Chat {
	return pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
}

// Proves O1–O3 at the chunk decoder: every data URL in order, typed failures.
func TestDecodeImageChunk(t *testing.T) {
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	chunk := func(urls ...string) []byte {
		images := make([]string, 0, len(urls))
		for _, u := range urls {
			images = append(images, `{"type":"image_url","image_url":{"url":"`+u+`"}}`)
		}
		return []byte(`{"choices":[{"index":0,"delta":{"content":"","images":[` + strings.Join(images, ",") + `]}}]}`)
	}

	evs, err := decodeImageChunk(chunk("data:image/png;base64,"+png, "data:image/png;base64,"+png))
	if err != nil || len(evs) != 2 {
		t.Fatalf("two images: %d events, err %v", len(evs), err)
	}
	if img := evs[1].(models.ImageEvent).Image; img.MIME != "image/png" || len(img.Data) == 0 {
		t.Fatalf("image = %+v", img)
	}

	if evs, err := decodeImageChunk([]byte(`{"choices":[{"delta":{"content":"hi"}}]}`)); err != nil || evs != nil {
		t.Fatalf("text chunk: %v, %v; want no events", evs, err)
	}

	var upr *claierr.UnexpectedProviderResponseError
	for name, payload := range map[string][]byte{
		"not a data URL":    chunk("https://example.com/a.png"),
		"not base64 URL":    chunk("data:image/png," + png),
		"no MIME":           chunk("data:;base64," + png),
		"malformed chunk":   []byte(`{"choices":[{"delta":{"images":{}}}]}`),
		"undecodable bytes": chunk("data:image/png;base64,***"),
	} {
		if _, err := decodeImageChunk(payload); !errors.As(err, &upr) {
			t.Fatalf("%s: err = %v, want *claierr.UnexpectedProviderResponseError", name, err)
		}
	}
}

package openrouter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baalimago/clai/internal/utils"
)

// OpenRouter modality source, held in the OpenRouter completer's Modalities field.
//
//	ModalityLookup.1. A nil field means CatalogModalities over the completer's own base
//	                  URL, with utils.GetClaiCacheDir() as the cache dir.
//	ModalityLookup.2. When the cache dir cannot be resolved, that lookup runs uncached.
type ModalityLookup func(ctx context.Context, model string) ([]string, error)

// CatalogModalities is the production lookup.
//  1. It returns architecture.output_modalities for the model from
//     GET <baseURL>/api/v1/models.
//  2. A successful answer is cached in <cacheDir>/openrouter-modalities/<sha256 of the
//     model id>.json, with no expiry, so each model costs one catalog fetch per machine. A fetch
//     failure is never cached (memory: an error is not a fact about the file).
//  3. A model missing from the catalog fails with ErrModelNotInCatalog, and that is
//     not cached either.
func CatalogModalities(baseURL, cacheDir string, client *http.Client) ModalityLookup {
	cache := modalityCache{dir: cacheDir}
	return func(ctx context.Context, model string) ([]string, error) {
		if modalities, err := cache.load(model); err == nil {
			return modalities, nil
		}
		catalog, err := fetchCatalog(ctx, client, baseURL+"/api/v1/models")
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(catalog, func(m Model) bool { return m.ID == model })
		if i < 0 {
			return nil, fmt.Errorf("%w: %q", ErrModelNotInCatalog, model)
		}
		modalities := catalog[i].Architecture.OutputModalities
		if modalities == nil {
			modalities = []string{}
		}
		// An unusable cache dir runs uncached (ModalityLookup.2).
		_ = cache.store(model, modalities)
		return modalities, nil
	}
}

var ErrModelNotInCatalog = errors.New("model not in the openrouter catalog")

var errNoCatalogURL = errors.New("no openrouter catalog beside the chat URL")

// catalogLookup is ModalityLookup.1. The catalog sits beside the chat endpoint, so a
// chat URL that does not end in chatPath has none.
func (o *OpenRouter) catalogLookup() ModalityLookup {
	base, found := strings.CutSuffix(o.StreamCompleter.URL, chatPath)
	if !found {
		err := fmt.Errorf("%w: %q does not end in %s", errNoCatalogURL, o.StreamCompleter.URL, chatPath)
		return func(context.Context, string) ([]string, error) { return nil, err }
	}
	cacheDir, err := utils.GetClaiCacheDir()
	if err != nil {
		cacheDir = "" // ModalityLookup.2
	}
	return CatalogModalities(base, cacheDir, http.DefaultClient)
}

func fetchCatalog(ctx context.Context, client *http.Client, url string) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build openrouter models request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch openrouter models: %w", err)
	}
	return decodeCatalog(resp)
}

var errUncached = errors.New("modality cache disabled")

// modalityCache holds one answer per model; an empty dir caches nothing.
type modalityCache struct{ dir string }

func (c modalityCache) path(model string) (string, error) {
	if c.dir == "" {
		return "", errUncached
	}
	sum := sha256.Sum256([]byte(model))
	return filepath.Join(c.dir, "openrouter-modalities", hex.EncodeToString(sum[:])+".json"), nil
}

func (c modalityCache) load(model string) ([]string, error) {
	path, err := c.path(model)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read modality cache: %w", err)
	}
	var modalities []string
	if err := json.Unmarshal(b, &modalities); err != nil {
		return nil, fmt.Errorf("decode modality cache %s: %w", path, err)
	}
	return modalities, nil
}

func (c modalityCache) store(model string, modalities []string) error {
	path, err := c.path(model)
	if err != nil {
		return err
	}
	b, err := json.Marshal(modalities)
	if err != nil {
		return fmt.Errorf("encode modality cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create modality cache dir: %w", err)
	}
	if err := utils.WriteFileAtomic(path, b, 0o644); err != nil {
		return fmt.Errorf("write modality cache: %w", err)
	}
	return nil
}

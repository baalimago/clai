package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func readJSONKeys(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var keys map[string]any
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatalf("Unmarshal(%s): %v", path, err)
	}
	return keys
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func TestDefaultDeps_buildsRealImageGenerators(t *testing.T) {
	deps := DefaultDeps()
	if deps.NewImageGenerator == nil {
		t.Fatal("NewImageGenerator is nil, want route.NewGenerator")
	}
	var unknown *imagegen.UnknownImageModelError
	if _, err := deps.NewImageGenerator(imagegen.GeneratorConfig{Model: "not-an-image-model"}); !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want the router's UnknownImageModelError", err)
	}
}

func TestPhotoModel_readsPhotoConfig(t *testing.T) {
	confDir := t.TempDir()
	writeJSON(t, filepath.Join(confDir, "photoConfig.json"), map[string]any{"model": "or:google/x-image"})
	if got, err := photoModel(confDir); err != nil || got != "or:google/x-image" {
		t.Fatalf("photoModel = %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "photoConfig.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := photoModel(confDir); err == nil {
		t.Fatal("a broken photoConfig.json must fail")
	}
}

// TestConfig_ImageKeysDefaults is query.9: configs written before this worklog
// gain the new keys with their README defaults, and nothing else changes.
func TestConfig_ImageKeysDefaults(t *testing.T) {
	confDir := t.TempDir()
	isolateEnv(t)
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Chdir(t.TempDir())
	price := map[string]any{"price": map[string]any{"input_usd_per_token": 0.001, "input_cached_usd_per_token": 0.0005, "output_usd_per_token": 0.002}}
	writeJSON(t, filepath.Join(confDir, "mock_test_test.json"), price)
	textBefore := map[string]any{"model": "test", "system-prompt": "be brief", "use-tools": false, "summarize-conversations": false}
	photoBefore := map[string]any{"model": "dall-e-3", "prompt-format": "%v", "output": map[string]any{"type": "local", "dir": "/pics", "prefix": "p"}}
	writeJSON(t, filepath.Join(confDir, "textConfig.json"), textBefore)
	writeJSON(t, filepath.Join(confDir, "photoConfig.json"), photoBefore)

	built := 0
	deps := Deps{NewImageGenerator: func(imagegen.GeneratorConfig) (imagegen.ImageGenerator, error) {
		built++
		return nil, errors.New("refused")
	}}
	var status int
	_ = testboil.CaptureStderr(t, func(t *testing.T) {
		_ = testboil.CaptureStdout(t, func(t *testing.T) {
			status = Run([]string{"q", "hi"}, deps)
		})
	})
	if status != 0 {
		t.Fatalf("query status %d", status)
	}

	text := readJSONKeys(t, filepath.Join(confDir, "textConfig.json"))
	for key, want := range map[string]any{"auto-open-images": false, "image-opener": ""} {
		if got, ok := text[key]; !ok || got != want {
			t.Fatalf("textConfig %q = %v (present %v), want %v", key, got, ok, want)
		}
	}
	if _, ok := text["image-generation"]; ok {
		t.Fatal(`textConfig gained "image-generation", a removed key`)
	}
	for key, want := range textBefore {
		if !reflect.DeepEqual(text[key], want) {
			t.Fatalf("textConfig %q changed: %v -> %v", key, want, text[key])
		}
	}
	photoConf := readJSONKeys(t, filepath.Join(confDir, "photoConfig.json"))
	for key, want := range map[string]any{"auto-open-images": false, "image-opener": ""} {
		if got, ok := photoConf[key]; !ok || got != want {
			t.Fatalf("photoConfig %q = %v (present %v), want %v", key, got, ok, want)
		}
	}
	for key, want := range photoBefore {
		if !reflect.DeepEqual(photoConf[key], want) {
			t.Fatalf("photoConfig %q changed: %v -> %v", key, want, photoConf[key])
		}
	}
	if built != 0 {
		t.Fatalf("a query that selects no image tool built %d generators", built)
	}
	if _, err := os.Stat(filepath.Join(confDir, "conversations", "assets")); !os.IsNotExist(err) {
		t.Fatalf("a text-only query touched the asset store: %v", err)
	}
	if _, err := photo.PreviousQueryImage(confDir); !errors.Is(err, photo.ErrNoPreviousImage) {
		t.Fatalf("a text-only query left an image: %v", err)
	}
}

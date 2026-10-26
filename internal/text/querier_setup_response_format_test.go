package text

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func responseFormatConf(t *testing.T) Configurations {
	t.Helper()
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Setenv("OPENROUTER_API_KEY", "")
	confDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confDir, "mcpServers"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return Configurations{
		Model: "test", ConfigDir: confDir, SkipAmbientMcpServers: true, Raw: true, Out: &strings.Builder{},
		ResponseFormat: &pub_models.ResponseFormat{Type: "json_object"},
	}
}

// TestNewQuerier_responseFormatUnsupported pins that -rf fails loudly on a
// model that cannot accept a response format, instead of silently streaming
// prose.
func TestNewQuerier_responseFormatUnsupported(t *testing.T) {
	_, err := NewQuerier(t.Context(), responseFormatConf(t), mockCompleter{})
	if !errors.Is(err, generic.ErrResponseFormatUnsupported) {
		t.Fatalf("NewQuerier err = %v, want ErrResponseFormatUnsupported", err)
	}
}

func TestNewQuerier_responseFormatSupported(t *testing.T) {
	if _, err := NewQuerier(t.Context(), responseFormatConf(t), &vendors.Mock{}); err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}
}

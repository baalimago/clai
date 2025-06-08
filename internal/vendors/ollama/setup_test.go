package ollama

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/vendors/vendorstest"
)

func TestSetup(t *testing.T) {
	vendorstest.RunSetupTests(t, "OLLAMA_API_KEY", false, func() models.StreamCompleter {
		v := OLLAMA_DEFAULT
		return &v
	})
}

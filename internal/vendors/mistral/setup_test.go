package mistral

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/vendors/vendorstest"
)

func TestSetup(t *testing.T) {
	vendorstest.RunSetupTests(t, "MISTRAL_API_KEY", true, func() models.StreamCompleter {
		v := Default
		return &v
	})
}

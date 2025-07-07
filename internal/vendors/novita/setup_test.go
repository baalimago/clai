package novita

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/vendors/vendorstest"
)

func TestSetup(t *testing.T) {
	vendorstest.RunSetupTests(t, "NOVITA_API_KEY", false, func() models.StreamCompleter {
		v := Default
		return &v
	})
}

package inception_test

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/vendors/inception"
	"github.com/baalimago/clai/internal/vendors/vendorstest"
)

func TestSetup(t *testing.T) {
	vendorstest.RunSetupTests(t, "INCEPTION_API_KEY", true, func() models.StreamCompleter {
		v := inception.Default
		return &v
	})
}

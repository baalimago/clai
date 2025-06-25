package openai

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/vendors/vendorstest"
)

func TestSetup(t *testing.T) {
	vendorstest.RunSetupTests(t, "OPENAI_API_KEY", true, func() models.StreamCompleter {
		v := GptDefault
		return &v
	})
}

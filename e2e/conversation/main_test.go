package conversation

import (
	"os"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

// harness holds this binary's CLI collaborators; TestMain refuses the paid ones.
var harness = e2etest.NewHarness()

func TestMain(m *testing.M) {
	harness.Deps.NewSummarizer = e2etest.RefusingSummarizer
	harness.Deps.NewImageGenerator = e2etest.RefusingImageGenerator
	e2etest.DetachTTY()
	os.Exit(m.Run())
}

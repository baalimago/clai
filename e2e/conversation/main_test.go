package conversation

import (
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/testenv"
)

// harness holds this binary's CLI collaborators; TestMain refuses the paid ones.
var harness = e2etest.NewHarness()

// The chat listing vendors scan the user's home for foreign sessions, which
// costs seconds per run of a developer's machine; HOME is isolated so a run
// pays for clai and not for those files.
func TestMain(m *testing.M) {
	harness.Deps.NewSummarizer = e2etest.RefusingSummarizer
	harness.Deps.NewImageGenerator = e2etest.RefusingImageGenerator
	e2etest.Main(m, testenv.Setup{})
}

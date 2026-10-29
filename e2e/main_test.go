package e2e

import (
	"os"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// cliModeEnv makes this test binary run as the clai executable (see claiBinDir).
const cliModeEnv = "E2E_CLI_MODE"

// harness holds this binary's CLI collaborators; TestMain refuses the paid ones.
var harness = e2etest.NewHarness()

// The chat listing vendors scan the user's home for foreign sessions, which
// costs seconds per run of a developer's machine; HOME is isolated so a run
// pays for clai and not for those files.
func TestMain(m *testing.M) {
	harness.Deps.NewSummarizer = e2etest.RefusingSummarizer
	harness.Deps.NewImageGenerator = e2etest.RefusingImageGenerator
	testenv.Tune()
	if os.Getenv(cliModeEnv) == "1" {
		// main.go's body, with this binary's collaborators.
		ancli.SetupSlog()
		os.Exit(harness.RunProfiled(os.Args[1:]))
	}
	// The PTY helper child keeps the environment its test built for it.
	if os.Getenv(e2etest.SetupPTYHelperEnv) == "1" {
		os.Exit(m.Run())
	}
	e2etest.Main(m, testenv.Setup{})
}

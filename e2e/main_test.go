package e2e

import (
	"os"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// cliModeEnv makes this test binary run as the clai executable (see claiBinDir).
const cliModeEnv = "CLAI_E2E_CLI_MODE"

// harness holds this binary's CLI collaborators; TestMain refuses the paid ones.
var harness = e2etest.NewHarness()

func TestMain(m *testing.M) {
	harness.Deps.NewSummarizer = e2etest.RefusingSummarizer
	if os.Getenv(cliModeEnv) == "1" {
		// main.go's body, with this binary's collaborators.
		ancli.SetupSlog()
		os.Exit(harness.RunProfiled(os.Args[1:]))
	}
	e2etest.DetachTTY()
	os.Exit(m.Run())
}

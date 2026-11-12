package tooling

import (
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

// harness holds this binary's CLI collaborators; TestMain refuses the paid ones.
var harness = e2etest.NewHarness()

func TestMain(m *testing.M) { e2etest.MainHarness(m, harness, e2etest.TestServerSetup()) }

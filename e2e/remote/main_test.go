// Package remote holds the e2e rows for remote targets, cloud and hub
// publishing, remote profiles and their validation, and the config commands
// that read and write their aliases. They run in their own binary, so their
// hub fixtures and their own -test.timeout budget are not charged to e2e's
// in-process rows.
package remote

import (
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/testenv"
)

// harness holds this binary's CLI collaborators; TestMain refuses the paid ones.
var harness = e2etest.NewHarness()

func TestMain(m *testing.M) { e2etest.MainHarness(m, harness, testenv.Setup{}) }

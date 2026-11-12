// Package interactive holds the e2e rows that need a terminal, a child process
// or a device fixture: the setup wizard, replay and reply-only on a
// pseudo-terminal, bash-driven completion, audio capture and image generation.
// They run in their own binary, so every row here shares a -test.timeout
// budget with rows that spawn and wait instead of with e2e's in-process rows.
package interactive

import (
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/testenv"
)

// harness holds this binary's CLI collaborators; TestMain refuses the paid ones.
var harness = e2etest.NewHarness()

func TestMain(m *testing.M) { e2etest.MainHarness(m, harness, testenv.Setup{}) }

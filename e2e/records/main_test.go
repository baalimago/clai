// Package records holds the e2e rows of the machine-readable surface: every
// -json run writes F14 records only, and each destination is used exactly when
// the same run without -json uses it. `clai tools -json` is the documented
// exception: it writes one JSON document (architecture/output.md). It is its
// own binary because those rows run every scenario twice and two of them wait
// a device out.
package records

import (
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

// harness holds this binary's CLI collaborators; TestMain refuses the paid ones.
var harness = e2etest.NewHarness()

func TestMain(m *testing.M) { e2etest.MainHarness(m, harness, e2etest.TestServerSetup()) }

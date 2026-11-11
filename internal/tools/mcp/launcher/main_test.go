package launcher

import (
	"testing"

	"github.com/baalimago/clai/internal/testenv"
)

// The rows classify npx command lines, and that classification looks the
// launcher up on PATH, so the binary reads Unresolvable on a host without node
// instead of testing the shape it names (2026-11-11).
func TestMain(m *testing.M) { testenv.MainWith(m, testenv.FakeBinsSetup("npx", "node")) }

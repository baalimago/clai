package e2etest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
)

// Main is the TestMain body the e2e packages share: a capped, hermetic binary
// whose working directory lies inside the isolated home, so the skills
// ancestor walk never climbs into the developer's tree. s.Before still runs
// from the package directory, so a go build in it finds the module.
func Main(m *testing.M, s testenv.Setup) {
	before := s.Before
	s.Before = func(home testenv.Home) error {
		if before != nil {
			if err := before(home); err != nil {
				return err
			}
		}
		work := filepath.Join(string(home), "work")
		if err := os.MkdirAll(work, 0o755); err != nil {
			return fmt.Errorf("create e2e work dir: %w", err)
		}
		if err := os.Chdir(work); err != nil {
			return fmt.Errorf("enter e2e work dir: %w", err)
		}
		return nil
	}
	testenv.MainWith(m, s)
}

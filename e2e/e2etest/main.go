package e2etest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
)

// CLIModeEnv makes a test binary run as the clai executable (CLIBinDir).
const CLIModeEnv = "E2E_CLI_MODE"

// SetupPTYHelperEnv marks the child process of the interactive setup PTY test.
const SetupPTYHelperEnv = "E2E_SETUP_PTY_HELPER"

// Main is the TestMain body the e2e packages share: a capped, hermetic binary
// whose working directory lies inside the isolated home, so the skills
// ancestor walk never climbs into the developer's tree. s.Before still runs
// from the package directory, so a go build in it finds the module.
//
// A binary started as the clai executable runs the CLI instead of the tests,
// and a PTY helper child keeps the environment its test built for it.
func Main(m *testing.M, h *Harness, s testenv.Setup) {
	testenv.Tune()
	if os.Getenv(CLIModeEnv) == "1" {
		os.Exit(h.RunProfiled(os.Args[1:]))
	}
	if os.Getenv(SetupPTYHelperEnv) == "1" {
		os.Exit(m.Run())
	}
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

// MainHarness is Main for a package whose harness refuses the paid
// collaborators: no e2e row may reach a model, an image service or a summary
// service.
func MainHarness(m *testing.M, h *Harness, s testenv.Setup) {
	h.Deps.NewSummarizer = RefusingSummarizer
	h.Deps.NewImageGenerator = RefusingImageGenerator
	Main(m, h, s)
}

// CLIBinDir returns a directory whose clai is this test binary, which Main runs
// as the CLI while CLIModeEnv is set, so a row that needs a fresh clai process
// never waits on a go build.
func CLIBinDir(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	if err := os.Symlink(self, filepath.Join(dir, "clai")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	t.Setenv(CLIModeEnv, "1")
	// A -race child otherwise idles 1s in its exit hook; a -cover one warns
	// without a dir.
	t.Setenv("GORACE", testenv.RaceExitSleepOff(os.Getenv("GORACE")))
	t.Setenv("GOCOVERDIR", t.TempDir())
	return dir
}

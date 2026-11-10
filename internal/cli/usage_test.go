package cli

import (
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/runenv"
)

// TestUsageExplainsCommandsAndSubsystems pins what 'clai help' says beyond
// the command table: where a command's own examples are, how LOG_LEVEL
// narrows what is shown, and every registered subsystem with its purpose.
func TestUsageExplainsCommandsAndSubsystems(t *testing.T) {
	useConfigDir(t)
	got := runCLI(t, "help", cliOpts{})
	if got.status != 0 || got.stderr != "" {
		t.Fatalf("status %d stderr %q, want 0 and none", got.status, got.stderr)
	}
	for _, want := range []string{
		"'clai query -h'",
		"LOG_LEVEL=debug clai query",
		"LOG_LEVEL=warn,mcp=debug",
		"vendor=debug",
		"-json",
		"Subsystems:",
		"Config dir: ",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
	for _, subsystem := range runenv.Subsystems() {
		row := false
		for line := range strings.SplitSeq(got.stdout, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 1 && fields[0] == subsystem.Name && strings.HasSuffix(line, subsystem.Purpose) {
				row = true
			}
		}
		if !row {
			t.Errorf("usage has no row for subsystem %q with purpose %q", subsystem.Name, subsystem.Purpose)
		}
	}
	if strings.Contains(got.stdout, "Examples:") {
		t.Errorf("usage still lists examples; each command's -h has its own")
	}
	if strings.Contains(got.stdout, "%!") {
		t.Errorf("usage has a formatting artefact: %q", got.stdout)
	}
}

// TestUsageLogLevelExamplesAreAccepted runs the usage's own LOG_LEVEL
// examples, so the help never teaches a value clai refuses.
func TestUsageLogLevelExamplesAreAccepted(t *testing.T) {
	useConfigDir(t)
	for _, level := range []string{"debug", "warn,mcp=debug", "warn,vendor=debug", "warn,vendor.openai=debug"} {
		t.Setenv("LOG_LEVEL", level)
		got := runCLI(t, "version", cliOpts{})
		if got.status != 0 || !strings.HasPrefix(got.stdout, "version: ") {
			t.Errorf("LOG_LEVEL=%s: status %d stdout %q stderr %q", level, got.status, got.stdout, got.stderr)
		}
	}
}

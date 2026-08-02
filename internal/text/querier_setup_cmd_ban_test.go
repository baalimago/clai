package text

import (
	"context"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// Test_NewQuerier_AppliesCmdBanList pins the per-run injection (D6): the
// effective list from Configurations.CmdBan must reach pkg/tools before any
// tool executes, so a configured ban is enforced at the spawn point.
func Test_NewQuerier_AppliesCmdBanList(t *testing.T) {
	// Avoid races with the cost manager error logger goroutine in NewQuerier.
	// Some tests capture stdout by swapping the global os.Stdout.
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	conf := Configurations{
		Model:     "mock",
		ConfigDir: t.TempDir(),
		CmdBan:    []string{"rm"},
	}

	if _, err := NewQuerier(context.Background(), conf, &MockQuerier{}); err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}

	_, err := pkgtools.Cmd.Call(pub_models.Input{"command": "rm -rf /"})
	if err == nil {
		t.Fatal("expected banned command to be refused after NewQuerier with CmdBan")
	}
	for _, want := range []string{"command is banned by policy", `matched entry "rm"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected refusal to contain %q, got %q", want, err.Error())
		}
	}
}

// Test_NewQuerier_NoCmdBanStaysPermissive pins the default (D4): with no bans
// configured the setter still runs (D6) but with an empty list, so ad-hoc
// command execution works exactly as before.
func Test_NewQuerier_NoCmdBanStaysPermissive(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	conf := Configurations{
		Model:     "mock",
		ConfigDir: t.TempDir(),
	}

	if _, err := NewQuerier(context.Background(), conf, &MockQuerier{}); err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}

	out, err := pkgtools.Cmd.Call(pub_models.Input{"command": "echo hi"})
	if err != nil {
		t.Fatalf("expected permissive behavior, got %v", err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("expected output 'hi', got %q", out)
	}
}

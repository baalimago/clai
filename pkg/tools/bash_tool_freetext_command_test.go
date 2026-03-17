package tools

import (
	"runtime"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestFreetextCmdTool_Call_PreservesQuotedArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}

	out, err := FreetextCmd.Call(pub_models.Input{"command": `printf '%s' "hello world"`})
	if err != nil {
		t.Fatalf("freetext command failed: %v", err)
	}

	if got, want := out, "hello world"; got != want {
		t.Fatalf("unexpected output: got %q want %q", got, want)
	}
}

func TestFreetextCmdTool_Call_BadType(t *testing.T) {
	_, err := FreetextCmd.Call(pub_models.Input{"command": 123})
	if err == nil {
		t.Fatal("expected error for bad command type")
	}
	if !strings.Contains(err.Error(), "command") {
		t.Fatalf("expected contextual error mentioning command, got %v", err)
	}
}

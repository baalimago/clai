package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// assertCmdBanRefusal verifies a refusal error names the matched entry and
// states the rule (phase 2, D7).
func assertCmdBanRefusal(t *testing.T, err error, entry string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected ban refusal error")
	}
	msg := err.Error()
	for _, want := range []string{
		"command is banned by policy",
		fmt.Sprintf("matched entry %q", entry),
		"Do not run commands matching this rule",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected refusal to contain %q, got %q", want, msg)
		}
	}
}

// banCtx installs entries as the policy of a fresh test context.
func banCtx(t *testing.T, entries ...string) context.Context {
	t.Helper()
	return WithCmdBanContext(t.Context(), entries)
}

func TestCmdBanEnforcement_ValidateCmdNotBanned(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		command string
		args    []string
		wantErr bool
		want    string // matched entry, when banned
	}{
		{"empty list permissive", []string{}, "rm -rf /", nil, false, ""},
		{"freetext banned", []string{"rm"}, "rm -rf /", nil, true, "rm"},
		{"freetext allowed", []string{"rm"}, "echo hi", nil, false, ""},
		{"command token participates", []string{"git"}, "git", []string{"log"}, true, "git"},
		{"arg token participates", []string{"rm"}, "echo", []string{"rm", "-rf", "/"}, true, "rm"},
		{"arg phrase flattened", []string{"git commit"}, "sh", []string{"-c", "git commit"}, true, "git commit"},
		{"first match in list order", []string{"rm", "rm -rf"}, "rm -rf /", nil, true, "rm"},
		{"non-contiguous argv not banned", []string{"git commit"}, "git", []string{"-C", "/path", "commit"}, false, ""},
		{"empty command not banned", []string{"rm"}, "", nil, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCmdNotBannedWithContext(banCtx(t, tt.entries...), tt.command, tt.args)
			if tt.wantErr {
				assertCmdBanRefusal(t, err, tt.want)
				return
			}
			if err != nil {
				t.Fatalf("validateCmdNotBannedWithContext(%q, %v) = %v, want nil", tt.command, tt.args, err)
			}
		})
	}
}

// TestCmdBanEnforcement_NoPolicyPermissive pins the D29 default: a context
// that carries no policy, and the context-free Call entry point, enforce
// nothing.
func TestCmdBanEnforcement_NoPolicyPermissive(t *testing.T) {
	if err := validateCmdNotBannedWithContext(context.Background(), "rm -rf /", nil); err != nil {
		t.Fatalf("context without policy must be permissive, got %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}
	for name, call := range map[string]func(pub_models.Input) (string, error){
		"Cmd.Call":            Cmd.Call,
		"Cmd.CallWithContext": func(in pub_models.Input) (string, error) { return Cmd.CallWithContext(t.Context(), in) },
	} {
		t.Run(name, func(t *testing.T) {
			out, err := call(pub_models.Input{"command": "echo hi"})
			if err != nil {
				t.Fatalf("no policy must be permissive: %v", err)
			}
			if !strings.Contains(out, "hi") {
				t.Fatalf("expected output 'hi', got %q", out)
			}
		})
	}
}

// TestCmdBanEnforcement_ContextPolicySnapshotsInput pins the ownership rule:
// the policy is copied at WithCmdBanContext, so a caller mutating its slice
// afterwards never alters the installed policy.
func TestCmdBanEnforcement_ContextPolicySnapshotsInput(t *testing.T) {
	entries := []string{"rm"}
	ctx := WithCmdBanContext(t.Context(), entries)
	entries[0] = "sudo"

	assertCmdBanRefusal(t, validateCmdNotBannedWithContext(ctx, "rm -rf /", nil), "rm")
	if err := validateCmdNotBannedWithContext(ctx, "sudo apt update", nil); err != nil {
		t.Fatalf("caller mutation after WithCmdBanContext leaked into the policy: %v", err)
	}
}

func TestCmdBanEnforcement_FreetextRefusesBannedBeforeSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker-dir")
	_, err := Cmd.CallWithContext(banCtx(t, "rm"), pub_models.Input{"command": "rm -rf " + marker})
	assertCmdBanRefusal(t, err, "rm")
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("banned command must never spawn, marker exists: %v", statErr)
	}
}

func TestCmdBanEnforcement_FreetextQuotedBypassBanned(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker-dir")
	_, err := Cmd.CallWithContext(banCtx(t, "rm"), pub_models.Input{"command": "sh -c \"rm -rf " + marker + "\""})
	assertCmdBanRefusal(t, err, "rm")
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("banned command must never spawn, marker exists: %v", statErr)
	}
}

func TestCmdBanEnforcement_AsyncRefusesBannedBeforeSpawn(t *testing.T) {
	ResetAsyncCmdManagerForTests()
	tests := []struct {
		name    string
		entries []string
		command string
		args    []string
		want    string
	}{
		{"phrase in command and args", []string{"git commit"}, "git", []string{"commit", "-m", "x"}, "git commit"},
		{"command token matches", []string{"git"}, "git", []string{"log"}, "git"},
		{"arg token matches", []string{"rm"}, "echo", []string{"rm", "-rf", "/"}, "rm"},
		{"phrase inside an arg", []string{"git commit"}, "sh", []string{"-c", "git commit -m x"}, "git commit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := AsyncCmdRun.CallWithContext(banCtx(t, tt.entries...), pub_models.Input{
				"command": tt.command,
				"args":    anySlice(tt.args),
				"cwd":     t.TempDir(), // harmless even if the ban check regressed
			})
			assertCmdBanRefusal(t, err, tt.want)
			if got := AsyncCmdSnapshotForTests(); len(got) != 0 {
				t.Fatalf("banned async command must never spawn, snapshot=%+v", got)
			}
		})
	}
}

func TestCmdBanEnforcement_AsyncNonContiguousAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}
	ResetAsyncCmdManagerForTests()

	out, err := AsyncCmdRun.CallWithContext(banCtx(t, "git commit"), pub_models.Input{
		"command": "sh",
		"args":    []any{"-c", "true"},
		"cwd":     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("allowed async command refused: %v", err)
	}
	if !strings.Contains(out, `"async_cmd_id"`) {
		t.Fatalf("expected spawn payload, got %q", out)
	}
}

func TestCmdBanEnforcement_AllowedPasses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}
	out, err := Cmd.CallWithContext(banCtx(t, "rm"), pub_models.Input{"command": "echo hi"})
	if err != nil {
		t.Fatalf("allowed command refused: %v", err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("expected output 'hi', got %q", out)
	}
}

func TestCmdBanEnforcement_EmptyCommandErrorUnchanged(t *testing.T) {
	_, err := Cmd.CallWithContext(banCtx(t, "rm"), pub_models.Input{"command": ""})
	if err == nil {
		t.Fatal("expected empty-command error")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("expected existing empty-string error, got %q", err)
	}
	if strings.Contains(err.Error(), "banned by policy") {
		t.Fatalf("ban check must run after validation, got %q", err)
	}
}

func TestCmdBanEnforcement_DescriptionsMentionRefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		desc string
	}{
		{"cmd", Cmd.Specification().Description},
		{"async_cmd", AsyncCmdRun.Specification().Description},
	} {
		if !strings.Contains(tc.desc, "refused by configured policy") {
			t.Fatalf("%s description must mention policy refusal, got %q", tc.name, tc.desc)
		}
	}
}

func anySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

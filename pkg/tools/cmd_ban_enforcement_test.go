package tools

import (
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

func TestCmdBanEnforcement_SetAndReset(t *testing.T) {
	ResetCmdBanListForTests()
	t.Cleanup(ResetCmdBanListForTests)

	if err := validateCmdNotBanned("rm -rf /", nil); err != nil {
		t.Fatalf("default state must be permissive, got %v", err)
	}
	SetCmdBanList([]string{"rm"})
	if err := validateCmdNotBanned("rm -rf /", nil); err == nil {
		t.Fatal("expected ban after SetCmdBanList")
	}
	ResetCmdBanListForTests()
	if err := validateCmdNotBanned("rm -rf /", nil); err != nil {
		t.Fatalf("reset must restore permissive default, got %v", err)
	}
}

func TestCmdBanEnforcement_SetterSnapshotsInput(t *testing.T) {
	ResetCmdBanListForTests()
	t.Cleanup(ResetCmdBanListForTests)

	entries := []string{"rm"}
	SetCmdBanList(entries)
	// Caller mutation after the setter returns must not leak into the active
	// list: the setter owns an immutable snapshot of its input (README
	// "Ban-list ownership", review 6 R6-02).
	entries[0] = "sudo"

	assertCmdBanRefusal(t, validateCmdNotBanned("rm -rf /", nil), "rm")
	if err := validateCmdNotBanned("sudo apt update", nil); err != nil {
		t.Fatalf("caller mutation after SetCmdBanList leaked into the snapshot: %v", err)
	}
}

func TestCmdBanEnforcement_ContextPolicyOverridesGlobalPolicy(t *testing.T) {
	SetCmdBanList([]string{"rm"})
	t.Cleanup(ResetCmdBanListForTests)

	ctx := WithCmdBanContext(t.Context(), []string{"touch"})
	assertCmdBanRefusal(t, validateCmdNotBannedWithContext(ctx, "touch marker", nil), "touch")
	if err := validateCmdNotBannedWithContext(ctx, "rm -rf marker", nil); err != nil {
		t.Fatalf("context policy should replace global policy, got %v", err)
	}
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
			SetCmdBanList(tt.entries)
			t.Cleanup(ResetCmdBanListForTests)
			err := validateCmdNotBanned(tt.command, tt.args)
			if tt.wantErr {
				assertCmdBanRefusal(t, err, tt.want)
				return
			}
			if err != nil {
				t.Fatalf("validateCmdNotBanned(%q, %v) = %v, want nil", tt.command, tt.args, err)
			}
		})
	}
}

func TestCmdBanEnforcement_FreetextRefusesBannedBeforeSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker-dir")
	tests := []struct {
		name string
		call func(pub_models.Input) (string, error)
	}{
		{"Cmd.Call", func(in pub_models.Input) (string, error) { return Cmd.Call(in) }},
		{"Cmd.CallWithContext", func(in pub_models.Input) (string, error) { return Cmd.CallWithContext(t.Context(), in) }},
		{"Cmd.Call", func(in pub_models.Input) (string, error) { return Cmd.Call(in) }},
		{"Cmd.CallWithContext", func(in pub_models.Input) (string, error) { return Cmd.CallWithContext(t.Context(), in) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetCmdBanList([]string{"rm"})
			t.Cleanup(ResetCmdBanListForTests)

			_, err := tt.call(pub_models.Input{"command": "rm -rf " + marker})
			assertCmdBanRefusal(t, err, "rm")
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("banned command must never spawn, marker exists: %v", statErr)
			}
		})
	}
}

func TestCmdBanEnforcement_FreetextQuotedBypassBanned(t *testing.T) {
	SetCmdBanList([]string{"rm"})
	t.Cleanup(ResetCmdBanListForTests)

	marker := filepath.Join(t.TempDir(), "marker-dir")
	_, err := Cmd.Call(pub_models.Input{"command": "sh -c \"rm -rf " + marker + "\""})
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
			SetCmdBanList(tt.entries)
			t.Cleanup(ResetCmdBanListForTests)

			_, err := AsyncCmdRun.Call(pub_models.Input{
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
	SetCmdBanList([]string{"git commit"})
	t.Cleanup(ResetCmdBanListForTests)

	out, err := AsyncCmdRun.Call(pub_models.Input{
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
	SetCmdBanList([]string{"rm"})
	t.Cleanup(ResetCmdBanListForTests)

	out, err := Cmd.Call(pub_models.Input{"command": "echo hi"})
	if err != nil {
		t.Fatalf("allowed command refused: %v", err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("expected output 'hi', got %q", out)
	}
}

func TestCmdBanEnforcement_DefaultPermissive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}
	ResetCmdBanListForTests()
	t.Cleanup(ResetCmdBanListForTests)

	out, err := Cmd.Call(pub_models.Input{"command": "echo hi"})
	if err != nil {
		t.Fatalf("default state must be permissive: %v", err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("expected output 'hi', got %q", out)
	}
}

func TestCmdBanEnforcement_EmptyCommandErrorUnchanged(t *testing.T) {
	SetCmdBanList([]string{"rm"})
	t.Cleanup(ResetCmdBanListForTests)

	_, err := Cmd.Call(pub_models.Input{"command": ""})
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
		{"cmd", Cmd.Specification().Description},
		{"freetext_command", Cmd.Specification().Description},
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

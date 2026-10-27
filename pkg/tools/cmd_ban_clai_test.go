package tools

import (
	"slices"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// A nested clai's prompt is prose for its model, not a command: a worker
// brief that says "rm old files" is not refused by entry rm. Everything that
// is executed is still matched.
func TestCmdBanEnforcement_NestedClaiPromptIsProse(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    string // matched entry; "" when allowed
	}{
		{"worker brief mentioning rm", "clai", []string{"-p", "worker", "q", "rm old files, then commit"}, ""},
		{"clai by path", "/usr/local/bin/clai", []string{"q", "rm the build dir"}, ""},
		{"a lone rm argument is still a token", "clai", []string{"q", "rm"}, "rm"},
		{"clai itself can be banned", "clai", []string{"q", "hello there"}, "clai"},
		{"a shell running clai is still matched", "sh", []string{"-c", "clai q 'rm old files'"}, "rm"},
		{"other commands keep literal matching", "git", []string{"commit", "-m", "rm old files"}, "rm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := []string{"rm"}
			if tt.want == "clai" {
				entries = []string{"clai"}
			}
			err := validateCmdNotBannedWithContext(banCtx(t, entries...), tt.command, tt.args)
			if tt.want != "" {
				assertCmdBanRefusal(t, err, tt.want)
				return
			}
			if err != nil {
				t.Fatalf("validateCmdNotBannedWithContext(%q, %q) = %v, want nil", tt.command, tt.args, err)
			}
		})
	}
}

// Under a policy, nothing may touch the variable that hands the policy to
// nested clai runs.
func TestCmdBanEnforcement_PolicyVariableIsProtected(t *testing.T) {
	ctx := banCtx(t, "rm")
	for _, command := range []string{"CLAI_CMD_BAN= clai q hi", "unset CLAI_CMD_BAN; clai q hi", "env -u CLAI_CMD_BAN clai q hi"} {
		if err := validateCmdNotBannedWithContext(ctx, command, nil); err == nil || !strings.Contains(err.Error(), CmdBanEnv) {
			t.Fatalf("%q: err = %v, want a refusal naming %s", command, err, CmdBanEnv)
		}
	}
	ResetAsyncCmdManagerForTests()
	_, err := AsyncCmdRun.CallWithContext(ctx, pub_models.Input{
		"command": "clai",
		"args":    []any{"q", "hi"},
		"env":     map[string]any{CmdBanEnv: ""},
		"cwd":     t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), CmdBanEnv) {
		t.Fatalf("env override: err = %v, want a refusal naming %s", err, CmdBanEnv)
	}
	if got := AsyncCmdSnapshotForTests(); len(got) != 0 {
		t.Fatalf("refused async command must never spawn, snapshot=%+v", got)
	}
	if err := validateCmdNotBannedWithContext(t.Context(), "CLAI_CMD_BAN= true", nil); err != nil {
		t.Fatalf("without a policy the variable is free: %v", err)
	}
}

// The policy rides the environment of every spawned command, so a nested
// clai enforces it on what its own model runs.
func TestCmdBanEnv_HandsThePolicyToChildren(t *testing.T) {
	if got := withCmdBanEnv(t.Context(), nil); len(got) != 0 {
		t.Fatalf("no policy: env = %v, want untouched", got)
	}
	got := withCmdBanEnv(banCtx(t, "rm", "git commit"), map[string]string{"A": "1"})
	if got["A"] != "1" || got[CmdBanEnv] != "rm\ngit commit" {
		t.Fatalf("env = %v", got)
	}
	if entries := ParseCmdBanEnv("rm\n\n git commit \n"); !slices.Equal(entries, []string{"rm", "git commit"}) {
		t.Fatalf("ParseCmdBanEnv = %q", entries)
	}
	if entries := ParseCmdBanEnv(""); len(entries) != 0 {
		t.Fatalf("ParseCmdBanEnv(\"\") = %q", entries)
	}
}

func TestCmdBanEnforcement_FreetextChildSeesThePolicy(t *testing.T) {
	out, err := Cmd.CallWithContext(banCtx(t, "rm", "git commit"), pub_models.Input{"command": "env"})
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	if !strings.Contains(out, CmdBanEnv+"=rm\ngit commit\n") {
		t.Fatalf("child env lacks the policy:\n%s", out)
	}
}

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/text"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// writeFailOnceCheck returns a shell script that rejects its first run, then
// accepts every later run, proving the loop retries a gated completion.
func writeFailOnceCheck(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "verified")
	script := filepath.Join(dir, "check.sh")
	body := fmt.Sprintf("#!/bin/sh\nif [ -f %q ]; then exit 0; fi\ntouch %q\necho 'goal not verified yet' >&2\nexit 1\n", marker, marker)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile(check script): %v", err)
	}
	return script
}

// loadLoopConversations returns every persisted conversation except the
// global-scope mirror, which every iteration overwrites.
func loadLoopConversations(t *testing.T, confDir string) []pub_models.Chat {
	t.Helper()
	convDir := filepath.Join(confDir, "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", convDir, err)
	}
	var chats []pub_models.Chat
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || name == "globalScope.json" {
			continue
		}
		loaded, err := chat.FromPath(filepath.Join(convDir, name))
		if err != nil {
			t.Fatalf("FromPath(%q): %v", name, err)
		}
		chats = append(chats, loaded)
	}
	return chats
}

// toolResultFor returns the tool message paired with a call of the given name.
func toolResultFor(t *testing.T, conv pub_models.Chat, toolName string) (string, bool) {
	t.Helper()
	callIDs := make(map[string]struct{})
	for _, msg := range conv.Messages {
		for _, call := range msg.ToolCalls {
			if call.Name == toolName {
				callIDs[call.ID] = struct{}{}
			}
		}
	}
	for _, msg := range conv.Messages {
		if msg.Role != "tool" {
			continue
		}
		if _, ok := callIDs[msg.ToolCallID]; ok {
			return msg.Content, true
		}
	}
	return "", false
}

func Test_e2e_loop_persists_selected_profile(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	profileName := "loop-profile"
	profile := text.Profile{
		Name:            profileName,
		Model:           "test",
		UseTools:        true,
		Prompt:          "loop profile prompt",
		SaveReplyAsConv: new(true),
	}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("Marshal(profile): %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "profiles", profileName+".json"), body, 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}

	_, status := harness.RunOne(t, confDir, "-r -cm test -p "+profileName+" -check "+writeFailOnceCheck(t)+" l run tool_complete_task")
	testboil.FailTestIfDiff(t, status, 0)

	convs := loadLoopConversations(t, confDir)
	if len(convs) != 2 {
		t.Fatalf("saved %d conversations, want one per iteration", len(convs))
	}
	for _, conv := range convs {
		if conv.Profile != profileName {
			t.Fatalf("conversation %q persisted profile = %q, want %q", conv.ID, conv.Profile, profileName)
		}
	}
}

func Test_e2e_loop_achieves_goal(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_MOCK_COMPLETE_TASK_STATUS", "achieved")
	t.Setenv("CLAI_MOCK_COMPLETE_TASK_SUMMARY", "The goal is achieved.")

	stdout, status := harness.RunOne(t, confDir, "-r -cm test l run tool_complete_task")
	testboil.FailTestIfDiff(t, status, 0)

	convs := loadLoopConversations(t, confDir)
	if len(convs) != 1 {
		t.Fatalf("saved %d conversations, want the one completed iteration. stdout=%q", len(convs), stdout)
	}
	result, ok := toolResultFor(t, convs[0], "complete_task")
	if !ok {
		t.Fatalf("complete_task call was not persisted, messages: %+v", convs[0].Messages)
	}
	testboil.AssertStringContains(t, result, "recorded")
	if convs[0].GroupKey == "" {
		t.Fatal("completed conversation has an empty GroupKey")
	}
}

func Test_e2e_loop_completion_tool_is_additive(t *testing.T) {
	t.Setenv("CLAI_MOCK_COMPLETE_TASK_STATUS", "achieved")
	t.Setenv("CLAI_MOCK_COMPLETE_TASK_SUMMARY", "The goal is achieved.")

	for _, selection := range []string{"-t *", "-t ls"} {
		t.Run(selection, func(t *testing.T) {
			confDir := e2etest.SetupMainTestConfigDir(t)
			_, status := harness.RunOne(t, confDir, "-r -cm test "+selection+" l run tool_complete_task")
			testboil.FailTestIfDiff(t, status, 0)

			convs := loadLoopConversations(t, confDir)
			if len(convs) != 1 {
				t.Fatalf("saved %d conversations, want the completed iteration", len(convs))
			}
			result, ok := toolResultFor(t, convs[0], "complete_task")
			if !ok {
				t.Fatalf("complete_task call was not persisted, messages: %+v", convs[0].Messages)
			}
			testboil.AssertStringContains(t, result, "recorded")
		})
	}
}

func Test_e2e_loop_handover_and_check_gate_groups_chats(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_MOCK_COMPLETE_TASK_STATUS", "achieved")
	t.Setenv("CLAI_MOCK_COMPLETE_TASK_SUMMARY", "The goal is verified.")
	checkScript := writeFailOnceCheck(t)

	_, status := harness.RunOne(t, confDir, "-r -cm test -check "+checkScript+" l run tool_complete_task")
	testboil.FailTestIfDiff(t, status, 0)

	convs := loadLoopConversations(t, confDir)
	if len(convs) != 2 {
		t.Fatalf("saved %d conversations, want one per iteration; a StopEvent must not cancel the outer loop", len(convs))
	}

	groupKey := convs[0].GroupKey
	if groupKey == "" {
		t.Fatal("first conversation has an empty GroupKey")
	}
	if convs[1].GroupKey != groupKey {
		t.Fatalf("GroupKeys differ: %q != %q, want every iteration grouped under the first user message", groupKey, convs[1].GroupKey)
	}

	var rejected, accepted bool
	for _, conv := range convs {
		result, ok := toolResultFor(t, conv, "complete_task")
		if !ok {
			t.Fatalf("conversation %q has no complete_task call, messages: %+v", conv.ID, conv.Messages)
		}
		rejected = rejected || strings.Contains(result, "verification command")
		accepted = accepted || strings.Contains(result, "recorded")
	}
	if !rejected {
		t.Fatal("iteration 1 did not persist the rejected verification")
	}
	if !accepted {
		t.Fatal("iteration 2 did not persist the accepted completion")
	}
}

func Test_e2e_loop_blocked_and_max_iterations(t *testing.T) {
	cases := []struct {
		name     string
		args     string
		env      map[string]string
		wantErr  string
		wantConv int
	}{
		{
			name:     "blocked submission",
			args:     "-r -cm test l run tool_complete_task",
			env:      map[string]string{"CLAI_MOCK_COMPLETE_TASK_STATUS": "blocked", "CLAI_MOCK_COMPLETE_TASK_SUMMARY": "Need credentials."},
			wantErr:  "loop task blocked at iteration 1: Need credentials.",
			wantConv: 1,
		},
		{
			name:     "max iterations exhausted",
			args:     "-r -cm test -mi 2 l finish the work",
			wantErr:  "loop reached the -max-iterations limit (2)",
			wantConv: 2,
		},
		{
			name:     "negative max iterations",
			args:     "-r -cm test -mi -1 l finish the work",
			wantErr:  "-max-iterations",
			wantConv: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			confDir := e2etest.SetupMainTestConfigDir(t)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}

			var status int
			stderr := testboil.CaptureStderr(t, func(t *testing.T) {
				_, status = harness.RunOne(t, confDir, tc.args)
			})
			testboil.FailTestIfDiff(t, status, 1)
			testboil.AssertStringContains(t, stderr, tc.wantErr)

			if convs := loadLoopConversations(t, confDir); len(convs) != tc.wantConv {
				t.Fatalf("saved %d conversations, want %d", len(convs), tc.wantConv)
			}
		})
	}
}

func Test_e2e_loop_help_and_dispatch(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)

	if _, ok := harness.Commands()["loop|l"]; !ok {
		t.Fatal("cli.Commands misses the loop|l key")
	}

	for _, args := range []string{"l -h", "loop -h"} {
		t.Run(args, func(t *testing.T) {
			var status int
			stdout := testboil.CaptureStdout(t, func(t *testing.T) {
				status = harness.Run(strings.Split(args, " "))
			})
			testboil.FailTestIfDiff(t, status, 0)
			for _, want := range []string{"loop <text>", "-cm", "-t", "-mi", "-max-iterations", "-check"} {
				testboil.AssertStringContains(t, stdout, want)
			}
		})
	}

	usage := testboil.CaptureStdout(t, func(t *testing.T) {
		harness.Run(nil)
	})
	testboil.AssertStringContains(t, usage, "loop|l")
}

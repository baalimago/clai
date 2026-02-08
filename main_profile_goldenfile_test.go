package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_profile2_is_applied_to_conversations(t *testing.T) {
	// Regression test:
	// Ensure that when explicitly selecting profile 2 via -p/-profile,
	// the resulting conversation written to disk (globalScope.json) has
	// the selected profile.
	//
	// Note: in query mode, clai only writes a new conversation file when the
	// run produces both user+assistant messages (i.e. > 2 messages in total).
	// The "test" model is an echo querier which (in raw mode) doesn't create a
	// full chat, so this test asserts globalScope.json which is always written.

	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	required := []string{
		"conversations",
		"profiles",
		"mcpServers",
		"conversations/dirs",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	profileName := "2"
	profilePath := filepath.Join(confDir, "profiles", fmt.Sprintf("%s.json", profileName))
	profile := text.Profile{
		Name:            profileName,
		Model:           "test",
		UseTools:        false,
		Tools:           []string{},
		Prompt:          "profile2 system prompt",
		SaveReplyAsConv: true,
		McpServers:      nil,
	}
	b, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("Marshal(profile): %v", err)
	}
	if err := os.WriteFile(profilePath, b, 0o644); err != nil {
		t.Fatalf("WriteFile(profilePath): %v", err)
	}

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("-r -cm test -p 2 q hello", " "))
	})
	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.AssertStringContains(t, stdout, "hello\n")

	prev, err := chat.LoadPrevQuery(confDir)
	if err != nil {
		t.Fatalf("LoadPrevQuery: %v", err)
	}
	if prev.Profile != profileName {
		t.Fatalf("globalScope profile: expected %q, got %q", profileName, prev.Profile)
	}

	// If a conversation file was also created, it must carry the same profile.
	convDir := filepath.Join(confDir, "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || name == "globalScope.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(convDir, name))
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", name, err)
		}
		var c models.Chat
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatalf("Unmarshal(%q): %v", name, err)
		}
		if c.Profile != profileName {
			t.Fatalf("conversation profile (%s): expected %q, got %q", name, profileName, c.Profile)
		}
	}
}

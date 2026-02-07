package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_profile2_is_applied_to_conversations(t *testing.T) {
	// Regression test:
	// Ensure that when explicitly selecting profile 2 via -p/-profile,
	// the resulting conversations written to disk (both prevQuery and
	// the saved conversation) have the selected profile.
	//
	// This test intentionally points the config at an empty directory, and
	// creates the profile file up-front, matching the structure of existing
	// goldenfile tests.

	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	// Create required directory structure (same contract as other goldenfile tests).
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

	// First create a profile "2" so that it exists when running.
	profileName := "2"
	profilePath := filepath.Join(confDir, "profiles", fmt.Sprintf("%s.json", profileName))
	profile := text.Profile{
		Name:            profileName,
		Model:           "test",
		UseTools:        false,
		Tools:           []string{},
		Prompt:          "profile2 system prompt",
		SaveReplyAsConv: true,
		// keep as nil; json omitempty
		McpServers: nil,
	}
	b, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("Marshal(profile): %v", err)
	}
	if err := os.WriteFile(profilePath, b, 0o644); err != nil {
		t.Fatalf("WriteFile(profilePath): %v", err)
	}

	// Run a query with profile 2 and save replies.
	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("-r -cm test -p 2 q hello", " "))
	})
	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.AssertStringContains(t, stdout, "hello\n")

	// Validate prevQuery has the profile and a conversation was written with the same profile.
	convDir := filepath.Join(confDir, "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}

	var gotPrev models.Chat
	var gotConv models.Chat
	var foundPrev bool
	var foundConv bool

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}

		p := filepath.Join(convDir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", p, err)
		}

		var c models.Chat
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatalf("Unmarshal(%q): %v", p, err)
		}

		switch name {
		case "prevQuery.json":
			foundPrev = true
			gotPrev = c
		default:
			// expect exactly one saved conversation besides prevQuery
			if foundConv {
				t.Fatalf("expected exactly 1 conversation file besides prevQuery.json, found multiple (at least %q and %q)", gotConv.ID, c.ID)
			}
			foundConv = true
			gotConv = c
		}
	}

	if !foundPrev {
		t.Fatalf("expected %q to exist", filepath.Join(convDir, "prevQuery.json"))
	}
	if !foundConv {
		t.Fatalf("expected a saved conversation to exist in %q", convDir)
	}

	if gotPrev.Profile != profileName {
		t.Fatalf("prevQuery profile: expected %q, got %q", profileName, gotPrev.Profile)
	}
	if gotConv.Profile != profileName {
		t.Fatalf("conversation profile: expected %q, got %q", profileName, gotConv.Profile)
	}
}

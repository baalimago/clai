package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/remote"
)

// The meeting-transcriber profile ships as a hub-ready example, so it must
// pass the strict validator and name only tools this catalog can provide
// (worklog 2026-10-28-audio-record-tool, profile.1).
func Test_meeting_transcriber_profile_is_valid(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "profiles", "meeting-transcriber.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if _, err := remote.Validate(path, raw); err != nil {
		t.Fatalf("Validate(%q): %v", path, err)
	}

	var profile struct {
		Model    string   `json:"model"`
		Prompt   string   `json:"prompt"`
		UseTools bool     `json:"use_tools"`
		Tools    []string `json:"tools"`
	}
	if err := json.Unmarshal(raw, &profile); err != nil {
		t.Fatalf("Unmarshal(%q): %v", path, err)
	}
	if profile.Model == "" || profile.Prompt == "" || !profile.UseTools {
		t.Fatalf("profile needs a model, a prompt and use_tools: %+v", profile)
	}
	if len(profile.Tools) == 0 {
		t.Fatal("profile names no tools")
	}

	reg := NewRegistry()
	registerLocalTools(reg, func(string) (string, error) { return "/usr/bin/tool", nil })
	for _, name := range profile.Tools {
		if strings.HasPrefix(name, "mcp_") {
			if !strings.HasSuffix(name, "*") || len(name) <= len("mcp_*") {
				t.Errorf("mcp tool %q is not an mcp_<server>* glob", name)
			}
			continue
		}
		if _, ok := reg.Get(name); !ok {
			t.Errorf("profile names tool %q, which the tool catalog does not provide", name)
		}
	}
}

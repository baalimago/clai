package openai

import (
	"encoding/json"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type fakeTool struct {
	spec pub_models.Specification
}

func (f fakeTool) Specification() pub_models.Specification { return f.spec }
func (f fakeTool) Call(pub_models.Input) (string, error)   { return "", nil }

func TestChatGPT_ToolMappingSetsToolName(t *testing.T) {
	t.Parallel()

	g := &ChatGPT{}

	schema := &pub_models.InputSchema{Type: "object", Properties: map[string]pub_models.ParameterObject{}}
	schema.Patch()

	g.RegisterTool(fakeTool{spec: pub_models.Specification{
		Name:        "ls",
		Description: "list files",
		Inputs:      schema,
	}})

	toolsMapped := g.mapResponsesTools()

	if len(toolsMapped) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(toolsMapped))
	}
	if toolsMapped[0].Name != "ls" {
		t.Fatalf("expected tool name %q, got %q", "ls", toolsMapped[0].Name)
	}
}

// TestChatGPT_ResponsesToolWithoutInputsSendsEmptySchema pins that a tool which
// declares no inputs still goes on the wire with an object schema: a typed nil
// pointer inside Parameters escapes omitempty and encodes as
// "parameters":null, which strict validators reject (2026-10-30 audio_record).
func TestChatGPT_ResponsesToolWithoutInputsSendsEmptySchema(t *testing.T) {
	t.Parallel()

	g := &ChatGPT{}
	g.RegisterTool(fakeTool{spec: pub_models.Specification{Name: "pwd", Description: "print cwd"}})

	raw, err := json.Marshal(g.mapResponsesTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	want := `"parameters":{"type":"object","required":[],"properties":{}}`
	if !strings.Contains(string(raw), want) {
		t.Fatalf("tools JSON = %s, want %s", raw, want)
	}
}

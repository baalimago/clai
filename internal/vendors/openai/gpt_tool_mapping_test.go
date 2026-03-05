package openai

import (
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

	toolsMapped := make([]responsesTool, 0, len(g.tools))
	for _, tool := range g.tools {
		spec := tool.Specification()
		toolsMapped = append(toolsMapped, responsesTool{
			Type:        "function",
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  spec.Inputs,
		})
	}

	if len(toolsMapped) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(toolsMapped))
	}
	if toolsMapped[0].Name != "ls" {
		t.Fatalf("expected tool name %q, got %q", "ls", toolsMapped[0].Name)
	}
}

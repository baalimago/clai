package tools

import (
	"fmt"
	"os"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type MkdirTool pub_models.Specification

var Mkdir = MkdirTool{
	Name:        "mkdir",
	Description: "Create directories, including any missing parent directories.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"directory": {
				Type:        "string",
				Description: "The directory path to create.",
			},
		},
		Required: []string{"directory"},
	},
}

func (m MkdirTool) Call(input pub_models.Input) (string, error) {
	directory, ok := input["directory"].(string)
	if !ok {
		return "", fmt.Errorf("directory must be a string")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("create directory %q: %w", directory, err)
	}
	return fmt.Sprintf("Successfully created directory %s", directory), nil
}

func (m MkdirTool) Specification() pub_models.Specification {
	return pub_models.Specification(Mkdir)
}

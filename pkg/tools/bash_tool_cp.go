package tools

import (
	"fmt"
	"os/exec"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type CpTool pub_models.Specification

var Cp = CpTool{
	Name:        "cp",
	Description: "Copy a file or directory recursively. Metadata is preserved by default.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"source": {
				Type:        "string",
				Description: "The file or directory to copy.",
			},
			"destination": {
				Type:        "string",
				Description: "The destination path.",
			},
			"preserve": {
				Type:        "boolean",
				Description: "Preserve metadata and symbolic links. Defaults to true.",
			},
		},
		Required: []string{"source", "destination"},
	},
}

func (c CpTool) Call(input pub_models.Input) (string, error) {
	source, ok := input["source"].(string)
	if !ok || source == "" {
		return "", fmt.Errorf("source must be a non-empty string")
	}
	destination, ok := input["destination"].(string)
	if !ok || destination == "" {
		return "", fmt.Errorf("destination must be a non-empty string")
	}
	preserve := true
	if input["preserve"] != nil {
		preserve, ok = input["preserve"].(bool)
		if !ok {
			return "", fmt.Errorf("preserve must be a boolean")
		}
	}
	flag := "-R"
	if preserve {
		flag = "-a"
	}
	output, err := exec.Command("cp", flag, "--", source, destination).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("copy %q to %q: %w, output: %s", source, destination, err, output)
	}
	return fmt.Sprintf("Successfully copied %s to %s", source, destination), nil
}

func (c CpTool) Specification() pub_models.Specification {
	return pub_models.Specification(Cp)
}

package tools

import (
	"fmt"
	"os/exec"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type FindTool pub_models.Specification

var Find = FindTool{
	Name:        "find",
	Description: "Search for files in a directory hierarchy. Uses linux command 'find'.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"directory": {
				Type:        "string",
				Description: "The directory to start the search from.",
			},
			"name": {
				Type:        "string",
				Description: "The name pattern to search for.",
			},
			"type": {
				Type:        "string",
				Description: "The file type to search for (f: regular file, d: directory).",
			},
			"maxdepth": {
				Type:        "integer",
				Description: "The maximum depth of directories to search.",
			},
		},
		Required: []string{"directory"},
	},
}

func (f FindTool) Call(input pub_models.Input) (string, error) {
	directory, ok := input["directory"].(string)
	if !ok {
		return "", fmt.Errorf("directory must be a string")
	}
	cmd := exec.Command("find", directory)
	if input["name"] != nil {
		name, ok := input["name"].(string)
		if !ok {
			return "", fmt.Errorf("name must be a string")
		}
		cmd.Args = append(cmd.Args, "-name", name)
	}
	if input["type"] != nil {
		fileType, ok := input["type"].(string)
		if !ok {
			return "", fmt.Errorf("type must be a string")
		}
		cmd.Args = append(cmd.Args, "-type", fileType)
	}
	if input["maxdepth"] != nil {
		maxdepth, ok := input["maxdepth"].(float64)
		if !ok {
			return "", fmt.Errorf("maxdepth must be a number")
		}
		cmd.Args = append(cmd.Args, "-maxdepth", fmt.Sprintf("%v", maxdepth))
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run find: %w, output: %v", err, string(output))
	}
	return string(output), nil
}

func (f FindTool) Specification() pub_models.Specification {
	return pub_models.Specification(Find)
}

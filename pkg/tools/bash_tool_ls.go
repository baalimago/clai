package tools

import (
	"fmt"
	"os/exec"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type LsTool pub_models.Specification

var LS = LsTool{
	Name:        "ls",
	Description: "List the files in a directory. Uses the Linux command 'ls'.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"directory": {
				Type:        "string",
				Description: "The directory to list the files of.",
			},
			"all": {
				Type:        "boolean",
				Description: "Show all files, including hidden files.",
			},
			"long": {
				Type:        "boolean",
				Description: "Use a long listing format.",
			},
		},
		Required: []string{"directory"},
	},
}

func (f LsTool) Call(input pub_models.Input) (string, error) {
	directory, ok := input["directory"].(string)
	if !ok {
		return "", fmt.Errorf("directory must be a string")
	}
	cmd := exec.Command("ls", directory)
	if input["all"] != nil {
		all, ok := input["all"].(bool)
		if !ok {
			return "", fmt.Errorf("all must be a boolean")
		}
		if all {
			cmd.Args = append(cmd.Args, "-a")
		}
	}
	if input["long"] != nil {
		long, ok := input["long"].(bool)
		if !ok {
			return "", fmt.Errorf("long must be a boolean")
		}
		if long {
			cmd.Args = append(cmd.Args, "-l")
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run ls: %w, output: %v", err, string(output))
	}
	return string(output), nil
}

func (f LsTool) Specification() pub_models.Specification {
	return pub_models.Specification(LS)
}

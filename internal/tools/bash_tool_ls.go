package tools

import (
	"fmt"
	"os/exec"
)

type LsTool UserFunction

var LS = LsTool{
	Name:        "ls",
	Description: "List the files in a directory. Uses the Linux command 'ls'.",
	Inputs: InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
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

func (f LsTool) Call(input Input) (string, error) {
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

func (f LsTool) UserFunction() UserFunction {
	return UserFunction(LS)
}

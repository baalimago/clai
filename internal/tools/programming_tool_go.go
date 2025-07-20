package tools

import (
	"fmt"
	"os/exec"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type GoTool pub_models.Specification

var Go = GoTool{
	Name:        "go",
	Description: "Run Go commands like 'go test' and 'go run' to compile, test, and run Go programs. Run 'go help' to get details of this tool.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"command": {
				Type:        "string",
				Description: "The Go command to run (e.g., 'run', 'test', 'build').",
			},
			"args": {
				Type:        "string",
				Description: "Additional arguments for the Go command (e.g., file names, flags).",
			},
			"dir": {
				Type:        "string",
				Description: "The directory to run the command in (optional, defaults to current directory).",
			},
		},
		Required: []string{"command"},
	},
}

func (g GoTool) Call(input pub_models.Input) (string, error) {
	command, ok := input["command"].(string)
	if !ok {
		return "", fmt.Errorf("command must be a string")
	}

	args := []string{command}

	if inputArgs, ok := input["args"].(string); ok {
		args = append(args, strings.Fields(inputArgs)...)
	}

	cmd := exec.Command("go", args...)

	if dir, ok := input["dir"].(string); ok {
		cmd.Dir = dir
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run go command: %w, output: %v", err, string(output))
	}

	return string(output), nil
}

func (g GoTool) Specification() pub_models.Specification {
	return pub_models.Specification(Go)
}

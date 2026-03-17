package tools

import (
	"fmt"
	"os/exec"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type FreetextCmdTool pub_models.Specification

var FreetextCmd = FreetextCmdTool{
	Name:        "freetext_command",
	Description: "Run any entered string as a terminal command.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"command": {
				Type:        "string",
				Description: "The freetext comand. May be any string. Will return error on non-zero exit code.",
			},
		},
		Required: []string{"command"},
	},
}

func (r FreetextCmdTool) Call(input pub_models.Input) (string, error) {
	freetextCmd, ok := input["command"].(string)
	if !ok {
		return "", fmt.Errorf("read command input: command must be a string")
	}
	if freetextCmd == "" {
		return "", fmt.Errorf("validate command input: command must not be empty")
	}

	cmd := exec.Command("sh", "-c", freetextCmd)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run freetext command %q: %w, output: %v", freetextCmd, err, string(output))
	}
	return string(output), nil
}

func (r FreetextCmdTool) Specification() pub_models.Specification {
	return pub_models.Specification(FreetextCmd)
}

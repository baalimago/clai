package tools

import (
	"fmt"
	"os/exec"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type PwdTool pub_models.Specification

var Pwd = PwdTool{
	Name:        "pwd",
	Description: "Print the current working directory. Uses the Linux command 'pwd'.",
	Inputs: &pub_models.InputSchema{
		Type:       "object",
		Required:   make([]string, 0),
		Properties: map[string]pub_models.ParameterObject{},
	},
}

func (p PwdTool) Call(input pub_models.Input) (string, error) {
	cmd := exec.Command("pwd")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run pwd: %w, output: %v", err, string(output))
	}
	return string(output), nil
}

func (p PwdTool) Specification() pub_models.Specification {
	return pub_models.Specification(Pwd)
}

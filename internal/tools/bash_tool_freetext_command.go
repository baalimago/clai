package tools

import (
	"fmt"
	"os/exec"
	"strings"
)

type FreetextCmdTool Specification

var FreetextCmd = FreetextCmdTool{
	Name:        "freetext_command",
	Description: "Run any entered string as a terminal command.",
	Inputs: InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"command": {
				Type:        "string",
				Description: "The freetext comand. May be any string. Will return error on non-zero exit code.",
				Enum:        make([]string, 0),
			},
		},
		Required: []string{"command"},
	},
}

func (r FreetextCmdTool) Call(input Input) (string, error) {
	freetextCmd, ok := input["command"].(string)
	if !ok {
		return "", fmt.Errorf("freetextCmd must be a string")
	}
	freetextCmdSplit := strings.Split(freetextCmd, " ")
	var potentialArgsFlags []string
	if len(freetextCmdSplit) > 0 {
		potentialArgsFlags = freetextCmdSplit[1:]
	}
	cmd := exec.Command(freetextCmdSplit[0], potentialArgsFlags...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("error: '%w', output: %v", err, string(output))
	}
	return string(output), nil
}

func (r FreetextCmdTool) Specification() Specification {
	return Specification(FreetextCmd)
}

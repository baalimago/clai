package tools

import (
	"fmt"
	"os/exec"
)

type CatTool UserFunction

var Cat = CatTool{
	Name:        "cat",
	Description: "Display the contents of a file. Uses the linux command 'cat'.",
	Inputs: &InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"file": {
				Type:        "string",
				Description: "The file to display the contents of.",
			},
			"number": {
				Type:        "boolean",
				Description: "Number all output lines.",
			},
			"showEnds": {
				Type:        "boolean",
				Description: "Display $ at end of each line.",
			},
			"squeezeBlank": {
				Type:        "boolean",
				Description: "Suppress repeated empty output lines.",
			},
		},
		Required: []string{"file"},
	},
}

func (c CatTool) Call(input Input) (string, error) {
	file, ok := input["file"].(string)
	if !ok {
		return "", fmt.Errorf("file must be a string")
	}
	cmd := exec.Command("cat", file)
	if input["number"] != nil {
		number, ok := input["number"].(bool)
		if !ok {
			return "", fmt.Errorf("number must be a boolean")
		}
		if number {
			cmd.Args = append(cmd.Args, "-n")
		}
	}
	if input["showEnds"] != nil {
		showEnds, ok := input["showEnds"].(bool)
		if !ok {
			return "", fmt.Errorf("showEnds must be a boolean")
		}
		if showEnds {
			cmd.Args = append(cmd.Args, "-E")
		}
	}
	if input["squeezeBlank"] != nil {
		squeezeBlank, ok := input["squeezeBlank"].(bool)
		if !ok {
			return "", fmt.Errorf("squeezeBlank must be a boolean")
		}
		if squeezeBlank {
			cmd.Args = append(cmd.Args, "-s")
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run cat: %w, output: %v", err, string(output))
	}
	return string(output), nil
}

func (c CatTool) UserFunction() UserFunction {
	return UserFunction(Cat)
}

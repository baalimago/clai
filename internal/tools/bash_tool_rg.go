package tools

import (
	"fmt"
	"os/exec"
)

type RipGrepTool Specification

var RipGrep = RipGrepTool{
	Name:        "rg",
	Description: "Search for a pattern in files using ripgrep.",
	Inputs: &InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"pattern": {
				Type:        "string",
				Description: "The pattern to search for.",
			},
			"path": {
				Type:        "string",
				Description: "The path to search in.",
			},
			"case_sensitive": {
				Type:        "boolean",
				Description: "Whether the search should be case sensitive.",
			},
			"line_number": {
				Type:        "boolean",
				Description: "Whether to show line numbers.",
			},
			"hidden": {
				Type:        "boolean",
				Description: "Whether to search hidden files and directories.",
			},
		},
		Required: []string{"pattern"},
	},
}

func (r RipGrepTool) Call(input Input) (string, error) {
	pattern, ok := input["pattern"].(string)
	if !ok {
		return "", fmt.Errorf("pattern must be a string")
	}
	cmd := exec.Command("rg", pattern)
	if input["path"] != nil {
		path, ok := input["path"].(string)
		if !ok {
			return "", fmt.Errorf("path must be a string")
		}
		cmd.Args = append(cmd.Args, path)
	}
	if input["case_sensitive"] != nil {
		caseSensitive, ok := input["case_sensitive"].(bool)
		if !ok {
			return "", fmt.Errorf("case_sensitive must be a boolean")
		}
		if caseSensitive {
			cmd.Args = append(cmd.Args, "--case-sensitive")
		}
	}
	if input["line_number"] != nil {
		lineNumber, ok := input["line_number"].(bool)
		if !ok {
			return "", fmt.Errorf("line_number must be a boolean")
		}
		if lineNumber {
			cmd.Args = append(cmd.Args, "--line-number")
		}
	}
	if input["hidden"] != nil {
		hidden, ok := input["hidden"].(bool)
		if !ok {
			return "", fmt.Errorf("hidden must be a boolean")
		}
		if hidden {
			cmd.Args = append(cmd.Args, "--hidden")
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		// exit status 1 is not found, and not to be considered an error
		if err.Error() == "exit status 1" {
			err = nil
			output = []byte(fmt.Sprintf("found no hits with pattern: '%s'", pattern))
		} else {
			return "", fmt.Errorf("failed to run rg: %w, output: %v", err, string(output))
		}
	}
	return string(output), nil
}

func (r RipGrepTool) Specification() Specification {
	return Specification(RipGrep)
}

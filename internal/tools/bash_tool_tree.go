package tools

import (
	"fmt"
	"os/exec"
)

type FileTreeTool UserFunction

var FileTree = FileTreeTool{
	Name:        "local_file_tree",
	Description: "List the filetree of some directory. Uses linux command 'tree'.",
	Inputs: InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"directory": {
				Type:        "string",
				Description: "The directory to list the filetree of.",
			},
			"level": {
				Type:        "integer",
				Description: "The depth of the tree to display.",
			},
		},
		Required: []string{"directory"},
	},
}

func (f FileTreeTool) Validate(input Input) error {
	var missingFields []string
	for _, requiredField := range f.Inputs.Required {
		if _, ok := input[requiredField]; !ok {
			missingFields = append(missingFields, requiredField)
		}
	}
	if input["level"] != nil {
		_, ok := input["level"].(float64)
		if !ok {
			return fmt.Errorf("level must be an integer")
		}
	}
	if len(missingFields) > 0 {
		return NewValidationError(missingFields)
	}
	return nil
}

func (f FileTreeTool) Call(input Input) (string, error) {
	directory, ok := input["directory"].(string)
	if !ok {
		return "", fmt.Errorf("directory must be a string")
	}
	cmd := exec.Command("tree", directory)
	if input["level"] != nil {
		level, ok := input["level"].(float64)
		if !ok {
			return "", fmt.Errorf("level must be a number")
		}
		cmd.Args = append(cmd.Args, "-L")
		cmd.Args = append(cmd.Args, fmt.Sprintf("%v", level))
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run tree: %w, output: %v", err, string(output))
	}
	return string(output), nil
}

func (f FileTreeTool) UserFunction() UserFunction {
	return UserFunction(FileTree)
}

package tools

import (
	"fmt"
	"os/exec"
)

type FileTypeTool UserFunction

var FileType = FileTypeTool{
	Name:        "file_type",
	Description: "Determine the file type of a given file. Uses the linux command 'file'.",
	Inputs: &InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"file_path": {
				Type:        "string",
				Description: "The path to the file to analyze.",
			},
			"mime_type": {
				Type:        "boolean",
				Description: "Whether to display the MIME type of the file.",
			},
		},
		Required: []string{"file_path"},
	},
}

func (f FileTypeTool) Call(input Input) (string, error) {
	filePath, ok := input["file_path"].(string)
	if !ok {
		return "", fmt.Errorf("file_path must be a string")
	}
	cmd := exec.Command("file", filePath)
	if input["mime_type"] != nil {
		mimeType, ok := input["mime_type"].(bool)
		if !ok {
			return "", fmt.Errorf("mime_type must be a boolean")
		}
		if mimeType {
			cmd.Args = append(cmd.Args, "--mime-type")
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run file command: %w, output: %v", err, string(output))
	}
	return string(output), nil
}

func (f FileTypeTool) UserFunction() UserFunction {
	return UserFunction(FileType)
}

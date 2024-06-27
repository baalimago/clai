package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

type WriteFileTool UserFunction

var WriteFile = WriteFileTool{
	Name:        "write_file",
	Description: "Write content to a file. Creates the file if it doesn't exist, or overwrites it if it does.",
	Inputs: &InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"file_path": {
				Type:        "string",
				Description: "The path to the file to write to.",
			},
			"content": {
				Type:        "string",
				Description: "The content to write to the file.",
			},
			"append": {
				Type:        "boolean",
				Description: "If true, append to the file instead of overwriting it.",
			},
		},
		Required: []string{"file_path", "content"},
	},
}

func (w WriteFileTool) Call(input Input) (string, error) {
	filePath, ok := input["file_path"].(string)
	if !ok {
		return "", fmt.Errorf("file_path must be a string")
	}

	content, ok := input["content"].(string)
	if !ok {
		return "", fmt.Errorf("content must be a string")
	}

	append := false
	if input["append"] != nil {
		append, ok = input["append"].(bool)
		if !ok {
			return "", fmt.Errorf("append must be a boolean")
		}
	}

	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	var flag int
	if append {
		flag = os.O_APPEND | os.O_CREATE | os.O_WRONLY
	} else {
		flag = os.O_TRUNC | os.O_CREATE | os.O_WRONLY
	}

	file, err := os.OpenFile(filePath, flag, 0o644)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	_, err = file.WriteString(content)
	if err != nil {
		return "", fmt.Errorf("failed to write to file: %w", err)
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), filePath), nil
}

func (w WriteFileTool) UserFunction() UserFunction {
	return UserFunction(WriteFile)
}

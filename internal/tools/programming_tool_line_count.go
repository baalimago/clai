package tools

import (
	"bufio"
	"fmt"
	"os"
)

type LineCountTool Specification

var LineCount = LineCountTool{
	Name:        "line_count",
	Description: "Count the number of lines in a file.",
	Inputs: InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"file_path": {
				Type:        "string",
				Description: "The path to the file to count lines of.",
			},
		},
		Required: []string{"file_path"},
	},
}

func (l LineCountTool) Call(input Input) (string, error) {
	filePath, ok := input["file_path"].(string)
	if !ok {
		return "", fmt.Errorf("file_path must be a string")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return fmt.Sprintf("%d", count), nil
}

func (l LineCountTool) Specification() Specification {
	return Specification(LineCount)
}

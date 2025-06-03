package tools

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type RowsBetweenTool UserFunction

var RowsBetween = RowsBetweenTool{
	Name:        "rows_between",
	Description: "Fetch the lines between two line numbers (inclusive) from a file.",
	Inputs: &InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"file_path": {
				Type:        "string",
				Description: "The path to the file to read.",
			},
			"start_line": {
				Type:        "integer",
				Description: "First line to include (1-based, inclusive).",
			},
			"end_line": {
				Type:        "integer",
				Description: "Last line to include (1-based, inclusive).",
			},
		},
		Required: []string{"file_path", "start_line", "end_line"},
	},
}

func (r RowsBetweenTool) Call(input Input) (string, error) {
	filePath, ok := input["file_path"].(string)
	if !ok {
		return "", fmt.Errorf("file_path must be a string")
	}
	startLine, ok := input["start_line"].(int)
	if !ok {
		// Accept float64 (from JSON decoding)
		if f, ok := input["start_line"].(float64); ok {
			startLine = int(f)
		} else if s, ok := input["start_line"].(string); ok {
			startLine, _ = strconv.Atoi(s)
		} else {
			return "", fmt.Errorf("start_line must be an integer")
		}
	}
	endLine, ok := input["end_line"].(int)
	if !ok {
		if f, ok := input["end_line"].(float64); ok {
			endLine = int(f)
		} else if s, ok := input["end_line"].(string); ok {
			endLine, _ = strconv.Atoi(s)
		} else {
			return "", fmt.Errorf("end_line must be an integer")
		}
	}

	if startLine <= 0 || endLine < startLine {
		return "", fmt.Errorf("invalid line range")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for i := 1; scanner.Scan(); i++ {
		if i >= startLine && i <= endLine {
			lines = append(lines, scanner.Text())
		}
		if i > endLine {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to scan file: %w", err)
	}

	return strings.Join(lines, "\n"), nil
}

func (r RowsBetweenTool) UserFunction() UserFunction {
	return UserFunction(RowsBetween)
}

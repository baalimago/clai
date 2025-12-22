package tools

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type RowsBetweenTool pub_models.Specification

var RowsBetween = RowsBetweenTool{
	Name:        "rows_between",
	Description: "Fetch the lines between two line numbers (inclusive) from a file.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
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

func (r RowsBetweenTool) Call(input pub_models.Input) (string, error) {
	filePath, ok := input["file_path"].(string)
	if !ok {
		return "", fmt.Errorf("file_path must be a string")
	}
	startLine, ok := input["start_line"].(int)
	if !ok {
		// Accept float64 (from JSON decoding)
		if f, isFloat := input["start_line"].(float64); isFloat {
			startLine = int(f)
		} else if s, isString := input["start_line"].(string); isString {
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
			lineWithNumber := fmt.Sprintf("%d: %s", i, scanner.Text())
			lines = append(lines, lineWithNumber)
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

func (r RowsBetweenTool) Specification() pub_models.Specification {
	return pub_models.Specification(RowsBetween)
}

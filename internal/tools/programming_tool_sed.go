package tools

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type SedTool UserFunction

var Sed = SedTool{
	Name:        "sed",
	Description: "Perform a basic regex substitution on each line or within a specific line range of a file (like 'sed s/pattern/repl/g'). Overwrites the file.",
	Inputs: &InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"file_path": {
				Type:        "string",
				Description: "The path to the file to modify.",
			},
			"pattern": {
				Type:        "string",
				Description: "The regex pattern to search for.",
			},
			"repl": {
				Type:        "string",
				Description: "The replacement string.",
			},
			"start_line": {
				Type:        "integer",
				Description: "Optional. First line to modify (1-based, inclusive).",
			},
			"end_line": {
				Type:        "integer",
				Description: "Optional. Last line to modify (1-based, inclusive).",
			},
		},
		Required: []string{"file_path", "pattern", "repl"},
	},
}

func (s SedTool) Call(input Input) (string, error) {
	filePath, ok := input["file_path"].(string)
	if !ok {
		return "", fmt.Errorf("file_path must be a string")
	}
	pattern, ok := input["pattern"].(string)
	if !ok {
		return "", fmt.Errorf("pattern must be a string")
	}
	repl, ok := input["repl"].(string)
	if !ok {
		return "", fmt.Errorf("repl must be a string")
	}

	var startLine, endLine int
	if v, ok := input["start_line"]; ok {
		switch n := v.(type) {
		case float64:
			startLine = int(n)
		case int:
			startLine = n
		case string:
			startLine, _ = strconv.Atoi(n)
		}
	}
	if v, ok := input["end_line"]; ok {
		switch n := v.(type) {
		case float64:
			endLine = int(n)
		case int:
			endLine = n
		case string:
			endLine, _ = strconv.Atoi(n)
		}
	}

	raw, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	lines := strings.Split(string(raw), "\n")
	for i := range lines {
		lineNum := i + 1
		if (startLine == 0 && endLine == 0) ||
			(startLine > 0 && endLine > 0 && lineNum >= startLine && lineNum <= endLine) ||
			(startLine > 0 && endLine == 0 && lineNum >= startLine) ||
			(startLine == 0 && endLine > 0 && lineNum <= endLine) {
			lines[i] = re.ReplaceAllString(lines[i], repl)
		}
	}

	out := strings.Join(lines, "\n")
	err = os.WriteFile(filePath, []byte(out), 0o644)
	if err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	return fmt.Sprintf("sed: replaced occurrences of %q with %q in %s (%d-%d)", pattern, repl, filePath, startLine, endLine), nil
}

func (s SedTool) UserFunction() UserFunction {
	return UserFunction(Sed)
}

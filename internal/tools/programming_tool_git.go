package tools

import (
	"fmt"
	"os/exec"
	"strconv"
)

type GitTool Specification

var Git = GitTool{
	Name:        "git",
	Description: "Run read-only git commands like log, diff, show, blame and status.",
	Inputs: &InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"operation": {
				Type:        "string",
				Description: "The git operation to run.",
				Enum:        []string{"log", "diff", "show", "status", "blame"},
			},
			"file": {
				Type:        "string",
				Description: "Optional file path used by diff, show or blame.",
			},
			"commit": {
				Type:        "string",
				Description: "Optional commit hash used by show or diff.",
			},
			"range": {
				Type:        "string",
				Description: "Optional revision range for log or diff.",
			},
			"n": {
				Type:        "integer",
				Description: "Number of log entries to display.",
			},
			"dir": {
				Type:        "string",
				Description: "Directory containing the git repository (optional).",
			},
		},
		Required: []string{"operation"},
	},
}

func (g GitTool) Call(input Input) (string, error) {
	op, ok := input["operation"].(string)
	if !ok {
		return "", fmt.Errorf("operation must be a string")
	}

	args := []string{op}

	switch op {
	case "log":
		if v, ok := input["n"]; ok {
			num := 0
			switch n := v.(type) {
			case int:
				num = n
			case float64:
				num = int(n)
			case string:
				if i, err := strconv.Atoi(n); err == nil {
					num = i
				}
			}
			if num > 0 {
				args = append(args, "-n", fmt.Sprintf("%d", num))
			}
		}
		if r, ok := input["range"].(string); ok && r != "" {
			args = append(args, r)
		}
	case "diff":
		if r, ok := input["range"].(string); ok && r != "" {
			args = append(args, r)
		}
		if f, ok := input["file"].(string); ok && f != "" {
			args = append(args, "--", f)
		}
	case "show":
		if c, ok := input["commit"].(string); ok && c != "" {
			args = append(args, c)
		}
		if f, ok := input["file"].(string); ok && f != "" {
			args = append(args, "--", f)
		}
	case "status":
		args = []string{"status", "--short"}
	case "blame":
		if f, ok := input["file"].(string); ok && f != "" {
			args = append(args, f)
		} else {
			return "", fmt.Errorf("file is required for blame")
		}
	default:
		return "", fmt.Errorf("unsupported git operation: %s", op)
	}

	cmd := exec.Command("git", args...)
	if d, ok := input["dir"].(string); ok && d != "" {
		cmd.Dir = d
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run git %s: %w, output: %s", op, err, string(output))
	}
	return string(output), nil
}

func (g GitTool) Specification() Specification {
	return Specification(Git)
}

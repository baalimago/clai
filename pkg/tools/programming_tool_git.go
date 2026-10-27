package tools

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"unicode"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type GitTool pub_models.Specification

var Git = GitTool{
	Name:        "git",
	Description: "Run read-only git commands like log, diff, show, blame and status.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"operation": {
				Type:        "string",
				Description: "The git operation to run.",
				Enum:        &[]string{"log", "diff", "show", "status", "blame"},
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
				Description: "Optional revision range for log or diff. Holds one or more space-separated arguments, such as '--stat main...HEAD' or '--format=%D'; quote an argument that contains spaces.",
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

func (g GitTool) Call(input pub_models.Input) (string, error) {
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
		rangeArgs, err := gitRangeArgs(input)
		if err != nil {
			return "", err
		}
		args = append(args, rangeArgs...)
	case "diff":
		rangeArgs, err := gitRangeArgs(input)
		if err != nil {
			return "", err
		}
		args = append(args, rangeArgs...)
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

func gitRangeArgs(input pub_models.Input) ([]string, error) {
	r, ok := input["range"].(string)
	if !ok || strings.TrimSpace(r) == "" {
		return nil, nil
	}
	args, err := splitQuotedArgs(r)
	if err != nil {
		return nil, fmt.Errorf("range %q: %w", r, err)
	}
	for _, a := range args {
		if a == "--output" || strings.HasPrefix(a, "--output=") {
			return nil, fmt.Errorf("range %q: --output writes a file, and the git tool is read-only", r)
		}
	}
	return args, nil
}

func splitQuotedArgs(raw string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var quote rune
	inArg := false
	for _, c := range raw {
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(c)
		case c == '"' || c == '\'':
			quote, inArg = c, true
		case unicode.IsSpace(c):
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(c)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

func (g GitTool) Specification() pub_models.Specification {
	return pub_models.Specification(Git)
}

package tools

import (
	"fmt"
	"os/exec"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type DateTool pub_models.Specification

var Date = DateTool{
	Name:        "date",
	Description: "Get or format the current date and time. Wraps the linux 'date' command and is optimized for agentic workloads.",
	Inputs: &pub_models.InputSchema{
		Type:     "object",
		Required: make([]string, 0),
		Properties: map[string]pub_models.ParameterObject{
			"format": {
				Type:        "string",
				Description: "Optional format string passed to 'date +FORMAT'. Common example: '%Y-%m-%d %H:%M:%S'. If omitted, uses system default format.",
			},
			"utc": {
				Type:        "boolean",
				Description: "If true, returns time in UTC (equivalent to 'TZ=UTC date').",
			},
			"rfc3339": {
				Type:        "boolean",
				Description: "If true, returns time in RFC3339 format (e.g. 2006-01-02T15:04:05Z07:00). Overrides 'format' if both are set.",
			},
			"unix": {
				Type:        "boolean",
				Description: "If true, returns the current Unix timestamp in seconds. Overrides 'format' if set.",
			},
			"args": {
				Type:        "string",
				Description: "Raw argument string forwarded to the underlying 'date' command. Use only if other flags are not sufficient.",
			},
		},
	},
}

func (d DateTool) Call(input pub_models.Input) (string, error) {
	var args []string

	// Highest priority: unix or rfc3339 helper flags (agent-friendly)
	if v, ok := input["unix"].(bool); ok && v {
		args = append(args, "+%s")
	} else if v, ok := input["rfc3339"].(bool); ok && v {
		args = append(args, "+%Y-%m-%dT%H:%M:%S%z")
	} else if format, ok := input["format"].(string); ok && format != "" {
		args = append(args, "+"+format)
	}

	// Raw args (lowest level escape hatch)
	if raw, ok := input["args"].(string); ok && raw != "" {
		// Let the user fully control arguments; do not mix with above
		args = []string{raw}
	}

	cmd := exec.Command("date", args...)

	// Support UTC via env var; avoids shell wrapping
	if v, ok := input["utc"].(bool); ok && v {
		cmd.Env = append(cmd.Env, "TZ=UTC")
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run date: %w, output: %v", err, string(output))
	}
	return string(output), nil
}

func (d DateTool) Specification() pub_models.Specification {
	return pub_models.Specification(Date)
}

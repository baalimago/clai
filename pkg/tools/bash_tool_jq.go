package tools

import (
	"fmt"
	"os/exec"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type JQTool pub_models.Specification

var JQ = JQTool{
	Name:        "jq",
	Description: "Process a JSON file with the real jq executable. The filter uses normal jq syntax and semantics.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"file": {
				Type:        "string",
				Description: "JSON file to process.",
			},
			"filter": {
				Type:        "string",
				Description: "jq filter expression, such as '.[] | select(.speaker == \"A\")'.",
			},
			"compact": {
				Type:        "boolean",
				Description: "Use compact output (-c).",
			},
			"raw_output": {
				Type:        "boolean",
				Description: "Output strings without JSON quoting (-r).",
			},
			"slurp": {
				Type:        "boolean",
				Description: "Read all JSON inputs into an array before applying the filter (-s).",
			},
			"sort_keys": {
				Type:        "boolean",
				Description: "Sort object keys in output (-S).",
			},
			"exit_status": {
				Type:        "boolean",
				Description: "Set jq's exit status from the last output value (-e).",
			},
		},
		Required: []string{"file", "filter"},
	},
}

func (j JQTool) Call(input pub_models.Input) (string, error) {
	file, ok := input["file"].(string)
	if !ok {
		return "", fmt.Errorf("file must be a string")
	}
	filter, ok := input["filter"].(string)
	if !ok {
		return "", fmt.Errorf("filter must be a string")
	}

	args := make([]string, 0, 8)
	for _, flag := range []struct {
		input string
		arg   string
	}{
		{"compact", "-c"},
		{"raw_output", "-r"},
		{"slurp", "-s"},
		{"sort_keys", "-S"},
		{"exit_status", "-e"},
	} {
		value, exists := input[flag.input]
		if !exists || value == nil {
			continue
		}
		enabled, ok := value.(bool)
		if !ok {
			return "", fmt.Errorf("%s must be a boolean", flag.input)
		}
		if enabled {
			args = append(args, flag.arg)
		}
	}
	if strings.HasPrefix(filter, "-") {
		filter = " " + filter // leading space stops jq parsing the filter as an option
	}
	args = append(args, filter, "--", file)

	output, err := exec.Command("jq", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run jq: %w, output: %s", err, output)
	}
	return string(output), nil
}

func (j JQTool) Specification() pub_models.Specification {
	return pub_models.Specification(j)
}

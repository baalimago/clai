package tools

import (
	"fmt"
	"math"
	"os/exec"
	"strconv"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type HeadTool pub_models.Specification

var Head = HeadTool{
	Name:        "head",
	Description: "Display the first part of a file. Uses the system command 'head'.",
	Inputs:      countedFileInputs(),
}

func (h HeadTool) Call(input pub_models.Input) (string, error) {
	return runCountedFileTool("head", input)
}

func (h HeadTool) Specification() pub_models.Specification {
	return pub_models.Specification(Head)
}

func countedFileInputs() *pub_models.InputSchema {
	return &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"file": {
				Type:        "string",
				Description: "File to read.",
			},
			"lines": {
				Type:        "integer",
				Description: "Number of lines to show (-n). Defaults to 10.",
			},
			"bytes": {
				Type:        "integer",
				Description: "Number of bytes to show (-c). Mutually exclusive with lines.",
			},
		},
		Required: []string{"file"},
	}
}

func runCountedFileTool(executable string, input pub_models.Input) (string, error) {
	file, ok := input["file"].(string)
	if !ok {
		return "", fmt.Errorf("file must be a string")
	}
	lineValue, hasLines := input["lines"]
	byteValue, hasBytes := input["bytes"]
	hasLines = hasLines && lineValue != nil
	hasBytes = hasBytes && byteValue != nil
	if hasLines && hasBytes {
		return "", fmt.Errorf("lines and bytes are mutually exclusive")
	}

	args := make([]string, 0, 4)
	if hasLines {
		count, err := integerInput("lines", lineValue)
		if err != nil {
			return "", err
		}
		args = append(args, "-n", strconv.FormatInt(count, 10))
	}
	if hasBytes {
		count, err := integerInput("bytes", byteValue)
		if err != nil {
			return "", err
		}
		args = append(args, "-c", strconv.FormatInt(count, 10))
	}
	args = append(args, "--", file)

	output, err := exec.Command(executable, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run %s: %w, output: %s", executable, err, output)
	}
	return string(output), nil
}

func integerInput(name string, value any) (int64, error) {
	number, ok := value.(float64)
	if !ok || math.Trunc(number) != number || math.Abs(number) > math.MaxInt64 {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return int64(number), nil
}

package tools

import (
	"fmt"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ClaiResult - Get result
var ClaiResult = &claiResultTool{}

type claiResultTool struct{}

func (t *claiResultTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "clai_result",
		Description: "Get the stdout, stderr and statuscode of the run-id",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"run_id"},
			Properties: map[string]pub_models.ParameterObject{
				"run_id": {
					Type:        "string",
					Description: "The run-id returned by clai_run",
				},
			},
		},
	}
}

func (t *claiResultTool) Call(input pub_models.Input) (string, error) {
	runIDRaw, ok := input["run_id"]
	if !ok {
		return "", fmt.Errorf("missing run_id")
	}
	runID, ok := runIDRaw.(string)
	if !ok {
		return "", fmt.Errorf("run_id must be a string")
	}

	claiRunsMu.Lock()
	process, ok := claiRuns[runID]
	claiRunsMu.Unlock()

	if !ok {
		return "", fmt.Errorf("unknown run_id: %s", runID)
	}

	return fmt.Sprintf("Exit Code: %d\nStdout:\n%s\nStderr:\n%s", process.exitCode, process.stdout.String(), process.stderr.String()), nil
}

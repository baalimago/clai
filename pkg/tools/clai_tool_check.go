package tools

import (
	"fmt"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ClaiCheck - Check status
var ClaiCheck = &claiCheckTool{}

type claiCheckTool struct{}

func (t *claiCheckTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "clai_check",
		Description: "Check status of the run-id: RUNNING, COMPLETED, FAILED",
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

func (t *claiCheckTool) Call(input pub_models.Input) (string, error) {
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

	if !process.done {
		return "RUNNING", nil
	}

	if process.exitCode != 0 || process.err != nil {
		return "FAILED", nil
	}

	return "COMPLETED", nil
}

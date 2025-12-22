package tools

import (
	"fmt"
	"os/exec"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ClaiHelp - Run `clai help`
var ClaiHelp = &claiHelpTool{}

type claiHelpTool struct{}

const desc = `Run 'clai help' to output instructions on how to use the tool.

Guidelines when using clai tools:
	* Always run 'clai help' to understand how to use the tool
	* Always run 'clai profiles' to know which profiles to use
	* Always run 'clai tools' to find which tools you can utilize for the clai_run subprocess workers`

func (t *claiHelpTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "clai_help",
		Description: desc,
		Inputs: &pub_models.InputSchema{
			Type:       "object",
			Properties: map[string]pub_models.ParameterObject{},
			Required:   make([]string, 0),
		},
	}
}

func (t *claiHelpTool) Call(input pub_models.Input) (string, error) {
	cmd := exec.Command(ClaiBinaryPath, "help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to run clai help: %w", err)
	}
	return string(out), nil
}

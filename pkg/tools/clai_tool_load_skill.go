package tools

import (
	"errors"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var LoadSkill = &loadSkillTool{}

type loadSkillTool struct{}

func (t *loadSkillTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "load_skill",
		Description: "Load a trusted skill by name when a task matches one of the advertised skill descriptors. If the skill descriptor lists arguments, include them in `arguments` as the raw payload to render into the skill.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"skill"},
			Properties: map[string]pub_models.ParameterObject{
				"skill":     {Type: "string", Description: "The invocation name of the skill to load."},
				"arguments": {Type: "string", Description: "Raw argument string for the skill. Supply this when the descriptor lists arguments."},
			},
		},
	}
}

func (t *loadSkillTool) Call(pub_models.Input) (string, error) {
	return "", errors.New("load_skill is handled internally by clai")
}

package skills

import (
	"fmt"
	"maps"
	"slices"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func applyToolPolicy(base map[string]pub_models.LLMTool, state ActivationState, knownToolNames map[string]struct{}) (map[string]pub_models.LLMTool, []string) {
	active := map[string]pub_models.LLMTool{}
	maps.Copy(active, base)
	warnings := []string{}
	for name := range state.Allowed {
		if _, ok := active[name]; ok {
			continue
		}
		if _, ok := knownToolNames[name]; ok {
			warnings = append(warnings, fmt.Sprintf("skill requested unavailable tool %q", name))
			continue
		}
		warnings = append(warnings, fmt.Sprintf("skill requested unknown tool %q", name))
	}
	for name := range state.Disallowed {
		delete(active, name)
	}
	slices.Sort(warnings)
	return active, warnings
}

func mergeActivationState(state *ActivationState, skill Skill, req ActivationRequest) {
	state.Records = append(state.Records, ActivationRecord{SkillName: skill.Name, RawArgs: req.RawArgs, Args: append([]string{}, req.Args...)})
	state.LoadedSkills = append(state.LoadedSkills, skill)
	for _, name := range skill.Parsed.Metadata.AllowedTools {
		state.Allowed[name] = struct{}{}
	}
	for _, name := range skill.Parsed.Metadata.DisallowedTools {
		state.Disallowed[name] = struct{}{}
	}
}

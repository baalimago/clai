package skills

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func applyToolPolicy(base, localTools map[string]pub_models.LLMTool, state ActivationState, knownToolNames map[string]struct{}) (map[string]pub_models.LLMTool, []string, []string) {
	active := map[string]pub_models.LLMTool{}
	maps.Copy(active, base)
	warnings := []string{}
	enabled := []string{}
	for name := range state.Allowed {
		if _, ok := active[name]; ok {
			continue
		}
		if tool, ok := localTools[name]; ok {
			active[name] = tool
			enabled = append(enabled, name)
			continue
		}
		if strings.HasPrefix(name, "mcp_") {
			warnings = append(warnings, fmt.Sprintf("skill requested unavailable MCP tool %q", name))
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
	enabled = slices.DeleteFunc(enabled, func(name string) bool {
		_, exists := active[name]
		return !exists
	})
	slices.Sort(warnings)
	slices.Sort(enabled)
	return active, warnings, enabled
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

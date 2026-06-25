package skills

import (
	"context"
	"fmt"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func (m *Manager) LoadSkill(ctx context.Context, name, rawArgs string, baseTools map[string]pub_models.LLMTool) (LoadedSkill, error) {
	skill, ok := m.Skills[name]
	if !ok {
		return LoadedSkill{ActivationErr: fmt.Sprintf("unknown skill %q", name)}, nil
	}
	req := ActivationRequest{Name: name, RawArgs: rawArgs, Args: parseArgs(rawArgs)}
	if m.Config.MaxActivatedSkills > 0 && len(m.state.Records) >= m.Config.MaxActivatedSkills {
		return LoadedSkill{ActivationErr: fmt.Sprintf("skill activation cap exceeded: maxActivatedSkills=%d", m.Config.MaxActivatedSkills)}, nil
	}
	if err := m.ensureTrusted(ctx, skill); err != nil {
		return LoadedSkill{}, err
	}
	rendered, err := renderSkill(skill, req)
	if err != nil {
		return LoadedSkill{}, err
	}
	mergeActivationState(&m.state, skill, req)
	activeTools, warnings := applyToolPolicy(baseTools, m.state, m.knownToolNames)
	return LoadedSkill{
		Skill:        skill,
		RenderedBody: rendered,
		Warnings:     warnings,
		ActiveTools:  activeTools,
		RawArgs:      rawArgs,
	}, nil
}

func parseArgs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var args []string
	var cur strings.Builder
	var quote rune
	escaped := false
	for _, r := range raw {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

func toSet(values []string) map[string]struct{} {
	ret := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			ret[value] = struct{}{}
		}
	}
	return ret
}

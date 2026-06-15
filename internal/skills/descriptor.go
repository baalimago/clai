package skills

import (
	"fmt"
	"slices"
	"strings"
)

const descriptorHead = `The following skills provide specialized instructions for specific tasks.
Call %s when the task matches a skill description.
If a skill lists arguments, pass them in the %s field of %s as the raw user-specific payload for that skill.
If skills are enabled for the run, clai must always include the internal %s tool in the model-visible tool list, even when user-selected external tools are filtered to a narrower subset.
When a loaded skill references a relative path, resolve it against the skill directory and use that absolute path in tool commands.

<available_skills>
%s
</available_skills>`

const descriptorSkillTemplate = `<skill>
  <name>%s</name>
  <description>%s</description>%s
  <location>%s</location>
</skill>`

func (m *Manager) DescriptorBlock() string {
	if len(m.Skills) == 0 {
		return ""
	}
	var skillsXML strings.Builder
	for _, skill := range sortedSkills(m.Skills) {
		if skill.Parsed.Metadata.DisableModelInvocation {
			continue
		}
		argsLine := ""
		if len(skill.Parsed.Metadata.Arguments) > 0 {
			argsLine = fmt.Sprintf("\n  <arguments>%s</arguments>", xmlEscape(strings.Join(skill.Parsed.Metadata.Arguments, ", ")))
		}
		fmt.Fprintf(
			&skillsXML, descriptorSkillTemplate,
			xmlEscape(skill.Name),
			xmlEscape(skill.Parsed.Metadata.Description),
			argsLine,
			xmlEscape(skill.Path),
		)
		skillsXML.WriteByte('\n')
	}
	return fmt.Sprintf(
		descriptorHead,
		"`load_skill`",
		"`arguments`",
		"`load_skill`",
		"`load_skill`",
		strings.TrimRight(skillsXML.String(), "\n"),
	)
}

func sortedSkills(skills map[string]Skill) []Skill {
	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}
	slices.Sort(names)
	ret := make([]Skill, 0, len(names))
	for _, name := range names {
		ret = append(ret, skills[name])
	}
	return ret
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;").Replace(s)
}

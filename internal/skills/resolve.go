package skills

import "cmp"

type Resolution struct {
	Active   map[string]Skill
	Shadowed []ShadowedSkill
	Invalid  []InvalidSkill
}

type precedenceMap map[string]int

func resolveCandidates(candidates []Candidate, invalids []InvalidSkill, precedence precedenceMap) Resolution {
	active := make(map[string]Skill, len(candidates))
	shadowed := make([]ShadowedSkill, 0)
	for _, candidate := range candidates {
		if existing, ok := active[candidate.Skill.Name]; ok {
			if compareSkillPrecedence(candidate.Skill, existing, precedence) < 0 {
				shadowed = append(shadowed, ShadowedSkill{Winner: candidate.Skill, Loser: existing})
				active[candidate.Skill.Name] = candidate.Skill
				continue
			}
			shadowed = append(shadowed, ShadowedSkill{Winner: existing, Loser: candidate.Skill})
			continue
		}
		active[candidate.Skill.Name] = candidate.Skill
	}
	return Resolution{Active: active, Shadowed: shadowed, Invalid: invalids}
}

func compareSkillPrecedence(a, b Skill, precedence precedenceMap) int {
	if v := cmp.Compare(sourceRank(a.SourceClass), sourceRank(b.SourceClass)); v != 0 {
		return v
	}
	if a.SourceClass == "project" {
		aDepth := pathDepth(a.SourceRoot)
		bDepth := pathDepth(b.SourceRoot)
		if v := cmp.Compare(bDepth, aDepth); v != 0 {
			return v
		}
	}
	if a.SourceClass == "global" {
		if v := cmp.Compare(precedence[a.SourceRoot], precedence[b.SourceRoot]); v != 0 {
			return v
		}
	}
	return 0
}

func sourceRank(class string) int {
	switch class {
	case "project":
		return 0
	case "global":
		return 1
	default:
		return 2
	}
}

func pathDepth(path string) int {
	depth := 0
	for _, r := range path {
		if r == '/' {
			depth++
		}
	}
	return depth
}

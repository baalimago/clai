package skills

import (
	"strings"
	"testing"
)

func TestRenderSkillTokenAwareAndQuotedArgs(t *testing.T) {
	skill := Skill{
		Dir: "/tmp/skill",
		Parsed: ParsedSkill{
			NormalizedBody: "ALL:$ARGUMENTS\nIDX:$ARGUMENTS[1]\nPOS:$0\nNAME:$target\nDIR:${CLAUDE_SKILL_DIR}",
			Metadata:       Metadata{Arguments: []string{"target", "extra"}},
		},
	}
	got, err := renderSkill(skill, ActivationRequest{Name: "review", RawArgs: "\"hello world\" tail", Args: parseArgs("\"hello world\" tail")})
	if err != nil {
		t.Fatalf("renderSkill() error = %v", err)
	}
	for _, want := range []string{"ALL:\"hello world\" tail", "IDX:tail", "POS:hello world", "NAME:hello world", "DIR:/tmp/skill"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered body missing %q in %q", want, got)
		}
	}
}

func TestRenderSkillMissingArgumentSoftFails(t *testing.T) {
	skill := Skill{
		Parsed: ParsedSkill{
			NormalizedBody: "Need $2 and $focus and $ARGUMENTS[1]",
			Metadata:       Metadata{Arguments: []string{"focus"}},
		},
	}
	got, err := renderSkill(skill, ActivationRequest{})
	if err != nil {
		t.Fatalf("renderSkill() error = %v", err)
	}
	if got != "Need  and  and " {
		t.Fatalf("renderSkill() = %q", got)
	}
}

package skills

import (
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestApplyToolPolicyMergesActivationState(t *testing.T) {
	base := map[string]pub_models.LLMTool{"rg": staticTool{name: "rg"}, "cat": staticTool{name: "cat"}}
	state := ActivationState{
		Allowed:    map[string]struct{}{"rg": {}, "ls": {}, "nope": {}},
		Disallowed: map[string]struct{}{"cat": {}},
	}
	localTools := map[string]pub_models.LLMTool{"ls": staticTool{name: "ls"}}
	active, warnings, enabled := applyToolPolicy(base, localTools, state, map[string]struct{}{"ls": {}, "rg": {}, "cat": {}})
	if _, ok := active["cat"]; ok {
		t.Fatalf("expected cat removed")
	}
	if _, ok := active["ls"]; !ok {
		t.Fatalf("expected known local tool to be enabled")
	}
	if len(enabled) != 1 || enabled[0] != "ls" {
		t.Fatalf("unexpected enabled tools: %#v", enabled)
	}
	if len(warnings) != 1 || !strings.Contains(strings.Join(warnings, "\n"), "unknown tool") {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
}

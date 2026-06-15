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
	active, warnings := applyToolPolicy(base, state, map[string]struct{}{"ls": {}, "rg": {}, "cat": {}})
	if _, ok := active["cat"]; ok {
		t.Fatalf("expected cat removed")
	}
	if len(warnings) != 2 || !strings.Contains(strings.Join(warnings, "\n"), "unavailable tool") || !strings.Contains(strings.Join(warnings, "\n"), "unknown tool") {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
}

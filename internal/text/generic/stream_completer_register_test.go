package generic

import (
	"encoding/json"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type schemalessTestTool struct{}

func (schemalessTestTool) Call(pub_models.Input) (string, error) { return "", nil }

func (schemalessTestTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: "schemaless", Description: "no inputs"}
}

// TestRegisterToolWithoutInputs pins that a tool without an input schema
// registers as an empty object schema instead of panicking on the nil
// dereference (2026-10-30).
func TestRegisterToolWithoutInputs(t *testing.T) {
	var s StreamCompleter
	s.InternalRegisterTool(schemalessTestTool{})
	if len(s.tools) != 1 {
		t.Fatalf("registered %d tools, want 1", len(s.tools))
	}
	raw, err := json.Marshal(s.tools[0])
	if err != nil {
		t.Fatalf("marshal registered tool: %v", err)
	}
	want := `"parameters":{"type":"object","required":[],"properties":{}}`
	if !strings.Contains(string(raw), want) {
		t.Fatalf("tool JSON = %s, want %s", raw, want)
	}
}

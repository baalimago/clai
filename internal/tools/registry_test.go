package tools

import (
	"slices"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type mockLLMTool struct {
	name string
	spec pub_models.Specification
}

func (m *mockLLMTool) Call(input pub_models.Input) (string, error) {
	return "mock output", nil
}

func (m *mockLLMTool) Specification() pub_models.Specification {
	return m.spec
}

func newMockTool(name string) *mockLLMTool {
	return &mockLLMTool{
		name: name,
		spec: pub_models.Specification{Name: name},
	}
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry() returned nil")
	}
	if r.tools == nil {
		t.Error("registry.tools is nil")
	}
	if len(r.tools) != 0 {
		t.Errorf("expected empty registry, got %d tools", len(r.tools))
	}
}

func TestRegistry_Set(t *testing.T) {
	r := NewRegistry()
	tool := newMockTool("test-tool")

	r.Set("test", tool)

	if len(r.tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(r.tools))
	}

	stored, ok := r.tools["test"]
	if !ok {
		t.Error("tool not found in registry")
	}

	if stored != tool {
		t.Error("stored tool doesn't match original")
	}
}

func TestRegistry_Get(t *testing.T) {
	r := NewRegistry()
	tool := newMockTool("test-tool")
	r.Set("test", tool)

	// Test exact match
	got, ok := r.Get("test")
	if !ok {
		t.Error("Get() returned false for existing tool")
	}
	if got != tool {
		t.Error("Get() returned wrong tool")
	}

	// Test non-existent tool
	_, ok = r.Get("nonexistent")
	if ok {
		t.Error("Get() returned true for non-existent tool")
	}
}

func TestRegistry_All(t *testing.T) {
	r := NewRegistry()
	tool1 := newMockTool("tool1")
	tool2 := newMockTool("tool2")

	r.Set("test1", tool1)
	r.Set("test2", tool2)

	all := r.All()

	if len(all) != 2 {
		t.Errorf("expected 2 tools, got %d", len(all))
	}

	if all["test1"] != tool1 {
		t.Error("All() returned wrong tool for test1")
	}

	if all["test2"] != tool2 {
		t.Error("All() returned wrong tool for test2")
	}

	// Test that returned map is a copy
	all["test3"] = newMockTool("tool3")
	if len(r.tools) != 2 {
		t.Error("modifying returned map affected original registry")
	}
}

// Add to registry_test.go
func TestRegistry_WildcardGet(t *testing.T) {
	r := NewRegistry()

	// Setup test tools
	tools := map[string]*mockLLMTool{
		"bash_cat":           newMockTool("bash_cat"),
		"bash_find":          newMockTool("bash_find"),
		"prog_git":           newMockTool("prog_git"),
		"prog_go":            newMockTool("prog_go"),
		"web_fetch":          newMockTool("web_fetch"),
		"mcp_everyhing_test": newMockTool("mcp_everyhing_test"),
	}

	for name, tool := range tools {
		r.Set(name, tool)
	}

	testCases := []struct {
		pattern  string
		expected []string
	}{
		{"*", []string{"bash_cat", "bash_find", "prog_git", "prog_go", "web_fetch", "mcp_everyhing_test"}},
		{"bash_*", []string{"bash_cat", "bash_find"}},
		{"*_git", []string{"prog_git"}},
		{"*prog*", []string{"prog_git", "prog_go"}},
		{"bash_cat", []string{"bash_cat"}},
		{"nonexistent", []string{}},
		{"*nonexistent*", []string{}},
		{"mcp_everyhing*", []string{"mcp_everyhing_test"}},
	}

	for _, tc := range testCases {
		t.Run(tc.pattern, func(t *testing.T) {
			matches := r.WildcardGet(tc.pattern)

			if len(matches) != len(tc.expected) {
				t.Errorf("expected %d matches, got %d", len(tc.expected), len(matches))
				return
			}

			matchNames := make([]string, len(matches))
			for i, match := range matches {
				matchNames[i] = match.Specification().Name
			}

			for _, expected := range tc.expected {
				found := slices.Contains(matchNames, expected)
				if !found {
					t.Errorf("expected tool %s not found in matches", expected)
				}
			}
		})
	}
}

func TestWildcardMatch(t *testing.T) {
	testCases := []struct {
		pattern  string
		name     string
		expected bool
	}{
		{"*", "anything", true},
		{"bash_*", "bash_cat", true},
		{"bash_*", "prog_git", false},
		{"*_git", "prog_git", true},
		{"*_git", "bash_cat", false},
		{"*prog*", "prog_git", true},
		{"*prog*", "my_prog_tool", true},
		{"*prog*", "bash_cat", false},
		{"exact", "exact", true},
		{"exact", "not_exact", false},
	}

	for _, tc := range testCases {
		t.Run(tc.pattern+"_"+tc.name, func(t *testing.T) {
			result := WildcardMatch(tc.pattern, tc.name)
			if result != tc.expected {
				t.Errorf("wildcardMatch(%q, %q) = %v, want %v",
					tc.pattern, tc.name, result, tc.expected)
			}
		})
	}
}

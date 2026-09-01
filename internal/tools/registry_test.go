package tools

import (
	"os/exec"
	"slices"
	"sync"
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

func TestInitRegistersApplyPatch(t *testing.T) {
	origRegistry := Registry
	Registry = NewRegistry()
	t.Cleanup(func() {
		Registry = origRegistry
	})

	Init()
	if _, ok := Registry.Get("apply_patch"); !ok {
		t.Fatalf("expected apply_patch to be registered")
	}
	for _, name := range []string{"cmd", "freetext_command", "async_cmd", "async_cmd_run", "async_cmd_status", "async_cmd_logs", "async_cmd_await", "async_cmd_cancel", "mktemp"} {
		if _, ok := Registry.Get(name); !ok {
			t.Fatalf("expected %s to be registered", name)
		}
	}
}

func TestRegisterLocalToolsOmitsUnavailableExecutables(t *testing.T) {
	r := NewRegistry()
	registerLocalTools(r, func(name string) (string, error) {
		if name == "rsync" || name == "rg" || name == "sh" {
			return "", exec.ErrNotFound
		}
		return "/bin/" + name, nil
	})

	for _, name := range []string{"rsync", "rg", "cmd", "freetext_command"} {
		if _, ok := r.Get(name); ok {
			t.Fatalf("expected unavailable tool %q to be omitted", name)
		}
	}
	for _, name := range []string{"cp", "cat", "apply_patch", "async_cmd"} {
		if _, ok := r.Get(name); !ok {
			t.Fatalf("expected available tool %q to be registered", name)
		}
	}
}

func TestInitConcurrent(t *testing.T) {
	WithTestRegistry(t, func() {
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				Init()
				// Every caller must observe a fully populated registry once
				// Init has returned, not only the one which won the race
				if _, ok := Registry.Get("apply_patch"); !ok {
					t.Error("expected apply_patch to be registered after Init returned")
				}
			})
		}
		wg.Wait()
	})
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

func TestRegistry_SetAlias(t *testing.T) {
	r := NewRegistry()
	canonical := newMockTool("canon")
	alias := newMockTool("alias-name")

	r.Set("canon", canonical)
	r.SetAlias("alias-name", "canon", alias)

	if got, ok := r.Get("alias-name"); !ok || got != alias {
		t.Fatalf("expected alias-name to resolve to the registered tool")
	}
	aliases := r.Aliases()
	if len(aliases) != 1 || aliases["alias-name"] != "canon" {
		t.Fatalf("expected alias map {alias-name: canon}, got %v", aliases)
	}
	// The alias is a real registry key, so All() includes it (selection,
	// wildcard matching and Get keep working exactly as before).
	if _, ok := r.All()["alias-name"]; !ok {
		t.Fatal("expected alias-name in All()")
	}

	r.Reset()
	if len(r.Aliases()) != 0 {
		t.Fatal("expected Reset to clear aliases")
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
		"cmd":                newMockTool("cmd"),
		"async_cmd":          newMockTool("async_cmd"),
		"freetext_command":   newMockTool("freetext_command"),
		"mcp_everyhing_test": newMockTool("mcp_everyhing_test"),
	}

	for name, tool := range tools {
		r.Set(name, tool)
	}

	testCases := []struct {
		pattern  string
		expected []string
	}{
		{"*", []string{"bash_cat", "bash_find", "prog_git", "prog_go", "web_fetch", "cmd", "async_cmd", "freetext_command", "mcp_everyhing_test"}},
		{"bash_*", []string{"bash_cat", "bash_find"}},
		{"*_git", []string{"prog_git"}},
		{"*prog*", []string{"prog_git", "prog_go"}},
		{"bash_cat", []string{"bash_cat"}},
		{"nonexistent", []string{}},
		{"*nonexistent*", []string{}},
		{"mcp_everyhing*", []string{"mcp_everyhing_test"}},
		{"*cmd*", []string{"cmd", "async_cmd"}},
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

package tools

import (
	"os"
	"strings"
	"sync"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// registry is a threadsafe storage for LLMTools.
type registry struct {
	mu          sync.RWMutex
	tools       map[string]LLMTool
	debug       bool
	hasBeenInit bool
}

// NewRegistry returns an empty tools registry.
func NewRegistry() *registry {
	return &registry{tools: make(map[string]LLMTool), debug: misc.Truthy(os.Getenv("DEBUG"))}
}

// Get returns the tool registered under name.
func (r *registry) Get(name string) (LLMTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Add to registry.go
func (r *registry) WildcardGet(pattern string) []LLMTool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matches []LLMTool
	for name, tool := range r.tools {
		if wildcardMatch(pattern, name) {
			matches = append(matches, tool)
		}
	}
	return matches
}

func wildcardMatch(pattern, name string) bool {
	if pattern == "*" {
		return true
	}

	// Simple wildcard matching - supports * at start, end, or middle
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		// *substring*
		substr := pattern[1 : len(pattern)-1]
		return strings.Contains(name, substr)
	} else if strings.HasPrefix(pattern, "*") {
		// *suffix
		suffix := pattern[1:]
		return strings.HasSuffix(name, suffix)
	} else if strings.HasSuffix(pattern, "*") {
		// prefix*
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(name, prefix)
	}

	// No wildcards - exact match
	return pattern == name
}

// Set registers tool under the provided name.
func (r *registry) Set(name string, t LLMTool) {
	r.mu.Lock()
	if r.debug {
		ancli.Okf("adding tool too registry, name: %v\n", t.Specification().Name)
	}
	r.tools[name] = t
	r.mu.Unlock()
}

// All returns a copy of all registered tools keyed by name.
func (r *registry) All() map[string]LLMTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make(map[string]LLMTool, len(r.tools))
	for k, v := range r.tools {
		cp[k] = v
	}
	return cp
}

// Reset removes all registered tools. Primarily used for tests.
func (r *registry) Reset() {
	r.mu.Lock()
	r.tools = make(map[string]LLMTool)
	r.mu.Unlock()
}

package tools

import "sync"

// Registry is a threadsafe storage for LLMTools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]LLMTool
}

// NewRegistry returns an empty tools registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]LLMTool)}
}

// Get returns the tool registered under name.
func (r *Registry) Get(name string) (LLMTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Set registers tool under the provided name.
func (r *Registry) Set(name string, t LLMTool) {
	r.mu.Lock()
	r.tools[name] = t
	r.mu.Unlock()
}

// All returns a copy of all registered tools keyed by name.
func (r *Registry) All() map[string]LLMTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make(map[string]LLMTool, len(r.tools))
	for k, v := range r.tools {
		cp[k] = v
	}
	return cp
}

// Reset removes all registered tools. Primarily used for tests.
func (r *Registry) Reset() {
	r.mu.Lock()
	r.tools = make(map[string]LLMTool)
	r.mu.Unlock()
}

package tools

import (
	"maps"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// registry is a threadsafe storage for LLMTools.
type registry struct {
	mu sync.RWMutex
	// initOnce guards the one-time population of the registry, see Init. A
	// concurrent caller arriving mid-initialisation blocks until it has
	// completed, and so never observes a half-filled registry.
	initOnce sync.Once
	tools    map[string]models.LLMTool
	// aliases maps alias tool names to their canonical tool name. The alias
	// name is also a real key in tools (same tool instance), so Get and
	// WildcardGet keep working; the alias map only exists so the clai tools
	// listing can group aliases under their canonical entry instead of
	// repeating the description.
	aliases map[string]string
	debug   bool
}

// NewRegistry returns an empty tools registry.
func NewRegistry() *registry {
	return &registry{
		tools:   make(map[string]models.LLMTool),
		aliases: make(map[string]string),
		debug:   misc.Truthy(os.Getenv("DEBUG")),
	}
}

// Get returns the tool registered under name.
func (r *registry) Get(name string) (models.LLMTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Add to registry.go
func (r *registry) WildcardGet(pattern string) []models.LLMTool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matches []models.LLMTool
	for name, tool := range r.tools {
		if WildcardMatch(pattern, name) {
			matches = append(matches, tool)
		}
	}
	return matches
}

func WildcardMatch(pattern, name string) bool {
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
func (r *registry) Set(name string, t models.LLMTool) {
	r.mu.Lock()
	if strings.Contains(name, "printEnv") || strings.Contains(name, "get-env") {
		ancli.Warnf("found env printing tool, skipping for security's sake. Tool name: '%v'", name)
	}
	if r.debug || debugflags.Enabled("TOOLS_REGISTRY_SET") {
		ancli.Okf("adding tool too registry, name: %v\n", t.Specification().Name)
	}
	r.tools[name] = t
	r.mu.Unlock()
}

// SetAlias registers t under alias (so Get/WildcardGet keep resolving it) and
// records canonical as the name the alias points at. canonical must already be
// registered (it usually is: SetAlias is called right after Set(canonical, t)).
func (r *registry) SetAlias(alias, canonical string, t models.LLMTool) {
	r.mu.Lock()
	if _, ok := r.tools[canonical]; !ok {
		ancli.Warnf("SetAlias: canonical tool '%s' for alias '%s' is not registered", canonical, alias)
	}
	r.tools[alias] = t
	r.aliases[alias] = canonical
	r.mu.Unlock()
}

// Aliases returns a copy of the alias → canonical name map.
func (r *registry) Aliases() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return maps.Clone(r.aliases)
}

// All returns a copy of all registered tools keyed by name.
func (r *registry) All() map[string]models.LLMTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make(map[string]models.LLMTool, len(r.tools))
	maps.Copy(cp, r.tools)
	return cp
}

// Names returns every registered tool name, aliases included, sorted. It
// initializes the registry first, so completion hooks and setup tables get
// the same list from one place.
func Names() []string {
	Init()
	all := Registry.All()
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Reset removes all registered tools. Primarily used for tests.
func (r *registry) Reset() {
	r.mu.Lock()
	r.tools = make(map[string]models.LLMTool)
	r.aliases = make(map[string]string)
	r.mu.Unlock()
}

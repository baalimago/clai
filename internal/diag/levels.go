package diag

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// LevelLayer maps a default or dotted subsystem selector to a level.
// The empty selector names the default. Layers are ordered from low to high
// precedence. Registry names and selector names are case-insensitive.
type LevelLayer map[string]diagnostic.Level

// LevelRequest is the owner's snapshot of the registry and the configuration.
// Legacy holds the value of each legacy control named in the README's Legacy
// controls table, keyed by its variable name; the owner reads the environment,
// the resolver never does.
type LevelRequest struct {
	Registry []string
	Legacy   map[string]string
	Layers   []LevelLayer
	LogLevel string
}

type ResolvedLevels map[string]diagnostic.Level

type LevelResolver interface {
	// Resolve returns a level for every registered name and, under the empty
	// selector, the default. The default is the Diagnostic level parameter
	// (README Parameters). LOG_LEVEL accepts one
	// default and dotted name=level overrides, with the level names and the
	// alias F7 defines. The resolver is internal to clai. (Resolve.1, F7, F10)
	//
	// Legacy entries form the lowest-precedence layer, mapped as the README's
	// Legacy controls table states and in its row order. Layers follow, then
	// LOG_LEVEL, each replacing the same selector from an earlier layer. The
	// longest complete dotted selector wins after the layers merge.
	// (Resolve.2, F7, F9)
	//
	// Malformed entries, duplicate selectors in LOG_LEVEL, invalid levels,
	// unknown selectors, and a Legacy key the table does not name return an
	// error that identifies the entry. (Resolve.3, F7)
	Resolve(LevelRequest) (ResolvedLevels, error)
}

type levelResolver struct{}

// NewLevelResolver returns the internal diagnostic level resolver.
func NewLevelResolver() LevelResolver {
	return levelResolver{}
}

func (levelResolver) Resolve(request LevelRequest) (ResolvedLevels, error) {
	registry, err := normalizeRegistry(request.Registry)
	if err != nil {
		return nil, err
	}

	selectors := map[string]diagnostic.Level{"": diagnostic.LevelInfo}
	if err := applyLegacy(selectors, request.Legacy, registry); err != nil {
		return nil, err
	}
	for index, layer := range request.Layers {
		if err := applyLayer(selectors, layer, registry, index); err != nil {
			return nil, err
		}
	}
	if err := applyLogLevel(selectors, request.LogLevel, registry); err != nil {
		return nil, err
	}

	resolved := make(ResolvedLevels, len(request.Registry)+1)
	resolved[""] = selectors[""]
	for index, name := range request.Registry {
		resolved[name] = selectedLevel(registry[index], selectors)
	}
	return resolved, nil
}

func normalizeRegistry(names []string) ([]string, error) {
	normalized := make([]string, len(names))
	seen := make(map[string]struct{}, len(names))
	for index, name := range names {
		normalizedName := strings.ToLower(name)
		if !validSelector(normalizedName) {
			return nil, fmt.Errorf("invalid registry entry %q", name)
		}
		if _, exists := seen[normalizedName]; exists {
			return nil, fmt.Errorf("duplicate registry entry %q", name)
		}
		seen[normalizedName] = struct{}{}
		normalized[index] = normalizedName
	}
	return normalized, nil
}

func applyLegacy(selectors map[string]diagnostic.Level, legacy map[string]string, registry []string) error {
	keys := make([]string, 0, len(legacy))
	for key := range legacy {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !knownLegacyKey(key) {
			return fmt.Errorf("unknown legacy control %q", key)
		}
	}

	if misc.Truthy(legacy["NO_WARNINGS"]) {
		selectors[""] = diagnostic.LevelError
	}
	if misc.Truthy(legacy["DEBUG"]) {
		selectors[""] = diagnostic.LevelDebug
	}

	for _, subsystem := range legacySubsystems() {
		key := "DEBUG_" + strings.ToUpper(subsystem)
		if misc.Truthy(legacy[key]) {
			if err := setSelector(selectors, subsystem, diagnostic.LevelDebug, registry, key); err != nil {
				return err
			}
		}
	}
	for _, vendor := range legacyVendors() {
		key := "DEBUG_" + strings.ToUpper(vendor)
		if vendor == "anthropic" {
			key = "ANTHROPIC_DEBUG"
		}
		if misc.Truthy(legacy[key]) {
			if err := setSelector(selectors, "vendor."+vendor, diagnostic.LevelDebug, registry, key); err != nil {
				return err
			}
		}
	}
	if value, exists := legacy["LOG_LEVEL_SKILLS"]; exists {
		level, ok := parseLevel(value)
		if !ok {
			return fmt.Errorf("invalid legacy control LOG_LEVEL_SKILLS=%q", value)
		}
		if err := setSelector(selectors, "skills", level, registry, "LOG_LEVEL_SKILLS"); err != nil {
			return err
		}
	}
	return nil
}

func knownLegacyKey(key string) bool {
	return slices.Contains(legacyKeys(), key)
}

// LegacyControls reads every Legacy control that getenv reports set, for
// LevelRequest.Legacy.
func LegacyControls(getenv func(string) string) map[string]string {
	legacy := map[string]string{}
	for _, key := range legacyKeys() {
		if value := getenv(key); value != "" {
			legacy[key] = value
		}
	}
	return legacy
}

func legacyKeys() []string {
	keys := []string{"NO_WARNINGS", "DEBUG", "LOG_LEVEL_SKILLS", "DEBUG_CPU", "DEBUG_OUTPUT_FILE", "NO_COLOR", "SILENT", "ANCLI_NEWLINE"}
	for _, subsystem := range legacySubsystems() {
		keys = append(keys, "DEBUG_"+strings.ToUpper(subsystem))
	}
	for _, vendor := range legacyVendors() {
		key := "DEBUG_" + strings.ToUpper(vendor)
		if vendor == "anthropic" {
			key = "ANTHROPIC_DEBUG"
		}
		keys = append(keys, key)
	}
	return keys
}

func legacySubsystems() []string {
	return []string{
		"call", "chat", "claudified_msgs", "cost_manager", "dirscope", "lookback",
		"openrouter_model_catalog", "profiles", "reply_mode", "skills", "stoploss",
		"summary", "text_querier", "tools_registry_set", "verbose",
	}
}

func legacyVendors() []string {
	return []string{"openai", "openrouter", "mistral", "huggingface", "anthropic"}
}

func applyLayer(selectors map[string]diagnostic.Level, layer LevelLayer, registry []string, index int) error {
	keys := make([]string, 0, len(layer))
	for key := range layer {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := make(map[string]struct{}, len(layer))
	for _, key := range keys {
		normalized := strings.ToLower(key)
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("duplicate selector %q in layer %d", key, index)
		}
		seen[normalized] = struct{}{}
		level := layer[key]
		if !validLevel(level) {
			return fmt.Errorf("invalid level for selector %q in layer %d", key, index)
		}
		if err := setSelector(selectors, normalized, level, registry, key); err != nil {
			return err
		}
	}
	return nil
}

func applyLogLevel(selectors map[string]diagnostic.Level, configuration string, registry []string) error {
	if configuration == "" {
		return nil
	}
	entries := strings.Split(configuration, ",")
	defaultEntry := strings.TrimSpace(entries[0])
	if strings.Contains(defaultEntry, "=") {
		return fmt.Errorf("malformed LOG_LEVEL entry %q: default level must come first", entries[0])
	}
	level, ok := parseLevel(defaultEntry)
	if !ok {
		return fmt.Errorf("invalid LOG_LEVEL entry %q", entries[0])
	}
	selectors[""] = level

	seen := make(map[string]struct{}, len(entries)-1)
	for _, untrimmed := range entries[1:] {
		entry := strings.TrimSpace(untrimmed)
		selector, levelName, found := strings.Cut(entry, "=")
		selector = strings.ToLower(strings.TrimSpace(selector))
		levelName = strings.TrimSpace(levelName)
		if !found || selector == "" || strings.Contains(levelName, "=") {
			return fmt.Errorf("malformed LOG_LEVEL entry %q", untrimmed)
		}
		if _, exists := seen[selector]; exists {
			return fmt.Errorf("duplicate LOG_LEVEL entry %q", untrimmed)
		}
		seen[selector] = struct{}{}
		level, ok := parseLevel(levelName)
		if !ok {
			return fmt.Errorf("invalid LOG_LEVEL entry %q", untrimmed)
		}
		if err := setSelector(selectors, selector, level, registry, untrimmed); err != nil {
			return err
		}
	}
	return nil
}

func setSelector(selectors map[string]diagnostic.Level, selector string, level diagnostic.Level, registry []string, entry string) error {
	if selector != "" && (!validSelector(selector) || !knownSelector(selector, registry)) {
		return fmt.Errorf("unknown selector in entry %q", entry)
	}
	selectors[selector] = level
	return nil
}

func knownSelector(selector string, registry []string) bool {
	for _, name := range registry {
		if name == selector || strings.HasPrefix(name, selector+".") {
			return true
		}
	}
	return false
}

func validSelector(selector string) bool {
	if selector == "" || strings.TrimSpace(selector) != selector {
		return false
	}
	for segment := range strings.SplitSeq(selector, ".") {
		if segment == "" {
			return false
		}
	}
	return true
}

func parseLevel(name string) (diagnostic.Level, bool) {
	switch strings.ToLower(name) {
	case "error":
		return diagnostic.LevelError, true
	case "warn", "warning":
		return diagnostic.LevelWarn, true
	case "info":
		return diagnostic.LevelInfo, true
	case "debug":
		return diagnostic.LevelDebug, true
	case "trace":
		return diagnostic.LevelTrace, true
	default:
		return 0, false
	}
}

func validLevel(level diagnostic.Level) bool {
	return level >= diagnostic.LevelError && level <= diagnostic.LevelTrace
}

func selectedLevel(name string, selectors map[string]diagnostic.Level) diagnostic.Level {
	level := selectors[""]
	longest := 0
	for selector, candidate := range selectors {
		if selector != "" && len(selector) > longest && (name == selector || strings.HasPrefix(name, selector+".")) {
			level = candidate
			longest = len(selector)
		}
	}
	return level
}

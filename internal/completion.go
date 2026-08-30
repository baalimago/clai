package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

type completionData struct {
	Profiles      []string
	ShellContexts []string
	Models        []string
}

// CompletionSources lazily loads the config-dir-derived completion values,
// memoized per instance (one per command constructor in production).
// Loading happens only inside a hook call, keeping Flagset()/Subcommands()
// pure; a load failure yields no suggestions for that source, never an
// error — a broken completion must not disturb the shell. ToolNames is
// injected by the command package (clicmd must not import the tool
// registry); nil means no -t/-tools value completion.
type CompletionSources struct {
	ToolNames func() []string

	once sync.Once
	data completionData
}

func (s *CompletionSources) get() completionData {
	s.once.Do(func() {
		confDir, err := utils.GetClaiConfigDir()
		if err != nil {
			return
		}
		data, err := loadCompletionData(confDir)
		if err != nil {
			return
		}
		s.data = data
	})
	return s.data
}

// TextFlagValues completes flag values for the text commands (query, chat).
func (s *CompletionSources) TextFlagValues(flagName, partial string) []cmd.CompletionItem {
	switch flagName {
	case "cm", "chat-model":
		return PlainItems(partial, s.get().Models)
	case "p", "profile":
		return PlainItems(partial, s.get().Profiles)
	case "asc", "add-shell-context":
		return PlainItems(partial, s.get().ShellContexts)
	case "t", "tools":
		if s.ToolNames == nil {
			return nil
		}
		return toolValueItems(partial, s.ToolNames())
	case "prp", "profile-path":
		return []cmd.CompletionItem{{Value: "__files__", Kind: cmd.CompletionKindFile}}
	}
	return nil
}

// MediaFlagValues completes flag values for photo and video.
func MediaFlagValues(flagName, _ string) []cmd.CompletionItem {
	switch flagName {
	case "pd", "photo-dir", "vd", "video-dir":
		return []cmd.CompletionItem{{Value: "__dirs__", Kind: cmd.CompletionKindDir}}
	}
	return nil
}

// SuppressArgs stops positional completion once prompt text begins.
func SuppressArgs([]string, string) []cmd.CompletionItem {
	return []cmd.CompletionItem{}
}

// toolValueItems completes -t/-tools values with comma-split multi-value
// support: completion continues after the last comma, keeping the prefix.
func toolValueItems(partial string, names []string) []cmd.CompletionItem {
	lastComma := strings.LastIndex(partial, ",")
	if lastComma == -1 {
		return PlainItems(partial, names)
	}
	prefix := partial[:lastComma+1]
	items := PlainItems(partial[lastComma+1:], names)
	for i := range items {
		items[i].Value = prefix + items[i].Value
	}
	return items
}

// PlainItems filters options on the prefix and wraps the sorted matches as
// plain completion items.
func PlainItems(prefix string, options []string) []cmd.CompletionItem {
	matches := filterValues(prefix, options)
	items := make([]cmd.CompletionItem, 0, len(matches))
	for _, match := range matches {
		items = append(items, cmd.CompletionItem{Value: match, Kind: cmd.CompletionKindPlain})
	}
	return items
}

func filterValues(prefix string, options []string) []string {
	matches := make([]string, 0, len(options))
	for _, option := range options {
		if prefix == "" || strings.HasPrefix(option, prefix) {
			matches = append(matches, option)
		}
	}
	sort.Strings(matches)
	return matches
}

func loadCompletionData(configDir string) (completionData, error) {
	data := completionData{}

	profilesDir := filepath.Join(configDir, "profiles")
	profiles, err := readJSONBaseNames(profilesDir)
	if err != nil {
		return completionData{}, fmt.Errorf("read profiles for completion: %w", err)
	}
	data.Profiles = profiles

	shellContextsDir := filepath.Join(configDir, "shellContexts")
	shellContexts, err := readJSONBaseNames(shellContextsDir)
	if err != nil {
		return completionData{}, fmt.Errorf("read shell contexts for completion: %w", err)
	}
	data.ShellContexts = shellContexts

	models, err := discoverModelHistory(configDir)
	if err != nil {
		return completionData{}, fmt.Errorf("discover model history for completion: %w", err)
	}
	data.Models = models

	return data, nil
}

func readJSONBaseNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read directory %q: %w", dir, err)
	}

	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		out = append(out, strings.TrimSuffix(name, filepath.Ext(name)))
	}
	sort.Strings(out)
	return out, nil
}

func discoverModelHistory(configDir string) ([]string, error) {
	entries, err := os.ReadDir(configDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No config dir yet: there is no model history to offer.
			return nil, nil
		}
		return nil, fmt.Errorf("read config dir %q: %w", configDir, err)
	}

	uniq := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		model, ok := modelFromConfigFilename(strings.TrimSuffix(name, ".json"))
		if !ok || strings.TrimSpace(model) == "" {
			continue
		}
		uniq[model] = struct{}{}
	}

	out := make([]string, 0, len(uniq))
	for model := range uniq {
		out = append(out, model)
	}
	sort.Strings(out)
	return out, nil
}

func modelFromConfigFilename(base string) (string, bool) {
	switch {
	case strings.HasPrefix(base, "openai_gpt_"):
		return strings.TrimPrefix(base, "openai_gpt_"), true
	case strings.HasPrefix(base, "anthropic_claude_"):
		return strings.TrimPrefix(base, "anthropic_claude_"), true
	case strings.HasPrefix(base, "google_gemini_"):
		return strings.TrimPrefix(base, "google_gemini_"), true
	case strings.HasPrefix(base, "deepseek_deepseek_"):
		return strings.TrimPrefix(base, "deepseek_deepseek_"), true
	case strings.HasPrefix(base, "inception_mercury_"):
		return strings.TrimPrefix(base, "inception_mercury_"), true
	case strings.HasPrefix(base, "xai_grok_"):
		return strings.TrimPrefix(base, "xai_grok_"), true
	case strings.HasPrefix(base, "mistral_mistral_"):
		return strings.TrimPrefix(base, "mistral_mistral_"), true
	case strings.HasPrefix(base, "openrouter_chat_"):
		return "or:" + strings.ReplaceAll(strings.TrimPrefix(base, "openrouter_chat_"), "_", "/"), true
	case strings.HasPrefix(base, "ollama_"):
		parts := strings.SplitN(base, "_", 3)
		if len(parts) != 3 {
			return "", false
		}
		return "ollama:" + parts[2], true
	case strings.HasPrefix(base, "novita_"):
		parts := strings.SplitN(base, "_", 3)
		if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
			return "", false
		}
		return "novita:" + parts[1] + "/" + parts[2], true
	case strings.HasPrefix(base, "huggingface_"):
		parts := strings.SplitN(base, "_", 3)
		if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
			return "", false
		}
		return "hf:" + strings.ReplaceAll(parts[2], "_", "/") + ":" + parts[1], true
	default:
		return "", false
	}
}

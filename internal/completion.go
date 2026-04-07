package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
)

type completionResultKind string

const (
	completionResultKindPlain completionResultKind = "plain"
	completionResultKindFile  completionResultKind = "file"
	completionResultKindDir   completionResultKind = "dir"
)

type completionItem struct {
	Value string
	Kind  completionResultKind
}

type completionRequest struct {
	Args []string
}

type completionResponse struct {
	ReplaceToken string
	Items        []completionItem
}

type completionData struct {
	Profiles      []string
	ShellContexts []string
	Models        []string
}

type completionEngine struct {
	data completionData
}

type completionFlagSpec struct {
	Name        string
	TakesValue  bool
	ValueKind   completionResultKind
	ValueSource string
	CommaSplit  bool
}

var completionCommands = []string{
	"c",
	"chat",
	"completion",
	"confdir",
	"g",
	"glob",
	"h",
	"help",
	"p",
	"photo",
	"profiles",
	"q",
	"query",
	"re",
	"replay",
	"s",
	"setup",
	"t",
	"tools",
	"version",
	"v",
	"video",
}

var completionPromptCommands = map[string]struct{}{
	"query": {},
	"q":     {},
	"photo": {},
	"p":     {},
	"video": {},
	"v":     {},
	"glob":  {},
	"g":     {},
}

var completionChatSubcommands = []string{"continue", "delete", "help", "list"}

var completionGlobalFlags = []completionFlagSpec{
	{Name: "-I", TakesValue: true},
	{Name: "-add-shell-context", TakesValue: true, ValueSource: "shell-context"},
	{Name: "-asc", TakesValue: true, ValueSource: "shell-context"},
	{Name: "-chat-model", TakesValue: true, ValueSource: "model"},
	{Name: "-cm", TakesValue: true, ValueSource: "model"},
	{Name: "-dir-reply"},
	{Name: "-dre"},
	{Name: "-g", TakesValue: true},
	{Name: "-glob", TakesValue: true},
	{Name: "-i"},
	{Name: "-p", TakesValue: true, ValueSource: "profile"},
	{Name: "-pd", TakesValue: true, ValueKind: completionResultKindDir},
	{Name: "-photo-dir", TakesValue: true, ValueKind: completionResultKindDir},
	{Name: "-photo-model", TakesValue: true},
	{Name: "-photo-prefix", TakesValue: true},
	{Name: "-pm", TakesValue: true},
	{Name: "-pp", TakesValue: true},
	{Name: "-prp", TakesValue: true, ValueKind: completionResultKindFile},
	{Name: "-profile", TakesValue: true, ValueSource: "profile"},
	{Name: "-profile-path", TakesValue: true, ValueKind: completionResultKindFile},
	{Name: "-r"},
	{Name: "-raw"},
	{Name: "-re"},
	{Name: "-replace", TakesValue: true},
	{Name: "-reply"},
	{Name: "-t", TakesValue: true, ValueSource: "tool", CommaSplit: true},
	{Name: "-tools", TakesValue: true, ValueSource: "tool", CommaSplit: true},
	{Name: "-vd", TakesValue: true, ValueKind: completionResultKindDir},
	{Name: "-video-dir", TakesValue: true, ValueKind: completionResultKindDir},
	{Name: "-video-model", TakesValue: true},
	{Name: "-video-prefix", TakesValue: true},
	{Name: "-vm", TakesValue: true},
	{Name: "-vp", TakesValue: true},
}

func newCompletionEngine(data completionData) completionEngine {
	return completionEngine{data: data}
}

func (e completionEngine) Complete(req completionRequest) completionResponse {
	if len(req.Args) == 0 {
		return completionResponse{}
	}

	args := req.Args
	if args[0] == "clai" {
		args = args[1:]
	}
	if len(args) == 0 {
		return completionResponse{Items: appendCommandAndFlags("", nil)}
	}

	current := args[len(args)-1]
	prev := ""
	if len(args) > 1 {
		prev = args[len(args)-2]
	}

	if flagSpec, ok := completionFlagByName(prev); ok && flagSpec.TakesValue {
		return e.completeFlagValue(flagSpec, current)
	}

	for _, arg := range args[:len(args)-1] {
		if _, ok := completionPromptCommands[arg]; ok {
			return completionResponse{ReplaceToken: current}
		}
	}

	if len(args) >= 2 && args[0] == "chat" {
		return completionResponse{
			ReplaceToken: current,
			Items:        filterPlain(current, completionChatSubcommands),
		}
	}
	if len(args) >= 2 && args[0] == "tools" {
		return completionResponse{
			ReplaceToken: current,
			Items:        filterPlain(current, e.toolNames()),
		}
	}

	if len(args) == 1 {
		if strings.HasPrefix(current, "-") {
			return completionResponse{
				ReplaceToken: current,
				Items:        filterFlags(current),
			}
		}
		return completionResponse{
			ReplaceToken: current,
			Items:        appendCommandAndFlags(current, nil),
		}
	}

	if strings.HasPrefix(current, "-") {
		return completionResponse{
			ReplaceToken: current,
			Items:        filterFlags(current),
		}
	}

	return completionResponse{ReplaceToken: current}
}

func (e completionEngine) completeFlagValue(flagSpec completionFlagSpec, current string) completionResponse {
	switch {
	case flagSpec.ValueKind == completionResultKindFile:
		return completionResponse{
			ReplaceToken: current,
			Items: []completionItem{{
				Value: "__files__",
				Kind:  completionResultKindFile,
			}},
		}
	case flagSpec.ValueKind == completionResultKindDir:
		return completionResponse{
			ReplaceToken: current,
			Items: []completionItem{{
				Value: "__dirs__",
				Kind:  completionResultKindDir,
			}},
		}
	case flagSpec.ValueSource == "tool":
		return completionResponse{
			ReplaceToken: current,
			Items:        e.completeToolValue(current, flagSpec.CommaSplit),
		}
	case flagSpec.ValueSource == "profile":
		return completionResponse{
			ReplaceToken: current,
			Items:        filterPlain(current, e.data.Profiles),
		}
	case flagSpec.ValueSource == "model":
		return completionResponse{
			ReplaceToken: current,
			Items:        filterPlain(current, e.data.Models),
		}
	case flagSpec.ValueSource == "shell-context":
		return completionResponse{
			ReplaceToken: current,
			Items:        filterPlain(current, e.data.ShellContexts),
		}
	default:
		return completionResponse{ReplaceToken: current}
	}
}

func (e completionEngine) completeToolValue(current string, commaSplit bool) []completionItem {
	toolNames := e.toolNames()
	if !commaSplit || !strings.Contains(current, ",") {
		return filterPlain(current, toolNames)
	}

	lastComma := strings.LastIndex(current, ",")
	prefix := current[:lastComma+1]
	partial := current[lastComma+1:]
	matches := filterValues(partial, toolNames)
	items := make([]completionItem, 0, len(matches))
	for _, match := range matches {
		items = append(items, completionItem{
			Value: prefix + match,
			Kind:  completionResultKindPlain,
		})
	}
	return items
}

func (e completionEngine) toolNames() []string {
	all := tools.Registry.All()
	out := make([]string, 0, len(all))
	for name := range all {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func appendCommandAndFlags(prefix string, items []completionItem) []completionItem {
	combined := make([]completionItem, 0, len(completionCommands)+len(completionGlobalFlags)+len(items))
	combined = append(combined, items...)
	combined = append(combined, filterPlain(prefix, completionCommands)...)
	combined = append(combined, filterFlags(prefix)...)
	return combined
}

func filterFlags(prefix string) []completionItem {
	values := make([]string, 0, len(completionGlobalFlags))
	for _, spec := range completionGlobalFlags {
		values = append(values, spec.Name)
	}
	return filterPlain(prefix, values)
}

func filterPlain(prefix string, options []string) []completionItem {
	matches := filterValues(prefix, options)
	items := make([]completionItem, 0, len(matches))
	for _, match := range matches {
		items = append(items, completionItem{Value: match, Kind: completionResultKindPlain})
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

func completionFlagByName(name string) (completionFlagSpec, bool) {
	for _, spec := range completionGlobalFlags {
		if spec.Name == name {
			return spec, true
		}
	}
	return completionFlagSpec{}, false
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

func handleCompletionCommand(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("select shell for completion command: %w", utils.ErrUserInitiatedExit)
	}

	switch args[1] {
	case "bash":
		fmt.Print(bashCompletionScript())
		return utils.ErrUserInitiatedExit
	case "zsh":
		fmt.Print(zshCompletionScript())
		return utils.ErrUserInitiatedExit
	default:
		return fmt.Errorf("unsupported completion shell %q", args[1])
	}
}

func handleHiddenCompletion(ctx context.Context, args []string) error {
	_ = ctx
	configDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("resolve config dir for completion: %w", err)
	}

	data, err := loadCompletionData(configDir)
	if err != nil {
		return fmt.Errorf("load completion data: %w", err)
	}
	tools.Init()

	engine := newCompletionEngine(data)
	resp := engine.Complete(completionRequest{Args: args[1:]})
	for _, item := range resp.Items {
		fmt.Printf("%s\t%s\n", item.Value, item.Kind)
	}
	return utils.ErrUserInitiatedExit
}

func bashCompletionScript() string {
	return `#!/usr/bin/env bash
_clai_completion() {
  local IFS=$'\n'
  COMPREPLY=()
  local out
  out=$(clai __complete "${COMP_WORDS[@]}")
  local line value kind
  while IFS=$'\t' read -r value kind; do
    [[ -z "$value" ]] && continue
    case "$kind" in
      file)
        COMPREPLY+=( $(compgen -f -- "${COMP_WORDS[COMP_CWORD]}") )
        ;;
      dir)
        COMPREPLY+=( $(compgen -d -- "${COMP_WORDS[COMP_CWORD]}") )
        ;;
      *)
        COMPREPLY+=( "$value" )
        ;;
    esac
  done <<< "$out"
}
complete -F _clai_completion clai
`
}

func zshCompletionScript() string {
	return `#compdef clai
_clai_completion() {
  local -a lines
  lines=("${(@f)$(clai __complete "${words[@]}")}")
  local line value kind
  for line in "${lines[@]}"; do
    value="${line%%$'\t'*}"
    kind="${line#*$'\t'}"
    case "$kind" in
      file)
        _files
        return
        ;;
      dir)
        _files -/
        return
        ;;
      *)
        compadd -- "$value"
        ;;
    esac
  done
}
compdef _clai_completion clai
`
}

func hasEarlyCompletionCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	return slices.Contains([]string{"completion", "__complete"}, args[0])
}

package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
	"golang.org/x/exp/maps"
)

var defaultMcpServer = pub_models.McpServer{
	Command: "npx",
	Args:    []string{"-y", "@modelcontextprotocol/server-everything"},
}

func previewConfigItem(cfg config) error {
	b, err := os.ReadFile(cfg.filePath)
	if err != nil {
		return fmt.Errorf(
			"failed to read config preview from %q: %w",
			cfg.filePath,
			err,
		)
	}

	var jzon any
	if err := json.Unmarshal(b, &jzon); err != nil {
		return fmt.Errorf("failed to unmarshal json: %w", err)
	}

	indentedJSON, err := json.MarshalIndent(jzon, "", " ")
	if err != nil {
		return fmt.Errorf("failed to indent json: %w", err)
	}

	fmt.Print(colorPrimary("Selected config preview:\n"))
	fmt.Print(
		colorBreadtext(fmt.Sprintf("%s\n---\n", string(indentedJSON))),
	)
	return nil
}

func queryForAction(options []action) (action, error) {
	var ret action
	formattedOptions := make([]string, 0, len(options))
	for _, option := range options {
		formattedOptions = append(formattedOptions, option.String())
	}
	fmt.Print(colorSecondary(fmt.Sprintf("(%s): ", strings.Join(formattedOptions, ", "))))
	input, err := utils.ReadUserInput()
	if err != nil {
		return unset, fmt.Errorf("failed to query for action: %w", err)
	}
	for choiceStr, act := range choiceToAction {
		split := strings.Split(choiceStr, ",")
		if slices.Contains(split, input) {
			ret = act
			break
		}
	}
	if ret == unset {
		ancli.Warnf("invalid choice: %v", input)
		return queryForAction(options)
	}
	if ret == quit {
		return unset, utils.ErrUserInitiatedExit
	}
	return ret, nil
}

func actOnConfigItem(category setupCategory, cfg config) error {
	selectedAction, err := queryForAction(actionsWithNavigation(category.itemActions))
	if err != nil {
		if errors.Is(err, utils.ErrBack) || errors.Is(err, utils.ErrUserInitiatedExit) {
			return err
		}
		return fmt.Errorf("failed to query for config action: %w", err)
	}

	if err := executeConfigAction(cfg, selectedAction); err != nil {
		if errors.Is(err, utils.ErrBack) || errors.Is(err, utils.ErrUserInitiatedExit) {
			return err
		}
		return fmt.Errorf("failed to execute action %q for %q: %w", selectedAction, cfg.name, err)
	}
	return nil
}

func actionPasteMcpServer(mcpCfgPath string) error {
	pastedCfgs, err := pasteMcpServerConfig(mcpCfgPath)
	if err != nil {
		return fmt.Errorf("failed to paste mcp server config: %w", err)
	}
	for _, pastedCfg := range pastedCfgs {
		if err := executeConfigAction(pastedCfg, conf); err != nil {
			return fmt.Errorf("failed to configure pasted mcp server %q: %w", pastedCfg.name, err)
		}
	}
	return nil
}

func actionReconfigure(cfg config) error {
	f, err := os.Open(cfg.filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", cfg.filePath, err)
	}
	defer func() {
		_ = f.Close()
	}()

	b, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", cfg.filePath, err)
	}
	return interractiveReconfigure(cfg, b)
}

func unescapeEditWithEditor(toEdit string) (string, error) {
	unescapedStr := utils.UnescapeEditorString(toEdit)
	tmp, err := os.CreateTemp("", "unescapeEdit_*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	_, err = tmp.WriteString(unescapedStr)
	if closeErr := tmp.Close(); closeErr != nil {
		return "", fmt.Errorf("failed to close temp file: %w", closeErr)
	}
	if err != nil {
		return "", fmt.Errorf("failed to write string to edit: %w", err)
	}
	defer os.Remove(tmp.Name())

	tmpCfg := config{
		name:     "tmpToEdit",
		filePath: tmp.Name(),
	}

	err = actionReconfigureWithEditor(tmpCfg)
	if err != nil {
		return "", fmt.Errorf("failed to reconfigure with editor: %w", err)
	}

	b, err := os.ReadFile(tmpCfg.filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read edited file: %w", err)
	}

	unescapedStr = string(b)
	unescapedStr = strings.TrimSuffix(unescapedStr, "\r\n")
	unescapedStr = strings.TrimSuffix(unescapedStr, "\n")

	escapedStr := utils.EscapeEditorString(unescapedStr)
	return escapedStr, nil
}

func validateEditedStringField(cfg config, fieldName, rawEditedValue string) error {
	if fieldName != "template" {
		return nil
	}

	if filepath.Base(filepath.Dir(cfg.filePath)) != "shellContexts" {
		return nil
	}

	def := text.ShellContextDefinition{
		Template: utils.UnescapeEditorString(rawEditedValue),
	}
	renderer := text.ShellContextRenderer{}
	_, err := renderer.Render(context.Background(), cfg.name, def)
	if err != nil {
		return fmt.Errorf("validate shell context template for %q: %w", cfg.filePath, err)
	}
	return nil
}

// actionReconfigureStringFieldWithEditor opens the config, optionally queries
// the user to select a string field, then edits it via $EDITOR with unescape/re-escape.
func actionReconfigureStringFieldWithEditor(cfg config, fieldName string) error {
	b, err := os.ReadFile(cfg.filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", cfg.filePath, err)
	}

	var jzon map[string]any
	if err := json.Unmarshal(b, &jzon); err != nil {
		return fmt.Errorf("failed to unmarshal config from %s: %w", cfg.filePath, err)
	}

	if fieldName == "" {
		var selErr error
		fieldName, selErr = selectStringField(jzon)
		if selErr != nil {
			return fmt.Errorf("failed to select field: %w", selErr)
		}
	}

	rawValue, found := jzon[fieldName]
	if !found {
		return fmt.Errorf("missing string field %q in %s", fieldName, cfg.filePath)
	}

	stringValue, ok := rawValue.(string)
	if !ok {
		return fmt.Errorf("field %q in %s is not a string, got %T", fieldName, cfg.filePath, rawValue)
	}

	editedValue, err := unescapeEditWithEditor(stringValue)
	if err != nil {
		return fmt.Errorf("failed to edit field %q with editor: %w", fieldName, err)
	}
	if err := validateEditedStringField(cfg, fieldName, editedValue); err != nil {
		return fmt.Errorf("failed to validate edited field %q: %w", fieldName, err)
	}
	jzon[fieldName] = editedValue

	if err := writeConfig(cfg.filePath, jzon); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	ancli.Okf("updated field %q at path: %v", fieldName, cfg.filePath)
	return nil
}

// selectStringField filters jzon to string-typed keys (sorted), presents a
// single-choice table, and returns the selected key.
func selectStringField(jzon map[string]any) (string, error) {
	keys := stringKeysSorted(jzon)
	indices, err := utils.SelectFromTable(
		"Select field",
		utils.SlicePaginator(keys),
		"Select field [<num>]",
		func(i int, t string) (string, error) {
			return fmt.Sprintf("%d. %s", i, t), nil
		},
		utils.ThemeTableItems(),
		true,
		nil,
		os.Stdout,
		"",
	)
	if err != nil {
		return "", err
	}
	return keys[indices[0]], nil
}

// stringKeysSorted returns the keys of jzon whose values are strings,
// sorted alphabetically.
func stringKeysSorted(jzon map[string]any) []string {
	keys := make([]string, 0)
	for k, v := range jzon {
		typeOf := reflect.TypeOf(v)
		if typeOf == nil {
			continue
		}
		if typeOf.String() == "string" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// actionReconfigurePromptWithEditor by extracting the prompt from the selected config
// and then escape-editing the field. Lastly, reapply the prompt and save the profile
func actionReconfigurePromptWithEditor(cfg config) error {
	if err := actionReconfigureStringFieldWithEditor(cfg, "prompt"); err != nil {
		return fmt.Errorf("failed to edit prompt with editor: %w", err)
	}
	return nil
}

// actionReconfigureWithEditor. As in the $EDITOR environment variable
func actionReconfigureWithEditor(cfg config) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return fmt.Errorf("environment variable EDITOR is not set")
	}
	cmd := exec.Command(editor, cfg.filePath)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to edit file %s: %w", cfg.filePath, err)
	}
	newConfig, err := os.ReadFile(cfg.filePath)
	if err != nil {
		return fmt.Errorf("editor exited OK, failed to read config file %q after edit: %w", cfg.filePath, err)
	}
	ancli.Okf("updated:\n%v", string(newConfig))
	return nil
}

func actionRemove(cfg config) error {
	fmt.Print(colorSecondary(fmt.Sprintf("Are you sure you want to delete: '%v'?\n[y/n]: ", cfg.filePath)))
	input, err := utils.ReadUserInput()
	if err != nil {
		return fmt.Errorf("read delete confirmation: %w", err)
	}
	if input != "y" {
		return fmt.Errorf("aborting deletion: %w", errors.New("delete not confirmed"))
	}
	err = os.Remove(cfg.filePath)
	if err != nil {
		return fmt.Errorf("failed to delete file %q: %w", cfg.filePath, err)
	}
	ancli.PrintOK(fmt.Sprintf("deleted file: '%v'\n", cfg.filePath))
	return nil
}

func actionCopy(cfg config) (config, error) {
	fmt.Print(colorSecondary("Enter name for copy: "))
	newName, err := utils.ReadUserInput()
	if err != nil {
		return config{}, fmt.Errorf("read copy name: %w", err)
	}
	if newName == "" {
		return config{}, fmt.Errorf("name cannot be empty")
	}

	dir := filepath.Dir(cfg.filePath)
	newPath := filepath.Join(dir, newName+".json")

	if _, statErr := os.Stat(newPath); statErr == nil {
		return config{}, fmt.Errorf("file %q already exists", newPath)
	}

	srcBytes, readErr := os.ReadFile(cfg.filePath)
	if readErr != nil {
		return config{}, fmt.Errorf("read source file: %w", readErr)
	}

	if writeErr := os.WriteFile(newPath, srcBytes, 0o644); writeErr != nil {
		return config{}, fmt.Errorf("write copy: %w", writeErr)
	}

	ancli.PrintOK(fmt.Sprintf("copied to: '%v'\n", newPath))
	return config{name: newName, filePath: newPath}, nil
}

var errDoneEditing = errors.New("done editing")

// interractiveReconfigure presents the user with a field-by-field editing loop
// over the JSON config and writes the result to disk.
func interractiveReconfigure(cfg config, b []byte) error {
	var jzon map[string]any
	err := json.Unmarshal(b, &jzon)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config %v: %w", cfg.name, err)
	}

	claiConfigDir := claiConfigDirFromPath(cfg.filePath)
	if claiConfigDir == "" {
		return fmt.Errorf("failed to derive clai config dir from path: %q", cfg.filePath)
	}

	fmt.Print(colorPrimary("Current config:\n"))
	fmt.Print(colorBreadtext(fmt.Sprintf("%s\n---\n", b)))

	for {
		key, err := selectFieldToEdit(jzon)
		if err != nil {
			if errors.Is(err, errDoneEditing) {
				break
			}
			return err
		}

		nv, err := handleValue(key, jzon[key], claiConfigDir)
		if err != nil {
			return fmt.Errorf("failed to edit field %q: %w", key, err)
		}
		jzon[key] = nv
	}

	return writeConfig(cfg.filePath, jzon)
}

func claiConfigDirFromPath(filePath string) string {
	dir := filepath.Dir(filePath)
	base := filepath.Base(dir)
	for _, sub := range []string{"profiles", "shellContexts", "mcpServers"} {
		if base == sub {
			return filepath.Dir(dir)
		}
	}
	// For files directly in the config dir (e.g., model files, textConfig.json).
	// Assume the parent is the config dir.
	return dir
}

// sortedKeys returns the keys of jzon in alphabetical order.
func sortedKeys(jzon map[string]any) []string {
	keys := make([]string, 0, len(jzon))
	for k := range jzon {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func doneEditingAction() utils.TableAction {
	return utils.TableAction{
		Format: "[d]one",
		Short:  "d",
		Long:   "done",
		Action: func() error { return errDoneEditing },
	}
}

// selectFieldToEdit presents the user with a table of jzon keys and returns
// the chosen key. It propagates errDoneEditing, utils.ErrBack, and
// utils.ErrUserInitiatedExit directly; other errors are wrapped.
func selectFieldToEdit(jzon map[string]any) (string, error) {
	keys := sortedKeys(jzon)
	indices, err := utils.SelectFromTable(
		"Select field to edit",
		utils.SlicePaginator(keys),
		"Select field [<num>]",
		func(i int, key string) (string, error) {
			return fmt.Sprintf("%d. %s", i, key), nil
		},
		utils.ThemeTableItems(),
		true,
		[]utils.TableAction{doneEditingAction()},
		os.Stdout,
		"",
	)
	if err != nil {
		return "", err
	}
	return keys[indices[0]], nil
}

// writeConfig serializes jzon as indented JSON and writes it to filePath.
func writeConfig(filePath string, jzon map[string]any) error {
	newB, err := json.MarshalIndent(jzon, "", "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal new config: %w", err)
	}
	if err := os.WriteFile(filePath, newB, 0o644); err != nil {
		return fmt.Errorf("failed to write config at %q: %w", filePath, err)
	}
	return nil
}

var (
	errClearTools    = errors.New("clear tools")
	errAllTools      = errors.New("all tools")
	errDoneSelecting = errors.New("done selecting")
)

// getToolsValue presents an interactive toggle-table for tool selection
// and returns the ordered list of selected tool names.
func getToolsValue(v any) ([]string, error) {
	sArr, isSSlice := v.([]any)
	if !isSSlice {
		ancli.PrintWarn(fmt.Sprintf("invalid type for tools, expected string slice, got: %v. Returning empty slice\n", sArr))
		return []string{}, nil
	}

	currentlySelected := parseCurrentTools(sArr)
	names := sortedToolNames()
	doneAction, allAction, clearAction := toolSelectionActions()

	fmt.Print(colorPrimary("Tooling configuration\n"))
	for {
		indices, err := utils.SelectFromTable(
			"Toggle tools with comma/range (e.g. 0,2,5 or 0:3)",
			utils.SlicePaginator(names),
			"Select tools [<num>]",
			toolRowFormatter(currentlySelected),
			len(names),
			false,
			[]utils.TableAction{allAction, clearAction, doneAction},
			os.Stdout,
			"",
		)
		if err != nil {
			switch {
			case errors.Is(err, errDoneSelecting):
				return orderedSelectedTools(names, currentlySelected), nil
			case errors.Is(err, errAllTools):
				selectAllTools(names, currentlySelected)
				continue
			case errors.Is(err, errClearTools):
				clearToolSelections(currentlySelected)
				continue
			case errors.Is(err, utils.ErrBack):
				return drainCurrentTools(sArr), nil
			case errors.Is(err, utils.ErrUserInitiatedExit):
				return nil, err
			default:
				return nil, err
			}
		}
		toggleToolSelections(indices, names, currentlySelected)
	}
}

// parseCurrentTools converts a []any of tool name strings into a set map.
func parseCurrentTools(sArr []any) map[string]bool {
	selected := make(map[string]bool, len(sArr))
	for _, item := range sArr {
		if s, ok := item.(string); ok {
			selected[s] = true
		}
	}
	return selected
}

// sortedToolNames returns all registered tool names in alphabetical order.
func sortedToolNames() []string {
	tools.Init()
	allTools := tools.Registry.All()
	names := make([]string, 0, len(allTools))
	for name := range allTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// toolSelectionActions returns the three sentinel actions for the tools selection table.
func toolSelectionActions() (doneAction, allAction, clearAction utils.TableAction) {
	doneAction = utils.TableAction{
		Format: "[d]one",
		Short:  "d",
		Long:   "done",
		Action: func() error { return errDoneSelecting },
	}
	allAction = utils.TableAction{
		Format: "[a]ll",
		Short:  "a",
		Long:   "all",
		Action: func() error { return errAllTools },
	}
	clearAction = utils.TableAction{
		Format: "[c]lear all",
		Short:  "c",
		Long:   "clear",
		Action: func() error { return errClearTools },
	}
	return
}

// selectAllTools marks every name in the set as selected.
func selectAllTools(names []string, selected map[string]bool) {
	for _, n := range names {
		selected[n] = true
	}
}

// clearToolSelections removes all entries from the selected set.
func clearToolSelections(selected map[string]bool) {
	for k := range selected {
		delete(selected, k)
	}
}

// toggleToolSelections flips the selected state for each indexed tool name.
func toggleToolSelections(indices []int, names []string, selected map[string]bool) {
	for _, idx := range indices {
		if idx >= 0 && idx < len(names) {
			name := names[idx]
			if selected[name] {
				delete(selected, name)
			} else {
				selected[name] = true
			}
		}
	}
}

// orderedSelectedTools returns the selected tool names in the order they appear
// in the sorted names slice.
func orderedSelectedTools(names []string, selected map[string]bool) []string {
	ret := make([]string, 0, len(selected))
	for _, name := range names {
		if selected[name] {
			ret = append(ret, name)
		}
	}
	return ret
}

// drainCurrentTools extracts string values from a []any preserving order.
func drainCurrentTools(sArr []any) []string {
	ret := make([]string, 0, len(sArr))
	for _, item := range sArr {
		if s, ok := item.(string); ok {
			ret = append(ret, s)
		}
	}
	return ret
}

// toolRowFormatter returns a row formatter that prefixes selected tools
// with "[X]" and unselected tools with "[ ]".
func toolRowFormatter(currentlySelected map[string]bool) func(int, string) (string, error) {
	return func(i int, name string) (string, error) {
		prefix := "[ ]"
		if currentlySelected[name] {
			prefix = "[X]"
		}
		return fmt.Sprintf("%s %d. %s", prefix, i, name), nil
	}
}

// getNewValue handles primitive scalar values, dispatching on key name
// for model and shell_context or falling back to a text input prompt.
func getNewValue(k string, v any, claiConfigDir string) (any, error) {
	switch k {
	case "model":
		return getModelValue(v, claiConfigDir)
	case "shell_context":
		return getShellContextValue(v, claiConfigDir)
	}

	fmt.Print(colorBreadtext(fmt.Sprintf("Key: '%v', current: '%v'\n", k, v)))
	fmt.Print(colorSecondary("Please enter new value, or leave empty to keep: "))
	input, err := utils.ReadUserInput()
	if err != nil {
		return nil, fmt.Errorf("failed to read input for key %q: %w", k, err)
	}
	if input == "" {
		return v, nil
	}
	return castPrimitive(input), nil
}

// getModelValue presents a table of available models for selection.
func getModelValue(v any, claiConfigDir string) (any, error) {
	models, err := getAvailableModels(claiConfigDir)
	if err != nil {
		return nil, fmt.Errorf("failed to discover models: %w", err)
	}
	if len(models) == 0 {
		return v, nil
	}

	currentStr, _ := v.(string)
	fmt.Print(colorPrimary(fmt.Sprintf("Select model (current: %q):\n", currentStr)))
	choice, err := utils.SelectFromTable(
		"Available models",
		utils.SlicePaginator(models),
		"Select model <num>: ",
		func(i int, name string) (string, error) {
			return fmt.Sprintf("%d. %s", i, name), nil
		},
		utils.ThemeTableItems(),
		true,
		nil,
		os.Stdout,
		"",
	)
	if err != nil {
		// Back/quit → keep current value
		return v, nil
	}
	if len(choice) == 0 {
		return v, nil
	}
	return castPrimitive(models[choice[0]]), nil
}

// getShellContextValue presents a table of available shell contexts for selection.
func getShellContextValue(v any, claiConfigDir string) (any, error) {
	contexts, err := getAvailableShellContexts(claiConfigDir)
	if err != nil {
		return nil, fmt.Errorf("failed to discover shell contexts: %w", err)
	}
	if len(contexts) == 0 {
		return v, nil
	}

	currentStr, _ := v.(string)
	fmt.Print(colorPrimary(fmt.Sprintf("Select shell_context (current: %q):\n", currentStr)))
	choice, err := utils.SelectFromTable(
		"Available shell contexts",
		utils.SlicePaginator(contexts),
		"Select shell context <num>: ",
		func(i int, name string) (string, error) {
			return fmt.Sprintf("%d. %s", i, name), nil
		},
		utils.ThemeTableItems(),
		true,
		nil,
		os.Stdout,
		"",
	)
	if err != nil {
		// Back/quit → keep current value
		return v, nil
	}
	if len(choice) == 0 {
		return v, nil
	}
	return castPrimitive(contexts[choice[0]]), nil
}

// getAvailableModels discovers model configurations from the clai config directory.
func getAvailableModels(claiConfigDir string) ([]string, error) {
	cfgs, err := getConfigs(filepath.Join(claiConfigDir, "*.json"), []string{"textConfig", "photoConfig", "videoConfig"})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		name := strings.TrimSuffix(c.name, ".json")
		parts := strings.SplitN(name, "_", 3)
		if len(parts) < 3 {
			continue
		}
		canonical := text.CanonicalModelString(parts[0], parts[1], parts[2])
		if canonical != "" {
			names = append(names, canonical)
		}
	}
	return names, nil
}

// getAvailableShellContexts discovers shell context configurations from the clai config directory.
func getAvailableShellContexts(claiConfigDir string) ([]string, error) {
	shellCtxDir := filepath.Join(claiConfigDir, "shellContexts")
	cfgs, err := getConfigs(filepath.Join(shellCtxDir, "*.json"), []string{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		name := strings.TrimSuffix(c.name, ".json")
		names = append(names, name)
	}
	return names, nil
}

// handleValue dispatches value editing to the appropriate handler based on
// key name (for "tools") or value type (map, slice, primitive).
func handleValue(k string, v any, claiConfigDir string) (any, error) {
	if k == "tools" {
		return getToolsValue(v)
	}
	switch val := v.(type) {
	case map[string]any:
		return editMap(k, val, claiConfigDir)
	case []any:
		return editSlice(k, val, claiConfigDir)
	default:
		return getNewValue(k, val, claiConfigDir)
	}
}

// editMap presents an interactive loop for adding, updating, removing keys
// from a JSON map, or marking it as done.
func editMap(k string, m map[string]any, claiConfigDir string) (map[string]any, error) {
	edited := maps.Clone(m)
	for {
		keys := sortedKeys(edited)
		fmt.Print(colorSecondary(fmt.Sprintf("Map '%s' keys: %v\n[a]dd [u]pdate [r]emove [d]one: ", k, keys)))
		action, err := utils.ReadUserInput()
		if err != nil {
			return nil, fmt.Errorf("read map action: %w", err)
		}
		switch action {
		case "d", "":
			return edited, nil
		case "a":
			fmt.Print(colorSecondary("New key: "))
			nk, err := utils.ReadUserInput()
			if err != nil {
				return nil, fmt.Errorf("read new key: %w", err)
			}
			fmt.Print(colorSecondary("Value: "))
			nv, err := utils.ReadUserInput()
			if err != nil {
				return nil, fmt.Errorf("read new value: %w", err)
			}
			edited[nk] = castPrimitive(nv)
		case "r":
			fmt.Print(colorSecondary("Key to remove: "))
			rk, err := utils.ReadUserInput()
			if err != nil {
				return nil, fmt.Errorf("read key to remove: %w", err)
			}
			delete(edited, rk)
		case "u":
			fmt.Print(colorSecondary("Key to update: "))
			uk, err := utils.ReadUserInput()
			if err != nil {
				return nil, fmt.Errorf("read key to update: %w", err)
			}
			val, exists := edited[uk]
			if !exists {
				fmt.Print(colorBreadtext(fmt.Sprintf("no such key '%s'\n", uk)))
				continue
			}
			nv, err := handleValue(fmt.Sprintf("%s.%s", k, uk), val, claiConfigDir)
			if err != nil {
				return nil, fmt.Errorf("failed to handle map value %q: %w", uk, err)
			}
			edited[uk] = nv
		default:
			fmt.Print(colorBreadtext(fmt.Sprintf("unsupported map action %q\n", action)))
		}
	}
}

// editSlice presents an interactive loop for appending, updating, removing
// elements from a JSON array, or marking it as done.
func editSlice(k string, s []any, claiConfigDir string) ([]any, error) {
	edited := append([]any(nil), s...)
	for {
		fmt.Print(colorSecondary(fmt.Sprintf("Slice '%s': %v\n[a]ppend [u]pdate [r]emove [d]one: ", k, edited)))
		action, err := utils.ReadUserInput()
		if err != nil {
			return nil, fmt.Errorf("read slice action: %w", err)
		}
		switch action {
		case "d", "":
			return edited, nil
		case "a":
			fmt.Print(colorSecondary("Value: "))
			nv, err := utils.ReadUserInput()
			if err != nil {
				return nil, fmt.Errorf("read append value: %w", err)
			}
			edited = append(edited, castPrimitive(nv))
		case "r":
			fmt.Print(colorSecondary(fmt.Sprintf("Index to remove (0-%d, multi-select with ex: '1-3'): ", len(edited)-1)))
			idxStr, err := utils.ReadUserInput()
			if err != nil {
				return nil, fmt.Errorf("read remove index: %w", err)
			}
			var delErr error
			edited, delErr = deleteFromSlice(edited, idxStr)
			if delErr != nil {
				ancli.Errf("%v", delErr)
				continue
			}

		case "u":
			fmt.Print(colorSecondary(fmt.Sprintf("Index to update (0-%d): ", len(edited)-1)))
			idxStr, err := utils.ReadUserInput()
			if err != nil {
				return nil, fmt.Errorf("read update index: %w", err)
			}
			idx, err := strconv.Atoi(idxStr)
			if err != nil || idx < 0 || idx >= len(edited) {
				fmt.Println(colorBreadtext("invalid index"))
				continue
			}
			val := edited[idx]
			nv, err := handleValue(fmt.Sprintf("%s[%d]", k, idx), val, claiConfigDir)
			if err != nil {
				return nil, fmt.Errorf("failed to handle slice value at %d: %w", idx, err)
			}
			edited[idx] = nv
		default:
			fmt.Println(colorBreadtext("invalid slice action"))
		}
	}
}

// deleteFromSlice removes elements from a slice. The idxStr can be a single
// index (e.g. "2") or a range (e.g. "1-3" inclusive). Returns the modified
// slice or an error describing why the operation was invalid.
func deleteFromSlice(s []any, idxStr string) ([]any, error) {
	if !strings.Contains(idxStr, "-") {
		idx, convErr := strconv.Atoi(idxStr)
		if convErr != nil || idx < 0 || idx >= len(s) {
			return s, fmt.Errorf("invalid index: %v", idxStr)
		}
		return append(s[:idx], s[idx+1:]...), nil
	}

	split := strings.Split(idxStr, "-")
	p, q := -1, -1
	for _, part := range split {
		idx, err := strconv.Atoi(part)
		if err != nil {
			return s, fmt.Errorf("failed to convert %q to integer: %w", part, err)
		}
		if p == -1 {
			p = idx
		} else {
			q = idx
		}
	}

	switch {
	case p < 0:
		return s, fmt.Errorf("invalid range selection, p: %v, q: %v, len: %v", p, q, len(s))
	case q < 0:
		return s, fmt.Errorf("invalid range selection, p: %v, q: %v, len: %v", p, q, len(s))
	case q >= len(s):
		return s, fmt.Errorf("invalid range selection, p: %v, q: %v, len: %v", p, q, len(s))
	case p > q:
		return s, fmt.Errorf("invalid range selection, p: %v, q: %v, len: %v", p, q, len(s))
	}

	result, err := utils.DeleteRange(s, p, q)
	if err != nil {
		return s, fmt.Errorf("failed to delete range [%d-%d]: %w", p, q, err)
	}
	return result, nil
}

// castPrimitive attempts to convert a string value to bool, int, or float64.
// If the value is not a string, it is returned as-is.
func castPrimitive(v any) any {
	if misc.Truthy(v) {
		return true
	}

	if misc.Falsy(v) {
		return false
	}

	s, isString := v.(string)
	if !isString {
		return v
	}
	i, err := strconv.Atoi(s)
	if err == nil {
		return i
	}
	f, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return f
	}
	return s
}

func createConfigFile[T any](configTypePath, configType string, defaultConfig T) (config, error) {
	if _, err := os.Stat(configTypePath); os.IsNotExist(err) {
		if err := os.MkdirAll(configTypePath, os.ModePerm); err != nil {
			return config{}, fmt.Errorf("failed to create %s directory: %w", configType, err)
		}
	}
	fmt.Print(colorSecondary(fmt.Sprintf("Enter %s name: ", configType)))
	configName, err := utils.ReadUserInput()
	if err != nil {
		return config{}, fmt.Errorf("read %s name: %w", configType, err)
	}
	newConfigPath := path.Join(configTypePath, fmt.Sprintf("%v.json", configName))
	err = utils.CreateFile(newConfigPath, &defaultConfig)
	if err != nil {
		return config{}, fmt.Errorf("create %s file: %w", configType, err)
	}
	return config{
		name:     configName,
		filePath: newConfigPath,
	}, nil
}

func pasteMcpServerConfig(mcpConfDir string) ([]config, error) {
	ancli.Noticef("Paste your MCP server configuration below.")
	ancli.Noticef("Press Ctrl+D when finished (or type 'EOF' on a new line):")

	var lines []string
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "EOF" {
			break
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading input: %w", err)
	}

	pastedConfig := strings.Join(lines, "\n")
	if strings.TrimSpace(pastedConfig) == "" {
		return nil, fmt.Errorf("no configuration provided")
	}

	serverNames, err := ParseAndAddMcpServer(mcpConfDir, pastedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse mcp server: %w", err)
	}

	ret := make([]config, 0, len(serverNames))
	for _, s := range serverNames {
		ret = append(ret, config{
			name:     s,
			filePath: filepath.Join(mcpConfDir, fmt.Sprintf("%v.json", s)),
		})
	}

	return ret, nil
}

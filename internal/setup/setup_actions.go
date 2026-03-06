package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
	"golang.org/x/exp/maps"
)

func queryForAction(options []action) (action, error) {
	var ret action
	var userQuery strings.Builder
	userQuery.WriteString("Do you wish to ")
	for _, s := range options {
		userQuery.WriteString(fmt.Sprintf("%v, ", s))
	}
	userQuery.WriteString("[b]ack [q]uit: ")
	fmt.Print(colorSecondary(userQuery.String()))
	input, err := utils.ReadUserInput()
	if err != nil {
		return unset, fmt.Errorf("failed to query for action: %w", err)
	}
	switch input {
	case "c", "configure":
		if slices.Contains(options, conf) {
			ret = conf
		}
	case "d", "delete":
		if slices.Contains(options, del) {
			ret = del
		}
	case "n", "new":
		if slices.Contains(options, newaction) {
			ret = newaction
		}
	case "e", "configureWithEditor":
		if slices.Contains(options, confWithEditor) {
			ret = confWithEditor
		}
	case "p", "pasteNew":
		if slices.Contains(options, pasteNew) {
			ret = pasteNew
		}
	case "pr", "promptWithEditor":
		if slices.Contains(options, promptEditWithEditor) {
			ret = promptEditWithEditor
		}
	case "b", "back":
		return unset, fmt.Errorf("user chose to go back from actions: %w", utils.ErrBack)
	case "q", "quit":
		return unset, utils.ErrUserInitiatedExit
	}

	if ret == unset {
		return unset, fmt.Errorf("invalid choice: %v", input)
	}
	return ret, nil
}

func selectConfigItem(category setupCategory, cfgs []config) error {
	if len(cfgs) == 0 {
		return fmt.Errorf("found no configuration files for category %q", category.name)
	}

	selectedIndices, err := utils.SelectFromTable(
		fmt.Sprintf("Configs in %s", category.name),
		cfgs,
		"select config: [<num>], next[<enter>]/[n]ext, [p]rev, [q]uit): ",
		func(i int, cfg config) (string, error) {
			return fmt.Sprintf("%d. %s", i, cfg.name), nil
		},
		10,
		true,
		true,
	)
	if err != nil {
		return fmt.Errorf("failed to select config item: %w", err)
	}

	selectedIndex := selectedIndices[0]
	if selectedIndex < 0 || selectedIndex >= len(cfgs) {
		return fmt.Errorf("selected config index %d out of range", selectedIndex)
	}

	selectedCfg := cfgs[selectedIndex]
	if err := previewConfigItem(selectedCfg); err != nil {
		return fmt.Errorf("failed to preview selected config item %q: %w", selectedCfg.name, err)
	}

	return actOnConfigItem(category, selectedCfg)
}

func previewConfigItem(cfg config) error {
	if cfg.isSynthetic {
		return nil
	}

	b, err := os.ReadFile(cfg.filePath)
	if err != nil {
		return fmt.Errorf("failed to read config preview from %q: %w", cfg.filePath, err)
	}

	fmt.Print(colorPrimary("Selected config preview:\n"))
	fmt.Print(colorBreadtext(fmt.Sprintf("%s\n---\n", b)))
	return nil
}

func actOnConfigItem(category setupCategory, cfg config) error {
	if cfg.isSynthetic {
		return executeSyntheticConfig(cfg)
	}

	selectedAction, err := queryForAction(category.actions)
	if err != nil {
		if errors.Is(err, utils.ErrBack) {
			return fmt.Errorf("user returned to config list: %w", err)
		}
		return fmt.Errorf("failed to query for config action: %w", err)
	}

	if err := executeConfigAction(cfg, selectedAction); err != nil {
		return fmt.Errorf("failed to execute action %q for %q: %w", selectedAction, cfg.name, err)
	}
	return nil
}

func executeSyntheticConfig(cfg config) error {
	switch cfg.kind {
	case configKindCreateProfile:
		createdCfg, err := createProFile(cfg.filePath)
		if err != nil {
			return fmt.Errorf("failed to create profile config: %w", err)
		}
		if err := executeConfigAction(createdCfg, conf); err != nil {
			return fmt.Errorf("failed to configure created profile %q: %w", createdCfg.name, err)
		}
		return nil
	case configKindCreateMCPServer:
		createdCfg, err := createMcpServerFile(cfg.filePath)
		if err != nil {
			return fmt.Errorf("failed to create mcp server config: %w", err)
		}
		if err := executeConfigAction(createdCfg, conf); err != nil {
			return fmt.Errorf("failed to configure created mcp server %q: %w", createdCfg.name, err)
		}
		return nil
	case configKindPasteMCPConfig:
		pastedCfgs, err := pasteMcpServerConfig(cfg.filePath)
		if err != nil {
			return fmt.Errorf("failed to paste mcp server config: %w", err)
		}
		for _, pastedCfg := range pastedCfgs {
			if err := executeConfigAction(pastedCfg, conf); err != nil {
				return fmt.Errorf("failed to configure pasted mcp server %q: %w", pastedCfg.name, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported synthetic config kind %d", cfg.kind)
	}
}

func executeConfigAction(cfg config, a action) error {
	switch a {
	case conf:
		return reconfigure(cfg)
	case confWithEditor:
		return reconfigureWithEditor(cfg)
	case promptEditWithEditor:
		return reconfigurePromptWithEditor(cfg)
	case del:
		return remove(cfg)
	default:
		return fmt.Errorf("invalid action for config %q: %v", cfg.name, a)
	}
}

func reconfigure(cfg config) error {
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
	unescapedStr := strings.ReplaceAll(toEdit, "\\t", "\t")
	unescapedStr = strings.ReplaceAll(unescapedStr, "\\n", "\n")
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
		kind:     configKindNormal,
	}

	err = reconfigureWithEditor(tmpCfg)
	if err != nil {
		return "", fmt.Errorf("failed to reconfigure with editor: %w", err)
	}

	b, err := os.ReadFile(tmpCfg.filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read edited file: %w", err)
	}

	unescapedStr = string(b)
	escapedStr := strings.ReplaceAll(unescapedStr, "\t", "\\t")
	escapedStr = strings.ReplaceAll(escapedStr, "\n", "\\n")
	return escapedStr, nil
}

// reconfigurePromptWithEditor by extracting the prompt from the selected config
// and then escape-editing the field. Lastly, reapply the prompt and save the profile
func reconfigurePromptWithEditor(cfg config) error {
	b, err := os.ReadFile(cfg.filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", cfg.filePath, err)
	}
	var profile text.Profile
	err = json.Unmarshal(b, &profile)
	if err != nil {
		return fmt.Errorf("failed to unmarshal profile from %s: %w", cfg.filePath, err)
	}
	editedPrompt, err := unescapeEditWithEditor(profile.Prompt)
	if err != nil {
		return fmt.Errorf("failed to edit prompt with editor: %w", err)
	}
	profile.Prompt = editedPrompt
	editedB, err := json.MarshalIndent(profile, "", "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal edited profile %s: %w", cfg.filePath, err)
	}

	err = os.WriteFile(cfg.filePath, editedB, 0x755)
	if err != nil {
		return fmt.Errorf("failed to write profile %s: %w", cfg.filePath, err)
	}
	ancli.Okf("updated profile at path: %v", cfg.filePath)
	return nil
}

// reconfigureWithEditor. As in the $EDITOR environment variable
func reconfigureWithEditor(cfg config) error {
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

func remove(cfg config) error {
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

func interractiveReconfigure(cfg config, b []byte) error {
	var jzon map[string]any
	err := json.Unmarshal(b, &jzon)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config %v: %w", cfg.name, err)
	}
	fmt.Print(colorPrimary("Current config:\n"))
	fmt.Print(colorBreadtext(fmt.Sprintf("%s\n---\n", b)))
	newConfig, err := buildNewConfig(jzon)
	if err != nil {
		return fmt.Errorf("failed to build new config for %s: %w", cfg.name, err)
	}

	newB, err := json.MarshalIndent(newConfig, "", "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal new config for %s: %w", cfg.name, err)
	}
	err = os.WriteFile(cfg.filePath, newB, 0o644)
	if err != nil {
		return fmt.Errorf("failed to write new config at %q: %w", cfg.filePath, err)
	}
	ancli.PrintOK(fmt.Sprintf("wrote new config to: '%v'\n", cfg.filePath))
	return nil
}

func getToolsValue(v any) ([]string, error) {
	sArr, isSSlice := v.([]any)
	if !isSSlice {
		ancli.PrintWarn(fmt.Sprintf("invalid type for tools, expected string slice, got: %v. Returning empty slice\n", sArr))
		return []string{}, nil
	}
	fmt.Println(colorPrimary("Tooling configuration, select which tools you'd like for the profile to use"))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Index\tName\tDescription")
	fmt.Fprint(w, "-----\t----\t----------\n")
	indexMap := map[int]string{}
	i := 0
	for name, v := range tools.Registry.All() {
		indexMap[i] = name
		fmt.Fprintf(w, "%v\t%v\t%v\n", i, name, v.Specification().Description)
		i++
	}
	if err := w.Flush(); err != nil {
		return nil, fmt.Errorf("failed to flush tool table: %w", err)
	}
	fmt.Print(colorSecondary("Enter indices of tools to use (example: '1,3,4,2'): "))
	input, err := utils.ReadUserInput()
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}
	if input == "q" || input == "quit" {
		return []string{}, utils.ErrUserInitiatedExit
	}

	if input == "" {
		stringSlice, ok := v.([]string)
		if ok {
			return stringSlice, nil
		}
		return []string{}, nil
	}
	re := regexp.MustCompile(`\d`)
	digits := re.FindAllString(input, -1)

	var ret []string
	for _, d := range digits {
		dint, convErr := strconv.Atoi(d)
		if convErr != nil {
			return nil, fmt.Errorf("failed to convert tool index %q: %w", d, convErr)
		}
		t, exists := indexMap[dint]
		if !exists {
			ancli.PrintWarn(fmt.Sprintf("there is no index: %v, skipping", d))
			continue
		}
		ret = append(ret, t)
	}
	return ret, nil
}

func getNewValue(k string, v any) (any, error) {
	if k == "tools" {
		return getToolsValue(v)
	}
	var ret any
	fmt.Print(colorBreadtext(fmt.Sprintf("Key: '%v', current: '%v'\n", k, v)))
	fmt.Print(colorSecondary("Please enter new value, or leave empty to keep: "))
	input, err := utils.ReadUserInput()
	if err != nil {
		return nil, fmt.Errorf("failed to read input for key %q: %w", k, err)
	}
	if input == "" {
		ret = v
	} else {
		ret = castPrimitive(input)
	}
	return ret, nil
}

func handleValue(k string, v any) (any, error) {
	switch val := v.(type) {
	case map[string]any:
		return editMap(k, val)
	case []any:
		return editSlice(k, val)
	default:
		return getNewValue(k, val)
	}
}

func editMap(k string, m map[string]any) (map[string]any, error) {
	edited := maps.Clone(m)
	for {
		var keys []string
		for key := range edited {
			keys = append(keys, key)
		}
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
			nv, err := handleValue(fmt.Sprintf("%s.%s", k, uk), val)
			if err != nil {
				return nil, fmt.Errorf("failed to handle map value %q: %w", uk, err)
			}
			edited[uk] = nv
		default:
			fmt.Print(colorBreadtext(fmt.Sprintf("unsupported map action %q\n", action)))
		}
	}
}

func editSlice(k string, s []any) ([]any, error) {
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
			if strings.Contains(idxStr, "-") {
				split := strings.Split(idxStr, "-")
				p, q := -1, -1
				var multiDelErr error
			SPLIT_LOOP:
				for _, i := range split {
					idx, atoiErr := strconv.Atoi(i)
					if atoiErr != nil {
						multiDelErr = fmt.Errorf("failed to convert %q to integer: %w", i, atoiErr)
						break SPLIT_LOOP
					}
					if p == -1 {
						p = idx
					} else {
						q = idx
					}
					pTooLow := p < -1
					qTooLow := p > -1 && q < -1
					qTooHigh := q >= len(edited)
					pHigherThanQ := p > q && q != -1
					if qTooLow || pTooLow || qTooHigh || pHigherThanQ {
						checks := fmt.Sprintf("qTooLow: %v, pTooLow: %v, qTooHigh: %v, pHigherThanQ: %v", pTooLow, qTooLow, qTooHigh, pHigherThanQ)
						multiDelErr = fmt.Errorf("invalid range selection, p: %v, q: %v, len: %v. checks: %v", p, q, len(edited), checks)
						break SPLIT_LOOP
					}
				}
				if multiDelErr != nil {
					ancli.Errf("failed to delete range: %v", multiDelErr)
					continue
				}
				edited, err = utils.DeleteRange(edited, p, q)
				if err != nil {
					ancli.Errf("failed to delete range: %v", err)
					continue
				}
			} else {
				idx, convErr := strconv.Atoi(idxStr)
				if convErr != nil || idx < 0 || idx >= len(edited) {
					ancli.Errf("invalid index: %v", idxStr)
					continue
				}
				edited = append(edited[:idx], edited[idx+1:]...)
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
			nv, err := handleValue(fmt.Sprintf("%s[%d]", k, idx), val)
			if err != nil {
				return nil, fmt.Errorf("failed to handle slice value at %d: %w", idx, err)
			}
			edited[idx] = nv
		default:
			fmt.Println(colorBreadtext("invalid slice action"))
		}
	}
}

func buildNewConfig(jzon map[string]any) (map[string]any, error) {
	newConfig := make(map[string]any)
	for k, v := range jzon {
		nv, err := handleValue(k, v)
		if err != nil {
			return nil, fmt.Errorf("failed to handle key %q: %w", k, err)
		}
		newConfig[k] = nv
	}
	return newConfig, nil
}

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

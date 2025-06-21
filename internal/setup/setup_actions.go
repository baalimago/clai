package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
	"golang.org/x/exp/maps"
)

func queryForAction(options []action) (action, error) {
	var input string
	var ret action
	userQuery := "Do you wish to "
	for _, s := range options {
		userQuery += fmt.Sprintf("%v, ", s)
	}
	userQuery += "[q]uit: "
	fmt.Print(userQuery)
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
	case "q", "quit":
		return unset, utils.ErrUserInitiatedExit
	}

	if ret == unset {
		return unset, fmt.Errorf("invalid choice: %v", input)
	}
	return ret, nil
}

func configure(cfgs []config, a action) error {
	var input string
	index := len(cfgs) - 1
	if index == -1 {
		return fmt.Errorf("found no configuration files, cant %v", a)
	}
	if index != 0 {
		fmt.Println("Found config files: ")
		for i, cfg := range cfgs {
			fmt.Printf("\t%v: %v\n", i, cfg.name)
		}
		fmt.Print("Please pick index: ")
		shadowInput, err := utils.ReadUserInput()
		if err != nil {
			return err
		}
		input = shadowInput
		i, err := strconv.Atoi(input)
		if err != nil {
			return fmt.Errorf("invalid index: %v", input)
		}
		index = i
		if index < 0 || index >= len(cfgs) {
			return fmt.Errorf("invalid index: %v, must be between 0 and %v", index, len(cfgs))
		}
	}

	switch a {
	case conf:
		return reconfigure(cfgs[index])
	case confWithEditor:
		return reconfigureWithEditor(cfgs[index])
	case del:
		return remove(cfgs[index])
	default:
		return fmt.Errorf("invalid action, expected conf or del: %v", input)
	}
}

func reconfigure(cfg config) error {
	f, err := os.Open(cfg.filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %v", cfg.filePath, err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %v", cfg.filePath, err)
	}
	return interractiveReconfigure(cfg, b)
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
		return fmt.Errorf("failed to edit file %s: %v", cfg.filePath, err)
	}
	newConfig, err := os.ReadFile(cfg.filePath)
	if err != nil {
		return fmt.Errorf("editor exited OK, failed to read config file '%v' after, error: %v", cfg.filePath, err)
	}
	ancli.Okf("new config:\n%v", string(newConfig))
	return nil
}

func remove(cfg config) error {
	fmt.Printf("Are you sure you want to delete: '%v'?\n[y/n]: ", cfg.filePath)
	input, err := utils.ReadUserInput()
	if err != nil {
		return err
	}
	if input != "y" {
		return fmt.Errorf("aborting deletion")
	}
	err = os.Remove(cfg.filePath)
	if err != nil {
		return fmt.Errorf("failed to delete file: '%v', error: %v", cfg.filePath, err)
	}
	ancli.PrintOK(fmt.Sprintf("deleted file: '%v'\n", cfg.filePath))
	return nil
}

func interractiveReconfigure(cfg config, b []byte) error {
	var jzon map[string]any
	err := json.Unmarshal(b, &jzon)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %v, error: %w", cfg.name, err)
	}
	fmt.Printf("Current config:\n%s\n---\n", b)
	newConfig, err := buildNewConfig(jzon)
	if err != nil {
		return fmt.Errorf("failed to build new config: %w", err)
	}

	newB, err := json.MarshalIndent(newConfig, "", "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal new config: %w", err)
	}
	err = os.WriteFile(cfg.filePath, newB, 0o644)
	if err != nil {
		return fmt.Errorf("failed to write new config at: '%v', error: %w", cfg.filePath, err)
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
	fmt.Println("Tooling configuration, select which tools you'd like for the profile to use")
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
	w.Flush()
	fmt.Print("Enter indices of tools to use (example: '1,3,4,2'): ")
	input, err := utils.ReadUserInput()
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %v", err)
	}
	if input == "q" || input == "quit" {
		return []string{}, utils.ErrUserInitiatedExit
	}

	if input == "" {
		return v.([]string), nil
	}
	re := regexp.MustCompile(`\d`)
	digits := re.FindAllString(input, -1)

	var ret []string
	for _, d := range digits {
		dint, _ := strconv.Atoi(d)
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
	fmt.Printf("Key: '%v', current: '%v'\nPlease enter new value, or leave empty to keep: ", k, v)
	input, err := utils.ReadUserInput()
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
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
		fmt.Printf("Map '%s' keys: %v\n[a]dd [u]pdate [r]emove [d]one: ", k, keys)
		action, err := utils.ReadUserInput()
		if err != nil {
			return nil, err
		}
		switch action {
		case "d", "":
			return edited, nil
		case "a":
			fmt.Print("New key: ")
			nk, err := utils.ReadUserInput()
			if err != nil {
				return nil, err
			}
			fmt.Print("Value: ")
			nv, err := utils.ReadUserInput()
			if err != nil {
				return nil, err
			}
			edited[nk] = castPrimitive(nv)
		case "r":
			fmt.Print("Key to remove: ")
			rk, err := utils.ReadUserInput()
			if err != nil {
				return nil, err
			}
			delete(edited, rk)
		case "u":
			fmt.Print("Key to update: ")
			uk, err := utils.ReadUserInput()
			if err != nil {
				return nil, err
			}
			val, ok := edited[uk]
			if !ok {
				fmt.Printf("no such key '%s'\n", uk)
				continue
			}
			nv, err := handleValue(fmt.Sprintf("%s.%s", k, uk), val)
			if err != nil {
				return nil, err
			}
			edited[uk] = nv
		}
	}
}

func editSlice(k string, s []any) ([]any, error) {
	edited := append([]any(nil), s...)
	for {
		fmt.Printf("Slice '%s': %v\n[a]ppend [u]pdate [r]emove [d]one: ", k, edited)
		action, err := utils.ReadUserInput()
		if err != nil {
			return nil, err
		}
		switch action {
		case "d", "":
			return edited, nil
		case "a":
			fmt.Print("Value: ")
			nv, err := utils.ReadUserInput()
			if err != nil {
				return nil, err
			}
			edited = append(edited, castPrimitive(nv))
		case "r":
			fmt.Printf("Index to remove (0-%d, multi-select with ex: '1-3'): ", len(edited)-1)
			idxStr, err := utils.ReadUserInput()
			if err != nil {
				return nil, err
			}
			if strings.Contains(idxStr, "-") {
				split := strings.Split(idxStr, "-")
				p, q := -1, -1
				var multiDelErr error
			SPLIT_LOOP:
				for _, i := range split {
					idx, atoiErr := strconv.Atoi(i)
					if atoiErr != nil {
						ancli.Errf("failed to convert: '%v', err: %v", i, err)
						break SPLIT_LOOP
					}
					if p == -1 {
						p = idx
					} else {
						q = idx
					}
					pTooLow := p < -1
					qTooLow := (p > -1 && q < -1)
					qTooHigh := q >= len(edited)
					pHigherThanQ := (p > q && q != -1)
					if qTooLow || pTooLow || qTooHigh || pHigherThanQ {
						checks := fmt.Sprintf("qTooLow: %v, pTooLow: %v, qTooHigh: %v, pHigherThanQ: %v",
							pTooLow, qTooLow, qTooHigh, pHigherThanQ)
						multiDelErr = fmt.Errorf("invalid range selection, p: %v, q: %v, len: %v\nChecks:%v", p, q, len(edited), checks)
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
				idx, err := strconv.Atoi(idxStr)
				if err != nil || idx < 0 || idx >= len(edited) {
					ancli.Errf("invalid index: %v", idx)
					continue
				}
			}

		case "u":
			fmt.Printf("Index to update (0-%d): ", len(edited)-1)
			idxStr, err := utils.ReadUserInput()
			if err != nil {
				return nil, err
			}
			idx, err := strconv.Atoi(idxStr)
			if err != nil || idx < 0 || idx >= len(edited) {
				fmt.Println("invalid index")
				continue
			}
			val := edited[idx]
			nv, err := handleValue(fmt.Sprintf("%s[%d]", k, idx), val)
			if err != nil {
				return nil, err
			}
			edited[idx] = nv
		}
	}
}

func buildNewConfig(jzon map[string]any) (map[string]any, error) {
	newConfig := make(map[string]any)
	for k, v := range jzon {
		nv, err := handleValue(k, v)
		if err != nil {
			return nil, err
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
		// We don't really know what unholy value this might be, but let's just return it and hope it's benign
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

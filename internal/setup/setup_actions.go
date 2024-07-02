package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func queryForAction(options []action) (action, error) {
	var input string
	var ret action
	var userQuery = "Do you wish to "
	for _, s := range options {
		userQuery += fmt.Sprintf("%v, ", s)
	}
	userQuery += "[q]uit: "
	fmt.Print(userQuery)
	fmt.Scanln(&input)
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
		if slices.Contains(options, del) {
			ret = newaction
		}
	case "q", "quit":
		return unset, UserExit
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
		fmt.Scanln(&input)
		i, err := strconv.Atoi(input)
		if err != nil {
			return fmt.Errorf("invalid response, failed to convert choice: %v, to integer: %v", input, err)
		}
		index = i
		if index < 0 || index >= len(cfgs) {
			return fmt.Errorf("invalid index: %v, must be between 0 and %v", index, len(cfgs))
		}
	}

	switch a {
	case conf:
		return reconfigure(cfgs[index])
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

func remove(cfg config) error {
	fmt.Printf("Are you sure you want to delete: '%v'?\n[y/n]: ", cfg.filePath)
	var input string
	fmt.Scanln(&input)
	if input != "y" {
		return fmt.Errorf("aborting deletion")
	}
	err := os.Remove(cfg.filePath)
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
		return fmt.Errorf("failed to build new config: %v", err)
	}

	newB, err := json.MarshalIndent(newConfig, "", "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal new config: %v", err)
	}
	err = os.WriteFile(cfg.filePath, newB, 0o644)
	if err != nil {
		return fmt.Errorf("failed to write new config at: '%v', error: %v", cfg.filePath, err)
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
	for name, v := range tools.Tools {
		indexMap[i] = name
		fmt.Fprintf(w, "%v\t%v\t%v\n", i, name, v.UserFunction().Description)
		i++
	}
	w.Flush()
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter indices of tools to use (example: '1,3,4,2'): ")
	input, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %v", err)
	}
	if input == "q" || input == "quit" {
		return []string{}, UserExit
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
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Key: '%v', current: '%v'\nPlease enter new value, or leave empty to keep: ", k, v)
	input, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %v", err)
	}
	input = strings.TrimRight(input, "\n")
	if input == "" {
		ret = v
	} else {
		ret = input
		ret = castPrimitive(ret)
	}
	return ret, nil
}

func buildNewConfig(jzon map[string]any) (map[string]any, error) {
	newConfig := make(map[string]any)
	for k, v := range jzon {
		var newValue any
		maplike, isMap := v.(map[string]any)
		if isMap {
			m, err := buildNewConfig(maplike)
			if err != nil {
				return nil, fmt.Errorf("failed to parse nested map-like: %v", err)
			}
			newValue = m
		} else {
			n, err := getNewValue(k, v)
			if err != nil {
				return nil, fmt.Errorf("failed to get new value: %w", err)
			}
			newValue = n
		}
		newConfig[k] = newValue
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

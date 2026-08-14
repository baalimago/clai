package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// requiredConfigDirs lists directories that must exist under the clai config dir.
// Keep this in sync with any feature that persists state to disk.
var requiredConfigDirs = []string{"conversations", "profiles", "mcpServers", "conversations/dirs", "shellContexts", "skills"}

func ConfigDirPaths() []string {
	paths := make([]string, len(requiredConfigDirs))
	copy(paths, requiredConfigDirs)
	return paths
}

func ResolveConfigDirPath(configDir string, components []string) (string, error) {
	if len(components) == 0 {
		return configDir, nil
	}

	known := make(map[string]struct{}, len(requiredConfigDirs))
	for _, p := range requiredConfigDirs {
		known[p] = struct{}{}
	}

	var joined []string
	for _, component := range components {
		if component == "" {
			continue
		}
		joined = append(joined, component)
	}
	if len(joined) == 0 {
		return configDir, nil
	}

	subPath := path.Join(joined...)
	if _, exists := known[subPath]; !exists {
		return "", fmt.Errorf("unknown config subpath %q", subPath)
	}

	return filepath.Join(configDir, subPath), nil
}

type shellContextDefaultFile struct {
	Shell         string            `json:"shell"`
	TimeoutMS     int               `json:"timeout_ms"`
	TimedOutValue string            `json:"timed_out_value"`
	ErrorValue    string            `json:"error_value"`
	Template      string            `json:"template"`
	Vars          map[string]string `json:"vars"`
}

func CreateConfigDir(configPath string) error {
	for _, d := range requiredConfigDirs {
		err := ensureDirExists(configPath, d)
		if err != nil {
			return fmt.Errorf("failed to setup config dir: %w", err)
		}
	}
	if err := ensureDefaultShellContexts(configPath); err != nil {
		return fmt.Errorf("ensure default shell contexts: %w", err)
	}
	return nil
}

func ensureDirExists(configPath, toCreate string) error {
	shouldExist := path.Join(configPath, toCreate)
	if _, err := os.Stat(shouldExist); os.IsNotExist(err) {
		if err := os.MkdirAll(shouldExist, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create .clai + .clai/%v directory: %w", toCreate, err)
		}
	}
	return nil
}

func ensureDefaultShellContexts(configPath string) error {
	defaults := map[string]shellContextDefaultFile{
		"default": {
			Shell:         "",
			TimeoutMS:     250,
			TimedOutValue: "",
			ErrorValue:    "",
			Template: `cwd: {{.cwd}}
date: {{.date}}
user: {{.user}}
{{if .hostname}}hostname: {{.hostname}}
{{end}}{{if .shell}}shell: {{.shell}}
{{end}}{{if .python_venv}}python env: {{.python_venv}}
{{end}}{{if .k8s_context}}k8s context: {{.k8s_context}}
{{end}}{{if .go_version}}go version: {{.go_version}}
{{end}}{{if .git_branch}}git branch: {{.git_branch}}
{{end}}{{if .git_status_short}}git dirty: {{.git_status_short}}
{{end}}{{if .docker_context}}docker context: {{.docker_context}}
{{end}}{{if .tmux_session}}tmux session: {{.tmux_session}}
{{end}}{{if .ssh_connection}}ssh: {{.ssh_connection}}
{{end}}`,
			Vars: map[string]string{
				"cwd":              "pwd",
				"date":             "date '+%Y-%m-%d %H:%M:%S %Z'",
				"user":             "id -un",
				"hostname":         "(hostname 2>/dev/null || uname -n 2>/dev/null) | head -n 1",
				"shell":            `printf "%s" "${SHELL##*/}"`,
				"python_venv":      `if [ -n "$VIRTUAL_ENV" ]; then basename "$VIRTUAL_ENV"; elif [ -n "$CONDA_DEFAULT_ENV" ]; then printf "%s" "$CONDA_DEFAULT_ENV"; fi`,
				"k8s_context":      "kubectl config current-context 2>/dev/null",
				"go_version":       "go version 2>/dev/null | awk '{print $3}'",
				"git_branch":       "git branch --show-current 2>/dev/null",
				"git_status_short": "git status --short 2>/dev/null | wc -l | tr -d ' ' | awk '{if ($1 != 0) print $1 \" changes\"}'",
				"docker_context":   "docker context show 2>/dev/null",
				"tmux_session":     `if [ -n "$TMUX" ]; then tmux display-message -p '#S' 2>/dev/null; fi`,
				"ssh_connection":   `printf "%s" "$SSH_CONNECTION"`,
			},
		},
	}

	for name, def := range defaults {
		if err := createDefaultShellContextFile(configPath, name, def); err != nil {
			return fmt.Errorf("create default shell context %q: %w", name, err)
		}
	}
	return nil
}

func createDefaultShellContextFile(configPath, name string, def shellContextDefaultFile) error {
	shellContextPath := filepath.Join(configPath, "shellContexts", name+".json")
	if _, err := os.Stat(shellContextPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat shell context file %q: %w", shellContextPath, err)
	}

	b, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal shell context file %q: %w", shellContextPath, err)
	}
	b = append(b, byte('\n'))
	if err := os.WriteFile(shellContextPath, b, 0o644); err != nil {
		return fmt.Errorf("write shell context file %q: %w", shellContextPath, err)
	}
	return nil
}

func createDefaultConfigFile[T any](configDirPath, configFileName string, dflt *T) error {
	configFilePath := filepath.Join(configDirPath, configFileName)
	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("attempting to create file: '%v'\n", configFilePath))
		}
		err := CreateFile(configFilePath, dflt)
		if err != nil {
			return fmt.Errorf("failed to write config: '%v', error: %w", configFileName, err)
		}
	}
	return nil
}

func runMigrationCallback(migrationCb func(string) error, configDirPath string) error {
	if migrationCb != nil {
		err := migrationCb(configDirPath)
		if err != nil {
			ancli.PrintWarn(fmt.Sprintf("failed to migrate for config, error: %v\n", err))
			return err
		}
	}
	return nil
}

// ReadonlyConfig makes config loads read-only: missing fields are filled in
// memory, but the file is never rewritten and no upgrade announcement is
// printed. Set for raw (machine-readable) runs so shell hooks and scripts
// never mutate the user's configs as a side effect (config migration design,
// Q5). This is what keeps a precmd hook running `clai -r chat dirv2` from
// silently migrating the configs before the user's own commands run.
var ReadonlyConfig bool

// NoCreateConfig is stronger than ReadonlyConfig: it also suppresses config
// dir and default config file creation, migration callbacks, and the resulting
// reads. Set for read-only chat subcommands so `clai chat list` can run
// against a read-only filesystem or a config dir that does not exist yet.
var NoCreateConfig bool

func LoadConfigFromFile[T any](
	configDirPath,
	configFileName string,
	migrationCb func(string) error,
	dflt *T,
) (T, error) {
	conf, _, err := loadConfigFromFile(configDirPath, configFileName, migrationCb, dflt, true)
	return conf, err
}

// LoadConfigFromFileCollect is LoadConfigFromFile without the stdout
// announcement. It returns the JSON paths of every field it added to a
// pre-existing file, e.g. "stoploss" or
// "stoploss.max-tokens-handover-instructions"; an empty list means the file
// needed no upgrade (or was freshly created). Callers that must control
// when the announcement is printed (e.g. the interactive setup wizard,
// which prints it just before its TUI starts and pads the block with a
// blank separator line that absorbs the wizard's one-line clear overshoot)
// use this to announce at the right time.
func LoadConfigFromFileCollect[T any](
	configDirPath,
	configFileName string,
	migrationCb func(string) error,
	dflt *T,
) (T, []string, error) {
	return loadConfigFromFile(configDirPath, configFileName, migrationCb, dflt, false)
}

// ConfigUpgradeMessage returns the announcement text for fields added to a
// config file, e.g. "added new field(s) to textConfig.json: stoploss".
func ConfigUpgradeMessage(configFileName string, added []string) string {
	return fmt.Sprintf("added new field(s) to %v: %v", configFileName, strings.Join(added, ", "))
}

func loadConfigFromFile[T any](
	configDirPath,
	configFileName string,
	migrationCb func(string) error,
	dflt *T,
	announce bool,
) (T, []string, error) {
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("attempting to load file: %v%v\n", configDirPath, configFileName))
	}

	configPath := path.Join(configDirPath, configFileName)

	// NoCreateConfig makes the whole load read-only: no config dir, default
	// file, migration, or upgrade rewrite may be produced as a side effect.
	// A missing file yields the in-memory defaults instead of an error, so
	// read-only commands work on a read-only filesystem or against a config
	// dir that does not exist yet.
	if NoCreateConfig {
		fileBytes, err := os.ReadFile(configPath)
		if err != nil {
			if os.IsNotExist(err) {
				conf, cloneErr := cloneConfigDefault(dflt)
				if cloneErr != nil {
					var nilVal T
					return nilVal, nil, fmt.Errorf("clone default config '%v': %w", configFileName, cloneErr)
				}
				return conf, nil, nil
			}
			var nilVal T
			return nilVal, nil, fmt.Errorf("failed to read config '%v', error: %v", configFileName, err)
		}

		var conf T
		if err := json.Unmarshal(fileBytes, &conf); err != nil {
			return conf, nil, fmt.Errorf("failed to unmarshal config '%v', error: %v", configFileName, err)
		}

		var present map[string]json.RawMessage
		if err := json.Unmarshal(fileBytes, &present); err != nil {
			return conf, nil, fmt.Errorf("failed to parse config '%v' keys, error: %v", configFileName, err)
		}
		_ = fillMissingFromDefaults(&conf, dflt, present, "")

		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("found config (read-only): %v\n", debug.IndentedJsonFmt(conf)))
		}
		return conf, nil, nil
	}

	err := CreateConfigDir(configDirPath)
	if err != nil {
		var nilVal T
		return nilVal, nil, err
	}

	_, statErr := os.Stat(configPath)
	existedBefore := statErr == nil

	err = createDefaultConfigFile(configDirPath, configFileName, dflt)
	if err != nil {
		var nilVal T
		return nilVal, nil, err
	}

	err = runMigrationCallback(migrationCb, configDirPath)
	if err != nil {
		var nilVal T
		return nilVal, nil, err
	}

	fileBytes, err := os.ReadFile(configPath)
	if err != nil {
		var nilVal T
		return nilVal, nil, fmt.Errorf("failed to read config '%v', error: %v", configFileName, err)
	}

	var conf T
	err = json.Unmarshal(fileBytes, &conf)
	if err != nil {
		return conf, nil, fmt.Errorf("failed to unmarshal config '%v', error: %v", configFileName, err)
	}

	// Presence-based upgrade: fields absent from the on-disk config are filled
	// from the non-zero defaults; fields already present are never touched,
	// even when their value is the zero value. The file is the user's source
	// of truth (config migration design, Q4).
	var present map[string]json.RawMessage
	if err := json.Unmarshal(fileBytes, &present); err != nil {
		return conf, nil, fmt.Errorf("failed to parse config '%v' keys, error: %v", configFileName, err)
	}
	added := fillMissingFromDefaults(&conf, dflt, present, "")

	if ReadonlyConfig {
		// Raw (machine-readable) runs must not mutate the user's configs as a
		// side effect and must not pollute machine output with human
		// announcements: fill missing fields in memory but never rewrite the
		// file and never announce (config migration design, Q5).
		return conf, nil, nil
	}
	if len(added) > 0 {
		err = CreateFile(configPath, &conf)
		if err != nil {
			return conf, nil, fmt.Errorf("failed to write config '%v' post missing-field appendage, error: %w", configFileName, err)
		}
		// Announce upgrades of pre-existing files only; fresh files and files
		// produced by a migration callback carry their own message (Q5). The
		// collect variant returns the same set so callers never announce
		// upgrades the announce path would have kept silent.
		if !existedBefore {
			added = nil
		} else if announce {
			ancli.PrintOK(ConfigUpgradeMessage(configFileName, added) + "\n")
		}
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("found config: %v\n", debug.IndentedJsonFmt(conf)))
	}
	return conf, added, nil
}

// cloneConfigDefault deep-copies a config default via JSON so a read-only load
// of a missing file returns a value that can be mutated by callers without
// aliasing the package-level default.
func cloneConfigDefault[T any](dflt *T) (T, error) {
	var conf T
	if dflt == nil {
		return conf, nil
	}
	b, err := json.Marshal(dflt)
	if err != nil {
		return conf, err
	}
	if err := json.Unmarshal(b, &conf); err != nil {
		return conf, err
	}
	return conf, nil
}

// fillMissingFromDefaults fills fields of loaded that are absent from the
// on-disk config (per present) using defaults from dflt. A key that is
// present in the file is never touched, even when its value is the zero
// value: the file is the user's source of truth (Q4). Nested JSON objects are
// recursed into so missing subfields of a present object are also filled (Q3).
// Fields tagged `migrate:"true"` are filled even when their default is the
// zero value, so new feature knobs whose natural default is 0 (e.g.
// stoploss.max-tool-calls-after-handover, 0 = unlimited) surface in upgraded
// configs; such fields must not carry the `omitempty` json option, or the
// marshaled rewrite would drop the filled zero again. It returns the JSON
// paths of every field it filled, e.g. "stoploss" or
// "stoploss.max-tokens-handover-instructions".
func fillMissingFromDefaults(loaded, dflt any, present map[string]json.RawMessage, prefix string) []string {
	added := []string{}
	lv := reflect.ValueOf(loaded)
	if lv.Kind() == reflect.Pointer {
		lv = lv.Elem()
	}
	dv := reflect.ValueOf(dflt)
	if dv.Kind() == reflect.Pointer {
		dv = dv.Elem()
	}
	lt := lv.Type()
	for i := 0; i < lt.NumField(); i++ {
		sf := lt.Field(i)
		if !sf.IsExported() {
			continue
		}
		key, skip := jsonConfigKey(sf)
		if skip {
			continue
		}
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		raw, isPresent := present[key]
		if isPresent {
			// Recurse into a present object so missing subfields are filled.
			if isJSONObject(raw) && isStructValue(lv.Field(i)) {
				var sub map[string]json.RawMessage
				if json.Unmarshal(raw, &sub) == nil {
					lvf := lv.Field(i)
					dvf := dv.Field(i)
					if lvf.Kind() == reflect.Pointer {
						if lvf.IsNil() {
							lvf.Set(reflect.New(lvf.Type().Elem()))
						}
						lvf = lvf.Elem()
					}
					if dvf.Kind() == reflect.Pointer {
						if dvf.IsNil() {
							continue
						}
						dvf = dvf.Elem()
					}
					added = append(added, fillMissingFromDefaults(lvf.Addr().Interface(), dvf.Addr().Interface(), sub, path)...)
				}
			}
			continue
		}
		dvf := dv.Field(i)
		if dvf.IsZero() && sf.Tag.Get("migrate") != "true" {
			continue
		}
		lv.Field(i).Set(cloneDefaultValue(dvf))
		added = append(added, path)
	}
	return added
}

// jsonConfigKey returns the JSON key of a struct field as encoding/json would
// name it: the json tag name, or the Go field name when there is no tag. skip
// is true for fields tagged json:"-".
func jsonConfigKey(sf reflect.StructField) (key string, skip bool) {
	tag, ok := sf.Tag.Lookup("json")
	if !ok || tag == "" {
		return sf.Name, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "", true
	}
	return parts[0], false
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func isStructValue(v reflect.Value) bool {
	t := v.Type()
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}

// cloneDefaultValue deep-copies a default value so the merged config never
// aliases (and therefore can never mutate) the package-level default it was
// filled from.
func cloneDefaultValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		cp := reflect.New(v.Type().Elem())
		cp.Elem().Set(cloneDefaultValue(v.Elem()))
		return cp
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			cp.Index(i).Set(cloneDefaultValue(v.Index(i)))
		}
		return cp
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			cp.SetMapIndex(iter.Key(), cloneDefaultValue(iter.Value()))
		}
		return cp
	default:
		return v
	}
}

func ReturnNonDefault[T comparable](a, b, defaultVal T) (T, error) {
	if a != defaultVal && b != defaultVal {
		return defaultVal, fmt.Errorf("values are mutually exclusive")
	}
	if a != defaultVal {
		return a, nil
	}
	if b != defaultVal {
		return b, nil
	}
	return defaultVal, nil
}

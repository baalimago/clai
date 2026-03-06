package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// requiredConfigDirs lists directories that must exist under the clai config dir.
// Keep this in sync with any feature that persists state to disk.
var requiredConfigDirs = []string{"conversations", "profiles", "mcpServers", "conversations/dirs", "shellContexts"}

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
{{- if .hostname }}
hostname: {{.hostname}}
{{- end }}
{{- if .shell }}
shell: {{.shell}}
{{- end }}
{{- if .python_venv }}
python env: {{.python_venv}}
{{- end }}
{{- if .k8s_context }}
k8s context: {{.k8s_context}}
{{- end }}
{{- if .go_version }}
go version: {{.go_version}}
{{- end }}
{{- if .git_branch }}
git branch: {{.git_branch}}
{{- end }}
{{- if .git_status_short }}
git dirty: {{.git_status_short}}
{{- end }}
{{- if .docker_context }}
docker context: {{.docker_context}}
{{- end }}
{{- if .tmux_session }}
tmux session: {{.tmux_session}}
{{- end }}
{{- if .ssh_connection }}
ssh: {{.ssh_connection}}
{{- end }}
`,
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

func LoadConfigFromFile[T any](
	configDirPath,
	configFileName string,
	migrationCb func(string) error,
	dflt *T,
) (T, error) {
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("attempting to load file: %v%v\n", configDirPath, configFileName))
	}

	err := CreateConfigDir(configDirPath)
	if err != nil {
		var nilVal T
		return nilVal, err
	}

	err = createDefaultConfigFile(configDirPath, configFileName, dflt)
	if err != nil {
		var nilVal T
		return nilVal, err
	}

	err = runMigrationCallback(migrationCb, configDirPath)
	if err != nil {
		var nilVal T
		return nilVal, err
	}

	configPath := path.Join(configDirPath, configFileName)
	var conf T
	err = ReadAndUnmarshal(configPath, &conf)
	if err != nil {
		return conf, fmt.Errorf("failed to unmarshal config '%v', error: %v", configFileName, err)
	}

	// Append any new fields from defauly config, in case of config extension
	hasChanged := setNonZeroValueFields(&conf, dflt)

	if len(hasChanged) > 0 {
		err = CreateFile(configPath, &conf)
		if err != nil {
			return conf, fmt.Errorf("failed to write config '%v' post zero-field appendage, error: %v", configFileName, err)
		}
		ancli.PrintOK(fmt.Sprintf("appended new fields: '%s', to textConfig and updated config file: '%v'\n", hasChanged, configPath))
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("found config: %v\n", debug.IndentedJsonFmt(conf)))
	}
	return conf, nil
}

// setNonZeroValueFields on a using b as template
func setNonZeroValueFields[T any](a, b *T) []string {
	hasChanged := []string{}
	t := reflect.TypeOf(*a)
	for f := range t.Fields() {
		aVal := reflect.ValueOf(a).Elem().FieldByName(f.Name)
		bVal := reflect.ValueOf(b).Elem().FieldByName(f.Name)
		if f.IsExported() && aVal.IsZero() && !bVal.IsZero() {
			hasChanged = append(hasChanged, f.Tag.Get("json"))
			aVal.Set(bVal)
		}
	}
	return hasChanged
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

package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// Theme holds ANSI color configuration for terminal output.
// Values are raw ANSI escape sequences (e.g. "\u001b[38;2;120;140;160m").
//
// This file is loaded from <clai-config-dir>/theme.json on startup.
// If NO_COLOR is set truthy, all colorization should be disabled.
//
// Keep this config stable; new fields should be appended with defaults.
// Users can customize their own theme.json.
type Theme struct {
	Primary   string `json:"primary"`
	Secondary string `json:"secondary"`
	Breadtext string `json:"breadtext"`

	RoleSystem    string `json:"roleSystem"`
	RoleUser      string `json:"roleUser"`
	RoleTool      string `json:"roleTool"`
	RoleReasoning string `json:"roleReasoning"`
	RoleOther     string `json:"roleOther"`

	NotificationBell bool `json:"notificationBell"`

	TableItems int `json:"tableItems"`
}

func defaultTheme() *Theme {
	// Muted gray-blue palette.
	return &Theme{
		Primary:   "\u001b[38;2;110;130;150m",
		Secondary: "\u001b[38;2;140;165;190m",
		Breadtext: "\u001b[38;2;200;210;220m",

		// Match AttemptPrettyPrint defaults (BLUE/CYAN/MAGENTA).
		RoleSystem:       "\u001b[34m",
		RoleUser:         "\u001b[36m",
		RoleTool:         "\u001b[35m",
		RoleReasoning:    "\u001b[38;2;180;170;150m",
		RoleOther:        "\u001b[34m",
		NotificationBell: true,

		TableItems: 10,
	}
}

var globalTheme = *defaultTheme()

// LoadTheme loads (and possibly creates) the theme.json file within the config dir.
// It is safe to call multiple times. After loading, stores the theme for consumer access.
func LoadTheme(configDirPath string) error {
	conf, err := LoadConfigFromFile(configDirPath, "theme.json", migrateThemeConfig, defaultTheme())
	if err != nil {
		return fmt.Errorf("load theme config: %w", err)
	}
	globalTheme = conf
	return nil
}

// TableTheme returns the table.Theme derived from the current global theme.
func TableTheme() table.Theme {
	return table.Theme{
		Primary:   globalTheme.Primary,
		Secondary: globalTheme.Secondary,
		Breadtext: globalTheme.Breadtext,
		Items:     globalTheme.TableItems,
	}
}

func migrateThemeConfig(configDirPath string) error {
	themePath := ThemeConfigPath(configDirPath)
	hasNotificationBell := hasJSONKey(themePath, "notificationBell")
	if hasNotificationBell {
		return nil
	}

	type themeMigration struct {
		Primary          string `json:"primary"`
		Secondary        string `json:"secondary"`
		Breadtext        string `json:"breadtext"`
		RoleSystem       string `json:"roleSystem"`
		RoleUser         string `json:"roleUser"`
		RoleTool         string `json:"roleTool"`
		RoleOther        string `json:"roleOther"`
		NotificationBell bool   `json:"notificationBell"`
		TableItems       int    `json:"tableItems"`
	}

	var conf themeMigration
	err := ReadAndUnmarshal(themePath, &conf)
	if err != nil {
		return fmt.Errorf("read theme config for migration: %w", err)
	}

	conf.NotificationBell = true

	err = WriteFile(themePath, &conf)
	if err != nil {
		return fmt.Errorf("write theme config migration: %w", err)
	}
	return nil
}

// ThemeConfigPath returns the fully qualified theme.json path.
func ThemeConfigPath(configDirPath string) string {
	return filepath.Join(configDirPath, "theme.json")
}

// RoleColor returns the theme color for a chat role.
func RoleColor(role string) string {
	switch role {
	case "tool":
		return globalTheme.RoleTool
	case "user":
		return globalTheme.RoleUser
	case "system":
		return globalTheme.RoleSystem
	case "reasoning":
		return globalTheme.RoleReasoning
	default:
		return globalTheme.RoleOther
	}
}

func NotificationBellEnabled() bool { return globalTheme.NotificationBell }

func hasJSONKey(path, key string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var raw map[string]json.RawMessage
	err = json.Unmarshal(content, &raw)
	if err != nil {
		return false
	}
	_, exists := raw[key]
	return exists
}

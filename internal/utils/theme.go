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

	ToolOutputRows int `json:"toolOutputRows"`

	RollingOutput RollingOutputConfig `json:"rollingOutput"`
}

// RollingOutputConfig configures the shared rolling activity viewport that
// contains streamed reasoning and tool blocks.
type RollingOutputConfig struct {
	Enabled bool `json:"enabled"`

	WindowCellHeight int `json:"windowCellHeight"`
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

		ToolOutputRows: 6,

		RollingOutput: RollingOutputConfig{
			Enabled:          true,
			WindowCellHeight: 30,
		},
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
	content, err := os.ReadFile(themePath)
	if err != nil {
		return fmt.Errorf("read theme config for migration: %w", err)
	}
	var conf map[string]json.RawMessage
	if err := json.Unmarshal(content, &conf); err != nil {
		return fmt.Errorf("read theme config for migration: %w", err)
	}
	changed := false

	// Rename the kebab-case rolling-output block to the canonical camelCase
	// rollingOutput key. The camelCase spelling wins when both are present.
	if raw, ok := conf["rolling-output"]; ok {
		if _, exists := conf["rollingOutput"]; !exists {
			conf["rollingOutput"] = raw
		}
		delete(conf, "rolling-output")
		changed = true
	}

	// The obsolete flat rollingOutput boolean (pre-block config shape) would
	// collide with the nested block key; drop it so it keeps being ignored.
	if raw, ok := conf["rollingOutput"]; ok && !isJSONObject(raw) {
		delete(conf, "rollingOutput")
		changed = true
	}

	// Rename window-cell-height to windowCellHeight inside the block.
	if raw, ok := conf["rollingOutput"]; ok {
		var sub map[string]json.RawMessage
		if json.Unmarshal(raw, &sub) == nil {
			subChanged := false
			if height, ok := sub["window-cell-height"]; ok {
				if _, exists := sub["windowCellHeight"]; !exists {
					sub["windowCellHeight"] = height
				}
				delete(sub, "window-cell-height")
				subChanged = true
			}
			if subChanged {
				updated, err := json.Marshal(sub)
				if err != nil {
					return fmt.Errorf("marshal rolling output config migration: %w", err)
				}
				conf["rollingOutput"] = updated
				changed = true
			}
		}
	}

	// Add the field without decoding into a fixed legacy struct. A fixed struct
	// drops newer and unknown fields when it rewrites the file, including the
	// obsolete flat rolling keys from earlier config shapes.
	if _, ok := conf["notificationBell"]; !ok {
		conf["notificationBell"] = json.RawMessage("true")
		changed = true
	}

	if changed {
		err = WriteFile(themePath, &conf)
		if err != nil {
			return fmt.Errorf("write theme config migration: %w", err)
		}
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

// ToolOutputRows returns the maximum terminal rows used for non-raw tool output.
func ToolOutputRows() int { return globalTheme.ToolOutputRows }

// RollingOutputEnabled reports whether reasoning and tool activity share a
// rolling terminal viewport.
func RollingOutputEnabled() bool { return globalTheme.RollingOutput.Enabled }

// RollingOutputWindowCellHeight returns the maximum terminal cells (rows) for
// the shared rolling activity viewport.
func RollingOutputWindowCellHeight() int { return globalTheme.RollingOutput.WindowCellHeight }

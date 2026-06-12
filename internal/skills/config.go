package skills

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type rawConfig struct {
	Enabled            *bool    `json:"enabled,omitempty"`
	GlobalSkillDirs    []string `json:"globalSkillDirs"`
	ProjectSkillDirs   []string `json:"projectSkillDirs"`
	TrustAllSkills     bool     `json:"trust_all_skills"`
	MaxActivatedSkills int      `json:"maxActivatedSkills"`
}

func LoadConfig(configDir string) (Config, error) {
	path := filepath.Join(configDir, configFileName)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := writeJSONFile(path, defaultConfig); err != nil {
			return Config{}, err
		}
		return defaultConfig, nil
	}
	var raw rawConfig
	if err := readJSON(path, &raw); err != nil {
		return Config{}, err
	}
	cfg := Config{
		GlobalSkillDirs:    raw.GlobalSkillDirs,
		ProjectSkillDirs:   raw.ProjectSkillDirs,
		TrustAllSkills:     raw.TrustAllSkills,
		MaxActivatedSkills: raw.MaxActivatedSkills,
	}
	if raw.Enabled != nil {
		cfg.Enabled = *raw.Enabled
	}
	cfg = withConfigDefaults(cfg)
	if err := writeJSONFile(path, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func withConfigDefaults(cfg Config) Config {
	if cfg.ProjectSkillDirs == nil {
		cfg.ProjectSkillDirs = append([]string{}, defaultConfig.ProjectSkillDirs...)
	}
	if cfg.GlobalSkillDirs == nil {
		cfg.GlobalSkillDirs = []string{}
	}
	if cfg.MaxActivatedSkills == 0 {
		cfg.MaxActivatedSkills = defaultConfig.MaxActivatedSkills
	}
	return cfg
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func readJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

package text

import (
	"fmt"
	"os"
	"path"

	"github.com/baalimago/clai/internal/utils"
)

func findProfile(profileName string) (Profile, error) {
	cfg, _ := os.UserConfigDir()
	profilePath := path.Join(cfg, ".clai", "profiles")
	var p Profile
	err := utils.ReadAndUnmarshal(path.Join(profilePath, fmt.Sprintf("%v.json", profileName)), &p)
	if err != nil {
		return p, err
	}
	return p, nil
}

func (c *Configurations) ProfileOverrides() error {
	if c.UseProfile == "" {
		return nil
	}
	profile, err := findProfile(c.UseProfile)
	if err != nil {
		return fmt.Errorf("failed to find profile: %w", err)
	}
	c.Model = profile.Model
	c.SystemPrompt = profile.Prompt
	c.UseTools = profile.UseTools
	c.Tools = profile.Tools
	c.SaveReplyAsConv = profile.SaveReplyAsConv
	return nil
}

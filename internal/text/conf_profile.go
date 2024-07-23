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
	newPrompt := profile.Prompt
	if c.CmdMode {
		// SystmePrompt here is CmdPrompt, keep it and remoind llm to only suggest  cmd
		newPrompt = fmt.Sprintf("You will get this pattern: || <cmd-prompt> | <custom guided profile> ||. It is VERY vital that you DO NOT disobey the <cmd-prompt> with whatever is posted in <custom guided profile. || %v| %v ||", c.CmdModePrompt, profile.Prompt)
	}
	c.SystemPrompt = newPrompt
	c.UseTools = profile.UseTools && !c.CmdMode
	c.Tools = profile.Tools
	c.SaveReplyAsConv = profile.SaveReplyAsConv
	return nil
}

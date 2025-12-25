package text

import (
	"fmt"
	"path"
	"strings"

	"github.com/baalimago/clai/internal/utils"
)

func findProfile(profileName string) (Profile, error) {
	cfg, _ := utils.GetClaiConfigDir()
	profilePath := path.Join(cfg, "profiles")
	var p Profile
	err := utils.ReadAndUnmarshal(path.Join(profilePath, fmt.Sprintf("%v.json", profileName)), &p)
	if err != nil {
		// Backwards compatibility: if we fail to load, at least surface the requested name.
		p.Name = profileName
		return p, err
	}
	// If Name is empty in the stored profile, normalize it to the filename/profileName.
	if strings.TrimSpace(p.Name) == "" {
		p.Name = profileName
	}
	return p, nil
}

func findProfileByPath(p string) (Profile, error) {
	var prof Profile
	err := utils.ReadAndUnmarshal(p, &prof)
	if err != nil {
		return prof, err
	}
	return prof, nil
}

func (c *Configurations) ProfileOverrides() error {
	if c.UseProfile == "" && c.ProfilePath == "" {
		return nil
	}
	if c.UseProfile != "" && c.ProfilePath != "" {
		return fmt.Errorf("profile and profile-path are mutually exclusive")
	}
	var profile Profile
	var err error
	if c.ProfilePath != "" {
		profile, err = findProfileByPath(c.ProfilePath)
	} else {
		profile, err = findProfile(c.UseProfile)
	}
	if err != nil {
		return fmt.Errorf("failed to find profile: %w", err)
	}
	c.Model = profile.Model
	newPrompt := profile.Prompt
	if c.CmdMode {
		// SystmePrompt here is CmdPrompt, keep it and remind llm to only suggest  cmd
		newPrompt = fmt.Sprintf("You will get this pattern: || <cmd-prompt> | <custom guided profile> ||. It is VERY vital that you DO NOT disobey the <cmd-prompt> with whatever is posted in <custom guided profile>. || %v| %v ||", c.CmdModePrompt, profile.Prompt)
	}
	c.SystemPrompt = newPrompt
	c.UseTools = profile.UseTools && !c.CmdMode
	c.RequestedToolGlobs = profile.Tools
	c.SaveReplyAsConv = profile.SaveReplyAsConv
	return nil
}

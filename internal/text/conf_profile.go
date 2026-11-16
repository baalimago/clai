package text

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/pkg/text/models"
)

func findProfile(profileName string) (Profile, error) {
	cfg, err := utils.GetClaiConfigDir()
	if err != nil {
		return Profile{}, err
	}
	profilePath := path.Join(cfg, "profiles", fmt.Sprintf("%v.json", profileName))
	p, err := loadProfile(profilePath)
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
	return loadProfile(p)
}

type profileSelection struct {
	profile  Profile
	selector string
}

func selectProfile(name string) (profileSelection, error) {
	p, err := findProfile(name)
	if err == nil {
		return profileSelection{p, name}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return profileSelection{}, err
	}
	cfg, cfgErr := utils.GetClaiConfigDir()
	if cfgErr != nil {
		return profileSelection{}, cfgErr
	}
	dir := filepath.Join(cfg, "profiles")
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		return profileSelection{}, diag.Errorf("list profiles for %q: %w", name, readErr)
	}
	var selected profileSelection
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		candidate, loadErr := loadProfile(filepath.Join(dir, entry.Name()))
		if loadErr != nil {
			return profileSelection{}, diag.Errorf("resolve legacy profile %q: %w", name, loadErr)
		}
		if candidate.Name != name {
			continue
		}
		if selected.selector != "" {
			return profileSelection{}, fmt.Errorf("ambiguous profile display name %q: matches %q and %q; select a profile with -p or -prp", name, selected.selector, strings.TrimSuffix(entry.Name(), ".json"))
		}
		selected = profileSelection{candidate, strings.TrimSuffix(entry.Name(), ".json")}
	}
	if selected.selector == "" {
		return profileSelection{}, err
	}
	return selected, nil
}

func loadProfile(profilePath string) (Profile, error) {
	p, err := utils.ReadConfigFile(profilePath, &DefaultProfile)
	if err != nil {
		return p, err
	}
	p.Prompt = utils.RehydrateEscapedConfigString(p.Prompt)
	return p, nil
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
	profileDir := ""
	selector := c.UseProfile
	if c.ProfilePath != "" {
		profile, err = findProfileByPath(c.ProfilePath)
		profileDir = filepath.Dir(c.ProfilePath)
		if err == nil {
			selector, err = filepath.Abs(c.ProfilePath)
		}
	} else {
		var selected profileSelection
		selected, err = selectProfile(c.UseProfile)
		profile, selector = selected.profile, selected.selector
		if err == nil {
			cfg, cfgErr := utils.GetClaiConfigDir()
			if cfgErr != nil {
				return cfgErr
			}
			profileDir = filepath.Join(cfg, "profiles")
		}
	}
	if err != nil {
		return diag.Errorf("failed to find profile: %w", err)
	}

	c.UseProfile = selector
	if target, _, _ := strings.Cut(c.ProfileTarget, "#"); target != "" {
		c.UseProfile = target
	}

	c.Model = profile.Model
	if profile.Audio != nil {
		c.AudioModel = profile.Audio.Model
	}
	c.SystemPrompt = profile.Prompt
	c.UseTools = profile.UseTools || (len(profile.McpServers) > 0)
	// A fetched or installed profile gets only the tools it names: with
	// use_tools alone, a prompt-injected model would reach every local tool
	// and MCP server.
	fetched := c.ProfileFetched || profile.Source != nil
	if fetched && len(profile.Tools) == 0 && len(profile.McpServers) == 0 {
		c.UseTools = false
	}
	c.ProfileSkills = profile.Skills
	switch {
	case profile.UseSkills != nil:
		c.UseSkills = *profile.UseSkills
		c.ProfileUseSkillsSet = true
	case len(profile.Skills) > 0:
		c.UseSkills = true
		c.ProfileUseSkillsSet = true
	}
	if profile.UseLookback != nil {
		// Profile precedence (CLI > profile > file-default) is preserved by writing
		// directly into UseLookback here; setupLookback reads it as the base value.
		c.UseLookback = *profile.UseLookback
	}
	c.RequestedToolGlobs = profile.Tools
	// Purely additive cascade (R1-04 revision, 2026-08-02): the profile's
	// cmd-ban merges onto the file base; an omitted or explicitly empty list
	// contributes nothing. No source removes another source's bans.
	if profile.CmdBan != nil {
		c.CmdBan = append(c.CmdBan, profile.CmdBan...)
	}
	// A fetched or installed profile cannot keep its runs out of the chat
	// history; only your own config decides that for it.
	if profile.SaveReplyAsConv != nil && !fetched {
		c.SaveReplyAsConv = *profile.SaveReplyAsConv
	}
	// A shell context runs commands from this machine's own definitions and
	// puts their output in the prompt; which ones, and when, is the user's
	// choice, never a fetched or installed profile's.
	if strings.TrimSpace(profile.ShellContext) != "" && !fetched {
		c.ShellContext = profile.ShellContext
	}
	mcpServers := make([]models.McpServer, 0)
	for name, m := range profile.McpServers {
		m.Name = name
		if err := serverconfig.ValidateRequiredEnv(name, m); err != nil {
			return err
		}
		if m.EnvFile != "" {
			expanded, expandErr := utils.ExpandUserPath(m.EnvFile)
			if expandErr != nil {
				return diag.Errorf("failed to expand envfile path %q: %w", m.EnvFile, expandErr)
			}
			if profileDir != "" && !filepath.IsAbs(expanded) {
				expanded = filepath.Join(profileDir, expanded)
			}
			m.EnvFile = expanded
		}
		c.RequestedToolGlobs = append(c.RequestedToolGlobs, fmt.Sprintf("mcp_%v*", name))
		mcpServers = append(mcpServers, m)
	}
	c.McpServers = mcpServers
	return nil
}

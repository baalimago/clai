package text

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/pkg/text/models"
)

func findProfile(profileName string) (Profile, error) {
	cfg, _ := utils.GetClaiConfigDir()
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
	if c.ProfilePath != "" {
		profile, err = findProfileByPath(c.ProfilePath)
		profileDir = filepath.Dir(c.ProfilePath)
	} else {
		profile, err = findProfile(c.UseProfile)
		cfg, _ := utils.GetClaiConfigDir()
		profileDir = filepath.Join(cfg, "profiles")
	}
	if err != nil {
		return fmt.Errorf("failed to find profile: %w", err)
	}

	// Ensure the selected profile is carried forward into persisted conversations.
	// The underlying query flow reads `c.UseProfile` when stamping chats.
	if strings.TrimSpace(profile.Name) != "" {
		c.UseProfile = profile.Name
	}

	c.Model = profile.Model
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
	if strings.TrimSpace(profile.ShellContext) != "" {
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
				return fmt.Errorf("failed to expand envfile path %q: %w", m.EnvFile, expandErr)
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

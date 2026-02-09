package text

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
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
	c.RequestedToolGlobs = profile.Tools
	c.SaveReplyAsConv = profile.SaveReplyAsConv
	mcpServers := make([]models.McpServer, 0)
	for name, m := range profile.McpServers {
		m.Name = name
		if m.EnvFile != "" && profileDir != "" && !filepath.IsAbs(m.EnvFile) {
			m.EnvFile = filepath.Join(profileDir, m.EnvFile)
		}
		c.RequestedToolGlobs = append(c.RequestedToolGlobs, fmt.Sprintf("mcp_%v*", name))
		mcpServers = append(mcpServers, m)
		if misc.Truthy(os.Getenv("DEBUG_PROFILES")) {
			ancli.Noticef("adding: %v", debug.IndentedJsonFmt(m))
		}
	}
	c.McpServers = mcpServers
	return nil
}

package setup

import (
	"os"
	"path/filepath"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/video"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// ConfigRunPrep runs the shared pre-command config work for config-touching
// commands (query/chat/photo/video/audio/setup): config-dir resolution,
// theme loading, and the united config migration. Commands that render
// content without touching the mode configs (replay, dir-replay, the
// read-only chat subcommands) call internal.PrepTheme directly instead;
// completion, __complete, confdir and version stay fully side-effect-free.
// main.go injects it into the commands that need it; the setup command
// calls it directly.
func ConfigRunPrep(deferAnnouncements bool) (string, []string, error) {
	claiConfDir, err := internal.PrepTheme()
	if err != nil {
		return "", nil, err
	}

	announcements := migrateConfigs(claiConfDir, deferAnnouncements)
	return claiConfDir, announcements, nil
}

// LoadPhotoConfig loads the photo configuration with the old-config
// migration applied; injected into the photo command by main.go.
func LoadPhotoConfig(confDir string) (photo.Configurations, error) {
	return utils.LoadConfigFromFile(confDir, "photoConfig.json", migrateOldPhotoConfig, &photo.DEFAULT)
}

// migrateConfigs runs the united config migration: every mode config and
// profile is upgraded before the command runs, so each command sees the
// current schema (config migration design, Q5). Broken configs downgrade to
// warnings, same policy as LoadTheme. With deferAnnouncements (the setup
// wizard), upgrade announcements are collected and returned instead of
// printed, so the wizard can print them before its first TUI frame.
func migrateConfigs(claiConfDir string, deferAnnouncements bool) []string {
	var deferred []string
	announceUpgrade := func(configFileName string, added []string) {
		if len(added) == 0 {
			return
		}
		msg := utils.ConfigUpgradeMessage(configFileName, added)
		if deferAnnouncements {
			deferred = append(deferred, msg)
			return
		}
		ancli.PrintOK(msg + "\n")
	}
	if _, added, err := utils.LoadConfigFromFileCollect(claiConfDir, "textConfig.json", text.MigrateOldChatConfig, &text.Default); err != nil {
		ancli.Warnf("failed to upgrade textConfig.json: %v\n", err)
	} else {
		announceUpgrade("textConfig.json", added)
	}
	if _, added, err := utils.LoadConfigFromFileCollect(claiConfDir, "photoConfig.json", migrateOldPhotoConfig, &photo.DEFAULT); err != nil {
		ancli.Warnf("failed to upgrade photoConfig.json: %v\n", err)
	} else {
		announceUpgrade("photoConfig.json", added)
	}
	if _, added, err := utils.LoadConfigFromFileCollect(claiConfDir, "videoConfig.json", nil, &video.Default); err != nil {
		ancli.Warnf("failed to upgrade videoConfig.json: %v\n", err)
	} else {
		announceUpgrade("videoConfig.json", added)
	}
	if _, added, err := utils.LoadConfigFromFileCollect(claiConfDir, "audioConfig.json", nil, &audio.Default); err != nil {
		ancli.Warnf("failed to upgrade audioConfig.json: %v\n", err)
	} else {
		announceUpgrade("audioConfig.json", added)
	}

	// Profiles are upgraded the same way: every profiles/*.json is migrated
	// against DefaultProfile, so all profiles carry the current schema even
	// when never selected via -p/-profile-path. Broken profiles downgrade to
	// warnings (same policy as the mode configs above); a missing profiles
	// dir is not an error — it means no profiles exist yet.
	profilesDir := filepath.Join(claiConfDir, "profiles")
	profileFiles, err := os.ReadDir(profilesDir)
	if err != nil {
		if !os.IsNotExist(err) {
			ancli.Warnf("failed to list profiles dir %q: %v\n", profilesDir, err)
		}
		return deferred
	}
	for _, f := range profileFiles {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		if _, added, err := utils.LoadConfigFromFileCollect(profilesDir, f.Name(), nil, &text.DefaultProfile); err != nil {
			ancli.Warnf("failed to upgrade profile %v: %v\n", f.Name(), err)
		} else {
			announceUpgrade(f.Name(), added)
		}
	}
	return deferred
}

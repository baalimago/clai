package utils

import (
	"fmt"
	"os"
	"path"
)

// GetClaiConfigDir returns the path to the clai configuration directory.
// The directory is located inside the user's configuration directory
// as <UserConfigDir>/.clai.
func GetClaiConfigDir() (string, error) {
	// Respect XDG override to allow tests to isolate config paths.
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		return path.Join(xdgConfigHome, ".clai"), nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config directory: %w", err)
	}
	return path.Join(cfg, ".clai"), nil
}

// GetClaiCacheDir returns the path to the clai cache directory.
// The directory is located inside the user's cache directory
// as <UserCacheDir>/clai.
func GetClaiCacheDir() (string, error) {
	// Respect XDG override to allow tests to isolate cache paths.
	if xdgCacheHome := os.Getenv("XDG_CACHE_HOME"); xdgCacheHome != "" {
		return path.Join(xdgCacheHome, "clai"), nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user cache directory: %w", err)
	}
	return path.Join(cacheDir, "clai"), nil
}

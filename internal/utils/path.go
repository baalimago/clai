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
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user cache directory: %w", err)
	}
	return path.Join(cacheDir, "clai"), nil
}

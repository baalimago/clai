package utils

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// GetClaiConfigDir returns the path to the clai configuration directory.
// The directory is located inside the user's configuration directory
// as <UserConfigDir>/.clai, unless overridden by CLAI_CONFIG_DIR.
func GetClaiConfigDir() (string, error) {
	if claiConfigHome := os.Getenv("CLAI_CONFIG_DIR"); claiConfigHome != "" {
		return claiConfigHome, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config directory: %w", err)
	}
	return path.Join(cfg, ".clai"), nil
}

// ExpandUserPath expands environment variables ($VAR or ${VAR}) and a
// leading tilde in p. Unset variables expand to the empty string, matching
// shell semantics. Paths like ~user are returned unchanged.
func ExpandUserPath(p string) (string, error) {
	p = os.ExpandEnv(p)
	if p == "" || p[0] != '~' {
		return p, nil
	}
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

// GetClaiCacheDir returns the path to the clai cache directory.
// The directory is located inside the user's cache directory
// as <UserCacheDir>/clai, unless overridden by CLAI_CACHE_DIR.
func GetClaiCacheDir() (string, error) {
	if claiCacheHome := os.Getenv("CLAI_CACHE_DIR"); claiCacheHome != "" {
		return claiCacheHome, nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user cache directory: %w", err)
	}
	return path.Join(cacheDir, "clai"), nil
}

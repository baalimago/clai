package main

import (
	"fmt"
	"os"
	"path"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func moveConfFromHomeToConfig() error {
	// check if directory '.clai' exists in homeDir
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home dir: %w", err)
	}
	oldClaiDir := path.Join(homeDir, ".clai")
	if _, err := os.Stat(oldClaiDir); !os.IsNotExist(err) {
		confDir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("failed to get conf dir: %w", err)
		}
		ancli.PrintWarn(fmt.Sprintf("oopsie detected: attempting to move config from: %v, to %v, to better adhere to standards\n", oldClaiDir, confDir))
		newClaiDir := path.Join(confDir, ".clai")
		err = os.Rename(oldClaiDir, newClaiDir)
		if err != nil {
			return fmt.Errorf("failed to rename: %w", err)
		} else {
			ancli.PrintOK(fmt.Sprintf("oopsie resolved: you'll now find your clai configurations in directory: '%v'\n ", newClaiDir))
		}
	}
	return nil
}

// handleOopsies by attemting to migrate and fix previous errors and issues caused by me, the writer of
// the application
func handleOopsies() error {
	err := moveConfFromHomeToConfig()
	if err != nil {
		ancli.PrintErr(fmt.Sprintf("failed to move conf from home to config: %v\n", err))
		ancli.PrintErr("manual intervension is adviced, the configuration has moved from os.UserHomeDir() -> os.UserConfigDir(). Aborting to avoid causing conflicts.\n")
		os.Exit(1)
	}
	return nil
}

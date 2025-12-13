package profiles

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// SubCmd handles `clai profiles` sub-commands.
//
// Usage:
//
//	clai profiles           # list configured profiles
//	clai profiles list
//
// Additional sub-commands can be added later (e.g. show, delete, etc.).
func SubCmd(ctx context.Context, args []string) error {
	_ = ctx // currently unused; kept for future expansion

	// We expect args[0] to be "profiles".
	fs := flag.NewFlagSet("profiles", flag.ContinueOnError)
	fs.SetOutput(nil) // silence default usage output; we handle errors ourselves

	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("failed to parse profiles flags: %w", err)
	}

	rest := fs.Args()
	if len(rest) == 0 || rest[0] == "list" {
		return runProfilesList()
	}

	return fmt.Errorf("unknown profiles subcommand: %q", rest[0])
}

// runProfilesList lists all static profiles from <XDG_CONFIG_HOME>/.clai/profiles.
func runProfilesList() error {
	configDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get clai config dir: %w", err)
	}

	profilesDir := filepath.Join(configDir, "profiles")
	if _, err := os.Stat(profilesDir); os.IsNotExist(err) {
		ancli.Warnf("no profiles directory found at %s\n", profilesDir)
		return utils.ErrUserInitiatedExit
	}

	files, err := os.ReadDir(profilesDir)
	if err != nil {
		return fmt.Errorf("failed to read profiles directory: %w", err)
	}

	if len(files) == 0 {
		ancli.Warnf("no profiles found in %s\n", profilesDir)
		return utils.ErrUserInitiatedExit
	}

	// local view of the on-disk profile; we only need a subset of fields here
	type profile struct {
		Name   string   `json:"name"`
		Model  string   `json:"model"`
		Tools  []string `json:"tools"`
		Prompt string   `json:"prompt"`
	}

	validCount := 0
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}

		fullPath := filepath.Join(profilesDir, f.Name())

		var p profile
		if err := utils.ReadAndUnmarshal(fullPath, &p); err != nil {
			// Skip malformed profile files
			continue
		}

		// Backwards compatible: if Name is empty, derive from filename (without .json).
		if strings.TrimSpace(p.Name) == "" {
			base := filepath.Base(f.Name())
			p.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}

		fmt.Printf("Name: %s\nModel: %s\nTools: %v\nFirst sentence prompt: %s\n---\n",
			p.Name,
			p.Model,
			p.Tools,
			getFirstSentence(p.Prompt),
		)
		validCount++
	}

	if validCount == 0 {
		ancli.Warnf("no valid profiles found in %s\n", profilesDir)
	}

	return utils.ErrUserInitiatedExit
}

// getFirstSentence returns the first sentence / line of a prompt, used for summaries.
func getFirstSentence(s string) string {
	if s == "" {
		return "[Empty prompt]"
	}

	idxDot := strings.Index(s, ".")
	idxExcl := strings.Index(s, "!")
	idxQues := strings.Index(s, "?")
	idxNewLine := strings.Index(s, "\n")

	minIdx := len(s)
	for _, idx := range []int{idxDot, idxExcl, idxQues, idxNewLine} {
		if idx != -1 && idx < minIdx {
			minIdx = idx
		}
	}

	if minIdx < len(s) {
		return s[:minIdx+1]
	}
	return s
}

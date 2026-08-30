package profiles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// List prints all static profiles from <UserConfigDir>/.clai/profiles.
func List() error {
	configDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get clai config dir: %w", err)
	}

	profilesDir := filepath.Join(configDir, "profiles")
	if _, err := os.Stat(profilesDir); os.IsNotExist(err) {
		ancli.Warnf("no profiles directory found at %s\n", profilesDir)
		return nil
	}

	files, err := os.ReadDir(profilesDir)
	if err != nil {
		return fmt.Errorf("failed to read profiles directory: %w", err)
	}

	if len(files) == 0 {
		ancli.Warnf("no profiles found in %s\n", profilesDir)
		return nil
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

		fmt.Printf(
			"Name: %s\nModel: %s\nTools: %v\nFirst sentence prompt: %s\n---\n",
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

	return nil
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

// Help explains what profiles are; surfaced in 'clai profiles -h' and
// re-exported by package internal for the e2e suite.
const Help = `Profiles overwrite certain model configurations. The intent of profiles
is to reduce usage for repetitive flags and to persist and tweak specific LLM agents.
For instance, you may create a \'gopher\' profile with a prompt that explains the agent is
a programming helper and then specify which tools it may use.

Use this profile by passing the \'-p/-profile\' flag. Example:

1. clai setup -> 2 -> follow the setup wizard (create \'gopher\' profile)
2. clai -p gopher -g internal/thing/handler.go q write tests for this file`

// Command builds the profiles command tree.
func Command() *internal.Command {
	c := &internal.Command{
		Name: "profiles",
		Desc: "List configured profiles",
		HelpText: "profiles [list]. Lists the profiles under <config-dir>/profiles.\n\n" +
			Help + `

Examples:
  clai profiles
  clai -p gopher q refactor this file`,
	}
	nonInteractive := &internal.NonInteractiveFlag{}
	c.Register = nonInteractive.Register
	c.NonInteractive = nonInteractive
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		if args := c.Args(); len(args) > 1 {
			return fmt.Errorf("unknown profiles subcommand: %q", args[1])
		}
		return List()
	}
	profilesList := &internal.Command{
		Name: "list",
		Desc: "List configured profiles",
		HelpText: `profiles list. Lists the profiles under <config-dir>/profiles.

Examples:
  clai profiles list`,
	}
	profilesList.OnRun = func(_ context.Context, _ *internal.Command) error {
		return List()
	}
	c.Subs = map[string]cmd.Command{"list": profilesList}
	return c
}

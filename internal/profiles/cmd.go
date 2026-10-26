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
		Source *Source  `json:"source"`
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

		// Mirror text.loadProfile: legacy files carry literal "\n" in the
		// prompt (architecture/config.md, "String encoding in edited values").
		p.Prompt = utils.RehydrateEscapedConfigString(p.Prompt)

		// Backwards compatible: if Name is empty, derive from filename (without .json).
		if strings.TrimSpace(p.Name) == "" {
			base := filepath.Base(f.Name())
			p.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}

		source := ""
		if p.Source != nil && p.Source.Target != "" {
			source = fmt.Sprintf("Source: %s\n", p.Source.Target)
		}
		fmt.Printf(
			"Name: %s\nModel: %s\nTools: %v\n%sFirst sentence prompt: %s\n---\n",
			p.Name,
			p.Model,
			p.Tools,
			source,
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

// RemoteHelp explains remote profile targets; surfaced in 'clai profiles -h'.
const RemoteHelp = `-prp also takes a remote target, fetched over https and kept in the cache:
  cchub:<handle>/<agent>[@<version>]   an agent on cchub, the clai hub (CLAI_CLOUD_URL, default https://www.clai.chat)
  gh:<owner>/<repo>[/<path>][@<ref>] agent.json, or <path>, in a GitHub repo
  https://<host>/<path>              any URL that answers application/json
A new target runs only once you trust it: in a terminal clai asks; elsewhere it
exits with status 3. A #sha256=<hex> suffix is consent for exactly those bytes.
CLAI_CLOUD=off makes targets cache-only.`

// Command builds the profiles command tree.
func Command(deps CommandDeps) *internal.Command {
	if deps.Out == nil {
		deps.Out = os.Stdout
	}
	c := &internal.Command{
		Name: "profiles",
		Desc: "List, install, update, trust and publish profiles",
		HelpText: "profiles [list|install|update|trust|publish]. Lists the profiles under <config-dir>/profiles.\n\n" +
			Help + "\n\n" + RemoteHelp + `

Examples:
  clai profiles
  clai -p gopher q refactor this file
  clai -prp cchub:lorentz/trade-bot@3 q 'Do your hourly run.'
  clai profiles install cchub:lorentz/trade-bot@3
  clai profiles publish trade-bot`,
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
	c.Subs = map[string]cmd.Command{
		"list":    profilesList,
		"trust":   remoteSub("trust", "Trust a remote profile target", trustHelp, 1, 1, func(ctx context.Context, a []string) error { return deps.Trust(ctx, a[0]) }),
		"install": remoteSub("install", "Install a remote profile target as a local profile", installHelp, 1, 2, func(ctx context.Context, a []string) error { return deps.Install(ctx, a[0], at(a, 1)) }),
		"publish": publishCommand(deps),
		"update":  remoteSub("update", "Update installed profiles to their newest version", updateHelp, 0, 1, func(ctx context.Context, a []string) error { return deps.Update(ctx, at(a, 0)) }),
	}
	return c
}

const trustHelp = `profiles trust <target>. Fetches a remote target, shows what it runs, and
records its hash in <config-dir>/trust.json (never synced), so -prp runs it
without asking, in cron and scripts too.

Examples:
  clai profiles trust cchub:lorentz/trade-bot@4`

const installHelp = `profiles install <target> [name]. Fetches a remote target and saves it as
<config-dir>/profiles/<name>.json with a source stamp, so clai -p <name> runs it
offline. The name defaults to the agent's, repo's or file's name.

Examples:
  clai profiles install cchub:lorentz/trade-bot@3
  clai profiles install gh:baalimago/agents/review.json@v1 reviewer`

const updateHelp = `profiles update [name]. Fetches the newest version of every installed
profile, or of the one named, and rewrites those that changed. A hub agent
follows its newest version; other targets re-read the same ref or URL.

Examples:
  clai profiles update
  clai profiles update trade-bot`

func at(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func remoteSub(name, desc, help string, minArgs, maxArgs int, run func(context.Context, []string) error) *internal.Command {
	sub := &internal.Command{Name: name, Desc: desc, HelpText: help}
	sub.OnRun = func(ctx context.Context, c *internal.Command) error {
		args := c.Args()[1:]
		if len(args) < minArgs || len(args) > maxArgs {
			return fmt.Errorf("usage: clai %s", strings.SplitN(help, ".", 2)[0])
		}
		return run(ctx, args)
	}
	return sub
}

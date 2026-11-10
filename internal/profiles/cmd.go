package profiles

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/profile"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// List prints all static profiles from <UserConfigDir>/.clai/profiles.
func List(ctx context.Context) error { return list(ctx, CommandDeps{}) }

// list is List with the command's checker environment, so a profile the
// checker refuses is named instead of listed as healthy.
func list(ctx context.Context, deps CommandDeps) error {
	out := deps.withEnv(ctx).Out
	configDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return diag.Errorf("failed to get clai config dir: %w", err)
	}

	profilesDir := filepath.Join(configDir, "profiles")
	if _, err := os.Stat(profilesDir); os.IsNotExist(err) {
		runenv.Warnf(ctx, runenv.SubsystemProfiles, "no profiles directory found at %s", profilesDir)
		return nil
	}

	files, err := os.ReadDir(profilesDir)
	if err != nil {
		return diag.Errorf("failed to read profiles directory: %w", err)
	}

	if len(files) == 0 {
		runenv.Warnf(ctx, runenv.SubsystemProfiles, "no profiles found in %s", profilesDir)
		return nil
	}

	// local view of the on-disk profile; we only need a subset of fields here
	type listedProfile struct {
		Name   string   `json:"name"`
		Model  string   `json:"model"`
		Tools  []string `json:"tools"`
		Prompt string   `json:"prompt"`
		Source *Source  `json:"source"`
		Audio  *struct {
			Model string `json:"model"`
		} `json:"audio"`
		file string
	}

	env := profile.Env{}
	if deps.ProfileEnv != nil {
		if built, err := deps.ProfileEnv(configDir); err == nil {
			env = built
		}
	}

	var healthy []listedProfile
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}

		fullPath := filepath.Join(profilesDir, f.Name())

		body, err := os.ReadFile(fullPath)
		if err != nil {
			runenv.Warnf(ctx, runenv.SubsystemProfiles, "Cannot read %s: %v. Explain it with: clai profiles validate %s", fullPath, err, fullPath)
			continue
		}
		findings, err := profile.Validate(body, profile.File, env)
		if err != nil {
			runenv.Warnf(ctx, runenv.SubsystemProfiles, "Cannot read %s: %v. Explain it with: clai profiles validate %s", fullPath, err, fullPath)
			continue
		}
		if findings.Failed(profile.File) {
			runenv.Warnf(ctx, runenv.SubsystemProfiles, "Cannot read %s: %s. Explain it with: clai profiles validate %s",
				fullPath, strings.Join(findings.Problems(profile.File), "; "), fullPath)
			continue
		}

		var p listedProfile
		if err := json.Unmarshal(body, &p); err != nil {
			// A file clai cannot read is named with the command that
			// explains it: listing only the readable ones hides a profile
			// that will never run.
			runenv.Warnf(ctx, runenv.SubsystemProfiles, "Cannot read %s: %v. Explain it with: clai profiles validate %s", fullPath, err, fullPath)
			continue
		}
		p.file = f.Name()
		healthy = append(healthy, p)
	}

	validCount := 0
	for _, p := range healthy {
		// Mirror text.loadProfile: legacy files carry literal "\n" in the
		// prompt (architecture/config.md, "String encoding in edited values").
		p.Prompt = utils.RehydrateEscapedConfigString(p.Prompt)

		// Backwards compatible: if Name is empty, derive from filename (without .json).
		if strings.TrimSpace(p.Name) == "" {
			base := filepath.Base(p.file)
			p.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}

		source := ""
		if p.Source != nil && p.Source.Target != "" {
			source = fmt.Sprintf("Source: %s\n", p.Source.Target)
		}
		audio := ""
		if p.Audio != nil && p.Audio.Model != "" {
			audio = fmt.Sprintf("Audio model: %s\n", p.Audio.Model)
		}
		fmt.Fprintf(out,
			"Name: %s\nModel: %s\nTools: %v\n%s%sFirst sentence prompt: %s\n---\n",
			p.Name,
			p.Model,
			p.Tools,
			audio,
			source,
			getFirstSentence(p.Prompt),
		)
		validCount++
	}

	if validCount == 0 {
		runenv.Warnf(ctx, runenv.SubsystemProfiles, "no valid profiles found in %s", profilesDir)
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
const Help = `A profile is a saved agent: a model, a prompt, the tools it may use, the MCP
servers it may start and the skills it brings (see 'clai skills -h'), kept in
<config-dir>/profiles/<name>.json. What a profile leaves out comes from your
text config. Create or edit one in the profiles menu of 'clai setup', or write
the JSON yourself.

An "audio": {"model": ...} block sets the model the audio_transcribe tool uses
when the agent's runs call it; -audio-model (-am) still overrides it.

Run one by name with -profile (-p), or by file path or remote target with
-profile-path (-prp):
  clai -profile gopher -glob internal/handler.go -query-summary query write tests for this file
  clai -profile-path ./reviewer.json -query-summary query review the staged diff`

// RemoteHelp explains remote profile targets; surfaced in 'clai profiles -h'.
const RemoteHelp = `Remote targets work with -prp, install and trust:
  cchub:<handle>/<agent>[@<version>]  an agent on the Hub at clai.chat
  gh:<owner>/<repo>[/<path>][@<ref>]  agent.json, or <path>, in a GitHub repo
  https://<host>/<path>               any URL that answers with profile JSON
A github.com/<owner>/<repo>/blob/<ref>/<path> link, as copied from the
browser, reads as gh:<owner>/<repo>/<path>@<ref>.
A target runs only once you trust it: a terminal asks, anything else exits with
status 3. Append #sha256=<hex> to trust exactly those bytes. Fetched targets
are cached; CLAI_CLOUD=off runs from the cache only, and CLAI_CLOUD_URL points
cchub: at another server.

Share your own profile on the Hub with 'clai profiles publish <name>', after
'clai cloud login'.`

// Command builds the profiles command tree.
func Command(deps CommandDeps) *internal.Command {
	c := &internal.Command{
		Name: "profiles",
		Desc: "List, validate, install, update, trust and publish profiles; print the schema",
		HelpText: "profiles [list|validate|schema|install|update|trust|publish]. Lists your profiles,\nor manages them with a subcommand. 'profiles validate <target>' prints what a\nrun or a publish would refuse, and 'profiles schema' prints the JSON Schema\ndocument this clai reads.\n\n" +
			Help + "\n\n" + RemoteHelp + `

Examples:
  clai profiles
  clai profiles validate ./reviewer.json
  clai profiles schema > profile.json
  clai -profile-path cchub:lorentz/trade-bot@3 -query-summary query 'Do your hourly run.'
  clai profiles trust cchub:lorentz/trade-bot@3
  clai profiles install cchub:lorentz/trade-bot@3
  clai profiles update
  clai profiles publish trade-bot -note 'tighter stop-loss'`,
	}
	nonInteractive := &internal.NonInteractiveFlag{}
	c.Register = nonInteractive.Register
	c.NonInteractive = nonInteractive
	c.OnRun = func(ctx context.Context, c *internal.Command) error {
		if args := c.Args(); len(args) > 1 {
			return fmt.Errorf("unknown profiles subcommand: %q", args[1])
		}
		return list(ctx, deps)
	}
	profilesList := &internal.Command{
		Name: "list",
		Desc: "List configured profiles",
		HelpText: `profiles list. Lists the profiles under <config-dir>/profiles.

Examples:
  clai profiles list`,
	}
	profilesList.OnRun = func(ctx context.Context, _ *internal.Command) error {
		return list(ctx, deps)
	}
	c.Subs = map[string]cmd.Command{
		"list":     profilesList,
		"validate": validateCommand(deps),
		"schema":   schemaCommand(deps),
		"trust":    remoteSub("trust", "Trust a remote profile target", trustHelp, 1, 1, func(ctx context.Context, a []string) error { return deps.Trust(ctx, a[0]) }),
		"install":  remoteSub("install", "Install a remote profile target as a local profile", installHelp, 1, 2, func(ctx context.Context, a []string) error { return deps.Install(ctx, a[0], at(a, 1)) }),
		"publish":  publishCommand(deps),
		"update":   remoteSub("update", "Update installed profiles to their newest version", updateHelp, 0, 1, func(ctx context.Context, a []string) error { return deps.Update(ctx, at(a, 0)) }),
	}
	return c
}

const trustHelp = `profiles trust <target>. Fetches a remote target, shows what it runs, and
records its hash in <config-dir>/trust.json (never synced), so -prp runs it
without asking, in cron and scripts too.

Examples:
  clai profiles trust cchub:lorentz/trade-bot@4`

const installHelp = `profiles install <target> [name]. Fetches a remote target and saves it as
<config-dir>/profiles/<name>.json with a source stamp, so clai -profile <name> runs it
offline. The name defaults to the agent's, repo's or file's name.

Examples:
  clai profiles install cchub:lorentz/trade-bot@3
  clai profiles install gh:baalimago/agents/review.json@v1 reviewer`

const updateHelp = `profiles update [name]. Fetches the newest version of every installed
profile, or of the one named, and rewrites those that changed. A Hub agent
follows its newest version; other targets re-read the same ref or URL.
With no name, each version you have not trusted yet shows what it runs and
asks first; naming the profile takes its newest version.

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

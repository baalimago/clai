package profiles

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/skills"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// PublishSkill sends a skill to the Hub: a skill under <config>/skills by
// name, a skill folder, or its SKILL.md. Only SKILL.md is sent.
func (d CommandDeps) PublishSkill(ctx context.Context, skill string, opts PublishOptions) error {
	confDir, err := d.configDir()
	if err != nil {
		return err
	}
	path := skill
	if !strings.ContainsRune(skill, os.PathSeparator) && !strings.HasSuffix(skill, ".md") {
		path = filepath.Join(confDir, "skills", skill)
	}
	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		path = filepath.Join(path, remote.SkillFile)
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("skill %s: no %s at %s", skill, remote.SkillFile, path)
	}
	if err != nil {
		return fmt.Errorf("read skill %s: %w", skill, err)
	}
	meta, err := skills.CheckSkill(body)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	name := opts.Name
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
		if hubName.MatchString(meta.Name) {
			name = meta.Name
		}
	}
	if !hubName.MatchString(name) {
		return fmt.Errorf("%q cannot be a Hub name (lower-case letters, digits and dashes); pass one with -name", name)
	}
	if d.FindSecret != nil {
		if where, found := d.FindSecret(path, body); found {
			return fmt.Errorf("%s holds what looks like a credential (%s): remove it, then publish", path, where)
		}
	}
	return d.send(ctx, confDir, skill, PublishRequest{
		Kind: "skill", Name: name, Body: string(body), Title: opts.Title, Summary: opts.Summary, Note: opts.Note, Handle: opts.Handle,
	}, func(pinned string) string { return `Use it in a profile: "skills": ["` + pinned + `"]` })
}

// SkillsHelp explains skills in profiles; surfaced in 'clai skills -h'.
const SkillsHelp = `A skill is a folder with a SKILL.md: instructions an agent loads when a task
needs them. clai finds your own in <config-dir>/skills, ~/.agents/skills and a
project's .claude/skills or agents/skills, once skills are on (-s '*').

A profile can bring its skills with it. List them under "skills":
  "skills": ["cchub:ada/review@2", "gh:anthropics/skills/skills/pdf@<ref>"]
  cchub:<handle>/<skill>[@<version>]  a skill on the Hub at clai.chat
  gh:<owner>/<repo>[/<path>][@<ref>]  <path>/SKILL.md in a GitHub repo; this is
                                      where skills.sh and SkillsMP list from
  https://<host>/<path>/SKILL.md      any URL that serves the file
They are fetched, trusted and cached with the profile, and turn skills on for
its runs. A profile you publish must pin each one.`

const skillsPublishHelp = `skills publish <skill> [flags]. Publishes a skill, by name under
<config-dir>/skills or by folder or SKILL.md path, to the Hub as your clai.chat
account (clai cloud login first). Publishing it again adds the next version.
It is refused if it holds a credential.

Examples:
  clai skills publish review
  clai skills publish ./skills/triage -name triage -note 'shorter steps'`

// SkillsCommand builds the skills command tree.
func SkillsCommand(deps CommandDeps) *internal.Command {
	if deps.Out == nil {
		deps.Out = os.Stdout
	}
	c := &internal.Command{
		Name: "skills",
		Desc: "Publish skills and use them from profiles",
		HelpText: "skills [publish]. Explains skills; publish puts one on the Hub.\n\n" + SkillsHelp + `

Examples:
  clai skills publish review
  clai -s '*' -qs q 'review the staged diff'`,
	}
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		if args := c.Args(); len(args) > 1 {
			return fmt.Errorf("unknown skills subcommand: %q", args[1])
		}
		fmt.Fprintln(deps.Out, c.Help())
		return nil
	}
	c.Subs = map[string]cmd.Command{
		"publish": newPublishCommand("skill", "Publish a skill to the Hub", skillsPublishHelp, deps.PublishSkill),
	}
	return c
}

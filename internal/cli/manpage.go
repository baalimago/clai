package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/version"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// ManPageHook installs the man page on a first run; see architecture/manpage.md.
type ManPageHook func(cmd.ManInstaller) (cmd.ManInstall, error)

func ensureManPage(i cmd.ManInstaller) (cmd.ManInstall, error) { return i.Ensure() }

const manEnvironment = `CLAI_CONFIG_DIR       the config directory, instead of the user config directory's .clai
CLAI_CACHE_DIR        the cache directory, instead of the user cache directory's clai
CLAI_CLOUD            off serves remote targets from the cache only
CLAI_CLOUD_URL        the clai.chat origin, instead of https://www.clai.chat
CLAI_CLOUD_TOKEN      the clai.chat key, instead of the one 'clai cloud login' saved
CLAI_SUMMARIZER       off turns conversation titles and summaries off
CLAI_AUDIO_STOP_FILE  a path whose creation stops a recording without a terminal
CLAI_MANPAGE          off stops the first run of a release from installing this page`

func manInstaller(cmds map[string]cmd.Command, usage usageText, deps Deps, p process) func() (cmd.ManInstaller, error) {
	return func() (cmd.ManInstaller, error) {
		v, err := deps.version()
		if err != nil {
			return cmd.ManInstaller{}, diag.Errorf("resolve clai's version: %w", err)
		}
		var text strings.Builder
		if err := usage.write(&text, cmds); err != nil {
			return cmd.ManInstaller{}, err
		}
		_, description, _ := strings.Cut(text.String(), "\n\n")
		all := withCompletionCommand(cmds)
		return cmd.ManInstaller{
			Page: cmd.ManPage{
				Name:        "clai",
				Summary:     usage.Summary,
				Synopsis:    usage.Synopsis,
				Version:     v,
				Description: description,
				Sections: []cmd.ManSection{
					{Title: "ENVIRONMENT", Body: manEnvironment},
					{Title: "SEE ALSO", Body: "glow(1), https://github.com/baalimago/clai"},
				},
				HomeDir: p.getenv("HOME"),
			},
			Commands: func() map[string]cmd.Command { return all },
			Env:      cmd.ManEnv{Getenv: p.getenv, Executable: p.executable},
		}, nil
	}
}

func withManCommand(cmds map[string]cmd.Command, installer func() (cmd.ManInstaller, error)) map[string]cmd.Command {
	cmds["man"] = cmd.NewManCommand(cmd.ManCommandConfig{
		Installer: installer,
		Stdout:    func(ctx context.Context) io.Writer { return runenv.From(ctx).Stdout() },
	})
	return cmds
}

func installsManPage(p process, format jsonl.Format, args []string, cmds map[string]cmd.Command) bool {
	if !p.stderrTerminal || format != jsonl.FormatText || p.getenv("CLAI_MANPAGE") == "off" {
		return false
	}
	valueFlags := valueFlagNames(cmds)
	command := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if command == "" && i+1 < len(args) {
				command = args[i+1]
			}
			break
		}
		if slices.Contains(helpFlags, arg) {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			if command == "" {
				command = arg
			}
			continue
		}
		if name := strings.TrimLeft(arg, "-"); command == "" && !strings.Contains(name, "=") && valueFlags[name] {
			i++
		}
	}
	return !slices.Contains([]string{"__complete", "completion", "man", "help"}, command)
}

func firstRunManPage(ctx context.Context, hook ManPageHook, build func() (cmd.ManInstaller, error)) {
	const remedy = "'clai man install' retries it (with sudo for a system directory), CLAI_MANPAGE=off stops trying"
	installer, err := build()
	if err != nil {
		runenv.Warnf(ctx, runenv.SubsystemClai, "could not install the man page: %v; %s", err, remedy)
		return
	}
	if !version.IsRelease(installer.Page.Version) {
		runenv.Debugf(ctx, runenv.SubsystemClai, "the man page is installed only by a release, not %q", installer.Page.Version)
		return
	}
	installed, err := hook(installer)
	if err != nil {
		runenv.Warnf(ctx, runenv.SubsystemClai, "could not install the man page: %v; %s", err, remedy)
		return
	}
	switch installed.State {
	case cmd.ManPageInstalled:
		runenv.Noticef(ctx, runenv.SubsystemClai, "installed the man page for clai %s in %s; %s", installer.Page.Version, installed.Path, manReadHint(installed))
	case cmd.ManPageUpdated:
		runenv.Noticef(ctx, runenv.SubsystemClai, "updated the man page in %s to clai %s; %s", installed.Path, installer.Page.Version, manReadHint(installed))
	}
}

func manReadHint(installed cmd.ManInstall) string {
	if installed.Reach == cmd.ManPageOnManPath {
		return "read it with 'man clai'"
	}
	root := filepath.Dir(filepath.Dir(installed.Path))
	return fmt.Sprintf("read it with 'man %s', or add %s to MANPATH for 'man clai'", installed.Path, root)
}

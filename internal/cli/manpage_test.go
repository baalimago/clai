package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

type manPageCalls struct {
	installers []cmd.ManInstaller
}

func (c *manPageCalls) hook(result cmd.ManInstall, err error) ManPageHook {
	return func(i cmd.ManInstaller) (cmd.ManInstall, error) {
		c.installers = append(c.installers, i)
		return result, err
	}
}

// interactiveRelease is a terminal run of the release v1.14.0 with env as
// its whole environment.
func interactiveRelease(env map[string]string, hook ManPageHook) func(*process, *Deps) {
	return func(p *process, deps *Deps) {
		p.stderrTerminal = true
		env["NO_COLOR"] = "true"
		p.getenv = func(name string) string { return env[name] }
		deps.Version = func() (string, error) { return "v1.14.0", nil }
		deps.ManPage = hook
	}
}

func Test_firstRunManPage_notices(t *testing.T) {
	tests := []struct {
		name   string
		result cmd.ManInstall
		err    error
		want   string
	}{
		{
			name:   "installed where man looks",
			result: cmd.ManInstall{State: cmd.ManPageInstalled, Path: "/p/share/man/man1/clai.1", Reach: cmd.ManPageOnManPath},
			want:   "info [clai] installed the man page for clai v1.14.0 in /p/share/man/man1/clai.1; read it with 'man clai'",
		},
		{
			name:   "updated where man looks",
			result: cmd.ManInstall{State: cmd.ManPageUpdated, Path: "/p/share/man/man1/clai.1", Reach: cmd.ManPageOnManPath},
			want:   "info [clai] updated the man page in /p/share/man/man1/clai.1 to clai v1.14.0; read it with 'man clai'",
		},
		{
			name:   "installed where man does not look",
			result: cmd.ManInstall{State: cmd.ManPageInstalled, Path: "/d/man/man1/clai.1", Reach: cmd.ManPageOffManPath},
			want:   "read it with 'man /d/man/man1/clai.1', or add /d/man to MANPATH for 'man clai'",
		},
		{
			name: "a failed install",
			err:  errors.New("write /p/clai.1: permission denied"),
			want: "warn [clai] could not install the man page: write /p/clai.1: permission denied; 'clai man install' retries it (with sudo for a system directory), CLAI_MANPAGE=off stops trying",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := &manPageCalls{}
			got := runProcess(t, "version", interactiveRelease(map[string]string{}, calls.hook(tc.result, tc.err)))
			if got.status != 0 {
				t.Fatalf("status = %d, want 0 whatever the install did; stderr %q", got.status, got.stderr)
			}
			if len(calls.installers) != 1 {
				t.Fatalf("hook called %d times, want 1", len(calls.installers))
			}
			if page := calls.installers[0].Page; page.Name != "clai" || page.Version != "v1.14.0" {
				t.Fatalf("installer page = %s %s, want clai v1.14.0", page.Name, page.Version)
			}
			if !strings.Contains(got.stderr, tc.want) {
				t.Fatalf("stderr = %q, want it to contain %q", got.stderr, tc.want)
			}
		})
	}

	for _, state := range []cmd.ManInstallState{cmd.ManPageCurrent, cmd.ManPageNewer, cmd.ManPagePackaged} {
		t.Run(state.String()+" is silent", func(t *testing.T) {
			calls := &manPageCalls{}
			got := runProcess(t, "version", interactiveRelease(map[string]string{}, calls.hook(cmd.ManInstall{State: state, Path: "/p/clai.1"}, nil)))
			if len(calls.installers) != 1 || got.stderr != "" {
				t.Fatalf("calls=%d stderr=%q, want one call and no output", len(calls.installers), got.stderr)
			}
		})
	}
}

func Test_firstRunManPage_skips(t *testing.T) {
	tests := []struct {
		name   string
		args   string
		adjust func(*process, *Deps)
	}{
		{name: "stderr is not a terminal", args: "version", adjust: func(p *process, _ *Deps) { p.stderrTerminal = false }},
		{name: "JSON output", args: "-json version"},
		{name: "CLAI_MANPAGE=off", args: "version", adjust: func(p *process, _ *Deps) {
			p.getenv = func(name string) string { return map[string]string{"CLAI_MANPAGE": "off", "NO_COLOR": "true"}[name] }
		}},
		{name: "a pseudo-version", args: "version", adjust: func(_ *process, d *Deps) {
			d.Version = func() (string, error) { return "v1.13.3-0.20261118133244-a7724a531a41", nil }
		}},
		{name: "shell completion", args: "__complete clai q"},
		{name: "the completion command", args: "completion bash"},
		{name: "the man command", args: "man"},
		{name: "man install", args: "man install " + t.TempDir()},
		{name: "help", args: "help"},
		{name: "a help flag", args: "-h"},
		{name: "help for a command", args: "help version"},
		{name: "a command's -h", args: "version -h"},
		{name: "help after a value flag", args: "-cm gpt-4 help"},
		{name: "a help flag after a value flag", args: "version -cm -h"},
		{name: "man after --", args: "-- man"},
		{name: "help after --", args: "-- help"},
		{name: "completion after --", args: "-- completion bash"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := &manPageCalls{}
			got := runProcess(t, tc.args, func(p *process, d *Deps) {
				interactiveRelease(map[string]string{}, calls.hook(cmd.ManInstall{State: cmd.ManPageInstalled, Path: "/p/clai.1"}, nil))(p, d)
				if tc.adjust != nil {
					tc.adjust(p, d)
				}
			})
			if len(calls.installers) != 0 {
				t.Fatalf("hook called %d times, want 0; stderr %q", len(calls.installers), got.stderr)
			}
			if strings.Contains(got.stderr, "man page") {
				t.Fatalf("stderr mentions the man page: %q", got.stderr)
			}
		})
	}

	t.Run("-h after -- is an argument", func(t *testing.T) {
		calls := &manPageCalls{}
		runProcess(t, "version -- -h", interactiveRelease(map[string]string{}, calls.hook(cmd.ManInstall{State: cmd.ManPageCurrent}, nil)))
		if len(calls.installers) != 1 {
			t.Fatalf("hook called %d times, want 1", len(calls.installers))
		}
	})

	t.Run("no hook", func(t *testing.T) {
		got := runProcess(t, "version", interactiveRelease(map[string]string{}, nil))
		if got.status != 0 || got.stderr != "" {
			t.Fatalf("status=%d stderr=%q, want a quiet run", got.status, got.stderr)
		}
	})
}

func Test_firstRunManPage_unknownVersionWarns(t *testing.T) {
	calls := &manPageCalls{}
	got := runProcess(t, "version", func(p *process, d *Deps) {
		interactiveRelease(map[string]string{}, calls.hook(cmd.ManInstall{}, nil))(p, d)
		d.Version = func() (string, error) { return "", errors.New("no build info") }
	})
	if len(calls.installers) != 0 || got.status != 0 {
		t.Fatalf("calls=%d status=%d, want no install and status 0", len(calls.installers), got.status)
	}
	if want := "warn [clai] could not install the man page: resolve clai's version: no build info"; !strings.Contains(got.stderr, want) {
		t.Fatalf("stderr = %q, want %q", got.stderr, want)
	}
}

func Test_firstRunManPage_beforeDispatch(t *testing.T) {
	var stdout strings.Builder
	sawOutput := false
	runProcess(t, "version", func(p *process, d *Deps) {
		interactiveRelease(map[string]string{}, func(cmd.ManInstaller) (cmd.ManInstall, error) {
			sawOutput = stdout.Len() > 0
			return cmd.ManInstall{State: cmd.ManPageCurrent}, nil
		})(p, d)
		p.stdout = &stdout
	})
	if sawOutput || stdout.Len() == 0 {
		t.Fatalf("the hook ran after the command printed (saw output %v, stdout %q)", sawOutput, stdout.String())
	}
}

func Test_DefaultDeps_installNoManPage(t *testing.T) {
	if DefaultDeps().ManPage != nil {
		t.Fatal("DefaultDeps sets ManPage; only main may")
	}
}

func Test_firstRunManPage_installsBesideTheBinaryOnPath(t *testing.T) {
	prefix := t.TempDir()
	exe := filepath.Join(prefix, "bin", "clai")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PATH": filepath.Dir(exe), "HOME": os.Getenv("HOME")}
	got := runProcess(t, "version", func(p *process, d *Deps) {
		interactiveRelease(env, FirstRunManPage())(p, d)
		p.executable = func() (string, error) { return exe, nil }
	})
	page := filepath.Join(prefix, "share", "man", "man1", "clai.1")
	if want := "installed the man page for clai v1.14.0 in " + page + "; read it with 'man clai'"; !strings.Contains(got.stderr, want) {
		t.Fatalf("stderr = %q, want %q", got.stderr, want)
	}
}

func Test_firstRunManPage_installs(t *testing.T) {
	data := t.TempDir()
	env := map[string]string{"XDG_DATA_HOME": data, "HOME": os.Getenv("HOME")}
	page := filepath.Join(data, "man", "man1", "clai.1")

	first := runProcess(t, "version", interactiveRelease(env, FirstRunManPage()))
	if !strings.Contains(first.stderr, "installed the man page for clai v1.14.0 in "+page) {
		t.Fatalf("first run stderr = %q, want the install notice", first.stderr)
	}
	body, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("read the installed page: %v", err)
	}
	for _, want := range []string{`.TH "CLAI" 1 "" "clai v1.14.0" "User Commands"`, `.SS "query|q"`, `.SS "man install"`, ".SH ENVIRONMENT"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("installed page lacks %q", want)
		}
	}

	second := runProcess(t, "version", interactiveRelease(env, FirstRunManPage()))
	if strings.Contains(second.stderr, "man page") {
		t.Fatalf("second run stderr = %q, want no notice for a current page", second.stderr)
	}
}

func Test_firstRunManPage_onlyTheNewestReleaseWrites(t *testing.T) {
	data := t.TempDir()
	env := map[string]string{"XDG_DATA_HOME": data, "HOME": os.Getenv("HOME")}
	page := filepath.Join(data, "man", "man1", "clai.1")
	release := func(v string) func(*process, *Deps) {
		return func(p *process, d *Deps) {
			interactiveRelease(env, FirstRunManPage())(p, d)
			d.Version = func() (string, error) { return v, nil }
		}
	}
	header := func() string {
		body, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("read the page: %v", err)
		}
		line, _, _ := strings.Cut(string(body), "\n")
		return line
	}

	runProcess(t, "version", release("v1.15.0"))
	older := runProcess(t, "version", release("v1.14.0"))
	if strings.Contains(older.stderr, "man page") || !strings.Contains(header(), `"clai v1.15.0"`) {
		t.Fatalf("an older release: stderr %q, page %q; want no notice and the v1.15.0 page", older.stderr, header())
	}
	newer := runProcess(t, "version", release("v1.16.0"))
	if !strings.Contains(newer.stderr, "updated the man page in "+page+" to clai v1.16.0") || !strings.Contains(header(), `"clai v1.16.0"`) {
		t.Fatalf("a newer release: stderr %q, page %q; want the update notice and the v1.16.0 page", newer.stderr, header())
	}
}

func Test_manCommand(t *testing.T) {
	home := os.Getenv("HOME")
	t.Setenv("CLAI_CONFIG_DIR", filepath.Join(home, ".config", ".clai"))
	release := func(_ *process, d *Deps) { d.Version = func() (string, error) { return "v1.14.0", nil } }

	got := runProcess(t, "man", release)
	if got.status != 0 {
		t.Fatalf("man status = %d, stderr %q", got.status, got.stderr)
	}
	for _, want := range []string{
		`.TH "CLAI" 1 "" "clai v1.14.0" "User Commands"`,
		"clai \\- (c)ommand (l)ine (a)rtificial (i)ntelligence\n",
		".B clai\n[command flags] <command> [command flags] [args]\n",
		".SH DESCRIPTION\n.in 0\n.nf\nPrerequisites:\n",
		`.SS "help"`, `.SS "completion"`, `.SS "man"`, `.SS "man install"`, `.SS "query|q"`, `.SS "chat|c list|l"`,
		".SH ENVIRONMENT\n", "CLAI_MANPAGE", "CLAI_CLOUD_URL", "CLAI_AUDIO_STOP_FILE", ".SH SEE ALSO\n",
		"Config dir: ~/.config/.clai",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("man output lacks %q", want)
		}
	}
	if strings.Contains(got.stdout, home) {
		t.Errorf("man output names the home directory %s; it must read ~", home)
	}
	if strings.Contains(got.stdout, `.SS "profiles"`) || strings.Contains(got.stdout, `.SS "setup|s"`) {
		t.Errorf("man output documents a hidden spelling")
	}

	root := t.TempDir()
	installed := runProcess(t, "man install "+root, release)
	want := filepath.Join(root, "man1", "clai.1")
	if installed.status != 0 || strings.TrimSpace(installed.stdout) != want {
		t.Fatalf("man install: status=%d stdout=%q stderr=%q, want the path %s", installed.status, installed.stdout, installed.stderr, want)
	}
	if body, err := os.ReadFile(want); err != nil || !strings.HasPrefix(string(body), `.TH "CLAI"`) {
		t.Fatalf("installed page: %v", err)
	}

	broken := runProcess(t, "man", func(_ *process, d *Deps) {
		d.Version = func() (string, error) { return "", errors.New("no build info") }
	})
	if broken.status != 1 || !strings.Contains(broken.stderr, "resolve clai's version: no build info") {
		t.Fatalf("man without a version: status=%d stderr=%q, want 1 and the version error", broken.status, broken.stderr)
	}
}

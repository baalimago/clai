package text

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestShellContext_Render_timeout_usesTimedOutValue_andWarns(t *testing.T) {
	def := ShellContextDefinition{
		Shell:         "/bin/sh",
		TimeoutMS:     5,
		TimedOutValue: "<timed out>",
		ErrorValue:    "<error>",
		Template:      "x={{.x}}\n",
		Vars: map[string]string{
			"x": "ignored",
		},
	}

	warns := make([]string, 0)
	r := ShellContextRenderer{
		RunVar: func(ctx context.Context, shell, cmd string, timeout time.Duration) (string, error) {
			ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			<-ctxTimeout.Done()
			return "", ctxTimeout.Err()
		},
		Warnf: func(format string, args ...any) {
			warns = append(warns, fmt.Sprintf(format, args...))
		},
	}

	got, err := r.Render(context.Background(), "ctxname", def)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "x=<timed out>" {
		t.Fatalf("unexpected render output:\nwant: %q\n got: %q", "x=<timed out>", got)
	}
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %#v", len(warns), warns)
	}
}

func TestShellContext_Render_preserves_embedded_newlines(t *testing.T) {
	def := ShellContextDefinition{
		Shell:    "/bin/sh",
		Template: "ctx={{.ctx}}\nbranch={{.branch}}\n",
		Vars: map[string]string{
			"ctx":    "ctx-cmd",
			"branch": "branch-cmd",
		},
	}

	r := ShellContextRenderer{
		RunVar: func(ctx context.Context, shell, cmd string, timeout time.Duration) (string, error) {
			if !strings.Contains(shell, "sh") {
				t.Fatalf("expected shell to contain sh, got %q", shell)
			}
			if cmd == def.Vars["ctx"] {
				return "/home/lorkin/Projects/not_wasmer/clai\ndate: 2026-06-25 09:44:20 EEST\nuser: lorkin", nil
			}
			return "main\nhostname: wasmerburk", nil
		},
	}

	got, err := r.Render(context.Background(), "ctxname", def)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "ctx=/home/lorkin/Projects/not_wasmer/clai\ndate: 2026-06-25 09:44:20 EEST\nuser: lorkin\nbranch=main\nhostname: wasmerburk"
	if got != want {
		t.Fatalf("unexpected render output:\nwant:\n%q\n got:\n%q", want, got)
	}
}

func TestAppendShellContextIfConfigured_preserves_trailing_newline_separation(t *testing.T) {
	ctxDir := t.TempDir()
	if err := os.MkdirAll(ctxDir+"/shellContexts", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	def := `{
	  "shell": "/bin/sh",
	  "template": "cwd: {{.cwd}}\n\nhost: {{.host}}\n",
	  "vars": {
	    "cwd": "printf '/tmp/project'",
	    "host": "printf 'box'"
	  }
	}`
	if err := os.WriteFile(ctxDir+"/shellContexts/minimal.json", []byte(def), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := AppendShellContextIfConfigured(context.Background(), ctxDir, "minimal", "SYSTEM", ShellContextRenderer{})
	if err != nil {
		t.Fatalf("AppendShellContextIfConfigured: %v", err)
	}
	want := "<shell context>\ncwd: /tmp/project\n\nhost: box\n</shell context>\nSYSTEM"
	if got != want {
		t.Fatalf("unexpected prompt:\nwant:\n%q\n got:\n%q", want, got)
	}
}

func TestShellContext_DefaultTemplate_separates_conditional_fields_with_single_lines(t *testing.T) {
	def, err := loadDefaultShellContextDefinitionForTest(t)
	if err != nil {
		t.Fatalf("load default shell context: %v", err)
	}

	r := ShellContextRenderer{
		RunVar: func(ctx context.Context, shell, cmd string, timeout time.Duration) (string, error) {
			switch cmd {
			case def.Vars["cwd"]:
				return "/tmp/project", nil
			case def.Vars["date"]:
				return "2026-06-25 13:00:00 EEST", nil
			case def.Vars["user"]:
				return "lorkin", nil
			case def.Vars["hostname"]:
				return "wasmerburk", nil
			case def.Vars["shell"]:
				return "zsh", nil
			case def.Vars["git_branch"]:
				return "main", nil
			default:
				return "", nil
			}
		},
	}

	got, err := r.Render(context.Background(), "default", def)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("expected no repeated blank lines, got %q", got)
	}
	want := "cwd: /tmp/project\ndate: 2026-06-25 13:00:00 EEST\nuser: lorkin\nhostname: wasmerburk\nshell: zsh\ngit branch: main"
	if got != want {
		t.Fatalf("unexpected render output:\nwant:\n%q\n got:\n%q", want, got)
	}
}

func loadDefaultShellContextDefinitionForTest(t *testing.T) (ShellContextDefinition, error) {
	t.Helper()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir+"/shellContexts", 0o755); err != nil {
		return ShellContextDefinition{}, err
	}
	if err := os.WriteFile(configDir+"/shellContexts/default.json", []byte(`{
	  "shell": "",
	  "timeout_ms": 250,
	  "timed_out_value": "",
	  "error_value": "",
	  "template": "cwd: {{.cwd}}\ndate: {{.date}}\nuser: {{.user}}\n{{if .hostname}}hostname: {{.hostname}}\n{{end}}{{if .shell}}shell: {{.shell}}\n{{end}}{{if .python_venv}}python env: {{.python_venv}}\n{{end}}{{if .k8s_context}}k8s context: {{.k8s_context}}\n{{end}}{{if .go_version}}go version: {{.go_version}}\n{{end}}{{if .git_branch}}git branch: {{.git_branch}}\n{{end}}{{if .git_status_short}}git dirty: {{.git_status_short}}\n{{end}}{{if .docker_context}}docker context: {{.docker_context}}\n{{end}}{{if .tmux_session}}tmux session: {{.tmux_session}}\n{{end}}{{if .ssh_connection}}ssh: {{.ssh_connection}}{{end}}",
	  "vars": {
	    "cwd": "pwd",
	    "date": "date '+%Y-%m-%d %H:%M:%S %Z'",
	    "user": "id -un",
	    "hostname": "(hostname 2>/dev/null || uname -n 2>/dev/null) | head -n 1",
	    "shell": "printf \"%s\" \"${SHELL##*/}\"",
	    "python_venv": "if [ -n \"$VIRTUAL_ENV\" ]; then basename \"$VIRTUAL_ENV\"; elif [ -n \"$CONDA_DEFAULT_ENV\" ]; then printf \"%s\" \"$CONDA_DEFAULT_ENV\"; fi",
	    "k8s_context": "kubectl config current-context 2>/dev/null",
	    "go_version": "go version 2>/dev/null | awk '{print $3}'",
	    "git_branch": "git branch --show-current 2>/dev/null",
	    "git_status_short": "git status --short 2>/dev/null | wc -l | tr -d ' ' | awk '{if ($1 != 0) print $1 \" changes\"}'",
	    "docker_context": "docker context show 2>/dev/null",
	    "tmux_session": "if [ -n \"$TMUX\" ]; then tmux display-message -p '#S' 2>/dev/null; fi",
	    "ssh_connection": "printf \"%s\" \"$SSH_CONNECTION\""
	  }
	}`), 0o644); err != nil {
		return ShellContextDefinition{}, err
	}
	return LoadShellContextDefinition(configDir, "default")
}

func TestLoadShellContextDefinition_preserves_literal_template_backslash_sequences(t *testing.T) {
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir+"/shellContexts", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configDir+"/shellContexts/default.json", []byte(`{
	  "template": "a\\nb\\n",
	  "vars": {}
	}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := LoadShellContextDefinition(configDir, "default")
	if err != nil {
		t.Fatalf("LoadShellContextDefinition: %v", err)
	}
	if got.Template != `a\nb\n` {
		t.Fatalf("template unexpectedly transformed: %q", got.Template)
	}
}

func TestLoadShellContextDefinition_does_not_unescape_var_commands(t *testing.T) {
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir+"/shellContexts", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configDir+"/shellContexts/default.json", []byte(`{
	  "template": "x",
	  "vars": {
	    "cmd": "printf 'a\\nb'"
	  }
	}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := LoadShellContextDefinition(configDir, "default")
	if err != nil {
		t.Fatalf("LoadShellContextDefinition: %v", err)
	}
	if got.Vars["cmd"] != `printf 'a\nb'` {
		t.Fatalf("vars command unexpectedly mutated: %q", got.Vars["cmd"])
	}
}

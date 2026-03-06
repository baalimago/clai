package text

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

type ShellContextDefinition struct {
	Shell         string            `json:"shell"`
	TimeoutMS     int               `json:"timeout_ms"`
	TimedOutValue string            `json:"timed_out_value"`
	ErrorValue    string            `json:"error_value"`
	Template      string            `json:"template"`
	Vars          map[string]string `json:"vars"`
}

func (d *ShellContextDefinition) withDefaults() ShellContextDefinition {
	out := *d
	if out.TimeoutMS <= 0 {
		out.TimeoutMS = 100
	}
	if out.TimedOutValue == "" {
		out.TimedOutValue = "<timed out>"
	}
	// error value may be empty by user choice; default only when not specified at all.
	if out.ErrorValue == "" {
		out.ErrorValue = "<error>"
	}
	if out.Vars == nil {
		out.Vars = map[string]string{}
	}
	if strings.TrimSpace(out.Shell) == "" {
		if envShell := strings.TrimSpace(os.Getenv("SHELL")); envShell != "" {
			out.Shell = envShell
		} else {
			out.Shell = "sh"
		}
	}
	return out
}

func LoadShellContextDefinition(configDir, name string) (ShellContextDefinition, error) {
	p := filepath.Join(configDir, "shellContexts", name+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		return ShellContextDefinition{}, fmt.Errorf("read shell context definition %q: %w", p, err)
	}
	var def ShellContextDefinition
	if err := json.Unmarshal(b, &def); err != nil {
		return ShellContextDefinition{}, fmt.Errorf("unmarshal shell context definition %q: %w", p, err)
	}
	return def.withDefaults(), nil
}

type ShellContextRenderer struct {
	RunVar func(ctx context.Context, shell, cmd string, timeout time.Duration) (string, error)
	Warnf  func(format string, args ...any)
}

func (r ShellContextRenderer) Render(ctx context.Context, ctxName string, def ShellContextDefinition) (string, error) {
	def = def.withDefaults()

	runner := r.RunVar
	if runner == nil {
		runner = runShellVar
	}

	data := make(map[string]string, len(def.Vars))
	for name, cmd := range def.Vars {
		val, err := runner(ctx, def.Shell, cmd, time.Duration(def.TimeoutMS)*time.Millisecond)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				data[name] = def.TimedOutValue
				if r.Warnf != nil {
					r.Warnf("shell-context %q: var %q timed out after %dms; using %q\n", ctxName, name, def.TimeoutMS, def.TimedOutValue)
				}
				continue
			}
			data[name] = def.ErrorValue
			continue
		}
		data[name] = val
	}

	tpl, err := template.New("shell-context").Parse(def.Template)
	if err != nil {
		return "", fmt.Errorf("parse shell context template: %w", err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute shell context template: %w", err)
	}
	return buf.String(), nil
}

func runShellVar(ctx context.Context, shell, cmd string, timeout time.Duration) (string, error) {
	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ex := exec.CommandContext(ctx2, shell, "-c", cmd)
	out, err := ex.Output()
	if err != nil {
		return "", fmt.Errorf("run %q -c %q: %w", shell, cmd, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

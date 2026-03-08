package text

import (
	"context"
	"fmt"
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
	if got != "x=<timed out>\n" {
		t.Fatalf("unexpected render output:\nwant: %q\n got: %q", "x=<timed out>\n", got)
	}
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %#v", len(warns), warns)
	}
}

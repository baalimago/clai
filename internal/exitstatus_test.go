package internal

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type codeErr int

func (e codeErr) Error() string { return "refused" }
func (e codeErr) ExitCode() int { return int(e) }

func TestExitStatus_recordsAWrappedExitCoder(t *testing.T) {
	ctx, status := WithExitStatus(t.Context())
	c := &Command{Name: "x", OnRun: func(context.Context, *Command) error {
		return fmt.Errorf("run: %w", codeErr(3))
	}}
	if err := c.Run(ctx); err == nil {
		t.Fatal("Run: want error")
	}
	if status.Code() != 3 || status.Resolve(1) != 3 || status.Resolve(0) != 0 {
		t.Fatalf("Code=%d Resolve(1)=%d", status.Code(), status.Resolve(1))
	}
}

func TestExitStatus_ignoresPlainErrorsAndBareContexts(t *testing.T) {
	ctx, status := WithExitStatus(t.Context())
	c := &Command{Name: "x", OnSetup: func(context.Context, *Command) error { return errors.New("plain") }}
	c.Flagset()
	if err := c.Setup(ctx); err == nil {
		t.Fatal("Setup: want error")
	}
	if status.Code() != 0 || status.Resolve(1) != 1 {
		t.Fatalf("Code=%d, want 0", status.Code())
	}
	if err := recordExit(t.Context(), codeErr(3)); err == nil {
		t.Fatal("recordExit without status: want the error back")
	}
}

func TestExitStatus_setReplacesOnlySuccess(t *testing.T) {
	_, status := WithExitStatus(t.Context())
	status.Set(4)
	if status.Resolve(0) != 4 || status.Resolve(1) != 1 || status.Resolve(2) != 2 {
		t.Fatalf("Resolve(0)=%d Resolve(1)=%d Resolve(2)=%d", status.Resolve(0), status.Resolve(1), status.Resolve(2))
	}
}

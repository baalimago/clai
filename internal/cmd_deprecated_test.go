package internal

import (
	"context"
	"strings"
	"testing"
)

func TestSetup_cmdCommand_isUnknown(t *testing.T) {
	ctx := context.Background()
	_, err := Setup(ctx, "", strings.Split("cmd echo hi", " "))
	if err == nil {
		t.Fatalf("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "unknown command") {
		t.Fatalf("expected unknown command error, got: %v", got)
	}
}

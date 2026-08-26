package tools

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestMktempTool_CallWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	directory, err := Mktemp.CallWithContext(ctx, pub_models.Input{})
	if err != nil {
		t.Fatalf("Mktemp.CallWithContext(): %v", err)
	}
	if !strings.HasPrefix(directory, os.TempDir()+string(os.PathSeparator)) {
		t.Fatalf("temporary directory %q is outside %q", directory, os.TempDir())
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("stat temporary directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %q to be a directory", directory)
	}

	cancel()
	for range 100 {
		_, err = os.Stat(directory)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("temporary directory still exists after context cancellation: %v", err)
}

func TestMktempTool_RequiresCancellableContext(t *testing.T) {
	if _, err := Mktemp.CallWithContext(context.Background(), pub_models.Input{}); err == nil {
		t.Fatal("expected cancellable-context requirement error")
	}
}

func TestMktempTool_RejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := Mktemp.CallWithContext(ctx, pub_models.Input{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestMktempTool_CallRequiresContext(t *testing.T) {
	if _, err := Mktemp.Call(pub_models.Input{}); err == nil {
		t.Fatal("expected context requirement error")
	}
}

func TestMktempTool_Specification(t *testing.T) {
	spec := Mktemp.Specification()
	if spec.Name != "mktemp" {
		t.Fatalf("spec name = %q, want mktemp", spec.Name)
	}
	if spec.Inputs == nil || len(spec.Inputs.Required) != 0 || len(spec.Inputs.Properties) != 0 {
		t.Fatalf("expected an empty input schema, got %+v", spec.Inputs)
	}
}

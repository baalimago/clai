package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestRsyncTool_CallBuildsSafeArguments(t *testing.T) {
	binDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	fake := filepath.Join(binDir, "rsync")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("ARGS_FILE", argsFile)

	_, err := Rsync.Call(pub_models.Input{
		"source":       "host:/source/",
		"destination":  "/destination",
		"allow_remote": true,
		"delete":       true,
		"dry_run":      true,
	})
	if err != nil {
		t.Fatalf("Rsync.Call(): %v", err)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"--archive", "--partial", "--delete", "--dry-run", "--", "host:/source/", "/destination"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("args: got %q want %q", got, want)
	}
}

func TestRsyncTool_RejectsRemoteWithoutOptIn(t *testing.T) {
	_, err := Rsync.Call(pub_models.Input{
		"source":      "host:/source",
		"destination": "/destination",
	})
	if err == nil || !strings.Contains(err.Error(), "allow_remote") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRsyncTool_ValidatesOptionalBooleans(t *testing.T) {
	for _, field := range []string{"allow_remote", "delete", "dry_run"} {
		t.Run(field, func(t *testing.T) {
			_, err := Rsync.Call(pub_models.Input{
				"source":      "source",
				"destination": "destination",
				field:         "yes",
			})
			if err == nil || !strings.Contains(err.Error(), field+" must be a boolean") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRsyncTool_Specification(t *testing.T) {
	spec := Rsync.Specification()
	if spec.Name != "rsync" {
		t.Fatalf("spec name: got %q want rsync", spec.Name)
	}
	if spec.Inputs == nil || len(spec.Inputs.Required) != 2 {
		t.Fatalf("unexpected input schema: %+v", spec.Inputs)
	}
}

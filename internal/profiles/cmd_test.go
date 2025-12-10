package profiles

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/utils"
)

func TestRunProfilesList_NoProfilesDir(t *testing.T) {
	tmp := t.TempDir()

	// Override XDG_CONFIG_HOME so GetClaiConfigDir points into our temp dir.
	origXDG := os.Getenv("XDG_CONFIG_HOME")
	if err := os.Setenv("XDG_CONFIG_HOME", tmp); err != nil {
		t.Fatalf("failed to set XDG_CONFIG_HOME: %v", err)
	}
	defer os.Setenv("XDG_CONFIG_HOME", origXDG)

	// Sanity: confirm GetClaiConfigDir resolves inside tmp
	cfgDir, err := utils.GetClaiConfigDir()
	if err != nil {
		t.Fatalf("failed to get clai config dir: %v", err)
	}
	if cfgDir == "" {
		t.Fatalf("expected non-empty config dir")
	}

	var buf bytes.Buffer
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := make(chan struct{})
	go func() {
		var out bytes.Buffer
		_, _ = out.ReadFrom(r)
		buf.Write(out.Bytes())
		close(done)
	}()

	err = runProfilesList()
	w.Close()
	os.Stdout = origStdout
	<-done

	if err == nil {
		t.Fatalf("expected user initiated exit error, got nil")
	}

	if !bytes.Contains(buf.Bytes(), []byte("no profiles directory")) {
		t.Fatalf("expected warning about missing profiles directory, got: %s", buf.String())
	}
}

func TestRunProfilesList_EmptyProfilesDir(t *testing.T) {
	tmp := t.TempDir()

	cfgDir, err := utils.GetClaiConfigDir()
	if err != nil {
		t.Fatalf("failed to get original clai config dir: %v", err)
	}
	_ = cfgDir // silence unused if not needed

	// Create a .clai/profiles dir inside tmp
	claiDir := filepath.Join(tmp, ".clai", "profiles")
	if err := os.MkdirAll(claiDir, 0o755); err != nil {
		t.Fatalf("failed to create profiles dir: %v", err)
	}

	origXDG := os.Getenv("XDG_CONFIG_HOME")
	if err := os.Setenv("XDG_CONFIG_HOME", tmp); err != nil {
		t.Fatalf("failed to set XDG_CONFIG_HOME: %v", err)
	}
	defer os.Setenv("XDG_CONFIG_HOME", origXDG)

	var buf bytes.Buffer
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := make(chan struct{})
	go func() {
		var out bytes.Buffer
		_, _ = out.ReadFrom(r)
		buf.Write(out.Bytes())
		close(done)
	}()

	err = runProfilesList()
	w.Close()
	os.Stdout = origStdout
	<-done

	if err == nil {
		t.Fatalf("expected user initiated exit error, got nil")
	}

	if !bytes.Contains(buf.Bytes(), []byte("no profiles found")) {
		t.Fatalf("expected warning about no profiles, got: %s", buf.String())
	}
}

func TestSubCmd_DefaultToList(t *testing.T) {
	ctx := context.Background()

	err := SubCmd(ctx, []string{"profiles"})
	if err == nil {
		t.Fatalf("expected user initiated exit error, got nil")
	}
}

func TestSubCmd_UnknownSubcommand(t *testing.T) {
	ctx := context.Background()

	err := SubCmd(ctx, []string{"profiles", "unknown"})
	if err == nil {
		t.Fatalf("expected error for unknown subcommand, got nil")
	}
}

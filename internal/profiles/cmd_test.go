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

	// Override config dir so GetClaiConfigDir points into our temp dir.
	t.Setenv("CLAI_CONFIG_HOME", filepath.Join(tmp, ".clai"))

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

	// Create a .clai/profiles dir inside tmp
	claiDir := filepath.Join(tmp, ".clai")
	profilesDir := filepath.Join(claiDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatalf("failed to create profiles dir: %v", err)
	}

	t.Setenv("CLAI_CONFIG_HOME", claiDir)

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

	err := runProfilesList()
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

func TestGetFirstSentence(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "[Empty prompt]",
		},
		{
			name:     "ends with period",
			input:    "Hello world.",
			expected: "Hello world.",
		},
		{
			name:     "ends with exclamation",
			input:    "Hello world!",
			expected: "Hello world!",
		},
		{
			name:     "ends with question",
			input:    "Hello world?",
			expected: "Hello world?",
		},
		{
			name:     "ends with newline",
			input:    "Hello world\nSecond line",
			expected: "Hello world\n",
		},
		{
			name:     "multiple terminators",
			input:    "First. Second! Third",
			expected: "First.",
		},
		{
			name:     "no terminator",
			input:    "Just a string",
			expected: "Just a string",
		},
		{
			name:     "single character",
			input:    "A",
			expected: "A",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getFirstSentence(tt.input)
			if result != tt.expected {
				t.Fatalf("expected %q, got %q",
					tt.expected, result)
			}
		})
	}
}

package profiles

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
)

func TestRunProfilesList_NoProfilesDir(t *testing.T) {
	tmp := t.TempDir()

	// Override config dir so GetClaiConfigDir points into our temp dir.
	t.Setenv("CLAI_CONFIG_DIR", filepath.Join(tmp, ".clai"))

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

	err = List()
	w.Close()
	os.Stdout = origStdout
	<-done

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
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

	t.Setenv("CLAI_CONFIG_DIR", claiDir)

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

	err := List()
	w.Close()
	os.Stdout = origStdout
	<-done

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if !bytes.Contains(buf.Bytes(), []byte("no profiles found")) {
		t.Fatalf("expected warning about no profiles, got: %s", buf.String())
	}
}

func TestList_Succeeds(t *testing.T) {
	if err := List(); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
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

func TestRunProfilesList_RehydratesLegacyEscapedPrompt(t *testing.T) {
	claiDir := filepath.Join(t.TempDir(), ".clai")
	profilesDir := filepath.Join(claiDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatalf("failed to create profiles dir: %v", err)
	}
	legacy := `{"name":"legacy","model":"m","prompt":"First line\\nSecond line"}`
	if err := os.WriteFile(filepath.Join(profilesDir, "legacy.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("failed to write profile: %v", err)
	}

	got := listStdout(t, claiDir)
	if !strings.Contains(got, "First sentence prompt: First line\n") {
		t.Fatalf("legacy escaped prompt not rehydrated in listing:\n%s", got)
	}
}

// listStdout returns what List prints for the profiles under claiDir.
func listStdout(t *testing.T, claiDir string) string {
	t.Helper()
	t.Setenv("CLAI_CONFIG_DIR", claiDir)
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = buf.ReadFrom(r)
		close(done)
	}()
	listErr := List()
	_ = w.Close()
	os.Stdout = origStdout
	<-done
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	return buf.String()
}

func TestRunProfilesList_showsTheAudioModel(t *testing.T) {
	writeProfile := func(t *testing.T, body string) string {
		t.Helper()
		claiDir := filepath.Join(t.TempDir(), ".clai")
		profilesDir := filepath.Join(claiDir, "profiles")
		if err := os.MkdirAll(profilesDir, 0o755); err != nil {
			t.Fatalf("failed to create profiles dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(profilesDir, "meeting.json"), []byte(body), 0o644); err != nil {
			t.Fatalf("failed to write profile: %v", err)
		}
		return claiDir
	}
	t.Run("set", func(t *testing.T) {
		got := listStdout(t, writeProfile(t, `{"name":"meeting","model":"m","audio":{"model":"diarize"}}`))
		if !strings.Contains(got, "Audio model: diarize\n") {
			t.Fatalf("listing lacks the audio model:\n%s", got)
		}
	})
	t.Run("unset", func(t *testing.T) {
		got := listStdout(t, writeProfile(t, `{"name":"meeting","model":"m"}`))
		if strings.Contains(got, "Audio model:") {
			t.Fatalf("listing shows an audio model it does not have:\n%s", got)
		}
	})
}

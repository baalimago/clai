package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_CONFIG_flag_defaults_do_not_override_mode_config(t *testing.T) {
	// Behaviour: config precedence is flags > file > defaults.
	// This test ensures that *default flag values* do not override values loaded
	// from textConfig.json.
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	// Create required config subdirs (matches existing goldenfile tests).
	required := []string{
		"conversations",
		"profiles",
		"mcpServers",
		"conversations/dirs",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	// Write a textConfig.json that sets the model to "test".
	cfg := text.Default
	cfg.Model = "test"
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal(text config): %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "textConfig.json"), b, 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		// Intentionally do not pass -cm; the config file should decide the model.
		gotStatus = run(strings.Split("-r q hello", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.FailTestIfDiff(t, stdout, "hello\n")
}

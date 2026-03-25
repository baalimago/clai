package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_e2e_same_prompt_twice_creates_two_separate_chats(t *testing.T) {
	confDir := setupMainTestConfigDir(t)

	runOne := func(t *testing.T, args string) int {
		t.Helper()
		oldArgs := os.Args
		t.Cleanup(func() {
			os.Args = oldArgs
		})

		var status int
		_ = testboil.CaptureStdout(t, func(t *testing.T) {
			status = run(strings.Split(args, " "))
		})
		return status
	}

	prompt := "please keep these as separate chats"
	status := runOne(t, "-r -cm test q "+prompt)
	testboil.FailTestIfDiff(t, status, 0)

	status = runOne(t, "-r -cm test q "+prompt)
	testboil.FailTestIfDiff(t, status, 0)

	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}

	chatFiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || name == "globalScope.json" {
			continue
		}
		chatFiles = append(chatFiles, name)
	}

	if len(chatFiles) < 2 {
		t.Fatalf("expected at least 2 separate persisted chats for same prompt, got %d: %v", len(chatFiles), chatFiles)
	}
}

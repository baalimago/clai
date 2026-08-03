package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestReplayDirScoped_RawOmitsReasoning(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	chat := pub_models.Chat{
		ID: "raw-dre-reasoning",
		Messages: []pub_models.Message{{
			Role:             "assistant",
			Content:          `{"answer":"done"}`,
			ReasoningContent: "private chain of thought",
		}},
	}
	convDir := filepath.Join(confDir, "conversations")
	if err := Save(convDir, chat); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := (&ChatHandler{confDir: confDir, convDir: convDir}).SaveDirScope("", chat.ID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	var replayErr error
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		replayErr = Replay(true, true)
	})
	if replayErr != nil {
		t.Fatalf("Replay: %v", replayErr)
	}
	if strings.Contains(stdout, "thinking") || strings.Contains(stdout, "private chain of thought") {
		t.Fatalf("raw dre output contains reasoning: %q", stdout)
	}
	if stdout != "{\"answer\":\"done\"}\n" {
		t.Fatalf("raw dre output: got %q", stdout)
	}
}

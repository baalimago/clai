package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// Test_e2e_chat_continue_sends_the_trailing_prompt pins that
// `chat continue <index> <prompt>` sends the prompt to the continued chat,
// instead of only previewing it.
func Test_e2e_chat_continue_sends_the_trailing_prompt(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	writeNotificationTestPriceFiles(t, confDir)
	textConf := text.Default
	textConf.Model = "test"
	textConf.SummarizeConversations = false
	if err := utils.CreateFile(filepath.Join(confDir, "textConfig.json"), &textConf); err != nil {
		t.Fatalf("CreateFile(textConfig.json): %v", err)
	}

	if _, status := harness.RunOne(t, confDir, "-r q first question"); status != 0 {
		t.Fatalf("seed query status %d", status)
	}
	stdout, status := harness.RunOne(t, confDir, "-r c c 0 follow up")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, stdout, "follow up\n")

	convs, err := filepath.Glob(filepath.Join(confDir, "conversations", "*.json"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	for _, p := range convs {
		if filepath.Base(p) != "globalScope.json" && strings.Contains(e2etest.ReadStringFile(t, p), `"content": "follow up"`) {
			return
		}
	}
	t.Fatalf("no saved conversation holds the follow up, files: %v", convs)
}

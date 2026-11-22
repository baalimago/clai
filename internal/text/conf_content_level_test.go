package text

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func TestInitialPromptIsTraceOnly(t *testing.T) {
	run := func(level diagnostic.Level) string {
		var out bytes.Buffer
		conf := Default
		conf.ConfigDir = t.TempDir()
		if err := conf.SetupInitialChat(procenv.Diagnostics(context.Background(), &out, level), []string{"q", "PROMPT-MARKER"}); err != nil {
			t.Fatalf("SetupInitialChat: %v", err)
		}
		return out.String()
	}
	debug := run(diagnostic.LevelDebug)
	if strings.Contains(debug, "MARKER") || !strings.Contains(debug, "InitialPrompt: messages=2 prompt_bytes=13") {
		t.Errorf("debug output must carry the prompt's size, not its text:\n%s", debug)
	}
	if trace := run(diagnostic.LevelTrace); !strings.Contains(trace, "PROMPT-MARKER") {
		t.Errorf("trace output lacks the prompt:\n%s", trace)
	}
}

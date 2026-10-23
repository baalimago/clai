package summary

import (
	"strconv"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
)

// TestLoopPromptsAskForOneShortLine pins that the loop prompts ask for a phase
// that fits the pinned header row and name the exact bound the tool enforces.
func TestLoopPromptsAskForOneShortLine(t *testing.T) {
	for name, prompt := range map[string]string{
		"loop phase": loopPhaseSystemPrompt(),
		"loop plan":  loopPlanSystemPrompt(),
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(prompt, "one short line") {
				t.Fatalf("prompt must ask for one short line: %q", prompt)
			}
			if want := "at most " + strconv.Itoa(models.LoopPhaseSummaryRunes) + " runes"; !strings.Contains(prompt, want) {
				t.Fatalf("prompt must name the %q bound: %q", want, prompt)
			}
		})
	}
}

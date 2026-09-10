package summary

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	ToolName        = "submit_summary"
	TitleMaxRunes   = 60
	SummaryMaxRunes = 240
	InputMaxRunes   = models.SummaryInputRunes
	MaxToolCalls    = 4
)

// submission holds the last valid result of one Summarize call.
type submission struct {
	mu      sync.Mutex
	title   string
	summary string
	set     bool
}

func (s *submission) put(title, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.title, s.summary, s.set = title, summary, true
}

func (s *submission) get() (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title, s.summary, s.set
}

type submitSummaryTool struct {
	holder *submission
}

func newSubmitSummaryTool(holder *submission) submitSummaryTool {
	return submitSummaryTool{holder: holder}
}

func (t submitSummaryTool) Call(input pub_models.Input) (string, error) {
	title, err := validateField(input, "title", TitleMaxRunes, true)
	if err != nil {
		return "", err
	}
	summary, err := validateField(input, "summary", SummaryMaxRunes, false)
	if err != nil {
		return "", err
	}
	t.holder.put(title, summary)
	return "accepted", nil
}

func (t submitSummaryTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name: ToolName,
		Description: fmt.Sprintf("Submit the label of the conversation. title: one line, at most %d runes. summary: at most %d runes. Call it exactly once; on a rejection, correct the named field and call again.",
			TitleMaxRunes, SummaryMaxRunes),
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"title", "summary"},
			Properties: map[string]pub_models.ParameterObject{
				"title": {
					Type:        "string",
					Description: fmt.Sprintf("Short imperative title, one line, at most %d runes.", TitleMaxRunes),
				},
				"summary": {
					Type:        "string",
					Description: fmt.Sprintf("Two sentences: what was asked and, when visible, the outcome. At most %d runes.", SummaryMaxRunes),
				},
			},
		},
	}
}

func validateField(input pub_models.Input, field string, maxRunes int, singleLine bool) (string, error) {
	raw, ok := input[field].(string)
	if !ok {
		return "", errors.New(field + ": required")
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New(field + ": empty")
	}
	if singleLine && strings.ContainsAny(trimmed, "\n\r") {
		return "", errors.New(field + ": single line")
	}
	collapsed := collapseWhitespace(trimmed)
	if n := utf8.RuneCountInString(collapsed); n > maxRunes {
		return "", fmt.Errorf("%s: at most %d runes, got %d", field, maxRunes, n)
	}
	return collapsed, nil
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

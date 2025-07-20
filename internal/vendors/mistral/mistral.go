package mistral

import (
	"context"
	"fmt"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

const MistralURL = "https://api.mistral.ai/v1/chat/completions"

var Default = Mistral{
	Model:       "mistral-large-latest",
	Temperature: 0.7,
	TopP:        1.0,
	URL:         MistralURL,
	MaxTokens:   100000,
}

type Mistral struct {
	generic.StreamCompleter
	Model       string  `json:"model"`
	URL         string  `json:"url"`
	TopP        float64 `json:"top_p"`
	Temperature float64 `json:"temperature"`
	SafePrompt  bool    `json:"safe_prompt"`
	MaxTokens   int     `json:"max_tokens"`
	RandomSeed  int     `json:"random_seed"`
}

func clean(msg []pub_models.Message) []pub_models.Message {
	// Mistral doesn't like additional fields in the tools call
	for i, m := range msg {
		if m.Role == "assistant" {
			if len(m.ToolCalls) > 0 {
				m.Content = ""
			}
			for j, tc := range m.ToolCalls {
				tc.Name = ""
				tc.Inputs = nil
				tc.Function.Description = ""
				tc.Function.Inputs = nil
				m.ToolCalls[j] = tc
			}
		}
		msg[i] = m
	}

	for i := 0; i < len(msg)-1; i++ {
		if msg[i].Role == "tool" && msg[i+1].Role == "system" {
			msg[i+1].Role = "assistant"
		}
	}

	// Merge consequtive assistant messages
	for i := 1; i < len(msg); i++ {
		if msg[i].Role == "assistant" && msg[i-1].Role == "assistant" {
			msg[i-1].Content += "\n" + msg[i].Content
			nMsg, err := utils.DeleteRange(msg, i, i)
			if err != nil {
				ancli.Errf("failed to delete range. No error management here... Not great. Why error here? Stop please...: %v", err)
			}
			msg = nMsg
			i--
		}
	}

	return msg
}

func (m *Mistral) Setup() error {
	err := m.StreamCompleter.Setup("MISTRAL_API_KEY", MistralURL, "DEBUG_MISTRAL")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}
	m.StreamCompleter.Model = m.Model
	m.StreamCompleter.MaxTokens = &m.MaxTokens
	m.StreamCompleter.Temperature = &m.Temperature
	m.StreamCompleter.TopP = &m.TopP
	toolChoice := "any"
	m.ToolChoice = &toolChoice
	m.Clean = clean

	return nil
}

func (m *Mistral) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	return m.StreamCompleter.StreamCompletions(ctx, chat)
}

func (m *Mistral) RegisterTool(tool tools.LLMTool) {
	m.InternalRegisterTool(tool)
}

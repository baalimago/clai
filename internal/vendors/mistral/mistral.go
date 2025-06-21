package mistral

import (
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

const MistralURL = "https://api.mistral.ai/v1/chat/completions"

var MINSTRAL_DEFAULT = Mistral{
	Model:       "mistral-large-latest",
	Temperature: 0.7,
	TopP:        1.0,
	Url:         MistralURL,
	MaxTokens:   100000,
}

type Mistral struct {
	generic.StreamCompleter
	Model       string  `json:"model"`
	Url         string  `json:"url"`
	TopP        float64 `json:"top_p"`
	Temperature float64 `json:"temperature"`
	SafePrompt  bool    `json:"safe_prompt"`
	MaxTokens   int     `json:"max_tokens"`
	RandomSeed  int     `json:"random_seed"`
}

func clean(msg []models.Message) []models.Message {
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

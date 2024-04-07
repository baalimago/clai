package anthropic

import (
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type Claude struct {
	Model            string       `json:"model"`
	MaxTokens        int          `json:"max_tokens"`
	Url              string       `json:"url"`
	AnthropicVersion string       `json:"anthropic-version"`
	AnthropicBeta    string       `json:"anthropic-beta"`
	client           *http.Client `json:"-"`
	apiKey           string       `json:"-"`
	debug            bool         `json:"-"`
}

var CLAUDE_DEFAULT = Claude{
	Model:            "claude-3-opus-20240229",
	Url:              ClaudeURL,
	AnthropicVersion: "2023-06-01",
	AnthropicBeta:    "messages-2023-12-15",
	MaxTokens:        1024,
}

type claudeReq struct {
	Model     string           `json:"model"`
	Messages  []models.Message `json:"messages"`
	MaxTokens int              `json:"max_tokens"`
	Stream    bool             `json:"stream"`
	System    string           `json:"system"`
}

// claudifyMessages converts from 'normal' openai chat format into a format which claud prefers
func claudifyMessages(msgs []models.Message) []models.Message {
	// the 'system' role only is accepted as a separate parameter in json
	// If the first message is a system one, assume it's the system prompt and pop it
	// This system message has already been added into the initial query
	if msgs[0].Role == "system" {
		msgs = msgs[1:]
	}

	// Convert system messages from 'system' to 'assistant', since
	for i, v := range msgs {
		v := v
		if v.Role == "system" {
			// Claude only likes user messages as first, and messages needs to be alternating
			v.Role = "assistant"
		}
		msgs[i] = v
	}

	// claude doesn't like having 'assistant' messages as the first messag
	// so, if there is a assistant message first, merge it into the upcoming
	// user message
	for msgs[0].Role == "assistant" {
		msgs[1].Content = fmt.Sprintf("%v\n%v", msgs[0].Content, msgs[1].Content)
		msgs = msgs[1:]
	}

	// claude doesn't reply if the latest message is from an assistant
	if msgs[len(msgs)-1].Role == "assistant" {
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("removing last message: %v", msgs[len(msgs)-1].Content))
		}
		msgs = msgs[:len(msgs)-1]
	}

	// claude also doesn't like it when two user messages are in a row
	for i := 1; i < len(msgs); {
		if msgs[i-1].Role == "user" && msgs[i].Role == "user" {
			msgs[i].Content = fmt.Sprintf("%v\n%v", msgs[i-1].Content, msgs[i].Content)
			tmp := msgs[0 : i-1]
			tmp = append(tmp, msgs[i:]...)
			msgs = tmp
		} else {
			i++
		}
	}

	return msgs
}

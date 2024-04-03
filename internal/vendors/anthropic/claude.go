package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/reply"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type Claude struct {
	Model            string       `json:"model"`
	MaxTokens        int          `json:"max_tokens"`
	Url              string       `json:"url"`
	AnthropicVersion string       `json:"anthropic-version"`
	AnthropicBeta    string       `json:"anthropic-beta"`
	Raw              bool         `json:"raw"`
	client           *http.Client `json:"-"`
	chat             models.Chat  `json:"-"`
	apiKey           string       `json:"-"`
	username         string       `json:"-"`
	debug            bool         `json:"-"`
}

type claudeReq struct {
	Model     string           `json:"model"`
	Messages  []models.Message `json:"messages"`
	MaxTokens int              `json:"max_tokens"`
	Stream    bool             `json:"stream"`
	System    string           `json:"system"`
}

// Query performs a streamCompletion and appends the returned message to it's internal chat.
// Then it stores the internal chat as prevQuery.json, so that it may be used n upcoming queries
func (c *Claude) Query(ctx context.Context) error {
	nextMsg, err := c.streamCompletions(ctx, c.chat)
	if err != nil {
		return fmt.Errorf("failed to stream completions: %w", err)
	}
	c.chat.Messages = append(c.chat.Messages, nextMsg)
	err = reply.SaveAsPreviousQuery(c.chat.Messages)
	if err != nil {
		return fmt.Errorf("failed to save as previous query: %w", err)
	}
	return nil
}

// TextQuery performs a streamCompletion and appends the returned message to it's internal chat.
// It therefore does not store it to prevQuery.json, and assumes that the calee will deal with
// storing the chat.
func (c *Claude) TextQuery(ctx context.Context, chat models.Chat) (models.Chat, error) {
	nextMsg, err := c.streamCompletions(ctx, chat)
	if err != nil {
		return chat, fmt.Errorf("failed to stream completions: %w", err)
	}
	chat.Messages = append(chat.Messages, nextMsg)
	return chat, nil
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

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
	Model            string `json:"model"`
	MaxTokens        int    `json:"max_tokens"`
	Url              string `json:"url"`
	AnthropicVersion string `json:"anthropic-version"`
	AnthropicBeta    string `json:"anthropic-beta"`
	client           *http.Client
	raw              bool
	chat             models.Chat
	apiKey           string
	username         string
	debug            bool
}

type ClaudeReq struct {
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
	newMsgs := make([]models.Message, 0)
	if msgs[0].Role == "system" {
		newMsgs = msgs[1:]
		msgs = newMsgs
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
	newMsgs = make([]models.Message, 0)
	for i := 0; i < len(msgs); i++ {
		hasMatched := false
		jointString := ""
		for (i+1 < len(msgs)) && msgs[i].Role == "user" && msgs[i+1].Role == "user" {
			hasMatched = true
			jointString = fmt.Sprintf("%v\n%v", jointString, fmt.Sprintf("%v\n%v", msgs[i].Content, msgs[i+1].Content))
			i += 2
		}
		if hasMatched {
			newMsgs = append(newMsgs, models.Message{
				Role:    "user",
				Content: jointString,
			})
		} else {
			hasMatched = false
			newMsgs = append(newMsgs, msgs[i])
		}
	}

	return newMsgs
}

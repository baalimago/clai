package openai

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

var GptDefault = ChatGPT{
	Model:       "gpt-4.1-mini",
	Temperature: 1.0,
	TopP:        1.0,
	URL:         ChatURL,
}

type ChatGPT struct {
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
	URL              string  `json:"url"`

	apiKey string
	debug  bool

	tools []pub_models.LLMTool
	usage *pub_models.Usage

	streamCompleter *generic.StreamCompleter
	useResponses    bool
}

func (g *ChatGPT) Setup() error {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("openai: missing OPENAI_API_KEY")
	}
	g.apiKey = apiKey
	g.debug = misc.Truthy(os.Getenv("DEBUG_OPENAI"))
	if g.Model == "" {
		g.Model = GptDefault.Model
	}
	url, useResponses := selectOpenAIURL(g.Model, g.URL)
	g.URL = url
	g.useResponses = useResponses
	return nil
}

func (g *ChatGPT) RegisterTool(tool pub_models.LLMTool) {
	g.tools = append(g.tools, tool)
}

func (g *ChatGPT) TokenUsage() *pub_models.Usage {
	if g.useResponses {
		return g.usage
	}
	if g.streamCompleter == nil {
		return nil
	}
	return g.streamCompleter.TokenUsage()
}

func (g *ChatGPT) setUsage(usage *pub_models.Usage) error {
	g.usage = usage
	return nil
}

func (g *ChatGPT) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	g.usage = nil
	url, useResponses := selectOpenAIURL(g.Model, g.URL)
	g.URL = url
	g.useResponses = useResponses

	if g.useResponses {
		toolsMapped := make([]responsesTool, 0, len(g.tools))
		for _, t := range g.tools {
			spec := t.Specification()
			toolsMapped = append(toolsMapped, responsesTool{
				Type:        "function",
				Name:        spec.Name,
				Description: spec.Description,
				Parameters:  spec.Inputs,
			})
		}

		s := &responsesStreamer{
			apiKey:      g.apiKey,
			url:         g.URL,
			model:       g.Model,
			debug:       g.debug,
			client:      http.DefaultClient,
			tools:       toolsMapped,
			usageSetter: g.setUsage,
		}

		out, err := s.stream(ctx, chat)
		if err != nil {
			return nil, fmt.Errorf("openai responses: stream: %w", err)
		}
		return out, nil
	}

	sc := &generic.StreamCompleter{}
	if err := sc.Setup("OPENAI_API_KEY", g.URL, "DEBUG_OPENAI"); err != nil {
		return nil, fmt.Errorf("openai chat: setup stream completer: %w", err)
	}
	g.streamCompleter = sc
	g.streamCompleter.Model = g.Model
	g.streamCompleter.MaxTokens = g.MaxTokens
	g.streamCompleter.FrequencyPenalty = &g.FrequencyPenalty
	g.streamCompleter.PresencePenalty = &g.PresencePenalty
	g.streamCompleter.Temperature = &g.Temperature
	g.streamCompleter.TopP = &g.TopP
	toolChoice := "auto"
	g.streamCompleter.ToolChoice = &toolChoice
	for _, tool := range g.tools {
		g.streamCompleter.InternalRegisterTool(tool)
	}

	out, err := g.streamCompleter.StreamCompletions(ctx, chat)
	if err != nil {
		return nil, fmt.Errorf("openai chat: stream: %w", err)
	}
	return out, nil
}

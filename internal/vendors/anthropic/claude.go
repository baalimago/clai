package anthropic

import (
	"maps"
	"net/http"
	"strconv"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Claude is the Anthropic text model configuration.
//
// Sampling rule: models of major version 5 and above reject `temperature`
// (HTTP 400), so it is omitted for them; older models get the configured value.
// The major version is the first dash-separated token of the model id that is
// a 1-2 digit number (e.g. claude-sonnet-5-5 -> 5, claude-3-5-sonnet -> 3);
// 8-digit date suffixes are not versions. An id with no parseable version also
// omits it, since the parameter is optional but a rejected one fails the request.
//
// This is a version rule and not a model list because a list goes stale at the
// vendor's next release and silently brings the 400 back (as gpt-6 did for OpenAI).
type Claude struct {
	Model              string                     `json:"model"`
	MaxTokens          int                        `json:"max_tokens"`
	URL                string                     `json:"url"`
	AnthropicVersion   string                     `json:"anthropic-version"`
	AnthropicBeta      string                     `json:"anthropic-beta"`
	Temperature        float64                    `json:"temperature"`
	TopP               float64                    `json:"top_p"`
	TopK               int                        `json:"top_k"`
	StopSequences      []string                   `json:"stop_sequences"`
	PrintInputCount    bool                       `json:"print_input_count"`
	client             *http.Client               `json:"-"`
	apiKey             string                     `json:"-"`
	debug              bool                       `json:"-"`
	debugFullStreamMsg string                     `json:"-"`
	tools              []pub_models.Specification `json:"-"`
	functionName       string                     `json:"-"`
	functionID         string                     `json:"-"`
	functionJSON       string                     `json:"-"`
	contentBlockType   string                     `json:"-"`
	amInputTokens      int                        `json:"-"`
}

var Default = Claude{
	Model:            "claude-sonnet-5-5",
	URL:              ClaudeURL,
	AnthropicVersion: "2023-06-01",
	AnthropicBeta:    "",
	Temperature:      0.5,
	MaxTokens:        8192,
	StopSequences:    make([]string, 0),
}

const temperatureRejectedFromMajor = 5

func (c *Claude) omitsTemperature() bool {
	major, ok := claudeMajorVersion(c.Model)
	return !ok || major >= temperatureRejectedFromMajor
}

func claudeMajorVersion(model string) (int, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if idx := strings.LastIndexByte(m, '/'); idx >= 0 {
		m = m[idx+1:]
	}
	for tok := range strings.SplitSeq(m, "-") {
		digits := 0
		for digits < len(tok) && tok[digits] >= '0' && tok[digits] <= '9' {
			digits++
		}
		if digits == 0 || digits > 2 || (digits < len(tok) && tok[digits] != '.') {
			continue
		}
		major, err := strconv.Atoi(tok[:digits])
		if err != nil {
			continue
		}
		return major, true
	}
	return 0, false
}

type claudeReq struct {
	Model         string                     `json:"model"`
	Messages      []ClaudeConvMessage        `json:"messages"`
	MaxTokens     int                        `json:"max_tokens,omitempty"`
	Stream        bool                       `json:"stream,omitempty"`
	System        string                     `json:"system,omitempty"`
	Temperature   float64                    `json:"temperature,omitempty"`
	TopP          float64                    `json:"top_p,omitempty"`
	TopK          int                        `json:"top_k,omitempty"`
	StopSequences []string                   `json:"stop_sequences,omitempty"`
	Tools         []pub_models.Specification `json:"tools,omitempty"`
}

// claudifyMessages converts from 'normal' openai chat format into a format which claud prefers
// this is especially important in order to make tooling work, probably reasoning too
func claudifyMessages(msgs []pub_models.Message) []ClaudeConvMessage {
	var ret []ClaudeConvMessage
	if len(msgs) == 0 {
		return ret
	}

	// Start from the second message if the first is a system message
	start := 0
	if msgs[0].Role == "system" {
		start = 1
	}

	for i := start; i < len(msgs); i++ {
		msg := msgs[i]
		role := msg.Role
		if role == "system" {
			role = "assistant"
		}

		var contentBlock any

		if len(msg.ToolCalls) > 0 {
			toolCallMsg := msg.ToolCalls[0]
			tmp := make(map[string]any)
			if toolCallMsg.Inputs != nil {
				tmp = make(map[string]any)
				maps.Copy(tmp, *toolCallMsg.Inputs)
			}
			contentBlock = ToolUseContentBlock{
				Type:  "tool_use",
				ID:    toolCallMsg.ID,
				Name:  toolCallMsg.Name,
				Input: &tmp,
			}
		} else if msg.Role == "tool" {
			role = "user"
			contentBlock = ToolResultContentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Content,
			}
		} else {
			contentBlock = TextContentBlock{
				Type: "text",
				Text: strings.TrimSpace(msg.Content),
			}
		}

		if len(ret) > 0 && ret[len(ret)-1].Role == role {
			ret[len(ret)-1].Content = append(ret[len(ret)-1].Content, contentBlock)
		} else {
			ret = append(ret, ClaudeConvMessage{
				Role:    role,
				Content: []any{contentBlock},
			})
		}
	}

	return ret
}

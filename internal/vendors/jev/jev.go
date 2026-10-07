package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const SystemOneURL = "https://api.typesafe.ai/v1/systemone"

var Default = Jev{
	Model: "jev-latest",
	URL:   SystemOneURL,
}

type Jev struct {
	Model string `json:"model"`
	URL   string `json:"url"`

	apiKey         string
	client         *http.Client
	responseFormat *generic.ResponseFormat
	usage          *pub_models.Usage
}

type systemOneRequest struct {
	State     any            `json:"state"`
	Model     string         `json:"model"`
	Questions map[string]any `json:"questions"`
}

type systemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   *systemOneUsage            `json:"usage"`
}

type systemOneUsage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

func (j *Jev) Setup() error {
	j.apiKey = os.Getenv("TYPESAFE_API_KEY")
	if j.apiKey == "" {
		return fmt.Errorf("jev: missing TYPESAFE_API_KEY")
	}
	if j.Model == "" {
		j.Model = Default.Model
	}
	if j.URL == "" {
		j.URL = SystemOneURL
	}
	if j.client == nil {
		j.client = http.DefaultClient
	}
	return nil
}

func (j *Jev) SetResponseFormat(rf *generic.ResponseFormat) {
	j.responseFormat = rf
}

func (j *Jev) TokenUsage() *pub_models.Usage {
	return j.usage
}

func (j *Jev) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	j.usage = nil
	questions, err := questionsFromResponseFormat(j.responseFormat)
	if err != nil {
		return nil, err
	}
	state, err := stateFromChat(chat)
	if err != nil {
		return nil, err
	}
	request := systemOneRequest{State: state, Model: j.Model, Questions: questions}
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("jev: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.URL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("jev: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+j.apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := j.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jev: send request: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(wrapOptional("jev: read response", readErr), wrapOptional("jev: close response", closeErr))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, generic.ResponseError(resp.StatusCode, body, nil)
	}
	var decoded systemOneResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("jev: decode response: %w", err)
	}
	if decoded.Model == "" {
		return nil, fmt.Errorf("jev: response is missing model")
	}
	if len(decoded.Answers) == 0 {
		return nil, fmt.Errorf("jev: response is missing answers")
	}
	if decoded.Usage == nil {
		return nil, fmt.Errorf("jev: response is missing usage")
	}
	if decoded.Usage.InputTokens == nil || decoded.Usage.OutputTokens == nil {
		return nil, fmt.Errorf("jev: response usage is missing input_tokens or output_tokens")
	}
	j.usage = &pub_models.Usage{
		PromptTokens:     *decoded.Usage.InputTokens,
		CompletionTokens: *decoded.Usage.OutputTokens,
		TotalTokens:      *decoded.Usage.InputTokens + *decoded.Usage.OutputTokens,
	}
	out := make(chan models.CompletionEvent, 2)
	select {
	case out <- string(body):
	case <-ctx.Done():
		return nil, fmt.Errorf("jev: request context ended before response delivery: %w", ctx.Err())
	}
	select {
	case out <- models.StopEvent{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("jev: request context ended before response completion: %w", ctx.Err())
	}
	close(out)
	return out, nil
}

func questionsFromResponseFormat(rf *generic.ResponseFormat) (map[string]any, error) {
	if rf == nil {
		return nil, fmt.Errorf("jev: response format is required; provide Jev questions with -rf")
	}
	if rf.Type != "json_schema" || rf.JSONSchema == nil {
		return nil, fmt.Errorf("jev: response format type must be json_schema with a questions schema")
	}
	questions := rf.JSONSchema.Schema
	if len(questions) == 0 {
		return nil, fmt.Errorf("jev: questions schema must contain at least one question")
	}
	for id, raw := range questions {
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("jev: question id must not be empty")
		}
		question, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("jev: question %q must be an object", id)
		}
		questionType, ok := question["type"].(string)
		if !ok {
			return nil, fmt.Errorf("jev: question %q is missing type", id)
		}
		if questionType != "noul" && questionType != "choice" && questionType != "score" {
			return nil, fmt.Errorf("jev: question %q has unsupported question type %q", id, questionType)
		}
		instructions, ok := question["instructions"]
		if !ok || instructions == nil || isEmptyString(instructions) {
			return nil, fmt.Errorf("jev: question %q requires instructions", id)
		}
		criteria, hasCriteria := question["criteria"]
		switch questionType {
		case "choice":
			options, ok := criteria.(map[string]any)
			if !hasCriteria || !ok || len(options) == 0 {
				return nil, fmt.Errorf("jev: choice question %q criteria must be a non-empty object", id)
			}
		case "score":
			levels, ok := criteria.([]any)
			if !hasCriteria || !ok || len(levels) < 2 || len(levels) > 10 {
				return nil, fmt.Errorf("jev: score question %q criteria must be an array with at least two and at most ten levels", id)
			}
		case "noul":
			if hasCriteria {
				if _, ok := criteria.(map[string]any); !ok {
					return nil, fmt.Errorf("jev: noul question %q criteria must be an object", id)
				}
			}
		}
	}
	return questions, nil
}

func stateFromChat(chat pub_models.Chat) (any, error) {
	state := make([]string, 0, len(chat.Messages))
	for i, message := range chat.Messages {
		if message.Role != "user" {
			continue
		}
		if len(message.ContentParts) == 0 {
			if strings.TrimSpace(message.Content) != "" {
				state = append(state, message.Content)
			}
			continue
		}
		var text strings.Builder
		for _, part := range message.ContentParts {
			if part.Type == "text" {
				text.WriteString(part.Text)
				continue
			}
			return nil, fmt.Errorf("jev: user message %d contains unsupported non-text content", i)
		}
		if strings.TrimSpace(text.String()) != "" {
			state = append(state, text.String())
		}
	}
	if len(state) == 0 {
		return nil, fmt.Errorf("jev: chat must contain a non-empty user message")
	}
	if len(state) == 1 {
		return state[0], nil
	}
	return state, nil
}

func isEmptyString(value any) bool {
	s, ok := value.(string)
	return ok && strings.TrimSpace(s) == ""
}

func wrapOptional(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

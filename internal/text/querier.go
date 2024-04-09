package text

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/reply"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type Querier[C models.StreamCompleter] struct {
	Url       string
	Raw       bool
	chat      models.Chat
	username  string
	termWidth int
	lineCount int
	line      string
	fullMsg   string
	debug     bool
	Model     C
}

func vendorType(fromModel string) (string, string, string) {
	if strings.Contains(fromModel, "gpt") {
		return "openai", "gpt", fromModel
	}
	if strings.Contains(fromModel, "claude") {
		return "anthropic", "claude", fromModel
	}
	if strings.Contains(fromModel, "mock") {
		return "mock", "mock", "mock"
	}

	return "VENDOR", "NOT", "FOUND"
}

func NewQuerier[C models.StreamCompleter](userConf Configurations, dfault C) (Querier[C], error) {
	vendor, model, modelVersion := vendorType(userConf.Model)
	configPath := path.Join(userConf.ConfigDir, ".clai", fmt.Sprintf("%v_%v_%v.json", vendor, model, modelVersion))
	querier := Querier[C]{}
	var modelConf C
	err := tools.ReadAndUnmarshal(configPath, &modelConf)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			data, err := json.Marshal(dfault)
			if err != nil {
				return querier, fmt.Errorf("failed to marshal default model: %v, error: %w", dfault, err)
			}
			err = os.WriteFile(configPath, data, os.FileMode(0o644))
			if err != nil {
				return querier, fmt.Errorf("failed to write default model: %v, error: %w", dfault, err)
			}

			err = tools.ReadAndUnmarshal(configPath, &modelConf)
			if err != nil {
				return querier, fmt.Errorf("failed to read default model: %v, error: %w", dfault, err)
			}
		} else {
			return querier, fmt.Errorf("failed to load querier of model: %v, error: %w", userConf.Model, err)
		}
	}

	err = modelConf.Setup()
	if err != nil {
		return Querier[C]{}, fmt.Errorf("failed to setup model: %w", err)
	}

	termWidth, err := tools.TermWidth()
	querier.termWidth = termWidth
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to get terminal size: %v\n", err))
	}
	currentUser, err := user.Current()
	if err == nil {
		querier.username = currentUser.Username
	} else {
		querier.username = "user"
	}
	querier.Model = modelConf
	querier.chat = userConf.InitialPrompt
	if misc.Truthy(os.Getenv("DEBUG")) {
		querier.debug = true
	}
	return querier, nil
}

// Query using the underlying model to stream completions and then print the output
// from the model to stdout. Blocking operation.
func (q *Querier[C]) Query(ctx context.Context) error {
	completionsChan, err := q.Model.StreamCompletions(ctx, q.chat)
	if err != nil {
		return fmt.Errorf("failed to stream completions: %w", err)
	}

	defer func() {
		err := reply.SaveAsPreviousQuery(q.chat.Messages)
		if err != nil {
			ancli.PrintErr(fmt.Sprintf("failed to save previous query: %v\n", err))
		}
		if q.Raw {
			return
		}

		if q.termWidth > 0 {
			tools.ClearTermTo(q.termWidth, q.lineCount)
		} else {
			fmt.Println()
		}
		tools.AttemptPrettyPrint(models.Message{
			Role:    "system",
			Content: q.fullMsg,
		}, q.username)
	}()

	for {
		select {
		case completion, ok := <-completionsChan:
			// Channel most likely gracefully closed
			if !ok {
				return nil
			}
			err := q.handleCompletion(completion)
			if err != nil {
				// check if error is context canceled or EOF, return nil as these are expected and handeled elsewhere
				if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
					return nil
				}
				return fmt.Errorf("failed to handle completion: %w", err)
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (q *Querier[C]) reset() {
	q.fullMsg = ""
	q.line = ""
	q.lineCount = 0
}

func (q *Querier[C]) TextQuery(ctx context.Context, chat models.Chat) (models.Chat, error) {
	q.reset()
	q.chat = chat
	err := q.Query(ctx)
	if err != nil {
		return models.Chat{}, fmt.Errorf("failed to query: %w", err)
	}
	q.chat.Messages = append(q.chat.Messages, models.Message{
		Role:    "system",
		Content: q.fullMsg,
	})
	if q.debug {
		ancli.PrintOK(fmt.Sprintf("chat: %v", q.chat))
	}
	return q.chat, nil
}

func (q *Querier[C]) handleCompletion(completion models.CompletionEvent) error {
	switch cast := completion.(type) {
	case string:
		q.handleToken(cast)
		return nil
	case error:
		return fmt.Errorf("completion stream error: %w", cast)
	default:
		return fmt.Errorf("unknown completion type: %v", completion)
	}
}

func (q *Querier[C]) handleToken(token string) {
	if q.termWidth > 0 {
		tools.UpdateMessageTerminalMetadata(token, &q.line, &q.lineCount, q.termWidth)
	}
	q.fullMsg += token
	fmt.Print(token)
}

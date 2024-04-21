package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/reply"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type Querier[C models.StreamCompleter] struct {
	Url             string
	Raw             bool
	chat            models.Chat
	username        string
	termWidth       int
	lineCount       int
	line            string
	fullMsg         string
	configDir       string
	debug           bool
	shouldSaveReply bool
	hasPrinted      bool
	Model           C
}

// Query using the underlying model to stream completions and then print the output
// from the model to stdout. Blocking operation.
func (q *Querier[C]) Query(ctx context.Context) error {
	completionsChan, err := q.Model.StreamCompletions(ctx, q.chat)
	if err != nil {
		return fmt.Errorf("failed to stream completions: %w", err)
	}

	defer q.postProcess()

	for {
		select {
		case completion, ok := <-completionsChan:
			// Channel most likely gracefully closed
			if !ok {
				return nil
			}
			err := q.handleCompletion(ctx, completion)
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

func (q *Querier[C]) postProcess() {
	// This is to ensure that it only post-processes once in recursive calls
	if q.hasPrinted {
		return
	}
	q.hasPrinted = true
	chatMsgscopy := make([]models.Message, len(q.chat.Messages))
	copy(chatMsgscopy, q.chat.Messages)
	newSysMsg := models.Message{
		Role:    "system",
		Content: q.fullMsg,
	}
	chatMsgscopy = append(chatMsgscopy, newSysMsg)
	if q.shouldSaveReply {
		err := reply.SaveAsPreviousQuery(q.configDir, chatMsgscopy)
		if err != nil {
			ancli.PrintErr(fmt.Sprintf("failed to save previous query: %v\n", err))
		}
	}

	// The token should already have been printed while streamed
	if q.Raw {
		return
	}

	if q.termWidth > 0 {
		utils.UpdateMessageTerminalMetadata(q.fullMsg, &q.line, &q.lineCount, q.termWidth)
		utils.ClearTermTo(q.termWidth, q.lineCount-1)
	} else {
		fmt.Println()
	}
	utils.AttemptPrettyPrint(newSysMsg, q.username, q.Raw)
}

func (q *Querier[C]) reset() {
	q.fullMsg = ""
	q.line = ""
	q.lineCount = 0
	q.hasPrinted = false
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

func (q *Querier[C]) handleCompletion(ctx context.Context, completion models.CompletionEvent) error {
	switch cast := completion.(type) {
	case tools.Call:
		return q.handleFunctionCall(ctx, cast)
	case string:
		q.handleToken(cast)
		return nil
	case error:
		return fmt.Errorf("completion stream error: %w", cast)
	default:
		return fmt.Errorf("unknown completion type: %v", completion)
	}
}

// handleFunctionCall by invoking the call, and then resondng to the ai with the output
func (q *Querier[C]) handleFunctionCall(ctx context.Context, call tools.Call) error {
	// Whatever is in q.fullMessage now is what the AI has streamed before the function call
	// which normally is handeled by the supercallee of Query, now we need to handle it here
	// There's room for improvement of this system..
	systemPreCallMessage := models.Message{
		Role:    "system",
		Content: q.fullMsg,
	}
	q.chat.Messages = append(q.chat.Messages, systemPreCallMessage)
	// Post process here since a function call should be treated as the function call
	// should be handeled mid-stream, but still requires multiple rounds of user input
	q.postProcess()
	systemToolsCall := models.Message{
		Role:    "tool",
		Content: fmt.Sprintf("retrieved funtion call struct from AI:\n%v", call.Json()),
	}
	q.chat.Messages = append(q.chat.Messages, systemToolsCall)
	q.reset()
	err := utils.AttemptPrettyPrint(systemToolsCall, "tool", q.Raw)
	if err != nil {
		return fmt.Errorf("failed to pretty print, stopping before tool invocation: %w", err)
	}

	out := tools.Invoke(call)
	toolsOutput := models.Message{
		Role:    "tool",
		Content: out,
	}
	q.chat.Messages = append(q.chat.Messages, toolsOutput)
	if q.debug || q.Raw {
		err = utils.AttemptPrettyPrint(toolsOutput, "tool", q.Raw)
		if err != nil {
			return fmt.Errorf("failed to pretty print, stopping before tool call return: %w", err)
		}
	} else {
		smallOutputMsg := models.Message{
			Role:    "tool",
			Content: shortenedOutput(out),
		}
		err = utils.AttemptPrettyPrint(smallOutputMsg, "tool", q.Raw)
		if err != nil {
			return fmt.Errorf("failed to pretty print, stopping before tool call return: %w", err)
		}
	}
	// Slight hack
	if call.Name == "test" {
		return nil
	}
	_, err = q.TextQuery(ctx, q.chat)
	if err != nil {
		return fmt.Errorf("failed to query after tool call: %w", err)
	}
	return nil
}

// shortenedOutput returns a shortened version of the output
func shortenedOutput(out string) string {
	maxTokens := 20
	outSplit := strings.Split(out, " ")
	outNewlineSplit := strings.Split(out, "\n")
	firstTokens := utils.GetFirstTokens([]string{out}, maxTokens)
	if len(firstTokens) < 20 {
		return out
	}
	firstTokensStr := strings.Join(firstTokens, " ")
	amLeft := len(outSplit) - maxTokens
	abbreviationType := "tokens"
	if len(outNewlineSplit) > 5 {
		firstTokensStr = strings.Join(utils.GetFirstTokens(outNewlineSplit, 5), "\n")
		amLeft = len(outNewlineSplit) - 5
		abbreviationType = "lines"
	}
	return fmt.Sprintf("%v\n...[and %v more %v]", firstTokensStr, amLeft, abbreviationType)
}

func (q *Querier[C]) handleToken(token string) {
	q.fullMsg += token
	fmt.Print(token)
}

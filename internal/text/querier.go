package text

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/reply"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
)

const (
	TOKEN_COUNT_FACTOR     = 1.1
	MAX_SHORTENED_NEWLINES = 5
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
	tokenWarnLimit  int
	cmdMode         bool
	execErr         error
}

// Query using the underlying model to stream completions and then print the output
// from the model to stdout. Blocking operation.
func (q *Querier[C]) Query(ctx context.Context) error {
	err := q.tokenLengthWarning()
	if err != nil {
		return fmt.Errorf("Querier.Query: %w", err)
	}
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
				if q.debug {
					ancli.PrintOK("exiting querier due to closed channel\n")
				}
				return nil
			}
			err := q.handleCompletion(ctx, completion)
			if err != nil {
				// check if error is context canceled or EOF, return nil as these are expected and handeled elsewhere
				if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
					if q.debug {
						ancli.PrintOK("exiting querier due to EOF error\n")
					}
					return nil
				}
				// Only add error if its not EOF or context.Canceled
				q.execErr = err

				if q.debug {
					ancli.PrintOK("exiting querier due to EOF error\n")
				}
				return fmt.Errorf("failed to handle completion: %w", err)
			}
		case <-ctx.Done():
			if q.debug {
				ancli.PrintOK("exiting querier due to context cancelation\n")
			}
			return nil
		}
	}
}

func (q *Querier[C]) tokenLengthWarning() error {
	amTokens := q.countTokens()
	if q.tokenWarnLimit > 0 && amTokens > q.tokenWarnLimit {
		ancli.PrintWarn(
			fmt.Sprintf("You're about to send: ~%v tokens to the model, which may amount to: ~$%.3f (applying worst input rates as of 2024-05). This limit may be changed in: '%v'. Do you wish to continue? [yY]: ",
				amTokens,
				// Worst rates found at 2024-05 were gpt-4-32k at $60 per 1M tokens
				float64(amTokens)*(float64(60)/float64(1000000)),
				path.Join(q.configDir, "textConfig.json"),
			))
		var userInput string
		reader := bufio.NewReader(os.Stdin)
		userInput, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read user input: %w", err)
		}
		switch userInput {
		case "y\n", "Y\n":
			// Continue on y or Y
		default:
			return errors.New("Querier.tokenLengthWarning: query canceled due to token amount check")
		}
	}
	return nil
}

// countTokens by simply counting the amount of strings which are delimited by whitespace
// and multiply by some factor. This factor is somewhat arbritrary, and adjusted to be good enough
// for all the different models
func (q *Querier[C]) countTokens() int {
	ret := 0
	for _, msg := range q.chat.Messages {
		ret += len(strings.Split(msg.Content, " "))
	}
	return int(float64(ret) * TOKEN_COUNT_FACTOR)
}

func (q *Querier[C]) postProcess() {
	// This is to ensure that it only post-processes once in recursive calls
	if q.hasPrinted {
		return
	}
	// Nothing to post process if message for some reason is empty (happens during tools calls sometimes)
	if q.fullMsg == "" {
		return
	}
	q.hasPrinted = true
	newSysMsg := models.Message{
		Role:    "system",
		Content: q.fullMsg,
	}
	q.chat.Messages = append(q.chat.Messages, newSysMsg)
	if q.shouldSaveReply {
		err := reply.SaveAsPreviousQuery(q.configDir, q.chat.Messages)
		if err != nil {
			ancli.PrintErr(fmt.Sprintf("failed to save previous query: %v\n", err))
		}
	}

	if q.debug {
		ancli.PrintOK(fmt.Sprintf("Querier.postProcess:\n%v\n", debug.IndentedJsonFmt(q)))
	}

	// Cmd mode is a bit of a hack, it will handle all output
	if q.cmdMode {
		err := q.handleCmdMode()
		if err != nil {
			ancli.PrintErr(fmt.Sprintf("Querier.postProcess: %v\n", err))
		}
		return
	}

	q.postProcessOutput(newSysMsg)
}

func (q *Querier[C]) postProcessOutput(newSysMsg models.Message) {
	// The token should already have been printed while streamed
	if q.Raw {
		return
	}

	if q.termWidth > 0 {
		utils.UpdateMessageTerminalMetadata(q.fullMsg, &q.line, &q.lineCount, q.termWidth)
		// Write the details of q to the file determined by the environment variable DEBUG_OUTPUT_FILE
		if debugOutputFile := os.Getenv("DEBUG_OUTPUT_FILE"); debugOutputFile != "" {
			file, err := os.Create(debugOutputFile)
			if err != nil {
				ancli.PrintErr(fmt.Sprintf("failed to create debug output file: %v\n", err))
			} else {
				defer file.Close()
				_, err = file.WriteString(debug.IndentedJsonFmt(struct {
					FullMessage string
					Line        string
					LineCount   int
					TermWidth   int
				}{
					FullMessage: q.fullMsg,
					Line:        q.line,
					LineCount:   q.lineCount,
					TermWidth:   q.termWidth,
				}))
				if err != nil {
					ancli.PrintErr(fmt.Sprintf("failed to write to debug output file: %v\n", err))
				}
			}
		}
		utils.ClearTermTo(q.termWidth, q.lineCount-1)
	} else {
		fmt.Println()
	}
	utils.AttemptPrettyPrint(newSysMsg, q.username, q.Raw)
}

func (q *Querier[C]) reset() {
	q.execErr = nil
	q.fullMsg = ""
	q.line = ""
	q.lineCount = 0
	q.hasPrinted = false
}

func (q *Querier[C]) TextQuery(ctx context.Context, chat models.Chat) (models.Chat, error) {
	q.reset()
	q.chat = chat
	// Query will update the chat with the latest system message
	err := q.Query(ctx)
	if err != nil {
		return models.Chat{}, fmt.Errorf("TextQuery: %w", err)
	}
	if q.debug {
		ancli.PrintOK(fmt.Sprintf("Querier.TextQuery:\n%v", debug.IndentedJsonFmt(q)))
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
	case models.NoopEvent:
		return nil
	case nil:
		if q.debug {
			ancli.PrintWarn("received nil completion event, which is slightly weird, but not necessarily an error")
		}
		return nil
	default:
		return fmt.Errorf("unknown completion type: %v", completion)
	}
}

// handleFunctionCall by invoking the call, and then resondng to the ai with the output
func (q *Querier[C]) handleFunctionCall(ctx context.Context, call tools.Call) error {
	if q.cmdMode {
		return errors.New("cant call tools in cmd mode")
	}
	// Whatever is in q.fullMessage now is what the AI has streamed before the function call
	// which normally is handeled by the supercallee of Query, now we need to handle it here.
	// There's room for improvement of this system..
	if q.fullMsg != "" {
		systemPreCallMessage := models.Message{
			Role:    "system",
			Content: q.fullMsg,
		}
		q.chat.Messages = append(q.chat.Messages, systemPreCallMessage)
	}

	if q.debug {
		ancli.PrintOK(fmt.Sprintf("received tool call: %v", call))
	}
	// Post process here since a function call should be treated as the function call
	// should be handeled mid-stream, but still requires multiple rounds of user input
	q.postProcess()

	// Fill some fields to make the chatgpt function spec happy
	if call.ID == "" {
		call.ID = "now-chatgpt-is-happy"
	}
	if call.Type == "" {
		call.Type = "function"
	}
	if call.Function.Name == "" {
		if call.Name == "" {
			call.Name = "EMPTY-STRING"
		}
		call.Function.Name = call.Name
	}
	if call.Function.Arguments == "" {
		call.Function.Arguments = call.JSON()
	}
	assistantToolsCall := models.Message{
		Role:      "assistant",
		Content:   call.PrettyPrint(),
		ToolCalls: []tools.Call{call},
	}
	q.reset()
	err := utils.AttemptPrettyPrint(assistantToolsCall, q.username, q.Raw)
	if err != nil {
		return fmt.Errorf("failed to pretty print, stopping before tool invocation: %w", err)
	}
	q.chat.Messages = append(q.chat.Messages, assistantToolsCall)

	out := tools.Invoke(call)
	// Chatgpt doesn't like responses which yield no output, even if they're valid (ls on empty dir)
	if out == "" {
		out = "<EMPTY-RESPONSE>"
	}
	toolsOutput := models.Message{
		Role:       "tool",
		Content:    out,
		ToolCallID: call.ID,
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
	maxRunes := 100
	outSplit := strings.Split(out, " ")
	outNewlineSplit := strings.Split(out, "\n")
	firstTokens := utils.GetFirstTokens(outSplit, maxTokens)
	amRunes := utf8.RuneCountInString(out)
	if len(firstTokens) < maxTokens && len(outNewlineSplit) < MAX_SHORTENED_NEWLINES && amRunes < maxRunes {
		return out
	}
	if amRunes > maxRunes {
		return fmt.Sprintf("%v... and %v more runes", out[:maxRunes], amRunes-maxRunes)
	}
	firstTokensStr := strings.Join(firstTokens, " ")
	amLeft := len(outSplit) - maxTokens
	abbreviationType := "tokens"
	if len(outNewlineSplit) > MAX_SHORTENED_NEWLINES {
		firstTokensStr = strings.Join(utils.GetFirstTokens(outNewlineSplit, MAX_SHORTENED_NEWLINES), "\n")
		amLeft = len(outNewlineSplit) - MAX_SHORTENED_NEWLINES
		abbreviationType = "lines"
	}
	return fmt.Sprintf("%v\n...[and %v more %v]", firstTokensStr, amLeft, abbreviationType)
}

func (q *Querier[C]) handleToken(token string) {
	q.fullMsg += token
	fmt.Print(token)
}

package text

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	rdebug "runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const (
	TokenCountFactor     = 1.1
	MaxShortenedNewlines = 5
	RateLimitRetries     = 3
	FallbackWaitDuration = 20 * time.Second
)

type Querier[C models.StreamCompleter] struct {
	Raw                     bool
	chat                    pub_models.Chat
	username                string
	termWidth               int
	lineCount               int
	line                    string
	fullMsg                 string
	configDir               string
	debug                   bool
	debugTextQuerierPrinted bool
	shouldSaveReply         bool
	hasPrinted              bool
	Model                   C
	tokenWarnLimit          int
	toolOutputRuneLimit     int
	cmdMode                 bool
	execErr                 error
	rateLimitRetries        int
	rateLimitLastAmTokens   int
	rateLimitRecursionLevel int

	// isLikelyGemini3Preview is set to true if it's likely that the current underlying model
	// is the gemini 3 preview which suffers from an issue where it insists on crashing if there
	// is no "though_signature" within extra content, while also sending requests which lack "though_signature"
	//
	// Maybe one day this hack can be removed.
	isLikelyGemini3Preview bool
}

func (q *Querier[C]) handleRateLimitErr(ctx context.Context, rateLimitErr models.ErrRateLimit) error {
	q.rateLimitRetries++
	counter, ok := any(q.Model).(models.InputTokenCounter)
	if ok {
		inCount, err := counter.CountInputTokens(ctx, q.chat)
		if err != nil {
			return fmt.Errorf("failed to count tokens: %w", err)
		}
		waitDur := time.Until(rateLimitErr.ResetAt)
		if waitDur < time.Second {
			ancli.Warnf("rate limit wait duration less than 1 second, setting to %v", FallbackWaitDuration)
			waitDur = FallbackWaitDuration
		}
		// Increase wait time if the rate limit 'didnt work', as in, gradually reduce amount of tokens
		// which can be used. But only by a factor of 20%
		if inCount < int(float64(q.rateLimitLastAmTokens)*0.8) {
			waitDur *= 2
			ancli.Warnf("am of input tokens is: %v, which is: %v lower than last. Exp-increasing sleep to: %v",
				inCount,
				q.rateLimitLastAmTokens-inCount,
				waitDur,
			)
		}
		time.Sleep(waitDur)
		q.rateLimitLastAmTokens = inCount
		summarizedChat, circumErr := generic.CircumventRateLimit(ctx,
			q,
			q.chat,
			inCount,
			rateLimitErr.TokensRemaining,
			rateLimitErr.MaxInputTokens,
			rateLimitErr.ResetAt,
			q.rateLimitRecursionLevel,
		)
		q.rateLimitRecursionLevel++
		if circumErr != nil {
			return fmt.Errorf("failed to circumvent rate limit: %w", circumErr)
		}
		// Replace existing chat with summarized chat
		q.chat = summarizedChat

		// Retry by using the new chat and querying once more. Will fill call stack.
		q.reset()
		return q.Query(ctx)
	} else {
		// No fancy logic, just sleep a while
		ancli.Warnf("detected rate limit at: %v tokens, will sleep until: %v\n", rateLimitErr.TokensRemaining, rateLimitErr.ResetAt)
		time.Sleep(time.Until(rateLimitErr.ResetAt.Add(time.Second * 10)))
		// Recursively call. This will look a bit wonky but should cause no side effects as post process
		// deferral is called below
		q.reset()
		return q.Query(ctx)
	}
}

// Query using the underlying model to stream completions and then print the output
// from the model to stdout. Blocking operation.
func (q *Querier[C]) Query(ctx context.Context) error {
	if q.rateLimitRetries > RateLimitRetries {
		return fmt.Errorf("rate limit retry limit exceeded (%v), giving up", RateLimitRetries)
	}
	err := q.tokenLengthWarning()
	if err != nil {
		return fmt.Errorf("Querier.Query: %w", err)
	}
	completionsChan, err := q.Model.StreamCompletions(ctx, q.chat)
	if err != nil {
		var rateLimitErr *models.ErrRateLimit
		if errors.As(err, &rateLimitErr) {
			return q.handleRateLimitErr(ctx, *rateLimitErr)
		}
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
				// where is "elsewhere?" not 100% sure. - LK 25-11
				if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
					if q.debug {
						ancli.PrintOK("exiting querier due to EOF error\n")
					}
					q.reset()
					return nil
				}
				// Only add error if its not EOF or context.Canceled
				q.execErr = err
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
			fmt.Sprintf("You're about to send: ~%v tokens to the model, which may amount to: ~$%.3f (using $3 /1 million tokens). This limit may be changed in: '%v'. Do you wish to continue? [yY]: ",
				amTokens,
				// Average rate at 25-06 at $3/1M tokens
				float64(amTokens)*(float64(3)/float64(1000000)),
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
// for all the different models. Each model has its own idea of what a 'token' is, and since this
// check is done before the corpus reaches llm we don't know how many tokens they consider it to be
func (q *Querier[C]) countTokens() int {
	ret := 0
	for _, msg := range q.chat.Messages {
		ret += len(strings.Split(msg.Content, " "))
	}
	return int(float64(ret) * TokenCountFactor)
}

func (q *Querier[C]) postProcess() {
	if q.Raw {
		// Print a new line, otherwise cursor remains on the same position on
		// the next contet block
		fmt.Println()
	}
	// This is to ensure that it only post-processes once in recursive calls
	if q.hasPrinted {
		return
	}
	// Nothing to post process if message for some reason is empty (happens during tools calls sometimes)
	if q.fullMsg == "" {
		return
	}
	q.hasPrinted = true
	newSysMsg := pub_models.Message{
		Role:    "system",
		Content: q.fullMsg,
	}
	q.chat.Messages = append(q.chat.Messages, newSysMsg)
	if q.shouldSaveReply {
		err := chat.SaveAsPreviousQuery(q.configDir, q.chat.Messages)
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

func (q *Querier[C]) postProcessOutput(newSysMsg pub_models.Message) {
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
	q.rateLimitRetries = 0
}

func (q *Querier[C]) TextQuery(ctx context.Context, chat pub_models.Chat) (pub_models.Chat, error) {
	q.reset()
	q.chat = chat
	// Query will update the chat with the latest system message
	err := q.Query(ctx)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("TextQuery: %w", err)
	}
	if q.debug && !q.debugTextQuerierPrinted {
		q.debugTextQuerierPrinted = true
		ancli.PrintOK(fmt.Sprintf("Querier.TextQuery:\n%v", debug.IndentedJsonFmt(q)))
	}

	return q.chat, nil
}

func (q *Querier[C]) handleCompletion(ctx context.Context, completion models.CompletionEvent) error {
	switch cast := completion.(type) {
	case pub_models.Call:
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

// checkIfGemini3Preview will check for thought_signature and if one is detected, return true
// otherwise false. It will also return true if q.isLikelyGemini3Preview is true.
func (q *Querier[C]) checkIfGemini3Preview(call pub_models.Call) bool {
	if q.isLikelyGemini3Preview {
		return q.isLikelyGemini3Preview
	}
	if call.ExtraContent == nil {
		return false
	}

	googleExtraContent, isGoogle := call.ExtraContent["google"]
	if !isGoogle {
		return false
	}
	googleExtraContentAsMap, _ := googleExtraContent.(map[string]any)
	_, hasThoughSignature := googleExtraContentAsMap["thought_signature"]
	ancli.Noticef("detected that its likely gemini 3 preview: %v", hasThoughSignature)
	return hasThoughSignature
}

// handleFunctionCall by invoking the call, and then resondng to the ai with the output
func (q *Querier[C]) handleFunctionCall(ctx context.Context, call pub_models.Call) error {
	if q.cmdMode {
		return errors.New("cant call tools in cmd mode")
	}

	if q.debug || misc.Truthy(os.Getenv("DEBUG_CALL")) {
		ancli.PrintOK(fmt.Sprintf("received tool call: %v", debug.IndentedJsonFmt(call)))
	}

	q.isLikelyGemini3Preview = q.checkIfGemini3Preview(call)

	if q.isLikelyGemini3Preview {
		if call.ExtraContent == nil {
			// Return nil if gemini 3 preview tries to call a tools call without
			// having extra content. This will break execution and dodge any post processing
			// by design, as the call from gemini is faulty. It seems to only happen
			// when the "true" call chain is complete and gemini actually wishes to end
			// the conversation/return to user
			return nil
		}
	}

	// Post process here since a function call should be treated as the function call
	// should be handeled mid-stream, but still requires multiple rounds of user input
	pre := q.shouldSaveReply
	q.shouldSaveReply = false
	q.postProcess()
	q.shouldSaveReply = pre

	call.Patch()

	assistantToolsCall := pub_models.Message{
		Role:      "assistant",
		Content:   call.PrettyPrint(),
		ToolCalls: []pub_models.Call{call},
	}
	q.reset()
	if !q.debug {
		err := utils.AttemptPrettyPrint(assistantToolsCall, q.username, q.Raw)
		if err != nil {
			return fmt.Errorf("failed to pretty print, stopping before tool invocation: %w", err)
		}
	}

	q.chat.Messages = append(q.chat.Messages, assistantToolsCall)

	out := tools.Invoke(call)
	out = limitToolOutput(out, q.toolOutputRuneLimit)
	// Chatgpt doesn't like responses which yield no output, even if they're valid (ls on empty dir)
	if out == "" {
		out = "<EMPTY-RESPONSE>"
	}
	toolsOutput := pub_models.Message{
		Role:       "tool",
		Content:    out,
		ToolCallID: call.ID,
	}
	q.chat.Messages = append(q.chat.Messages, toolsOutput)
	if q.Raw {
		err := utils.AttemptPrettyPrint(toolsOutput, "tool", q.Raw)
		if err != nil {
			return fmt.Errorf("failed to pretty print, stopping before tool call return: %w", err)
		}
	} else if q.debug {
		// NOOP, no printing
	} else {
		toolPrintContent := out
		if !strings.Contains(toolPrintContent, "mcp_") {
			toolPrintContent = shortenedOutput(out)
		}
		smallOutputMsg := pub_models.Message{
			Role:    "tool",
			Content: toolPrintContent,
		}
		err := utils.AttemptPrettyPrint(smallOutputMsg, "tool", q.Raw)
		if err != nil {
			return fmt.Errorf("failed to pretty print, stopping before tool call return: %w", err)
		}
	}
	// Slight hack
	if call.Name == "test" {
		return nil
	}
	_, err := q.TextQuery(ctx, q.chat)
	if err != nil {
		ancli.PrintErr("stack trace:\n" + string(rdebug.Stack()))
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
	if len(firstTokens) < maxTokens && len(outNewlineSplit) < MaxShortenedNewlines && amRunes < maxRunes {
		return out
	}
	if amRunes > maxRunes {
		return fmt.Sprintf("%v... and %v more runes", out[:maxRunes], amRunes-maxRunes)
	}
	firstTokensStr := strings.Join(firstTokens, " ")
	amLeft := len(outSplit) - maxTokens
	abbreviationType := "tokens"
	if len(outNewlineSplit) > MaxShortenedNewlines {
		firstTokensStr = strings.Join(utils.GetFirstTokens(outNewlineSplit, MaxShortenedNewlines), "\n")
		amLeft = len(outNewlineSplit) - MaxShortenedNewlines
		abbreviationType = "lines"
	}
	return fmt.Sprintf("%v\n...[and %v more %v]", firstTokensStr, amLeft, abbreviationType)
}

func limitToolOutput(out string, limit int) string {
	if limit <= 0 {
		return out
	}
	amRunes := utf8.RuneCountInString(out)
	if amRunes <= limit {
		return out
	}
	return fmt.Sprintf(
		"%v... and %v more characters. The tool's output has been restricted as it's too long. Please concentrate your tool calls to reduce the amount of tokens used!",
		out[:limit], amRunes-limit)
}

func (q *Querier[C]) handleToken(token string) {
	q.fullMsg += token
	if !q.debug {
		fmt.Print(token)
	}
}

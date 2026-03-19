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
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
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
	callStackLevel          int
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
	rateLimitLastAmTokens   int

	// Output of the querier. This is used mostly when Querier is invoked as an agent
	out io.Writer

	// isLikelyGemini3Preview is set to true if it's likely that the current underlying model
	// is the gemini 3 preview which suffers from an issue where it insists on crashing if there
	// is no "though_signature" within extra content, while also sending requests which lack "though_signature"
	//
	// Maybe one day this hack can be removed.
	isLikelyGemini3Preview bool

	maxToolCalls *int
	amToolCalls  int

	costManager       CostManager
	costMgrRdyChan    <-chan struct{}
	costMgrErrChan    <-chan error
	callUsageRecorder CallUsageRecorder
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
		utils.ClearTermTo(q.out, q.termWidth, q.lineCount-1)
	} else {
		fmt.Println()
	}
	utils.AttemptPrettyPrint(q.out, newSysMsg, q.username, q.Raw)
}

func (q *Querier[C]) postProcess() {
	session := &QuerySession{
		Chat:               q.chat,
		ShouldSaveReply:    q.shouldSaveReply,
		Raw:                q.Raw,
		FinalAssistantText: q.fullMsg,
		FinalUsage:         q.chat.TokenUsage,
		Finalized:          q.hasPrinted,
		Line:               q.line,
		LineCount:          q.lineCount,
	}
	sessionFinalizer[C]{querier: q}.Finalize(session)
	q.chat = session.Chat
	q.fullMsg = session.FinalAssistantText
	q.line = session.Line
	q.lineCount = session.LineCount
	q.hasPrinted = session.Finalized
}

func (q *Querier[C]) resetTransientState() {
	q.fullMsg = ""
	q.line = ""
	q.lineCount = 0
	q.hasPrinted = false
}

func (q *Querier[C]) handleToken(token string) {
	w := q.out
	if w == nil {
		w = os.Stdout
	}
	q.fullMsg += token
	if !q.debug {
		fmt.Fprint(w, token)
	}
}

func (q *Querier[C]) handleTokenForSession(session *QuerySession, token string) {
	w := q.out
	if w == nil {
		w = os.Stdout
	}
	session.AppendPendingText(token)
	q.fullMsg = session.PendingTextString()
	if !q.debug {
		fmt.Fprint(w, token)
	}
}

func (q *Querier[C]) currentTokenUsage() *pub_models.Usage {
	tokenCounter, isModelCounter := any(q.Model).(models.UsageTokenCounter)
	if !isModelCounter {
		if q.debug {
			ancli.Okf("is not usage token counter")
		}
		return nil
	}
	if q.debug && tokenCounter.TokenUsage() != nil {
		ancli.Okf("token usage: %v", *tokenCounter.TokenUsage())
	}
	return tokenCounter.TokenUsage()
}

// Query using the underlying model to stream completions and then print the output
// from the model to stdout. Blocking operation.
func (q *Querier[C]) Query(ctx context.Context) error {
	// Catch-all in the csae that stdout isn't set
	if q.out == nil {
		q.out = os.Stdout
	}
	session := &QuerySession{
		Chat:            q.chat,
		ShouldSaveReply: q.shouldSaveReply,
		Raw:             q.Raw,
		Line:            q.line,
		LineCount:       q.lineCount,
	}
	runner := sessionRunner[C]{
		querier:      q,
		recorder:     q.callUsageRecorder,
		toolExecutor: toolExecutor[C]{querier: q},
		finalizer:    sessionFinalizer[C]{querier: q},
	}
	err := runner.Run(ctx, session)
	q.chat = session.Chat
	q.fullMsg = session.FinalAssistantText
	q.line = session.Line
	q.lineCount = session.LineCount
	q.hasPrinted = session.Finalized
	q.amToolCalls = session.ToolCallsUsed
	q.isLikelyGemini3Preview = session.LikelyGeminiPreview
	return err
}

func (q *Querier[C]) TextQuery(ctx context.Context, chat pub_models.Chat) (pub_models.Chat, error) {
	q.resetTransientState()
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

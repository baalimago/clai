package display

import "github.com/baalimago/clai/internal/jsonl"

// ToolCall is one tool invocation the model requested.
type ToolCall struct {
	Name   string
	Inputs map[string]any
}

// ToolResult is the text one tool invocation returned to the model.
type ToolResult struct {
	Name   string
	Output string
}

// Progress is the state of one row of a board, or of a status line when Row
// is empty. State is one of the progress states of F14.
type Progress struct {
	Title  string
	Row    string
	State  string
	Detail string
}

// Prompt is a question clai asks before it reads an answer. Kind is one of
// the prompt kinds of F14.
type Prompt struct {
	Kind    string
	Text    string
	Choices []string
}

type ReplyWriter interface {
	// ReplyDelta shows one streamed part of the reply; the deltas of a reply
	// concatenate to its text. (ReplyDelta.1, F14)
	ReplyDelta(text string) error
	// ReasoningDelta shows one streamed part of the model's reasoning, kept
	// apart from the reply. (ReasoningDelta.1, F14)
	ReasoningDelta(text string) error
	// Reply shows the whole final reply once. (Reply.1, F14)
	Reply(text string) error
}

type ToolReporter interface {
	// ReportToolCall shows a call before the tool runs. (ReportToolCall.1, F14)
	ReportToolCall(ToolCall) error
	// ReportToolResult shows the result, with the failure state F14 defines
	// for tool_result. (ReportToolResult.1, F14)
	ReportToolResult(ToolResult) error
}

type ProgressReporter interface {
	// ReportProgress shows one state change. (ReportProgress.1, F14)
	ReportProgress(Progress) error
}

type Prompter interface {
	// ShowPrompt shows the question only; reading the answer stays with the
	// caller and its input. (ShowPrompt.1, F13, F14)
	ShowPrompt(Prompt) error
}

// Displayer is the JSON display of one destination. Each method writes one
// record to that destination only, in call order, and returns a failed
// write. (Displayer.1, F1, F11, F12)
type Displayer interface {
	ReplyWriter
	ToolReporter
	ProgressReporter
	Prompter
}

// NewJSONDisplayer returns the Displayer that encodes every call as the
// record F14 names, through enc, and nothing else. (NewJSONDisplayer.1, F12,
// F14)
func NewJSONDisplayer(enc jsonl.Encoder) Displayer {
	return &jsonDisplayer{enc: enc}
}

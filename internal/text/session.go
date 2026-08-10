package text

import (
	"strings"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type QuerySession struct {
	Chat               pub_models.Chat
	StartedAt          time.Time
	FinishedAt         time.Time
	PendingText        strings.Builder
	PendingReasoning   strings.Builder
	FinalAssistantText string
	FinalReasoningText string
	FinalUsage         *pub_models.Usage
	CompletedCalls     []CompletedModelCall
	ToolCallsUsed      int
	// PostHandoverToolCallsUsed counts tool calls made after the token
	// stoploss handover fired. It is the phase counter for the
	// max-tool-calls-after-handover wrap-up budget: the handover starts a
	// fresh allowance that pre-handover consumption never eats into.
	PostHandoverToolCallsUsed int
	// HandoverRequested is set once the token stoploss has injected the
	// handover user message. It switches the tool-budget phase: the
	// wrap-up allowance (max-tool-calls-after-handover) replaces the
	// pre-handover max-tool-calls budget.
	HandoverRequested   bool
	ShouldSaveReply     bool
	Raw                 bool
	Finalized           bool
	Failed              bool
	SawAnyText          bool
	SawStopEvent        bool
	LikelyGeminiPreview bool
	Line                string
	LineCount           int
}

type CompletedModelCall struct {
	StepIndex      int
	Model          string
	StartedAt      time.Time
	FinishedAt     time.Time
	Usage          *pub_models.Usage
	EndedWithTool  bool
	EndedWithReply bool
	EndedWithStop  bool
}

func (s *QuerySession) PendingTextString() string {
	return s.PendingText.String()
}

func (s *QuerySession) ResetPendingText() {
	s.PendingText.Reset()
	s.PendingReasoning.Reset()
}

func (s *QuerySession) AppendPendingText(token string) {
	s.PendingText.WriteString(token)
	if token != "" {
		s.SawAnyText = true
	}
}

func (s *QuerySession) FlushPendingTextToFinal() {
	s.FinalAssistantText = s.PendingText.String()
	s.FinalReasoningText = s.PendingReasoning.String()
	s.PendingText.Reset()
	s.PendingReasoning.Reset()
}

package text

import (
	"strings"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type QuerySession struct {
	Chat                pub_models.Chat
	StartedAt           time.Time
	FinishedAt          time.Time
	PendingText         strings.Builder
	FinalAssistantText  string
	FinalUsage          *pub_models.Usage
	CompletedCalls      []CompletedModelCall
	ToolCallsUsed       int
	ShouldSaveReply     bool
	Raw                 bool
	Finalized           bool
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
}

func (s *QuerySession) AppendPendingText(token string) {
	s.PendingText.WriteString(token)
	if token != "" {
		s.SawAnyText = true
	}
}

func (s *QuerySession) FlushPendingTextToFinal() {
	s.FinalAssistantText = s.PendingText.String()
	s.PendingText.Reset()
}

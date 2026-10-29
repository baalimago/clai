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
	// stepImages are the generated images not yet attached to an assistant message.
	stepImages []stepImage
	// rowEndedAt is the PendingText length when the display last ended its open row.
	rowEndedAt int
	// finalTextShown marks FinalAssistantText as shown already, before a tool call.
	finalTextShown bool
}

type stepImage struct {
	at    int // byte offset in the step's thinking-stripped text
	image pub_models.ImagePart
}

func (s *QuerySession) addStepImage(img pub_models.ImagePart) {
	s.SawAnyText = true
	s.stepImages = append(s.stepImages, stepImage{at: len(stripThinkingBlocks(s.PendingText.String())), image: img})
}

func (s *QuerySession) Produced() bool {
	return s.SawAnyText || s.ToolCallsUsed > 0
}

func (s *QuerySession) hasStepImages() bool { return len(s.stepImages) > 0 }

func (s *QuerySession) takeStepImages() []stepImage {
	images := s.stepImages
	s.stepImages = nil
	return images
}

// withStepImages splits text at each image's offset, so text and images keep their
// arrival order (Run.4).
func withStepImages(text string, images []stepImage) []pub_models.Part {
	parts := make([]pub_models.Part, 0, 2*len(images)+1)
	from := 0
	for _, img := range images {
		at := min(max(img.at, from), len(text))
		if at > from {
			parts = append(parts, pub_models.TextPart{Text: text[from:at]})
		}
		parts = append(parts, img.image)
		from = at
	}
	if from < len(text) {
		parts = append(parts, pub_models.TextPart{Text: text[from:]})
	}
	return parts
}

func (s *QuerySession) PendingTextString() string {
	return s.PendingText.String()
}

func (s *QuerySession) ResetPendingText() {
	s.PendingText.Reset()
	s.PendingReasoning.Reset()
	s.rowEndedAt = 0
	s.Line, s.LineCount = "", 0
}

// lineOpen reports whether streamed text left the display mid-row.
func (s *QuerySession) lineOpen() bool {
	n := s.PendingText.Len()
	return n > 0 && n != s.rowEndedAt && !strings.HasSuffix(s.PendingText.String(), "\n")
}

func (s *QuerySession) endRow() { s.rowEndedAt = s.PendingText.Len() }

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

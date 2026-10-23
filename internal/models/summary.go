package models

import (
	"context"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SummaryInputRunes caps the transcript the summarizer reads; the batch
// command's token estimate is derived from it.
const SummaryInputRunes = 2000

// LoopPhaseSummaryRunes caps a loop-phase summary. It is shorter than the chat
// label bound because the phase shares one header row with the iteration marker
// and stays readable at a typical terminal width. The summarizer rejects a
// longer submission and the loop display clips to the same bound, so the phase
// the model writes is the phase the header shows.
const LoopPhaseSummaryRunes = 120

// SummaryKind selects the instruction set and limits of a summarizer run.
type SummaryKind uint8

const (
	// SummaryKindConversation labels a whole conversation.
	SummaryKindConversation SummaryKind = iota
	// SummaryKindLoopPhase describes what the previous iteration of a loop
	// achieved and what remains toward the goal.
	SummaryKindLoopPhase
	// SummaryKindLoopPlan describes the approach the first iteration of a loop
	// intends to take toward the goal. It runs before any iteration has
	// finished, so there is no handover to summarise.
	SummaryKindLoopPlan
)

type SummaryRequest struct {
	Chat pub_models.Chat
	// Model is the explicit model to use. Empty lets the summarizer resolve
	// it: config summary-model → the conversation's last recorded model →
	// the text config model (D22).
	Model string
	// Kind selects the instruction set. The zero value labels a conversation.
	Kind SummaryKind
}

type Summary struct {
	Title   string
	Summary string
	// Model is the model that produced the result.
	Model string
	// Queries holds one row per summarizer run, Purpose "summary", cost when
	// the catalog was ready.
	Queries []pub_models.QueryCost
	// GeneratedAt is stamped by the summarizer; provenance only, never a
	// staleness index (D16).
	GeneratedAt time.Time
}

type Summarizer interface {
	Summarize(ctx context.Context, req SummaryRequest) (Summary, error)
}

// ApplyTo stamps the label onto c and appends the summarizer's usage rows;
// a zero GeneratedAt is replaced by the time of the call.
func (s Summary) ApplyTo(c *pub_models.Chat) {
	c.Title, c.Summary, c.SummaryAt = s.Title, s.Summary, s.GeneratedAt
	if c.SummaryAt.IsZero() {
		c.SummaryAt = time.Now().UTC()
	}
	c.Queries = append(c.Queries, s.Queries...)
}

package models

import (
	"context"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SummaryInputRunes caps the transcript the summarizer reads; the batch
// command's token estimate is derived from it.
const SummaryInputRunes = 2000

type SummaryRequest struct {
	Chat pub_models.Chat
	// Model is the explicit model to use. Empty lets the summarizer resolve
	// it: config summary-model → the conversation's last recorded model →
	// the text config model (D22).
	Model string
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

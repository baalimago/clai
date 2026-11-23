package chat

import (
	"context"
	"errors"
	"slices"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Forker writes a fork of a conversation and returns it as written.
type Forker interface {
	Fork(ctx context.Context, parent pub_models.Chat) (pub_models.Chat, error)
}

// NewForker builds the Forker over a Persister. newID and now are injected (chatid.New, time.Now).
func NewForker(p Persister, newID func() (string, error), now func() time.Time) Forker {
	return forker{persister: p, newID: newID, now: now}
}

var ErrForkParentUnsaved = errors.New("the chat to fork has no ID")

// ForkError is a fork that could not be written.
type ForkError struct {
	ParentID string
	Cause    error
}

func (e *ForkError) Error() string { return "fork chat " + e.ParentID + ": " + e.Cause.Error() }
func (e *ForkError) Unwrap() error { return e.Cause }

type forker struct {
	persister Persister
	newID     func() (string, error)
	now       func() time.Time
}

func (f forker) Fork(ctx context.Context, parent pub_models.Chat) (pub_models.Chat, error) {
	if parent.ID == "" {
		return pub_models.Chat{}, ErrForkParentUnsaved
	}
	id, err := f.newID()
	if err != nil {
		return pub_models.Chat{}, &ForkError{ParentID: parent.ID, Cause: err}
	}
	fork := copyChat(parent)
	fork.ID = id
	fork.Created = f.now()
	fork.Source = "clai"
	fork.SourceID = parent.ID
	saved, err := f.persister.Save(ctx, fork)
	if err != nil {
		return pub_models.Chat{}, &ForkError{ParentID: parent.ID, Cause: err}
	}
	return saved, nil
}

func copyChat(c pub_models.Chat) pub_models.Chat {
	out := c
	out.Messages = make([]pub_models.Message, len(c.Messages))
	for i, m := range c.Messages {
		m.ToolCalls = slices.Clone(m.ToolCalls)
		m.ReasoningItems = slices.Clone(m.ReasoningItems)
		m.Parts = slices.Clone(m.Parts)
		out.Messages[i] = m
	}
	out.Queries = slices.Clone(c.Queries)
	if c.TokenUsage != nil {
		u := *c.TokenUsage
		out.TokenUsage = &u
	}
	if c.RecentTokenUsage != nil {
		u := *c.RecentTokenUsage
		out.RecentTokenUsage = &u
	}
	return out
}

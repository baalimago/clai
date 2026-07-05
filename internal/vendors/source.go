package vendors

import (
	"context"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SourceRow is a lightweight descriptor for one external conversation,
// sufficient for the chat list table and dedup without reading the full body.
//
// NOTE: clai treats (Source, SourceID) as a unique pair.
//
// Source MUST be the stable identifier returned by the source reader's Source().
// SourceID MUST be non-empty; rows with empty SourceID should be skipped by callers.
//
// RawPath is used only for diagnostics (and must never leak message bodies).
//
// FirstUserMessage should be a short preview snippet (~100 chars, newlines→spaces).
// FullFirstUserMessage holds the complete, untruncated first user message text.
// FullFirstUserMessage is used for GroupKey computation to ensure foreign
// conversations participate in grouping identically to native conversations.
// Use FirstUserMessage for display; use FullFirstUserMessage for hashing/grouping.
//
// MessageCount may be approximate during discovery.
// Exact counts are available after Read() parses the full conversation.
//
// Created should be best-effort; if unknown, use the most sensible file timestamp.
//
// All fields are read-only.
//
// This contract is intentionally minimal so each vendor can implement discovery
// without exposing its internal storage format to internal/chat.
//
// If you extend this struct, ensure handler_list_chat.go table rendering and
// tests are updated.
type SourceRow struct {
	Source           string
	SourceID         string
	Created          time.Time
	FirstUserMessage string
	// Model is best-effort, discovered during Discover(), and may be
	// empty when the external source does not expose a model identifier.
	// Do not rely on it for logic; use it for display only.
	FullFirstUserMessage string
	MessageCount         int
	Model                string
	RawPath              string
}

// SourceReader discovers and reads conversations from an external tool.
//
// Discover MUST be read-only and fast (no full body parsing).
// Read MUST be self-contained (not depend on Discover state).
//
// Implementations must never write back to the external source.
//
// Source() MUST return a non-empty stable identifier.
// Duplicate Source() names are not allowed.
type SourceReader interface {
	Source() string
	Discover(ctx context.Context) ([]SourceRow, error)
	Read(ctx context.Context, sourceID string) (pub_models.Chat, error)
}

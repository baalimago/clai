package vendors

import (
	"context"
	"io/fs"
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
// MessageCount is exact: discovery reads the file to EOF on a cache miss, and
// Read() offers no later refinement.
//
// Cwd is the working directory the session was started from, best-effort;
// empty when the source does not record one. Used by the chat list's [d]ir
// filter to scope foreign conversations to the current directory.
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
	Cwd                  string
}

// SourceCache is a pull-validated index of what discovery last found: every
// entry is keyed by the fs.FileInfo taken before the file was read, and is
// current only while that (size, mod time) pair holds. A nil cache means
// "always scan"; a typed nil is not nil and would skip that branch, so
// constructors return an error rather than an unusable value. Implementations
// must be safe for concurrent use.
type SourceCache interface {
	// Lookup answers only while info still matches the pair the row was
	// stored under, and marks that row in use for this run.
	Lookup(absPath string, info fs.FileInfo) (SourceRow, bool)
	// Store marks the row in use. A zero row is a valid entry: it records
	// that absPath yields nothing.
	Store(absPath string, info fs.FileInfo, row SourceRow)
	// Locate answers from cached state alone, so the caller must validate it
	// with Lookup. Two files may carry one identity; the tie-break is
	// filepath.WalkDir's segment-by-segment order, not the smallest path.
	Locate(source, sourceID string) (absPath string, ok bool)
}

// SourceReader discovers and reads conversations from an external tool.
//
// Discover MUST be read-only; its cost is bounded by the cache, not by a line
// cap. Read MUST be self-contained (not depend on Discover state). Both take
// the cache: Discover builds the rows, Read resolves an identifier to a file.
//
// Implementations must never write back to the external source.
//
// Source() MUST return a non-empty stable identifier.
// Duplicate Source() names are not allowed.
type SourceReader interface {
	Source() string
	Discover(ctx context.Context, cache SourceCache) ([]SourceRow, error)
	Read(ctx context.Context, cache SourceCache, sourceID string) (pub_models.Chat, error)
}

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
	// Discover (added clauses):
	//  1. A row whose first user message is image-only gets that message's
	//     Message.Describe() as FirstUserMessage and FullFirstUserMessage, for
	//     example "[image 3f9a1c]".
	//  2. A cache hit whose row has an empty FirstUserMessage while its MessageCount is
	//     above zero is not trusted: the reader re-reads that file and stores the fresh
	//     row, so rows cached with an empty preview for an image-only first message
	//     heal on the next Discover.
	Discover(ctx context.Context, cache SourceCache) ([]SourceRow, error)
	// Read (added clauses):
	//  1. A user message's image block becomes a pending
	//     ImagePart{Origin: user, MIME, Data} at its position among the message's
	//     text parts. The message then carries Parts.
	//      - Claude Code: {"type":"image","source":{"type":"base64","media_type":M,"data":B}}
	//      - Pi (pi-ai ImageContent): {"type":"image","data":B,"mimeType":M}
	//  2. A tool result's image block becomes a pending ImagePart{Origin: tool} in the
	//     "tool" message's Parts, in source order with its text parts.
	//  3. An image-only user message is kept. Before this phase it was dropped.
	//  4. An image block that cannot be imported becomes TextPart
	//     "[image not imported: <reason>]" at its position, and Read does not fail.
	//     The reasons are: "url source" (not inline base64), "bad base64",
	//     "not an image" (asset.Sniff fails), and "mime mismatch" (the sniffed type
	//     differs from the declared one).
	//  5. A message with no image block is mapped exactly as before (I10).
	//  6. Read holds no asset store and never writes one (I6).
	Read(ctx context.Context, cache SourceCache, sourceID string) (pub_models.Chat, error)
}

# Continue from external conversations (Claude Desktop, Codex, …)

This document defines how clai **auto-discovers and continues conversations from external AI tools** — Claude Desktop/Code, Codex, Pi, Cursor, and others. Foreign conversations appear directly in `clai chat list` alongside native chats, can be inspected on-the-fly, and are **cloned into native clai chats** only when the user chooses to continue one. The `source` field also serves as a general-purpose identity link: chat forking, provenance tracking, and future cross-tool references all share the same mechanism.

## Motivation

Users accumulate conversation history across multiple AI tools. A conversation started in Claude Code should not be stranded there. clai should surface it in the normal chat list, let the user browse it, and — if the user decides to continue — clone it into a native clai chat. From that point it behaves identically to any other clai conversation: dirscope binding, reply flags, lookback search, and all.

The mechanism must be **seamless**: no explicit import step, no separate command. Foreign conversations appear automatically. The first explicit user action is `clai chat continue`, and that is when the clone happens.

The `source` field generalizes beyond external tools: when a user forks a clai chat, the fork records `Source: "clai"` and `SourceID: <parent-chat-id>`. A future `clai chat fork <id>` can reuse the same data model without new fields.

## High-level flow

```text
clai chat list
  │
  ├─ Read conversations/*.json        → native index rows  (Source = "")
  ├─ Call vendor.SourceReader.Discover() → foreign rows    (Source = "claude-code", …)
  ├─ Filter: skip foreign rows whose (Source, SourceID) already cloned
  ├─ Merge + sort by date → unified chat list table
  │
  ▼
User selects a foreign row (e.g. "claude-code | 5bb90… | fix auth bug…")
  │
  ├─ Inspect: vendor.SourceReader.Read(sourceID) → Chat (on-the-fly, not persisted)
  │   Shows chat info (message counts, role breakdown, preview)
  │
  ├─ Continue: vendor.SourceReader.Read(sourceID) → Chat
  │   Save as conversations/<newID>.json          (native clai chat)
  │   Set Source = "claude-code", SourceID = original
  │   upsertChatIndex
  │   Bind to directory (normal -dre flow)
  │
  ▼
Now a native clai chat. All operations work identically.
```

Key principle: **inspect is free, continue clones**. The external source is never modified. clai never writes back.

## Data model

### `Chat.Source` and `Chat.SourceID`

Two new fields on `pub_models.Chat` (aka `pkg/text/models.Chat`):

```go
type Chat struct {
    // ... existing fields ...

    // Source is a stable, human-readable origin label.
    // Empty ("") for native clai chats.
    // Examples: "claude-code", "codex", "pi", "cursor", "clai" (for forked chats).
    Source string `json:"source,omitempty"`

    // SourceID is the originating tool's conversation identifier, or the
    // parent chat ID when Source == "clai" (fork). Used for dedup during
    // listing and for provenance tracking.
    SourceID string `json:"source_id,omitempty"`

    // NOTE: clai treats (Source, SourceID) as a unique pair.
    // NOTE: clai never writes back to external sources; foreign data is read-only.
}
```

`Source` is a short, stable identifier string — not an opaque UUID — so it can be displayed directly in the chat list table. Each source reader declares its source name once.

`SourceID` is opaque and source-defined. For Claude Code, it is the JSONL `sessionId`. For a forked clai chat, it is the parent chat's `ID`.

The pair `(Source, SourceID)` is treated as unique: clai suppresses foreign listings that have already been cloned to a native chat with the same pair.

### `chatIndexRow.Source` and `chatIndexRow.SourceID`

Both are mirrored into the chat index:

```go
type chatIndexRow struct {
    // ... existing fields ...
    Source   string `json:"source,omitempty"`
    SourceID string `json:"source_id,omitempty"`
}
```

**Both fields are in the index**, unlike the earlier "import" design where `SourceID` was omitted. The rationale: every `clai chat list` invocation must dedup foreign conversations against already-cloned ones. Reading every chat file to check `SourceID` would make listing O(n) in disk reads. The index enables an O(1) in-memory dedup. `SourceID` is typically a UUID or similar — ~36 bytes — negligible index bloat.

`chatIndexRowFromChat` copies both fields from `Chat`.

### Compatibility

Existing chats have `Source == ""` and `SourceID == ""`. No migration.

**Chat index backward compatibility:** `chat_index.cache` is a JSON array of rows. Adding `source` / `source_id` fields is backward compatible: old caches simply omit them and decode to zero values. New caches will include them.

Code reading `Chat` / `chatIndexRow` must handle zero values gracefully.

## Source reader contract

Each external tool provides a `SourceReader` in its vendor package. The interface lives in `internal/vendors/source.go` (a new file at the `vendors` package level, alongside `mock.go`):

```go
package vendors

import (
    "context"
    pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SourceRow is a lightweight descriptor for one external conversation,
// sufficient for the chat list table and dedup without reading the full body.
type SourceRow struct {
    Source           string
    SourceID         string
    Created          time.Time
    FirstUserMessage string    // preview snippet (~100 chars)
    MessageCount     int
    RawPath          string    // filesystem path, for diagnostics
}

// SourceReader discovers and reads conversations from an external tool.
type SourceReader interface {
    // Source returns the stable identifier written to Chat.Source.
    Source() string

    // Discover returns lightweight descriptors for all importable
    // conversations. Must be read-only. A file that still matches its
    // cached row costs one stat; a changed file is scanned to EOF.
    Discover(ctx context.Context, cache SourceCache) ([]SourceRow, error)

    // Read converts one conversation into a full clai Chat. Called only
    // when the user inspects or continues a foreign conversation.
    Read(ctx context.Context, cache SourceCache, sourceID string) (pub_models.Chat, error)
}

// NOTE: internal/vendors/source.go must import "time" (and "context").
```

Both methods take a `vendors.SourceCache` because clai does not own the files
a foreign tool writes: it cannot be told a session changed, it can only pull.
A cache entry is keyed by the `fs.FileInfo` taken *before* the file was read,
and is current only while that `(size, mod time)` pair still holds. A nil
cache means "always scan" and is always correct, only slower. The
implementation is `chat.ForeignIndex`, which persists to
`foreign_index.cache` in the clai cache directory — derived state, deletable
at any moment with no effect beyond timing.

#### An error is not a fact about the file

A row may only be **cached**, or **pruned**, on something the filesystem
actually reported about the file's *content* — "the scan completed and found
nothing", "the file does not exist". "I could not open it", "I could not read
it to the end" and "I could not stat it" are facts about *this run*. They must
never be written into a cache that outlives it.

The distinction is exactly the derived-cache promise. A fact about the content
stays true until the content changes, and `(size, mod time)` detects that
change. A fact about the run is invisible to that pair — a permission denial, a
descriptor exhaustion, a mount that blipped, a read that failed on the wire
change neither size nor mod time — so a row written from one can never be
invalidated, and the cache stops being derived. At that point deleting
`foreign_index.cache` changes rows rather than timing.

Applied: a scan that could not start is not a scan that found nothing, a scan
that could not be read to its end is not a scan that found what it managed to
read, and a stat that failed is not a file that is gone.

**A content-determined bound is a fact about the file; a transport error is
not a fact about the file.** The rule's test is not "did an error occur" but
"would a cache-less rescan of the same bytes reach the same answer, and does
`(size, mod time)` invalidate it when the bytes change". A line longer than `vendors.ReadMaxToken`
ends its file's scan every run, in the same place, for the same bytes, and any
edit that removes the oversized line changes the file's size or mod time — so
`bufio.ErrTooLong` **is** a fact about the content and its truncated row stays
cacheable. Every other scanner error is a fact about this run alone, so
`discoverJSONLFile` skips the write for it. A blanket "any scan error skips the
cache" would be wrong in the other direction: it would silently revert the
oversized-line behaviour, with no test failing.

Because a truncated row is cached at its undercount and `ReadMaxToken` is a
compile-time constant that is not part of the cache key, changing that constant
requires a `foreignIndexVersion` bump — nothing else would invalidate the rows
it truncated.

### Push versus pull

Native and foreign conversations reach the chat list under opposite contracts,
and every rule in this section follows from that one difference.

|                                  | Native conversations                 | Foreign conversations                             |
| -------------------------------- | ------------------------------------ | ------------------------------------------------- |
| Who writes the source of truth   | clai                                 | Claude Code, pi                                   |
| How the index learns of a change | `upsertChatIndex` at save — **push** | nobody notifies clai — **pull**                   |
| Cost of knowing a row is current | zero; clai was the writer            | one `stat` per file                               |
| What invalidates a row           | nothing; the writer maintains it     | `size` or mod time differs from the cached pair   |
| What a lost cache costs          | a full rebuild over every chat       | a full rescan; recoverable, never a data loss     |

clai owns the native write path, so `chat_index.cache` is **pushed**: a row is
upserted at save time and is current because clai was the writer. clai is only
a spectator to the files Claude Code and pi write whenever they like, so
`foreign_index.cache` is **pulled**: a row is current only while the
`(size, mod time)` pair it was stored under still holds. That pair is taken
*before* the file is read, never after — a foreign corpus can grow while it is
being scanned, and a stat taken afterwards would key a row to bytes that were
never read.

`chat_index.cache` is a durable record. `foreign_index.cache` is a **derived
cache**: it may be deleted at any moment with no effect beyond timing, and a
missing, unreadable, corrupt or version-stale file simply falls through to a
full scan. The two never share a file, so a corrupt foreign row cannot trigger
a rebuild across the native corpus. Only `chat.SkipIndex` suppresses foreign
index I/O; `utils.NoCreateConfig` does not, because that flag is scoped to the
config directory while this cache lives in the cache directory — under it the
write is still attempted and a failure degrades in silence, with no stderr
noise (see `architecture/config.md`).

#### Cost budget

| Situation                              | Cost                                                                         |
| -------------------------------------- | ---------------------------------------------------------------------------- |
| Listing over an unchanged corpus       | one `stat` per session file plus one cache decode; it **opens no session file** |
| A file whose size or mod time changed  | that one file is scanned to EOF, by a pool of `min(8, NumCPU())` workers     |
| A file that yields no session identity | cached as such, and not rescanned until it changes                           |
| No cache at all                        | a full scan of every file — the pre-index behaviour, still correct           |

`TestDiscoverJSONL_cacheHitOpensNothing` is the standing proof of the first
row: it discovers a corpus through a counting filesystem and asserts zero opens
on the second pass. `TestForeignIndex_unchangedCorpusSurvivesSecondRun` proves
the cache survives between invocations, and
`TestDiscoverJSONL_cacheAgnosticResults` proves the cache is derived — a nil
cache and a warm cache produce identical rows.

### Shared JSONL skeleton

The filesystem/scanner boilerplate common to every JSONL-backed source lives
in `internal/vendors/jsonl.go`: `WalkJSONLFiles` (tolerant `*.jsonl` walking
with skip-dirs, e.g. Claude's `subagents/`), `ScanJSONLLines` (line-by-line
JSON scanning, bounded only by the scanner token size), `OpenAbs` and `StatAbs` (FS-injectable file
opening and stat), `HomeRelativeRoot`, `TextBlocksContent` (string-or-text-block
content flattening), and `MapAssistantBlocks` (assistant text/thinking/tool-call
block mapping, parameterized by `ToolCallBlockKeys` for vendor key names).

The discovery algorithm itself lives in `internal/vendors/jsonl_discover.go`:
`DiscoverJSONL` walks, scans and aggregates one `SourceRow` per file, and
`FindJSONLSession` resolves a session id back to the file that holds it —
from the cache when it names the file and one stat confirms it, otherwise by
walking and stopping at the first line that establishes a file's identity. Both are driven
by `vendors.JSONLSchema` (`internal/vendors/schema.go`), whose only data method
is `Fields(line []byte) LineFields`: a schema never sees a path, handle, reader
or line number, so it cannot decide how much of a file is read.

#### Implementing a new JSONL source

A new source implements `vendors.JSONLSchema` — `SourceName`, `Root`,
`SkipDirs` and `Fields(line []byte) LineFields` — plus its own full-body `Read`
mapping. Nothing else is required, and nothing else is allowed: vendor code
**never opens files**, never walks a directory and never counts lines, because
a schema is handed one line at a time and the generic layer alone decides how
many lines there are. That is what keeps the cost rule in code rather than in
prose, which is how the previous rule was lost.

A schema may also implement `vendors.LinePrefilter`
(`MayContribute(line []byte) bool`), which lets the scan skip decoding lines
that cannot contribute. Not implementing it is always correct, only slower.
Implementing it too narrowly is not: it silently undercounts. Every implementor
therefore runs `jsonltest.RunSchemaConformance` over its own fixture lines,
which fails on exactly that mistake **when the fixture corpus can witness it**:
the suite asserts `MayContribute` for every fixture line whose `Fields` is
non-zero, so it catches a filter that would hide a line the corpus actually
holds. It does not police the marker set itself — a marker that every fixture
line carries alongside another can be dropped without the suite noticing — so a
fixture corpus needs a line per marker for the suite to have that resolution.

`MayContribute` searches literal key spellings while `Fields` decodes with
`encoding/json`, which matches struct tags case-insensitively and accepts
escaped characters inside a key. A source is in contract while its JSON keys
are canonically spelled — the exact bytes the prefilter looks for — and a
source that varies its spelling is out of it and would undercount in silence.
That is judged a vendor format change rather than a hazard to engineer around:
write the prefilter against the spellings the source actually emits, and add a
fixture line for each marker.

`LineFields` is a **closed set**. A field enters it only when the chat list
gains a capability that consumes the field eagerly — sorting, dedup, group
collapse or the dirscope filter — because every listed row is fully populated
before page one is drawn. `TestLineFields_closedSet` reflects over the struct
and fails until the set is changed deliberately.

See `internal/vendors/anthropic/source_reader.go` and
`internal/vendors/pi/source_reader.go` as references.

### Registration

Each vendor package that supports conversation reading exposes a constructor or package-level instance. `internal/chat` imports the vendors it needs and calls their `Discover`.

```go
// In internal/chat/handler_list_chat.go (conceptual)
func allSourceReaders() []vendors.SourceReader {
	return []vendors.SourceReader{
		anthropic.SourceReader{}, // claude-code
		pi.SourceReader{},        // pi
		// future: codex.SourceReader{}, cursor.SourceReader{}, ...
	}
}

func sourceReaderByName(readers []vendors.SourceReader) (map[string]vendors.SourceReader, error) {
	m := map[string]vendors.SourceReader{}
	for _, r := range readers {
		name := r.Source()
		if name == "" {
			return nil, fmt.Errorf("source reader returned empty Source()")
		}
		if _, exists := m[name]; exists {
			return nil, fmt.Errorf("duplicate source reader name: %q", name)
		}
		m[name] = r
	}
	return m, nil
}
```

Adding a new source means:

1. Implement `SourceReader` in the vendor package.
2. Add it to `allSourceReaders()`.

No registry, no init-side-effects, no global mutable state.

**Routing rule (explicit):**

- UI/list rows MUST store only `Source` and `SourceID` (plus other display metadata). Do **not** store function pointers / interfaces in the row type.
- When the user selects a foreign row, clai MUST route to the correct `SourceReader` by looking up `row.Source` in the `map[string]SourceReader` built from `Source()`.
- If `row.Source` is not registered, treat this as an internal error (should be impossible if the row came from `Discover`).
- Duplicate `Source()` values are forbidden and MUST be rejected deterministically (covered by tests). This prevents “last writer wins” ambiguity and keeps provenance stable.

### Rules

- **`Discover` is read-only and index-backed** — a file whose `(size, mod time)` pair still matches its cached row is never opened at all; a file that misses the cache is scanned to EOF so its `MessageCount` is right. It must not mutate anything. The cost rule is in [Push versus pull](#push-versus-pull).
- **`Read` is self-contained** — it receives a `sourceID` and returns a complete `Chat`. It does not depend on prior `Discover` state.
- **Message mapping is vendor-defined** — each `Read` implementation decides how to map its native format into `pub_models.Message`. No middleware.
- **Tool calls are best-effort** — if the source format has tool uses and results, map them to `Message.ToolCalls` and `Message.Role == "tool"` with `ToolCallID`. Lossy mapping is acceptable; the goal is contextual continuity, not replay. (Current Claude reader: assistant `tool_use` blocks become `ToolCalls`; user `tool_result` blocks become separate `tool` messages.)
- **Vendor packages own their conversation format knowledge** — the Claude JSONL format belongs in `internal/vendors/anthropic/`, not in `internal/chat/`.

## Claude Desktop / Claude Code source reader

Location: `internal/vendors/anthropic/source_reader.go`

Implements `vendors.SourceReader` with `Source() == "claude-code"`.

### Discovery (`Discover`)

Scans:

1. `~/.claude/projects/` — each subdirectory is a project; each `*.jsonl` inside is a conversation.

For each `.jsonl` file whose `(size, mtime)` pair misses the foreign index, reads the **whole file** to extract:

- `SourceID` = `sessionId` from the first valid JSON line that contains it (typically `type: "user"`).
- `Created` = `timestamp` from the first valid JSON line that contains it (falls back to file mtime).
- `FirstUserMessage` = content of the first line with `type: "user"` and a string `message.content`, truncated to ~100 characters.
- `MessageCount` = count of `user` + `assistant` lines. **The count is exact from discovery onward**, with one pre-existing caveat: the list shows the same number `Read` would produce, and there is no later refinement, but a line longer than `vendors.ReadMaxToken` ends the scan of its whole file, so anything after it is uncounted — and because the result of that scan is cached, the undercount is now persisted rather than recomputed each run. Both `Discover` and `Read` truncate at the same bound, so they still agree with each other. The scan reads to EOF, and a schema's optional `MayContribute` byte prefilter decides which lines are worth decoding, so exactness costs decoding rather than reading. Misses are scanned by a bounded worker pool; hits cost one `stat`.
- `RawPath` = absolute path.

Session metadata is cross-referenced: the `local_*.json` files in `claude-code-sessions/` carry a `title` field. If found, `title` replaces `FirstUserMessage` in the preview column (the full `FirstUserMessage` is still available for fallback).

### Read (`Read`)

Parses the entire JSONL file, mapping each line to a clai `Message`:

| JSONL `type`      | Content shape                                                        | clai `Message`                                                                                             |
| ----------------- | -------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `user`            | `message.content` is a string                                        | `{role: "user", content: "<string>"}`                                                                      |
| `user`            | `message.content` is `[{type: "tool_result", tool_use_id, content}]` | `{role: "tool", content: "<content>", tool_call_id: "<tool_use_id>"}`                                      |
| `assistant`       | `message.content` has `{type: "text", text}`                         | `{role: "assistant", content: "<text>"}`                                                                   |
| `assistant`       | `message.content` has `{type: "thinking", thinking}`                 | Text goes to `reasoning_content`. If no text block follows, prepended to `content` as `[thinking] <text>`. |
| `assistant`       | `message.content` has `{type: "tool_use", id, name, input}`          | `{role: "assistant", tool_calls: [{id, function: {name, arguments}}]}`                                     |
| `queue-operation` | `enqueue` / `dequeue`                                                | Skipped.                                                                                                   |
| `system`          | any                                                                  | Skipped. Claude Code system messages contain tool schemas and other non-portable context.                  |

**Multi-block assistant messages**: Claude Code can emit `[thinking, text, tool_use]` in one line. These are merged into a single clai `Message`: text blocks concatenated into `content`, thinking into `reasoning_content`, tool uses into `ToolCalls`.

**First system message**: After conversion, one system message is prepended:

```text
Continued from Claude Code session <SourceID> (originally at <cwd>).
```

The `cwd` comes from the first `user` message's `cwd` field.

> Determinism note: do **not** include wall-clock time in the cloned chat by default.
> If “cloned at” is desired later, it MUST be sourced from an injectable clock for tests.

### Profile

`Read` does not set `chat.Profile`. The clone starts with no profile; the user's `-p` flag or the continue flow stamps it later.

## Chat list integration

### Unified listing

`clai chat list` is implemented in `internal/chat/handler_list_chat.go` and is **index-backed for the table rows**: it uses `NewChatIndexPaginator(cq.convDir)` to provide the list view, and then loads the selected chat JSON by ID via `cq.getByID(...)`.

Note: there also exists a `(*ChatHandler).list()` helper that reads every chat JSON file in the conversations directory, but the actual list UI (`handleListCmd → listChats`) uses the paginator/index. External-conversation integration MUST hook into the paginator/list selection path, not `list()`.

The flow is extended:

1. **Read native index rows** from `chat_index.cache` (as today) via `ChatIndexPaginator`.
2. **Discover foreign rows** by calling `Discover()` on each registered `SourceReader`.
3. **Dedup**: build a set of `(Source, SourceID)` from the native index rows **where `Source != ""`**. For each foreign `SourceRow`, if its `(Source, SourceID)` is in the set, skip it (already cloned).
4. **Merge** native rows + undiscovered foreign rows into a single slice used for selection.

   **Required refactor (explicit):** `handler_list_chat.go` must stop hard-coding `chatIndexRow` as the selection row type.

   Introduce a small UI-only row type (example):

   ```go
   type chatRowKind uint8

   const (
       chatRowNative chatRowKind = iota
       chatRowForeign
   )

   type chatListRow struct {
       Kind    chatRowKind
       ChatID  string // set for Kind==native
       Created time.Time

       // display fields (some N/A for foreign)
       Profile   string
       Model     string
       Tokens    int
       CostUSD   float64
       MsgCount  int
       Summary   string
       OriginDir string

       // for Kind==foreign
       Source   string
       SourceID string
   }
   ```

   - Native index rows map to `chatListRow{Kind: chatRowNative, ChatID: row.ID, ...}`.
   - Foreign discoveries map to `chatListRow{Kind: chatRowForeign, Source: "claude-code", SourceID: "...", ...}`.

   Avoid relying on `ChatID == ""` as a sentinel; it is brittle.

   This avoids overloading `chatIndexRow` (which is persistence/cache format) with UI-only concerns.

   **Dirscope filter compatibility:** any table actions / filters that depend on a native chat ID (e.g. dirscope filter) MUST apply only to rows where `Kind == native`.

   **Rule:** foreign rows are never removed by dirscope filtering; dirscope filtering is strictly a _native-row filter_.

   The user hides external conversations explicitly with the `[f]oreign convs` table action instead (offered only when a source produced rows; a second press shows them again). It drops foreign rows before grouping, so a group whose only other member was foreign collapses to its native member.

   **Required behavioral constraint:** if the current list contains **zero** native rows after filtering but foreign rows exist, the UI must still show those foreign rows (i.e. filter acts like `keep if foreign || (native && inDirscope)`).

   Acceptance note: add a test ensuring that enabling the dirscope filter does not hide foreign rows.

   Rationale: foreign conversations do not have a clai dirscope binding until cloned. Filtering them out would make the feature appear “broken” (nothing shows up) or would require inventing questionable heuristics (infer dirscope from foreign metadata).

5. **Sort** by `Created` descending.
6. **Display** the unified table with the new `Source` column.

### Source column

The `source` column is added to both table formats.

**Narrow** (width ≤ 120):

```text
%-6s| %-9s | %-20s| %-12s | %-6s | %v
Index | Source    | Created             | Model        | Cost   | About
```

**Wide** (width > 120):

```text
%-6s| %-9s | %-20s| %-8v | %-15s | %-18s | %-8s | %-6s | %v
Index | Source    | Created             | Messages | Profile         | Model              | Cost            | Tokens  | About
```

**Implementation note:** today `handler_list_chat.go` uses `chatIndexRow` directly for selection rows and `dirFilterAction` assumes the concrete type `chatIndexRow`. The integration MUST introduce a UI row type and update `dirFilterAction.Filter` to handle that type (and only filter native rows). This change is required for correctness and testability.

Rendering:

- Stored value: **native clai chats MUST keep `chat.Source == ""`**.
- Display value: render `Source == ""` as **`clai`** (colored with `theme.secondary`).
- `Source != ""` → displays as the source string.
- Foreign rows have `N/A` for Model, Cost, Tokens (data not available without full read).

### Selection and inspection

When the user selects a row from the table:

- **Native row** → existing flow: read from disk, show chat info, offer edit/delete/continue.
- **Foreign row** (not yet cloned) → call `sourceReader.Read(sourceID)` on-the-fly. Convert to `Chat`. Show chat info with an additional line:

  ```text
  source:   claude-code (session: 5bb90024…)
  ```

  The action prompt changes: instead of `[e]dit / [d]elete`, show:

  ```text
  [c]ontinue (clone to clai) / [b]ack / [q]uit
  ```

  **Implementation constraint (important):** clai is primarily "one invocation == one turn" and does not maintain a long-lived TUI event loop. Therefore "back" may be implemented as "do nothing and exit" in the first iteration, unless we explicitly add a re-render loop around `SelectFromTable`.

  **`[c]ontinue`** is the clone trigger.

### Clone on continue

**Stability note (tests):** do **not** inject wall-clock time into the cloned chat. Keeping cloned chats deterministic dramatically simplifies tests and avoids non-reproducible diffs.

If an injected timestamp is ever required, it MUST come from an injectable/test-controllable clock and use a stable format.

```text
User presses 'c' on a foreign chat
  │
  ├─ sourceReader.Read(sourceID) → Chat          (full conversion)
  ├─ Generate new clai chat ID                   (MUST be unique)
  │    - Avoid `HashIDFromPrompt(first_user_message)` alone (collision risk).
  │    - Prefer clai's existing new-chat ID generator, or hash(Source+SourceID).
  ├─ Set chat.Source, chat.SourceID
  ├─ Save(convDir, chat)                          (native persistence)
  ├─ upsertChatIndex(convDir, chat)               (now in index with Source + SourceID)
  ├─ UpdateDirScopeFromCWD(chat.ID)               (bind to directory)
  │
  ▼
  "chat <newID> is now replyable with clai -dre query <prompt>"
```

After cloning, the chat is a native clai chat. The original source file is untouched. Future `clai chat list` invocations will show the cloned chat as native (it has an index row with `Source = "claude-code"`), and dedup will suppress the original foreign listing for that `(Source, SourceID)`.

If the user selects a row where `Source != ""` but the chat exists in the index (already cloned), the row is treated as native — normal edit/delete/continue flow applies. The foreign listing was already suppressed by dedup; this case only arises if someone manually deletes the native chat but re-lists before the next dedup pass.

## Forking (future)

The `Source` field also enables chat forking:

```text
clai chat fork <chatID>
```

1. Read the source chat.
2. Clone it with a new `ID`.
3. Set `Source = "clai"`, `SourceID = <parent-chat-id>`.
4. Save and index.

The forked chat appears in `clai chat list` with `Source: clai`. The parent link is preserved in `SourceID`. This is **documented but not implemented** in the first iteration — the data model is designed to accommodate it.

## Dedup at list time

Every `clai chat list` invocation runs dedup:

1. Build `existing := map[string]bool` from native index rows where `Source != ""`: key = `Source + "\x00" + SourceID`.
2. For each `SourceRow` from `Discover`, if `existing[row.Source + "\x00" + row.SourceID]`, skip.
3. Remaining rows are shown as foreign.

This adds one map build per `clai chat list` call. The index is already loaded into memory; building the map is O(n) on the index size, which is negligible even with thousands of conversations.

**Edge case**: a user clones a foreign chat, then manually deletes the cloned `.json` file. The next `clai chat list` will re-discover the foreign conversation (since the index no longer has the `(Source, SourceID)` pair). This is correct behavior — the user removed the clone, so the foreign conversation is available again.

## Configuration and persistence

No new config files. The `Source` and `SourceID` fields are plain JSON on the existing `Chat` struct and `chat_index.cache`. The source reader list is compiled-in.

One derived cache file is added: `foreign_index.cache` in the clai **cache** directory (`utils.GetClaiCacheDir()`), holding one `(size, mod time)`-validated `SourceRow` per session file. It is not config and not a record — see [Push versus pull](#push-versus-pull) — and deleting it costs a rescan and nothing else.

Foreign *conversations* are still never persisted until the user explicitly clones them: the cache holds only the list-row descriptor, never message bodies. A source file is opened only when its cached row does not validate.

## Acceptance criteria

> Note: `internal/chat/handler_list_chat.go:listChats` is **index-backed** (via `ChatIndexPaginator`) for table rows. Any foreign row integration MUST hook into the paginator/list selection path, not the legacy `(*ChatHandler).list()` helper.

1. `clai chat list` discovers Claude Code conversations from `~/.claude/projects/` and displays them alongside native chats with `Source: claude-code`.
2. **Display-only rule:** native chats **store** `Source == ""` but **render** the Source column as `clai`. Foreign chats render their source name. Model/Cost/Tokens show `N/A` for foreign rows.
3. If the Claude directories do not exist, `clai chat list` still succeeds (foreign rows are simply absent).
   3b. If an external conversation is missing a usable `SourceID`, it is skipped (it must not appear as a foreign row).
   3c. If an external conversation is missing a usable timestamp for `Created`, the row SHOULD still be shown, using the source file's mtime for ordering (but still requiring `SourceID`). If both parsed timestamp and mtime are unavailable, show the row with `Created = time.Time{}` and sort it last.
4. If a `SourceReader.Discover` fails, the error is non-fatal: it skips that source.
   - In non-`DEBUG` mode: silent skip.
   - In `DEBUG` mode: print a **single-line** warning per source with only: source name + high-level error + (optional) path(s). Do **not** print message bodies or full JSON payloads.

4b. If `SourceReader.Read` fails when the user selects/inspects/continues a foreign row, clai shows a user-friendly error.

- Preferred: return to the list selection UI (non-fatal).
- Acceptable for first iteration (given clai’s "one invocation == one turn" bias): print the error and exit non-zero.

4c. If a foreign row references an unknown/unregistered `Source` (should not happen), clai treats it as an internal error.

- Preferred: return to the list selection UI (non-fatal).
- Acceptable for first iteration: print error and exit non-zero.

4d. "Back" semantics: if an action prompt offers `[b]ack`, the first iteration MAY implement it as an early return without re-displaying the list. If a full back-to-list loop is implemented later, it MUST be covered by tests. 5. Selecting a foreign chat prints chat info with the source line and offers `[c]ontinue` instead of `[e]dit`/`[d]elete`. 6. `[c]ontinue` on a foreign chat clones it to a native `conversations/<id>.json`, sets `Source`/`SourceID`, updates the index, binds to the directory, and uses a **unique** chat ID strategy.

**Testability requirement:** source discovery MUST be testable with a temp HOME. `Discover()` implementations SHOULD take their base search paths from environment (e.g. `HOME`) or a helper that can be overridden in tests; avoid hardcoding `os.UserHomeDir()` in a way that is difficult to control.

**Additional constraint:** clone must not mutate message ordering; it must preserve the external chronological order as best as possible.

**ID strategy requirement:** MUST NOT rely on `HashIDFromPrompt(...)` alone.

Preferred: reuse clai’s existing new-chat ID generator (random/entropy-based).

If clai does not have a suitable helper, implement `NewChatID()` using stdlib only (`time` + `crypto/rand`) to guarantee uniqueness within a run. A ULID-ish approach (time prefix + randomness) is acceptable as long as it’s deterministic-format and collision-resistant. Add unit tests for basic format and non-equality across many generations.

7. After cloning, the foreign listing is suppressed (dedup via `Source + SourceID` in the index).
8. Deleting the cloned native chat and re-listing re-surfaces the foreign conversation.
9. `clai chat continue <id>` on a cloned chat works identically to any native chat.
10. The `Source` column is present in both narrow and wide table formats.

10b. Foreign rows MUST NOT show an `OriginDir`/dirscope-derived value in the list UI (render as `N/A`/empty). Only cloned native chats participate in dirscope. 11. Rebuilding the chat index preserves `Source` and `SourceID` (they are fields on `Chat` and mirrored by `chatIndexRowFromChat`). 12. Tests exist and map to the above.

- Unit tests: Claude JSONL parsing (multi-block assistant, tool use/result, thinking), discovery + dedup, non-fatal failure modes, duplicate `Source()` detection, index round-trip, and table formatting.
- CLI/e2e tests: run `clai chat list` against a temp HOME with fixture Claude directories; select a foreign row and continue/clone; verify subsequent `chat list` dedups it and `chat continue <newID>` works. (May be implemented as integration tests that invoke handlers directly if the project does not yet have subprocess-driven CLI tests.)

13. `go test ./... -race -cover -timeout=10s` passes.
14. `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` passes.
15. `go run mvdan.cc/gofumpt@latest -w .` is clean.

## Implementation modules

| Module                                             | Purpose                                                                                                                                                                |
| -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `pkg/text/models/chat.go`                          | Add `Source` and `SourceID` to `Chat`.                                                                                                                                 |
| `internal/chat/index.go`                           | Add `Source` and `SourceID` to `chatIndexRow`; mirror in `chatIndexRowFromChat`.                                                                                       |
| `internal/vendors/source.go`                       | `SourceReader` interface and `SourceRow` struct.                                                                                                                       |
| `internal/vendors/anthropic/source_reader.go`      | Claude Code `SourceReader`: `Discover` (scan `~/.claude/projects/`, read JSONL headers, cross-reference session metadata) and `Read` (full JSONL → `Chat` conversion). |
| `internal/vendors/anthropic/source_reader_test.go` | Unit tests for JSONL parsing: multi-block assistant, thinking, tool use/result pairing, system message injection, edge cases.                                          |
| `internal/chat/handler_list_chat.go`               | Unify native + foreign listing; add `Source` column to both table formats; inspect foreign chats with on-the-fly `Read`; `[c]ontinue` clones to native chat.           |
| `internal/chat/handler_list_chat_test.go`          | Tests for unified listing, dedup, foreign row rendering, clone flow.                                                                                                   |
| `architecture/README.md`                           | Add this document.                                                                                                                                                     |

## Future sources

Each further source adds one file in its vendor package and one line to `allSourceReaders()`:

| Source     | Vendor package                   | Storage format                                          | Status          |
| ---------- | -------------------------------- | ------------------------------------------------------- | --------------- |
| Claude Code | `internal/vendors/anthropic/`   | local JSONL under `~/.claude/projects/`                 | **implemented** |
| Pi         | `internal/vendors/pi/`           | local JSONL under `~/.pi/agent/sessions/`               | **implemented** |
| Codex      | `internal/vendors/openai/`       | JSON or SQLite from `~/.codex/`                         | future          |
| Cursor     | `internal/vendors/cursor/` (new) | SQLite or workspace storage                             | future          |
| Gemini CLI | `internal/vendors/gemini/`       | local session files                                     | future          |

A JSONL-backed source is a schema and nothing more — see
[Implementing a new JSONL source](#implementing-a-new-jsonl-source). A source
that is not line-oriented (SQLite, an API) implements `SourceReader` directly;
it is deliberately not forced through the line abstraction.

### Source reader evaluation guideline

Before implementing a new `SourceReader`:

1. **Native storage format** — JSONL, SQLite, REST API, filesystem tree?
2. **Message structure** — roles, content types, tool calls, thinking blocks?
3. **Session identity** — what serves as `SourceID`?
4. **Discovery path** — auto-discoverable from well-known directories, or requires configuration?
5. **Lossiness** — what cannot be mapped to clai's `Message` model?

Document the answers as a preamble comment in the source reader file.

## UI and logging

Discovery is silent. No "discovered 12 conversations from claude-code" banner — foreign rows simply appear in the list table with their source column. This keeps `clai chat list` fast and uncluttered.

Clone emits a single notice:

```text
cloned claude-code session 5bb90024… → chat <newID>
chat <newID> is now replyable with "clai -dre query <prompt>"
```

All output uses existing `ancli` functions and respects `NO_COLOR`.

## Documentation alignment

This is the authoritative document for external conversation integration. `chat.md` references `Source`/`SourceID` as optional Chat fields. `dirscope.md` is unaffected (cloned chats participate in dirscope identically to native ones). The architecture index lists this document under "Core concepts."

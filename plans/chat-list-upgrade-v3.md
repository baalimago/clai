# Chat list upgrade v3

## Executive summary

This is the final design for the chat list upgrade.

It keeps the strongest ideas from v1 and v2, but tightens scope and execution so it is implementable without ambiguity:

1. upgrade `internal/utils/table` into a provider-backed table engine
2. keep the old slice API as a compatibility wrapper
3. add row-based search support to the shared table interaction model
4. move default table page size into `theme.json`
5. add terminal-height-aware clamping in shared utils
6. add a persistent chat metadata index so `clai chat list` stops eagerly loading every full chat
7. migrate chat numeric index resolution to the same canonical ordering source used by the list

The key conclusion is unchanged from the earlier drafts:

> improving pagination in the UI alone is not enough.

To actually stop loading all chats into memory, chat list needs a compact metadata source in addition to the shared table upgrade.

---

## What the code does today

After inspecting the codebase end to end:

### Current `chat list` flow

`ChatHandler.handleListCmd()` currently does:

1. `cq.list()`
2. `os.ReadDir(cq.convDir)`
3. `FromPath(...)` for every file
4. full JSON unmarshal into `pub_models.Chat`
5. append everything into a slice
6. sort the whole slice by `Created desc`
7. call `utils.SelectFromTable(..., 10, ...)`

That means pagination happens only after:

- every chat file has been read
- every chat has been unmarshaled
- all chats are in memory

So the current paging is cosmetic for large datasets.

### Current shared table state

`internal/utils/table.go` already supports:

- page navigation
- parsing numeric indices and ranges
- custom actions
- prompt rendering

But it is still fundamentally:

- slice-backed
- eager
- selection-index-oriented
- unaware of search
- unable to fetch data lazily

### Current theme/terminal state

- page size is hard-coded at callsites, often `10`
- `theme.json` has no table page size setting
- `internal/utils/term.go` exposes `TermWidth()` but not terminal height

### Current index semantics

`findChatByID()` resolves numeric chat indices by calling eager `list()`.

So today these commands all depend on the same expensive path:

- `clai chat list`
- `clai chat continue <index>`
- `clai chat delete <index>`

---

## Requirements restated as concrete design goals

The final design must satisfy all of these:

1. `clai chat list` must no longer load all full chats up front
2. the reusable solution must live in `internal/utils/table`
3. search must be row-based and fuzzy-ish
4. page size must be configurable in `theme.json`
5. page size must be clamped against window height
6. existing `SelectFromTable(...)` users must continue working
7. chat index ordering must be coherent across list/continue/delete

---

## Final architecture

## 1. Upgrade `internal/utils/table` to a provider-backed engine

The table module should become the reusable interaction engine.

### New shared types

Recommended additions:

```go
type TableQuery struct {
	Page     int
	PageSize int
	Search   string
}

type TablePage[T any] struct {
	Rows       []T
	TotalCount int
}

type TableProvider[T any] interface {
	Page(query TableQuery) (TablePage[T], error)
}
```

### New shared config

```go
type TableConfig struct {
	Header                 string
	SelectionPrompt        string
	PageSize               int
	OnlyOneSelect          bool
	EnableSearch           bool
	EmptyMessage           string
	AdditionalTableActions []CustomTableAction
}
```

### New primary API

```go
func SelectFromPagedTable[T any](
	provider TableProvider[T],
	rowFormatter func(globalIndex int, row T) (string, error),
	cfg TableConfig,
) ([]T, error)
```

### Why this API returns rows, not indices

For provider-backed, searchable tables, visible numeric positions are not stable identities:

- search can reorder rows
- filtering changes index meaning
- page-relative rows are not original-slice positions

Returning rows is the correct reusable abstraction.

---

## 2. Keep `SelectFromTable(...)` as a compatibility wrapper

Do not break existing table callsites.

The current API remains:

```go
func SelectFromTable[T any](
	header string,
	items []T,
	selectionType string,
	rowFormatter func(int, T) (string, error),
	pageSize int,
	onlyOneSelect bool,
	additionalTableActions []CustomTableAction,
) ([]int, error)
```

Implementation strategy:

1. wrap the slice in a simple slice provider
2. call `SelectFromPagedTable(...)`
3. map selected rows back to indices

This preserves:

- setup menus
- message edit/delete tables
- current number/range selection behavior
- `CustomTableAction`

while centralizing the new engine in one place.

---

## 3. Search belongs to the provider, not the generic table engine

The table engine should own:

- current page
- current page size
- current search string
- command parsing
- prompt rendering
- empty-state behavior

The provider should own:

- what fields are searchable
- how the row corpus is built
- how matching works
- how results are ranked

This is the correct split because fuzzy logic is domain-specific.

For chat list, the provider should search over row metadata.
For other future tables, search may mean something else.

---

## 4. Shared interaction model for paged searchable tables

The provider-backed table should support this command set:

- `Enter` or `n` => next page
- `p` => previous page
- `/query` => set search immediately
- `/` => prompt interactively for search input
- `c` => clear current search
- numeric selection / numeric ranges => select visible row numbers
- `q` => quit

### Important selection rule

When search is active, numeric selection refers to the currently visible filtered rows.

That matches user expectations for interactive search.

### Prompt format

The prompt should include page and search state, e.g.:

```text
(page: 2/8, search: "auth err", [enter]/[n]ext, [p]rev, [/]search, [c]lear, [q]uit):
```

If search is disabled, omit the search controls.

---

## 5. Add shared table page size to `theme.json`

This is a table-wide concern, so it belongs in the theme config.

### Theme field

Add:

```go
TablePageSize int `json:"tablePageSize"`
```

to `internal/utils.Theme`.

### Default

Default value:

```json
{
  "tablePageSize": 10
}
```

### Accessor

Add:

```go
func ThemeTablePageSize() int
```

### Migration

Extend the existing theme migration to backfill `tablePageSize` just like `notificationBell` was added.

### Validation

Use the same semantics as the existing config/default flow:

- if configured value is `> 0`, use it
- otherwise fall back to the default

---

## 6. Add terminal height support and a central page-size resolver

The shared table module should not duplicate page-size heuristics at callsites.

### New helper

Add to `internal/utils/term.go`:

```go
func TermHeight() (int, error)
```

This should mirror `TermWidth()`:

- respect an env override if desired (`LINES`)
- use ioctl when available
- fall back safely when not attached to a TTY

### New resolver

Add:

```go
func ResolveTablePageSize(configured int) int
```

Responsibilities:

1. start from explicit value or `ThemeTablePageSize()`
2. clamp to terminal height
3. enforce a small safe lower bound

### Clamp rule

The request says:

> If height is higher than window rows + 5, set it to window rows -5.

The practical interpretation should be:

```go
effective = min(configured, termRows-5)
```

because that expresses the likely intent: leave five lines of breathing room.

### Edge handling

When terminal height is small:

```go
if termRows <= 10 {
	effective = max(1, min(configured, termRows))
}
```

This avoids zero/negative page sizes.

---

## 7. Add a persistent chat metadata index

This is the data-layer change that makes lazy chat listing possible.

Without it, a globally sorted chat list still requires reading all files first.

### New file

Store:

```text
<clai-cache>/conversations/index.json
```

This index is cache data, not user-edited configuration, so it should live in the clai cache directory rather than the clai config directory.

### Entry shape

```go
type ChatListEntry struct {
	ID            string    `json:"id"`
	Created       time.Time `json:"created"`
	Profile       string    `json:"profile,omitempty"`
	MessageCount  int       `json:"messageCount"`
	FirstUserText string    `json:"firstUserText,omitempty"`
	TotalTokens   int       `json:"totalTokens,omitempty"`
	TotalCostUSD  float64   `json:"totalCostUSD,omitempty"`
}
```

This is enough to render rows and search them without loading full transcripts.

### Ordering

The index must be maintained in canonical order:

- `Created desc`

This becomes the single source of truth for unfiltered numeric chat ordering.

---

## 8. Chat index lifecycle and consistency rules

The index must be self-healing.

Existing users already have chat files but no index.

### Required read behavior

On load:

- if `index.json` is missing: rebuild it
- if `index.json` is malformed: warn and rebuild it
- if index entries refer to missing chat files: drop them
- if chat files exist on disk but are absent from the index: add them

### Required write behavior

The index must be updated on:

- `Save(...)`
- chat deletion
- message edit
- message deletion
- any other path that mutates a persisted normal chat

### Recommended helper surface

```go
type ChatIndex interface {
	Load() ([]ChatListEntry, error)
	Upsert(chat pub_models.Chat) error
	Remove(chatID string) error
	Rebuild() error
}
```

Operationally, the implementation should favor:

1. simple writes
2. resilient reads
3. rebuild-on-inconsistency

That is safer than trying to create a complex transactional index system.

---

## 9. Chat list provider on top of the new table engine

`clai chat list` should become a normal consumer of the upgraded shared table module.

### Suggested row type

```go
type ChatListRow struct {
	ID            string
	Created       time.Time
	MessageCount  int
	Profile       string
	TokenUsageStr string
	CostStr       string
	PromptSummary string
}
```

### Suggested provider

New file:

```text
internal/chat/chat_list_provider.go
```

Suggested shape:

```go
type ChatTableProvider struct {
	entries []ChatListEntry
}
```

### Provider behavior

Without search:

- preserve canonical `Created desc` index order
- slice out only the requested page

With search:

1. score indexed rows against the query
2. keep only matching rows
3. sort by score descending
4. tie-break by `Created desc`
5. return only the requested page

This gives row-based fuzzy find without loading full chat bodies.

---

## 10. Search design for chat rows

Search must be row-based.

That means it should search chat list metadata, not full transcript content.

### Search corpus

For each row, search over a normalized concatenation of:

- chat ID
- profile
- first user message text
- created date string(s)
- optionally token/cost string forms

For example:

```go
strings.Join([]string{
	entry.ID,
	entry.Profile,
	entry.FirstUserText,
	entry.Created.Format(time.RFC3339),
	entry.Created.Format("2006-01-02 15:04"),
}, " ")
```

### Scoring

No new third-party library should be added.

Implement a lightweight in-house scorer with these ranking rules:

1. exact substring match => strongest
2. token-prefix match => strong
3. ordered subsequence match => weaker
4. shorter match span => better
5. earlier match position => better
6. newer chats win tied scores

This is sufficient for a practical smart fuzzy search.

### Explicit non-goal

Do not search the full transcript body in this upgrade.

That would require either:

- loading full chats again, or
- building a separate full-text index

Neither is needed to satisfy the stated row-based search requirement.

---

## 11. New `chat list` runtime flow

After the redesign, `handleListCmd()` should do this:

1. resolve page size via shared table config
2. load or rebuild the chat metadata index
3. build a chat table provider over index entries
4. call `utils.SelectFromPagedTable(...)` with search enabled
5. receive one selected row
6. load exactly that full chat via `getByID(...)`
7. run existing `actOnChat(...)`

### Pseudocode

```go
func (cq *ChatHandler) handleListCmd(ctx context.Context) error {
	pageSize := utils.ResolveTablePageSize(utils.ThemeTablePageSize())

	provider, err := NewChatTableProvider(cq.convDir)
	if err != nil {
		return fmt.Errorf("create chat table provider: %w", err)
	}

	selectedRows, err := utils.SelectFromPagedTable(
		provider,
		cq.chatListRowFormatter(),
		utils.TableConfig{
			Header:          cq.chatListHeader(),
			SelectionPrompt: "goto chat: [<num>]",
			PageSize:        pageSize,
			OnlyOneSelect:   true,
			EnableSearch:    true,
			EmptyMessage:    "no chats matched current search",
		},
	)
	if err != nil {
		return fmt.Errorf("select chat from paged table: %w", err)
	}
	if len(selectedRows) == 0 {
		return nil
	}

	chat, err := cq.getByID(selectedRows[0].ID)
	if err != nil {
		return fmt.Errorf("load selected chat %q: %w", selectedRows[0].ID, err)
	}

	return cq.actOnChat(ctx, chat)
}
```

### Key effect

The list screen now loads:

- compact metadata for all chats
- full JSON for one selected chat

instead of:

- full JSON for all chats before showing page 1

---

## 12. Numeric index semantics must be unified

This is required for coherence.

There must be one canonical unfiltered ordering source:

- the chat metadata index sorted by `Created desc`

Then:

- `chat list` with no search uses that ordering
- `chat continue <index>` resolves against that ordering
- `chat delete <index>` resolves against that ordering

### Interactive filtered selection rule

Inside interactive search results, numeric selection refers to the visible filtered rows.

That is separate from CLI numeric arguments.

### Migration plan

This should be done in two phases:

#### Phase 1

- move `chat list` to the indexed provider-backed path
- keep `findChatByID()` on the old eager path if needed during initial rollout

#### Phase 2

- migrate `findChatByID()` to use the same chat index ordering
- remove the eager list dependency for numeric index resolution

The final target state is Phase 2.

---

## 13. Shared table implementation details worth preserving

The new engine must preserve current useful behaviors:

- support additional actions via `CustomTableAction`
- support one-select and multi-select modes
- support numeric ranges like `1:4`
- continue to colorize headers/prompts using theme colors

The compatibility wrapper should ensure old callsites gain:

- theme-based page sizing if desired
- paging bug fixes
- general prompt improvements

without requiring immediate callsite rewrites.

---

## 14. File/module plan

### Shared utils

Modify:

- `internal/utils/table.go`
- `internal/utils/theme.go`
- `internal/utils/term.go`

Add as needed:

- `internal/utils/table_provider.go`
- `internal/utils/table_provider_test.go`

### Chat

Add:

- `internal/chat/chat_list_index.go`
- `internal/chat/chat_list_index_test.go`
- `internal/chat/chat_list_provider.go`
- `internal/chat/chat_list_provider_test.go`

Modify:

- `internal/chat/chat.go`
- `internal/chat/chat_list.go` or equivalent list handling file
- `internal/chat/chat_persistence.go` if that is where save/delete/edit flows live

### Config / architecture docs

Update as needed:

- `architecture/chat.md`
- `architecture/config.md`

---

## 15. Test plan

This work should be delivered test-first.

### Shared table tests

Add tests for:

1. provider pagination returns correct visible rows
2. next/prev navigation respects bounds
3. `/query` applies provider search state
4. `/` interactive search input updates search state
5. `c` clears search state
6. numeric selection picks visible rows only
7. visible-row numbering works correctly under search
8. compatibility wrapper preserves old selected-index behavior

### Theme/terminal tests

Add tests for:

1. default `tablePageSize` migration/backfill
2. invalid configured page size falls back
3. page size clamps to terminal height
4. tiny terminal heights still yield page size >= 1

### Chat index tests

Add tests for:

1. rebuild from existing chat files populates metadata correctly
2. malformed index triggers rebuild
3. missing chat file referenced by index is dropped
4. new unindexed chat file is added
5. save updates existing entry
6. delete removes entry
7. edit/delete message updates prompt summary and message count when needed

### Chat provider tests

Add tests for:

1. unsearched pagination preserves `Created desc`
2. search filters by metadata only
3. exact matches outrank weak matches
4. tie-break uses `Created desc`
5. provider never requires loading full chat JSON

### Integration tests

Add tests for:

1. `chat list` loads page 1 without eager full-chat loading
2. selecting a listed row loads exactly one full chat
3. `continue <index>` resolves via canonical index ordering
4. `delete <index>` resolves via canonical index ordering

---

## 16. Rollout and migration notes

### Existing users

Users with old conversation files but no index should get an automatic rebuild on first indexed read.

### Safety vs performance

The important optimization is removing eager full-chat loading from list display.

Even if index rebuild remains O(number of chats), that cost is paid only when the compact metadata source is missing or broken.

### Backward compatibility

The old `SelectFromTable(...)` API remains available, so this upgrade should not force immediate refactors of unrelated interactive tables.

---

## 17. Decisions

### Decision 1

`internal/utils/table` becomes the shared reusable engine for paged searchable table interaction.

### Decision 2

`SelectFromPagedTable(...)` is the new provider-backed API.

### Decision 3

`SelectFromTable(...)` remains as a compatibility wrapper returning indices.

### Decision 4

Search state is managed by the shared table engine, but actual matching logic lives in the provider.

### Decision 5

`theme.json` gains `tablePageSize`, default `10`.

### Decision 6

Actual page size is resolved centrally and clamped to terminal height via `ResolveTablePageSize(...)`.

### Decision 7

`clai chat list` uses a persistent chat metadata index in the clai cache dir at `conversations/index.json`.

### Decision 8

Search is row-based over indexed metadata only, not full transcript contents.

### Decision 9

Canonical unfiltered chat ordering is `Created desc` from the metadata index.

### Decision 10

Interactive filtered numeric selection uses visible filtered row positions, while CLI numeric chat arguments use canonical unfiltered ordering.

---

## Final recommendation

Implement v3 exactly as follows:

1. build the provider-backed shared table engine
2. keep the old API as a wrapper
3. add theme-configured, terminal-clamped page sizing
4. add a persistent compact chat metadata index in the cache dir
5. move `chat list` onto the indexed provider path
6. then migrate numeric chat ID/index resolution to that same index ordering

That is the smallest design that fully solves the real problem instead of only improving the UI around it.
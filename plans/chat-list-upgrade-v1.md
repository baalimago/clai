## Scope update

This plan is updated to reflect the newer requirement:

- the **table module itself** should be upgraded so improvements are reusable everywhere
- `clai chat list` should become the first major consumer of that improved table engine

That means this plan is now split into:

1. **shared table-module enhancements**
2. **chat-list-specific provider/index work**

---

## Current state

### Chat list flow
`clai chat list` currently does this:

1. `ChatHandler.handleListCmd()`
2. `ChatHandler.list()`
   - `os.ReadDir(conversations/)`
   - for every file:
     - `FromPath(...)`
     - full JSON unmarshal to `models.Chat`
   - append all chats to a slice
   - sort entire slice by `Created desc`
3. `listChats(...)`
   - formats rows from the in-memory slice
   - calls `utils.SelectFromTable(..., 10, ...)`

### Main bottlenecks
This is expensive for large chat folders because it eagerly:
- reads every file
- unmarshals every chat
- stores every chat in memory
- sorts the full chat set
- only then renders page 1

So the biggest waste happens before pagination even starts.

### Table utility status
`internal/utils/table.go` already has paged navigation, but it is still fundamentally:
- slice-backed
- caller-sized
- navigation-only
- not search-aware
- not provider-backed
- not suitable for lazy domain data

### Theme status
`internal/utils/theme.go` loads `theme.json`.
There is no shared table configuration field today.

Important nuance:
`LoadConfigFromFile` backfills zero values from defaults, so numeric config must follow:
- positive integer = use it
- zero/negative = fallback/normalize

That is acceptable for table page size.

---

# Goals

We want a design that:

1. **Upgrades the shared table module**, not just chat list
2. **Supports lazy/paged data access**
3. **Supports row-based search**
4. **Makes table page size configurable in `theme.json`**
5. **Allows chat list to stop loading every chat into memory**
6. **Remains testable and incrementally adoptable**

---

# Proposed architecture

## 1. Upgrade the shared table module around a provider abstraction

Keep the current `SelectFromTable(...)` API for compatibility, but make it a wrapper over a more capable provider-based engine.

### New shared types
Suggested location:
- extend `internal/utils/table.go`, or
- split pieces into `internal/utils/table_provider.go`

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
	Header                  string
	SelectionPrompt         string
	PageSize                int
	OnlyOneSelect           bool
	EnableSearch            bool
	AdditionalTableActions  []CustomTableAction
	EmptyMessage            string
}
```

### New generic selector
Recommended shared entrypoint:

```go
func SelectFromPagedTable[T any](
	provider TableProvider[T],
	rowFormatter func(globalIndex int, item T) (string, error),
	cfg TableConfig,
) ([]T, error)
```

### Why return rows instead of indices
For lazy providers, displayed positions are not a reliable identity:
- they may be page-relative
- they may depend on active search
- they may not map cleanly to stable backing offsets

Returning selected rows is cleaner and more reusable.

### Compatibility wrapper
Existing API should remain:

```go
func SelectFromTable[T any](...) ([]int, error)
```

Implementation approach:
- create a slice-backed provider internally
- call `SelectFromPagedTable(...)`
- map selected rows back to indices

That lets all current callsites keep working while new features are centralized in one place.

---

## 2. Keep search state in the table module, but matching logic in providers

The shared table module should own:
- current search query
- interactive search commands
- prompt rendering
- page navigation
- empty-state rendering

The provider should own:
- how search is interpreted
- filtering
- scoring
- result ordering

This keeps the table engine generic and reusable across domains.

### Why not put fuzzy logic in `utils`
Because generic table code should not need to know:
- what fields matter for a row
- how rows should be ranked
- whether search should be substring, prefix, fuzzy, or exact

That belongs with the domain provider.

---

## 3. Shared table interaction model

Recommended controls:
- `Enter` / `n` = next page
- `p` = previous page
- `/query` = set search immediately
- `/` = prompt for search term
- `c` = clear search
- numeric input = select row(s)
- `q` = quit

Prompt should include active page and search state, for example:

```text
(page: 1/8, search: "auth err", next: [enter]/[n]ext, [p]rev, [/]search, [c]lear, [q]uit):
```

This interaction should become reusable for:
- chat list
- setup menus
- tool selection
- profile selection
- any future large selector

---

## 4. Shared table page size in theme.json

Since the enhancement is now table-wide, page size should be a shared theme setting.

### Theme addition
```json
{
  "tablePageSize": 10
}
```

### Theme struct addition
```go
TablePageSize int `json:"tablePageSize"`
```

### Accessor
```go
func ThemeTablePageSize() int
```

### Validation behavior
Because of current config backfill semantics:
- `> 0` => use configured value
- `<= 0` => normalize to default

Recommended default:
- `10`

---

## 5. Shared terminal-height clamp

We also need a shared helper for terminal height.

### New helper
In `internal/utils/term.go`:

```go
func TermHeight() (int, error)
```

mirroring `TermWidth()`.

### Shared page-size resolver
Recommended helper:

```go
func ResolveTablePageSize(configured int) int
```

Responsibilities:
1. start from explicit config or theme default
2. clamp against terminal height
3. apply safe lower bounds

### Clamp rule
Requested requirement:

> If height is higher than window rows + 5, set it to window rows -5.

That literal rule is awkward because many still-too-tall values would remain unclamped.

### Recommended practical rule
```go
effective = min(configured, termRows-5)
```

With a lower bound for small terminals, e.g.:

```go
if termRows <= 10 {
	effective = max(1, min(configured, termRows))
}
```

This is the recommended implementation unless product explicitly wants the literal rule.

---

## 6. Chat list still needs a persistent metadata index

Upgrading the table module is necessary, but it is not enough by itself.

If chat list still builds its data source by loading every conversation JSON first, the new table module will only paginate **after** the expensive work.

So chat list should also gain a persistent metadata index.

### New file
`<clai-config>/conversations/index.json`

### Suggested entry
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

### Semantics
This index is:
- list-oriented
- sorted by `Created desc`
- sufficient to render table rows without loading full chat files

### Why this is necessary
Without an index, a globally sorted lazy chat list is not really possible.
You cannot know the newest chats in correct order without touching all files first.

So:
- **shared table engine** solves reusable paging/search UI
- **chat index** solves chat-specific data scale

Both are needed.

---

## 7. Chat provider on top of the shared table engine

Once the shared table engine exists, chat list should implement a provider instead of creating a parallel selector.

### Suggested chat row
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
Suggested file:
`internal/chat/chat_list_provider.go`

```go
type ChatTableProvider struct {
	entries []ChatListEntry
}
```

Provider behavior:
- unfiltered mode:
  - preserve index order (`Created desc`)
- search mode:
  - build row corpus from metadata
  - score rows
  - keep matches only
  - sort by score desc, then `Created desc`

### Search corpus
Search should be row-based, using metadata such as:
- ID
- profile
- first user message summary
- formatted created date

Example:

```go
strings.Join([]string{
	entry.ID,
	entry.Profile,
	entry.FirstUserText,
	entry.Created.Format(time.RFC3339),
}, " ")
```

### Matching approach
No extra third-party dependencies should be added.

So implement a lightweight in-house scorer:
1. lowercase normalization
2. exact substring match = strong score
3. token-prefix match = medium score
4. subsequence match = weak score
5. earlier/denser matches rank higher
6. ties break by newer `Created`

This provides "smart fuzzy-ish" row search while preserving lazy full-chat loading.

---

## 8. Chat list flow after redesign

### `handleListCmd`
New flow:

1. resolve effective shared table page size
2. build/load self-healing chat index
3. construct chat provider
4. call `utils.SelectFromPagedTable(...)` with search enabled
5. get selected chat row
6. load exactly one full chat via `getByID(...)`
7. call existing `actOnChat(...)`

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

	chat, err := cq.getByID(selectedRows[0].ID)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("load selected chat %q: %w", selectedRows[0].ID, err)
	}

	return cq.actOnChat(ctx, chat)
}
```

---

## 9. Index maintenance and rebuild behavior

Because users already have existing conversation folders, the index must be rebuildable and self-healing.

### Proposed behavior
When the provider starts:
- load `index.json`
- if missing: rebuild from conversation files
- if malformed: warn and rebuild
- if entries reference missing chats: skip stale entries and compact on next write

### Rebuild helper
```go
func RebuildChatListIndex(convDir string) error
```

This expensive path is acceptable because it is:
- one-time
- recovery-oriented
- not the steady-state behavior

### Incremental updates
Maintain index on:
- `Save(...)`
- chat delete
- edit message
- delete messages
- any flow that writes a normal conversation file

Suggested index interface:

```go
type ChatListIndex interface {
	Load() ([]ChatListEntry, error)
	Upsert(chat pub_models.Chat) error
	Remove(chatID string) error
}
```

---

## 10. Backward compatibility strategy

### Preserve initially
- existing `SelectFromTable(...)`
- existing `list()` for current tests/legacy flows
- `getByID(...)`
- `FromPath(...)`
- `actOnChat(...)`

### Migrate in phases
#### Phase 1
- add shared provider-based table engine
- adapt old slice-based table API to it
- add shared theme page size + terminal clamp
- move `chat list` onto provider-based engine

#### Phase 2
- migrate chat numeric-index resolution in `findChatByID()` to use indexed order too
- update `chat continue <index>` and `chat delete <index>` to rely on the same indexed ordering source

This reduces risk while making the shared infra useful immediately.

---

# Testing strategy

Tests should validate behavior, not just internals.

## A. Shared table engine tests
New:
- `internal/utils/table_provider_test.go`

Test:
- provider paging works across pages
- search query is passed and preserved
- empty result states render correctly
- selection returns the correct row identity
- additional actions still work
- old `SelectFromTable` behavior remains compatible through the slice adapter

## B. Theme/table-size tests
Extend:
- `internal/utils/theme_*_test.go`
- `internal/utils/term_*_test.go`

Test:
- `tablePageSize` default exists
- existing `theme.json` gets field appended
- non-positive values normalize safely
- terminal height fallback works
- page size clamps to terminal height policy

## C. Chat index tests
New:
- `internal/chat/chat_list_index_test.go`

Test:
- rebuild from existing chats
- index sorted newest first
-/profile/message count/first user text extracted correctly
- tokens and cost are preserved
- stale/malformed state is repaired with contextual errors

## D. Chat provider tests
New:
- `internal/chat/chat_list_provider_test.go`
- `internal/chat/chat_list_search_test.go`

Test:
- page fetch returns only current page rows
- search matches on row metadata
- exact/prefix/subsequence scoring behaves as intended
- tie-breakers prefer newer chats
- selected row identity remains stable

## E. Chat handler integration tests
New:
- `internal/chat/handler_list_chat_paged_test.go`

Test:
- `handleListCmd` uses provider-backed selection
- first page render does not require loading all chats
- selecting a row loads exactly the chosen chat
- filtered selection maps to correct chat ID

---

# Suggested file layout

## Shared utils
### New
- `internal/utils/table_provider_test.go`
- optionally `internal/utils/table_provider.go`

### Modified
- `internal/utils/table.go`
- `internal/utils/theme.go`
- `internal/utils/term.go`

## Chat
### New
- `internal/chat/chat_list_index.go`
- `internal/chat/chat_list_index_test.go`
- `internal/chat/chat_list_provider.go`
- `internal/chat/chat_list_provider_test.go`
- `internal/chat/chat_list_search.go`
- `internal/chat/chat_list_search_test.go`
- `internal/chat/handler_list_chat_paged_test.go`

### Modified
- `internal/chat/handler_list_chat.go`
- `internal/chat/handler.go`
- `internal/chat/chat.go`
- any delete/edit flows that must update the index

## Docs
### Modified
- `architecture/chat.md`
- `architecture/colours.md`

---

# Recommendations

## Strong recommendations
1. **Upgrade the shared table module** with a provider-based paged selector.
2. **Keep search UI generic, but matching provider-specific.**
3. **Add `theme.json.tablePageSize`** as the shared default.
4. **Add terminal-height-based table-size clamping** in shared utils.
5. **Add a persistent chat metadata index** so chat list actually benefits from lazy paging.

## Important conclusion
Improving the table module alone is not enough to fix chat-list scale.

To satisfy the full requirement end-to-end:
- the shared table module must become provider-based
- chat list must stop deriving its provider data from eager full-chat loads

That is why this plan includes both layers.

---

# Open questions

1. **Clamp rule**
   - literal rule:
     - if `height > rows+5`, use `rows-5`
   - recommended rule:
     - use `min(height, rows-5)`

2. **Search UX**
   - inline `/query`
   - or explicit `[s]earch` prompt

3. **Index persistence**
   - acceptable to add `conversations/index.json`?
   - recommended answer: yes

4. **Numeric index semantics**
   - CLI numeric args should most likely stay bound to the unfiltered global list order
   - interactive search selection should follow the filtered visible list order

If approved, the next step is to convert this into a precise implementation sequence with exact function signatures and a test-first rollout order.
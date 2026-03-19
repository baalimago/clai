# Chat list upgrade v2

## Executive summary

This proposal keeps the strongest parts of `chat-list-upgrade-v1.md`:

- upgrade the shared `internal/utils/table` module instead of building a chat-only UI
- add provider-backed paging
- add row-based search
- move table page size into `theme.json`
- add a persistent chat metadata index so chat list can actually avoid eager full-chat loads

But it tightens the design in a few important ways:

1. it is grounded in the current code paths and compatibility constraints
2. it separates **UI paging** from **data loading/indexing** more explicitly
3. it defines a safer migration path for existing `SelectFromTable` callsites
4. it treats search as **row-ranking over indexed metadata**, not as a table concern
5. it addresses numeric index semantics across `chat list`, `chat continue`, and `chat delete`
6. it clarifies the terminal-height clamp requirement with a practical implementation

---

## What the code does today

After inspecting the codebase end to end:

### Chat list path

`clai chat list` currently flows like this:

1. `ChatHandler.handleListCmd()`
2. `ChatHandler.list()`
3. `os.ReadDir(cq.convDir)`
4. `FromPath(...)` for every file
5. full JSON unmarshal into `pub_models.Chat`
6. append all chats to a slice
7. sort the entire slice by `Created desc`
8. `listChats(...)`
9. `utils.SelectFromTable(..., 10, ...)`

That means pagination happens **after** all chats are already loaded into memory.

### Shared table utility

`internal/utils/table.go` already provides:

- page navigation
- selection parsing
- custom actions
- prompt rendering

But it is fundamentally:

- slice-backed
- eager
- not provider-backed
- not search-aware
- not appropriate for very large datasets

### Theme and terminal state

- `internal/utils/theme.go` has no shared table page-size setting
- `internal/utils/term.go` exposes `TermWidth()` but not height
- current page size is hard-coded to `10` in chat list and other table users

### Important compatibility facts

These matter for the redesign:

- setup flows already depend on `SelectFromTable`
- chat message edit/delete flows also depend on `SelectFromTable`
- `CustomTableAction` is already part of the table abstraction and should stay reusable
- `findChatByID()` currently resolves numeric indices by calling `list()`, which is also eager

So if we want the fix to be reusable and safe, we should extend the existing table module and preserve a compatibility wrapper.

---

## Problem statement

There are really two separate problems:

### Problem A: UI pagination is not reusable enough

The current table code can only paginate an already-loaded slice.

### Problem B: chat list has no cheap list-oriented data source

Even with a better table UI, chat list still cannot be truly lazy if it must read every chat JSON file just to know:

- ordering
- prompt summary
- profile
- token/cost info

So the solution must address both:

1. **shared provider-based table UI**
2. **chat metadata index**

Without both, upgrade 1 is only partially solved.

---

## Goals

1. `clai chat list` must stop loading all chats into memory up front
2. the improvement must live in `internal/utils/table`
3. we need row-based search with smart fuzzy-ish ranking
4. page size must become configurable via `theme.json`
5. page size must be clamped against terminal height
6. existing table callsites must keep working during migration
7. chat index-based commands should eventually share the same ordering source

---

## Proposed design

## 1. Extend the shared table module around a provider abstraction

Keep the current `SelectFromTable(...)` API, but implement the new capability under it.

### New shared types

Suggested home:

- `internal/utils/table.go`, or
- `internal/utils/table_provider.go` if you want cleaner separation

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

### New primary entrypoint

```go
func SelectFromPagedTable[T any](
	provider TableProvider[T],
	rowFormatter func(globalIndex int, row T) (string, error),
	cfg TableConfig,
) ([]T, error)
```

### Why return rows instead of indices

This is important.

For a provider-backed table, the visible row number is not a durable identity:

- pages can move
- search can reorder results
- filtered results do not map cleanly to original slice indices

Returning rows is the right abstraction.

### Keep the old API as a wrapper

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

Implementation:

- wrap the slice in a simple provider
- call `SelectFromPagedTable`
- map selected rows back to source indices

This preserves setup menus and other flows while centralizing improvements.

---

## 2. Keep search state in the table engine, but search semantics in the provider

The table engine should own:

- current page
- current search string
- command parsing
- prompt rendering
- empty-state rendering

The provider should own:

- which fields are searchable
- how matches are scored
- how filtered results are ordered

This is the clean split.

If fuzzy logic lives in `utils`, it becomes too generic to be good and too coupled to be reusable.

---

## 3. Shared interaction model

Recommended controls:

- `Enter` or `n` => next page
- `p` => previous page
- `/query` => set search immediately
- `/` => prompt for a search string
- `c` => clear search
- row numbers / ranges => select visible rows
- `q` => quit

Important behavioral rule:

- numeric selection should refer to the **visible filtered rows**
- the provider should receive the full query and return the current page

Example prompt:

```text
(page 2/14, search: "auth err", [enter]/[n]ext, [p]rev, [/]search, [c]lear, [q]uit): 
```

This gives us reusable behavior for future large selectors too.

---

## 4. Add shared table page size to theme.json

Because this is a shared table feature, the config should be theme-level.

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

### Defaulting behavior

Current config loading backfills zero values from defaults, so this is compatible with:

- `> 0` => use configured value
- `<= 0` => normalize to default

Recommended default remains `10`.

---

## 5. Add terminal height support and clamp table page size centrally

### New helper

In `internal/utils/term.go`:

```go
func TermHeight() (int, error)
```

mirroring `TermWidth()`.

### New resolver

```go
func ResolveTablePageSize(configured int) int
```

Responsibilities:

1. pick explicit configured size or theme default
2. clamp to terminal height
3. enforce a sane lower bound

### Requirement interpretation

Requested requirement:

> If height is higher than window rows + 5, set it to window rows -5.

The literal version is not very useful, because many too-large sizes would still be allowed.

The practical interpretation should be:

```go
effective = min(configured, termRows-5)
```

with protection for small terminals:

```go
if termRows <= 10 {
	effective = max(1, min(configured, termRows))
}
```

This matches the intent: do not let the table consume the whole screen.

---

## 6. Chat list needs a persistent metadata index

This is the most important data-layer change.

Without an index, you cannot truly lazy-load a globally sorted chat list, because you do not know the order until you inspect all files.

### Proposed file

`<clai-config>/conversations/index.json`

### Suggested entry type

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

This is enough to render list rows and perform search without loading the whole chat body.

### Why this is better than scanning files on every list

- first-page render becomes cheap
- memory use stays proportional to page/index metadata, not total transcript volume
- search can operate on compact row metadata
- ordering is stable and reusable across list-related flows

---

## 7. The index should be self-healing and incrementally maintained

A one-time rebuild path is required because existing users already have conversation files without an index.

### Required behavior

On index load:

- if missing: rebuild
- if malformed: rebuild
- if entries point to missing files: drop stale entries
- if new chats exist on disk but not in index: add them

### Suggested helper surface

```go
type ChatListIndex interface {
	Load() ([]ChatListEntry, error)
	Upsert(chat pub_models.Chat) error
	Remove(chatID string) error
	Rebuild() error
}
```

### Where index maintenance must happen

At minimum:

- `Save(...)`
- chat delete
- edit message
- delete messages
- any path that mutates persisted chats

This is better than rebuilding on every list.

### Important refinement over v1

Do not require a heavyweight always-perfect index transaction model in v1 implementation.

Instead:

1. keep index writes simple and contextual
2. make reads resilient
3. rebuild when inconsistency is detected

That is operationally safer and easier to ship.

---

## 8. Chat list provider design

With the shared provider-based table in place, `clai chat list` should become a provider consumer.

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

`internal/chat/chat_list_provider.go`

```go
type ChatTableProvider struct {
	index []ChatListEntry
}
```

Provider behavior:

- no search:
  - preserve `Created desc` index order
- with search:
  - score rows using indexed metadata
  - filter non-matches
  - sort by score desc, then `Created desc`

This gives efficient paging and good UX.

---

## 9. Search should be row-based and fuzzy-ish, using metadata only

The requirement says search should be row-based and enable smart fuzzy-find behavior.

That strongly suggests searching the **rendered meaning of the row**, not raw full-chat content.

### Search corpus

For each row, use something like:

```go
strings.Join([]string{
	entry.ID,
	entry.Profile,
	entry.FirstUserText,
	entry.Created.Format(time.RFC3339),
}, " ")
```

Potentially also:

- token string
- cost string
- maybe a short date form

### Scoring approach

No new third-party dependencies should be added.

So use a lightweight in-house scorer:

1. lowercase normalize row and query
2. exact full-substring match => highest score
3. token-prefix matches => medium-high
4. ordered subsequence match => lower score
5. denser / earlier matches rank higher
6. tie-break newer chats first

That gives a practical smart fuzzy search without complexity explosion.

### Important non-goal

Do **not** search full transcript bodies in this phase.

That would either:

- force eager loads again, or
- require a much larger full-text indexing project

The current requirement is row-based, so metadata search is the right scope.

---

## 10. Chat list flow after redesign

### New `handleListCmd`

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

### Important effect

List rendering now loads:

- index metadata for many chats
- full JSON for exactly one selected chat

instead of eagerly loading all full chats.

---

## 11. Numeric index semantics need a deliberate plan

This is the main thing v1 identified but did not fully resolve.

Today:

- `chat continue <index>`
- `chat delete <index>`

both depend on `findChatByID()`, which currently calls eager `list()`.

### Recommended policy

There should be one canonical unfiltered ordering source:

- chat index sorted by `Created desc`

Then:

- CLI numeric arguments should resolve against that canonical unfiltered ordering
- interactive filtered selections should resolve against the visible filtered rows

That is predictable and matches user expectations.

### Migration plan

#### Phase 1

- move `chat list` to index-backed provider/table
- keep `findChatByID()` temporarily on eager `list()` if needed for low-risk rollout

#### Phase 2

- migrate numeric resolution in `findChatByID()` to use the same chat index ordering
- ensure `chat continue 3` and `chat delete 3` match the list ordering users see when not searching

I strongly recommend doing Phase 2 soon after Phase 1, otherwise users may eventually see ordering drift between commands.

---

## 12. Backward compatibility with existing table users

The current reusable table is used outside chat list, especially in setup flows.

So the redesign should preserve:

- `CustomTableAction`
- number/range parsing
- simple slice-based selection
- one-select and multi-select behavior

### Compatibility strategy

Implement:

1. new provider API
2. slice-backed adapter
3. old API as a wrapper

This gives immediate reuse without a big-bang migration.

It also allows message-edit/delete flows to inherit better paging later, even if they do not need provider-backed data today.

---

## 13. Recommended implementation phases

## Phase 1: shared table foundation

- add `TableQuery`
- add `TablePage`
- add `TableProvider`
- add `TableConfig`
- add `SelectFromPagedTable`
- keep `SelectFromTable` as wrapper
- retain `CustomTableAction`
- add search prompt handling
- add empty-state behavior

## Phase 2: theme and terminal sizing

- add `tablePageSize` to `Theme`
- migrate `theme.json`
- add `ThemeTablePageSize()`
- add `TermHeight()`
- add `ResolveTablePageSize()`

## Phase 3: chat index

- add `chat_list_index.go`
- add rebuild/load/upsert/remove behavior
- integrate index writes into save/delete/edit flows

## Phase 4: chat provider and list migration

- add `chat_list_provider.go`
- add row formatter/search scorer
- switch `handleListCmd()` to provider-backed selection

## Phase 5: unify numeric index resolution

- switch `findChatByID()` numeric path to the chat index ordering
- keep exact-ID and legacy prompt-ID fallbacks

---

## 14. Testing strategy

Per project practice, tests should validate behavior first.

## A. Shared table tests

New:

- `internal/utils/table_provider_test.go`

Test:

- provider paging fetches only requested page rows
- search query is passed through correctly
- selection returns correct row identity
- empty result message is shown
- custom actions still work
- `SelectFromTable` wrapper preserves legacy behavior

## B. Theme and terminal sizing tests

Extend:

- `internal/utils/theme_*_test.go`
- `internal/utils/term_*_test.go`

Test:

- default `tablePageSize` exists
- migration appends field to older `theme.json`
- invalid/non-positive values normalize
- terminal height fallback works
- resolved page size clamps as intended

## C. Chat index tests

New:

- `internal/chat/chat_list_index_test.go`

Test:

- rebuild from existing chat files
- ordering is newest first
- metadata extraction is correct
- stale entries are repaired
- malformed index triggers rebuild

## D. Chat search/provider tests

New:

- `internal/chat/chat_list_provider_test.go`
- `internal/chat/chat_list_search_test.go`

Test:

- unfiltered paging preserves index order
- search matches ID/profile/summary/date fields
- exact/prefix/subsequence ranking works
- newer chats win ties

## E. Handler integration tests

New:

- `internal/chat/handler_list_chat_paged_test.go`

Test:

- `handleListCmd()` uses provider-backed loading
- selection loads only the chosen chat JSON
- filtered selection maps to the correct chat ID

## F. Numeric index resolution tests

New or extended:

- `internal/chat/handler_list_continue_test.go`
- `internal/chat/handler_test.go`

Test:

- `chat continue <index>` matches canonical index ordering
- `chat delete <index>` matches the same ordering

---

## 15. Suggested file changes

### Shared utils

New:

- `internal/utils/table_provider_test.go`
- optionally `internal/utils/table_provider.go`

Modified:

- `internal/utils/table.go`
- `internal/utils/theme.go`
- `internal/utils/term.go`

### Chat

New:

- `internal/chat/chat_list_index.go`
- `internal/chat/chat_list_index_test.go`
- `internal/chat/chat_list_provider.go`
- `internal/chat/chat_list_provider_test.go`
- `internal/chat/chat_list_search.go`
- `internal/chat/chat_list_search_test.go`
- `internal/chat/handler_list_chat_paged_test.go`

Modified:

- `internal/chat/chat.go`
- `internal/chat/handler.go`
- `internal/chat/handler_list_chat.go`

Potentially modified:

- any delete/edit/save callsites that should upsert/remove index entries

### Docs

Modified:

- `architecture/chat.md`
- theme/config docs if there is a doc section for them

---

## 16. Final recommendation

The right design is:

1. **upgrade `internal/utils/table` to support provider-backed paging and search**
2. **add `theme.json.tablePageSize` and central page-size resolution**
3. **add a self-healing persistent chat metadata index**
4. **build chat list search on indexed row metadata, not full chat content**
5. **follow up by unifying all numeric chat-index commands on the same canonical ordering**

That is the cleanest end-to-end solution and the one most likely to scale without regressing other table-driven flows.

---

## What I would explicitly adopt from v1

- provider-backed reusable table module
- compatibility wrapper for `SelectFromTable`
- shared page-size theme config
- terminal-height clamp helper
- persistent chat metadata index
- provider-owned search semantics

## What I would improve beyond v1

- make the dual-problem framing explicit: table UI + data index
- emphasize stability for existing non-chat table users
- define canonical numeric-index semantics
- keep search row-based and scoped to indexed metadata
- prefer resilient/self-healing index maintenance over an overly strict design
- sequence rollout so risk stays low
